// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// strOrList decodes a JSON string or a list of strings, as Docker's StrSlice
// does for CapAdd and similar fields. Accepting both forms keeps legitimate
// requests working; anything else is a decode error, and the body is refused.
type strOrList []string

func (s *strOrList) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = nil
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*s = list
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = strOrList{one}
	return nil
}

// hostConfig is the part of Docker's HostConfig (and of the update body's
// Resources) that can reach the host. Unknown fields are ignored, so newer API
// versions still decode; a field we do read in a type we cannot interpret is
// a decode error, and the request is refused.
type hostConfig struct {
	// A bool, as in Docker: a null leaves it as it was, there and here.
	Privileged   bool   `json:"Privileged"`
	PidMode      string `json:"PidMode"`
	IpcMode      string `json:"IpcMode"`
	NetworkMode  string `json:"NetworkMode"`
	UsernsMode   string `json:"UsernsMode"`
	UTSMode      string `json:"UTSMode"`
	CgroupnsMode string `json:"CgroupnsMode"`
	// Cgroup is the legacy "container:<id>" cgroup join.
	Cgroup string `json:"Cgroup"`

	CapAdd strOrList `json:"CapAdd"`
	// Binds is the legacy "src:dst[:opts]" form; Mounts the structured one.
	Binds        strOrList   `json:"Binds"`
	Mounts       []mountSpec `json:"Mounts"`
	VolumesFrom  strOrList   `json:"VolumesFrom"`
	VolumeDriver string      `json:"VolumeDriver"`

	SecurityOpt strOrList `json:"SecurityOpt"`
	// A pointer, because presence is what matters: Docker reads [] as
	// "mask nothing", and null as "use the defaults".
	MaskedPaths   *[]string         `json:"MaskedPaths"`
	ReadonlyPaths *[]string         `json:"ReadonlyPaths"`
	Annotations   map[string]string `json:"Annotations"`
	Runtime       string            `json:"Runtime"`

	Devices []struct {
		PathOnHost string `json:"PathOnHost"`
	} `json:"Devices"`
	DeviceCgroupRules strOrList         `json:"DeviceCgroupRules"`
	DeviceRequests    []json.RawMessage `json:"DeviceRequests"`

	Sysctls        map[string]string `json:"Sysctls"`
	CgroupParent   string            `json:"CgroupParent"`
	OomKillDisable *bool             `json:"OomKillDisable"`
	Tmpfs          map[string]string `json:"Tmpfs"`

	RestartPolicy restartPolicy `json:"RestartPolicy"`
}

// restartPolicy is Docker's {"Name": ...} object. A bare string is read as
// the name too, so a body the daemon will refuse as malformed is still read
// here rather than refused by the agent first.
type restartPolicy struct{ Name string }

func (p *restartPolicy) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		p.Name = name
		return nil
	}
	var obj struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	p.Name = obj.Name
	return nil
}

// restarts reports whether the daemon restarts the container on its own.
func (p restartPolicy) restarts() bool {
	n := strings.ToLower(strings.TrimSpace(p.Name))
	return n != "" && n != "no"
}

type mountSpec struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Target      string `json:"Target"`
	ReadOnly    bool   `json:"ReadOnly"`
	BindOptions *struct {
		Propagation string `json:"Propagation"`
	} `json:"BindOptions"`
	VolumeOptions *struct {
		DriverConfig *struct {
			Name    string            `json:"Name"`
			Options map[string]string `json:"Options"`
		} `json:"DriverConfig"`
	} `json:"VolumeOptions"`
}

// createBody covers container create and exec create. The embedded hostConfig
// reads host-config fields sent at the top level: exec carries Privileged
// there, and older daemons honour top-level HostConfig fields on create when
// the nested HostConfig is absent.
type createBody struct {
	hostConfig
	HostConfig       *hostConfig `json:"HostConfig"`
	NetworkingConfig *struct {
		EndpointsConfig map[string]json.RawMessage `json:"EndpointsConfig"`
	} `json:"NetworkingConfig"`
}

type volumeCreateBody struct {
	Driver     string            `json:"Driver"`
	DriverOpts map[string]string `json:"DriverOpts"`
}

type networkCreateBody struct {
	Driver string `json:"Driver"`
}

// origin says where a hostConfig came from. A request is what a caller asks
// for; an inspected one is what an existing container already has, with the
// daemon's defaults filled in.
type origin int

const (
	fromRequest origin = iota
	fromInspect
)

// allowedCaps is Docker's default capability set. Adding one of these back
// (after a CapDrop) gives a container nothing it would not have by default.
// Every other capability is refused: each one either acts on the host
// (SYS_ADMIN, SYS_MODULE, SYS_RAWIO, SYS_TIME, SYSLOG, SYS_BOOT, BPF,
// PERFMON, SYS_RESOURCE, WAKE_ALARM, BLOCK_SUSPEND, MAC_*), crosses process
// boundaries (SYS_PTRACE, DAC_READ_SEARCH, which opened the shocker escape),
// or exposes kernel attack surface with a record of host escapes (NET_ADMIN:
// netfilter and nf_tables).
var allowedCaps = map[string]struct{}{
	"AUDIT_WRITE":      {},
	"CHOWN":            {},
	"DAC_OVERRIDE":     {},
	"FOWNER":           {},
	"FSETID":           {},
	"KILL":             {},
	"MKNOD":            {},
	"NET_BIND_SERVICE": {},
	"NET_RAW":          {},
	"SETFCAP":          {},
	"SETGID":           {},
	"SETPCAP":          {},
	"SETUID":           {},
	"SYS_CHROOT":       {},
}

// readOnlyHostFiles may be bound read-only from outside the bind roots. They
// carry the host's time zone, which many images expect, and nothing else.
var readOnlyHostFiles = map[string]struct{}{
	"/etc/localtime": {},
	"/etc/timezone":  {},
}

// checkHostConfig returns why hc is refused, or "" when it is allowed.
func (f *filter) checkHostConfig(ctx context.Context, hc *hostConfig, from origin) string {
	if hc.Privileged {
		return "Privileged=true rejected"
	}
	for _, ns := range []struct{ name, mode string }{
		{"PID", hc.PidMode}, {"IPC", hc.IpcMode}, {"UTS", hc.UTSMode},
		{"user", hc.UsernsMode}, {"cgroup", hc.Cgroup},
	} {
		if !privateNamespace(ns.mode) {
			return fmt.Sprintf("%s namespace mode %q rejected: only a private namespace is allowed", ns.name, ns.mode)
		}
	}
	// On cgroup v1 hosts the daemon's default cgroupns is "host", so an
	// existing container reports it without having asked. It is refused
	// only when a caller asks for it.
	if !privateNamespace(hc.CgroupnsMode) && (from == fromRequest || !strings.EqualFold(strings.TrimSpace(hc.CgroupnsMode), "host")) {
		return fmt.Sprintf("cgroup namespace mode %q rejected: only a private namespace is allowed", hc.CgroupnsMode)
	}
	if reason := f.checkNetwork(ctx, hc.NetworkMode); reason != "" {
		return reason
	}
	for _, c := range hc.CapAdd {
		norm := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
		if _, ok := allowedCaps[norm]; !ok {
			return "capability rejected: " + c
		}
	}
	for _, bind := range hc.Binds {
		if reason := f.checkBind(ctx, bind); reason != "" {
			return reason
		}
	}
	for _, m := range hc.Mounts {
		if reason := f.checkMount(ctx, m); reason != "" {
			return reason
		}
	}
	if len(hc.VolumesFrom) > 0 {
		return "VolumesFrom rejected: it copies another container's mounts past the bind policy"
	}
	if d := strings.TrimSpace(hc.VolumeDriver); d != "" && !strings.EqualFold(d, "local") {
		return "volume driver rejected: " + d
	}
	for _, opt := range hc.SecurityOpt {
		if !allowedSecurityOpt(opt) {
			return "SecurityOpt rejected: " + opt
		}
	}
	if from == fromRequest {
		// An inspected container always reports the daemon's defaults here.
		if hc.MaskedPaths != nil {
			return "MaskedPaths override rejected"
		}
		if hc.ReadonlyPaths != nil {
			return "ReadonlyPaths override rejected"
		}
		if len(hc.Annotations) > 0 {
			return "runtime annotations rejected"
		}
	}
	if from == fromRequest {
		// A host may make another runtime its default (nvidia's runc
		// wrapper, say); an existing container is not refused for that.
		switch strings.TrimSpace(hc.Runtime) {
		case "", "runc", "io.containerd.runc.v2":
		default:
			return "runtime rejected: " + hc.Runtime
		}
	}
	for _, d := range hc.Devices {
		if strings.TrimSpace(d.PathOnHost) != "" {
			return "host device passthrough rejected: " + d.PathOnHost
		}
	}
	if len(hc.DeviceCgroupRules) > 0 {
		return "DeviceCgroupRules rejected"
	}
	if len(hc.DeviceRequests) > 0 {
		return "DeviceRequests rejected"
	}
	for key := range hc.Sysctls {
		if !isSafeSysctl(key) {
			return "non-namespaced sysctl rejected: " + key
		}
	}
	if strings.TrimSpace(hc.CgroupParent) != "" {
		return "CgroupParent rejected: " + hc.CgroupParent
	}
	if hc.OomKillDisable != nil && *hc.OomKillDisable {
		return "OomKillDisable rejected"
	}
	for target := range hc.Tmpfs {
		if isSensitiveTmpfsTarget(target) {
			return "tmpfs over sensitive path rejected: " + target
		}
	}
	return ""
}

// privateNamespace reports whether a namespace mode keeps the container in a
// namespace of its own. "host" shares the host's, and "container:<id>" joins
// another container's, which is the host's own if that container uses host.
func privateNamespace(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "private", "shareable", "none":
		return true
	}
	return false
}

// checkNetwork refuses the host network and joining another container's
// network namespace, by name or by the ID of the host network.
func (f *filter) checkNetwork(ctx context.Context, mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch {
	case m == "" || m == "default" || m == "bridge" || m == "none":
		return ""
	case m == "host" || strings.HasPrefix(m, "host:"):
		return "host network namespace rejected"
	case strings.HasPrefix(m, "container:"):
		return "joining another container's network namespace rejected"
	}
	n, err := f.daemon.network(ctx, strings.TrimSpace(mode))
	switch {
	case errors.Is(err, errNotFound):
		return "" // the daemon refuses an unknown network itself
	case err != nil:
		return "network " + mode + " could not be checked: " + err.Error()
	case strings.EqualFold(n.Driver, "host"):
		return "host network namespace rejected: " + mode
	}
	return ""
}

// windowsPath is a Windows drive path ("c:/", "c:\\"). A daemon on Windows
// reads "c:/:c:/host" as the host's C: drive, where a Linux reading takes the
// "c" before the first colon for a volume name.
var windowsPath = regexp.MustCompile(`^[A-Za-z]:([\\/]|$)`)

// dockerVolumeName is the daemon's own pattern for a volume name.
var dockerVolumeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// checkBind checks one "src:dst[:opts]" Binds entry.
func (f *filter) checkBind(ctx context.Context, bind string) string {
	if strings.ContainsAny(bind, "\\\x00") || windowsPath.MatchString(bind) || strings.HasPrefix(bind, "//") {
		return "bind rejected: " + bind
	}
	src, rest, _ := strings.Cut(bind, ":")
	_, opts, _ := strings.Cut(rest, ":")
	readOnly := false
	for _, o := range strings.Split(opts, ",") {
		switch strings.TrimSpace(o) {
		case "ro":
			readOnly = true
		case "shared", "rshared":
			return "shared mount propagation rejected: " + bind
		}
	}
	if !strings.HasPrefix(src, "/") {
		if !dockerVolumeName.MatchString(src) {
			return "bind rejected: " + bind + " (a source is an absolute host path or a volume name)"
		}
		return f.checkNamedVolume(ctx, src)
	}
	return f.checkBindSource(ctx, src, readOnly)
}

func (f *filter) checkMount(ctx context.Context, m mountSpec) string {
	switch strings.ToLower(strings.TrimSpace(m.Type)) {
	case "bind":
		if m.BindOptions != nil {
			switch strings.ToLower(strings.TrimSpace(m.BindOptions.Propagation)) {
			case "shared", "rshared":
				return "shared mount propagation rejected: " + m.Source
			}
		}
		return f.checkBindSource(ctx, m.Source, m.ReadOnly)
	case "volume":
		if m.VolumeOptions != nil && m.VolumeOptions.DriverConfig != nil {
			dc := m.VolumeOptions.DriverConfig
			if reason := f.checkVolumeOptions(ctx, dc.Name, dc.Options); reason != "" {
				return reason
			}
		}
		if m.Source != "" {
			if !dockerVolumeName.MatchString(m.Source) {
				return "volume source must be a volume name: " + m.Source
			}
			return f.checkNamedVolume(ctx, m.Source)
		}
		return ""
	case "tmpfs":
		if isSensitiveTmpfsTarget(m.Target) {
			return "tmpfs over sensitive path rejected: " + m.Target
		}
		return ""
	}
	return "mount type rejected: " + m.Type
}

// checkBindSource allows a host path only under a bind root, or one of the
// read-only time zone files, and never one that reaches a path the agent
// keeps its own secrets or state in. A path with ".." is refused outright:
// the kernel resolves ".." after any symlink, so its lexical clean form is
// not the path that gets mounted. A ":" is refused too: on Windows it names
// a drive, and no Linux path under a root needs one.
func (f *filter) checkBindSource(ctx context.Context, src string, readOnly bool) string {
	src = strings.TrimSpace(src)
	if !strings.HasPrefix(src, "/") || strings.HasPrefix(src, "//") || strings.ContainsAny(src, "\\:\x00") {
		return "bind source must be an absolute host path: " + src
	}
	for _, seg := range strings.Split(src, "/") {
		if seg == ".." {
			return "bind source with '..' rejected: " + src
		}
	}
	clean := path.Clean(src)
	if path.Base(clean) == "docker.sock" {
		return "Docker socket bind rejected: " + src
	}
	if _, ok := readOnlyHostFiles[clean]; ok && readOnly {
		return ""
	}
	root := f.rootFor(clean)
	if root == "" {
		if len(f.roots) == 0 {
			return "host bind mount rejected: " + src + " (no --allowed-bind-roots are set; use a named volume)"
		}
		return "host bind mount rejected: " + src + " is outside --allowed-bind-roots"
	}
	self, reason := f.selfInfo(ctx)
	if reason != "" {
		return reason
	}
	if p := self.overlapsProtected(clean); p != "" {
		return "host bind mount rejected: " + src + " reaches " + p + ", which holds the agent's own data"
	}
	return f.checkSymlinks(root, clean)
}

// rootFor returns the shortest bind root that holds p, or "".
func (f *filter) rootFor(p string) string {
	best := ""
	for _, r := range f.roots {
		if (p == r || strings.HasPrefix(p, r+"/")) && (best == "" || len(r) < len(best)) {
			best = r
		}
	}
	return best
}

// checkNamedVolume applies the volume policy to a named volume. A volume
// mounted into the agent's own container holds its state and keys, and no
// other container may mount it. A volume created outside the agent with
// local bind options mounts a host path just as a bind does. A volume that
// does not exist yet is created by the daemon with the local driver and no
// options.
func (f *filter) checkNamedVolume(ctx context.Context, name string) string {
	self, reason := f.selfInfo(ctx)
	if reason != "" {
		return reason
	}
	if _, ok := self.volumes[name]; ok {
		return "volume " + name + " holds the agent's own data"
	}
	v, err := f.daemon.volume(ctx, name)
	switch {
	case errors.Is(err, errNotFound):
		return ""
	case err != nil:
		return "volume " + name + " could not be checked: " + err.Error()
	}
	if reason := f.checkVolumeOptions(ctx, v.Driver, v.Options); reason != "" {
		return "volume " + name + ": " + reason
	}
	return ""
}

// checkVolumeOptions applies the bind policy to volume driver options. The
// local driver's bind (o=bind or rbind, or a device that is a local path)
// must name a path under a bind root; a host block device is refused.
// Network filesystems (nfs, nfs4, cifs, smb3) and tmpfs stay allowed.
// Options for any other driver are refused, because what they mean is up to
// the plugin.
func (f *filter) checkVolumeOptions(ctx context.Context, driver string, opts map[string]string) string {
	if len(opts) == 0 {
		return ""
	}
	if d := strings.ToLower(strings.TrimSpace(driver)); d != "" && d != "local" {
		return fmt.Sprintf("driver options for volume driver %q rejected", driver)
	}
	var typ, device, o string
	for k, v := range opts {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "type":
			typ = strings.ToLower(strings.TrimSpace(v))
		case "device":
			device = strings.TrimSpace(v)
		case "o":
			o = v
		}
	}
	bind, readOnly := false, false
	for _, opt := range strings.Split(o, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(opt), "=")
		switch strings.ToLower(name) {
		case "bind", "rbind":
			bind = true
		case "ro":
			readOnly = true
		}
	}
	localPath := strings.HasPrefix(device, "/") && !strings.HasPrefix(device, "//")
	if bind || localPath {
		if typ != "" && typ != "none" {
			return fmt.Sprintf("volume type %q on host device %q rejected", typ, device)
		}
		return f.checkBindSource(ctx, device, readOnly)
	}
	switch typ {
	case "nfs", "nfs4", "cifs", "smb3", "tmpfs":
		return ""
	}
	if typ != "" || device != "" {
		return fmt.Sprintf("volume type %q with device %q rejected", typ, device)
	}
	return ""
}

// localBindDevice returns the host path a local volume's options bind, and
// whether they bind one at all.
func localBindDevice(opts map[string]string) (string, bool) {
	var device, o string
	for k, v := range opts {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "device":
			device = strings.TrimSpace(v)
		case "o":
			o = v
		}
	}
	for _, opt := range strings.Split(o, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(opt), "=")
		if n := strings.ToLower(name); n == "bind" || n == "rbind" {
			return device, true
		}
	}
	if strings.HasPrefix(device, "/") && !strings.HasPrefix(device, "//") {
		return device, true
	}
	return "", false
}

// allowedSecurityOpt is an allow-list: the default seccomp and AppArmor
// profiles, no-new-privileges, and SELinux user/role/level labels. Everything
// else is refused, including seccomp=unconfined or any inline profile,
// apparmor=unconfined or any other profile, label=disable, label=type:*,
// systempaths=unconfined (clears the masked paths) and writable-cgroups=true.
// Docker splits on "=" and falls back to ":"; so do we.
func allowedSecurityOpt(opt string) bool {
	opt = strings.TrimSpace(opt)
	sep := "="
	if !strings.Contains(opt, "=") {
		sep = ":"
	}
	key, val, hasVal := strings.Cut(opt, sep)
	key = strings.ToLower(strings.TrimSpace(key))
	val = strings.ToLower(strings.TrimSpace(val))
	switch key {
	case "no-new-privileges":
		return true // only ever adds a restriction
	case "seccomp":
		return val == "builtin"
	case "apparmor":
		return val == "docker-default"
	case "writable-cgroups":
		return hasVal && val == "false"
	case "label":
		for _, prefix := range []string{"user:", "role:", "level:"} {
			if strings.HasPrefix(val, prefix) {
				return true
			}
		}
	}
	return false
}

// safeKernelSysctls are the kernel sysctls Docker namespaces by default. Only
// net.*, fs.mqueue.* and this IPC set are container-scoped (net.* because the
// host network is refused, the IPC set because the host IPC namespace is);
// anything else (general kernel.*, vm.*, ...) affects the host.
var safeKernelSysctls = map[string]struct{}{
	"kernel.shmmax":          {},
	"kernel.shmmni":          {},
	"kernel.shmall":          {},
	"kernel.shm_rmid_forced": {},
	"kernel.msgmax":          {},
	"kernel.msgmnb":          {},
	"kernel.msgmni":          {},
	"kernel.sem":             {},
}

func isSafeSysctl(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(k, "net.") || strings.HasPrefix(k, "fs.mqueue.") {
		return true
	}
	_, ok := safeKernelSysctls[k]
	return ok
}

// sensitiveTmpfsTargets are container paths a tmpfs must not shadow.
var sensitiveTmpfsTargets = []string{"/", "/proc", "/sys", "/etc"}

func isSensitiveTmpfsTarget(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return false
	}
	clean := path.Clean("/" + p)
	for _, s := range sensitiveTmpfsTargets {
		if clean == s || (s != "/" && strings.HasPrefix(clean, s+"/")) {
			return true
		}
	}
	return false
}
