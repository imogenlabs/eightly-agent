// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// NewDockerProxy returns a reverse proxy that forwards HTTP requests to the Docker daemon via a Unix socket.
func NewDockerProxy(socketPath string) http.Handler {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite strips Forwarded and X-Forwarded-* from the outbound request
			// and drops unparsable query parameters. The old Director passed the
			// client's values through and appended the client IP to X-Forwarded-For,
			// so restore exactly that.
			for _, h := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if v, ok := pr.In.Header[h]; ok {
					pr.Out.Header[h] = v
				}
			}
			pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
			if clientIP, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
				if prior := pr.Out.Header["X-Forwarded-For"]; len(prior) > 0 {
					clientIP = strings.Join(prior, ", ") + ", " + clientIP
				}
				pr.Out.Header.Set("X-Forwarded-For", clientIP)
			}

			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "docker"
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.URL.Path = dockerPath(pr.In.URL.Path)
		},
		Transport:     transport,
		FlushInterval: 100 * time.Millisecond,
	}

	// Wrap the proxy with a defense-in-depth deny filter. The agent
	// forwards to the Docker socket, which is host root: a malicious request
	// can grant a container full host takeover. Auth gates *who* can reach the
	// proxy; the filter gates *what* they can do.
	return denyDangerousCreates(proxy)
}

// dockerPath is the path the daemon will route on. The filter and the proxy
// both use it, so what is inspected is exactly what is forwarded: a path that
// is cleaned, with every leading /vX.Y prefix removed (a single strip would
// let /v1.45/v1.45/containers/create reach the daemon as a create while the
// filter saw something else).
func dockerPath(p string) string {
	p = path.Clean("/" + p)
	for {
		stripped := versionPrefix.ReplaceAllString(p, "")
		if stripped == p {
			break
		}
		p = path.Clean("/" + stripped)
	}
	return rewriteDockerPath(p)
}

func rewriteDockerPath(path string) string {
	switch path {
	case "/containers":
		return "/containers/json"
	case "/images":
		return "/images/json"
	case "/volumes":
		return "/volumes"
	case "/networks":
		return "/networks"
	default:
		return path
	}
}

// maxInspectBody caps how much of an inspected body we buffer. Real create
// bodies are a few KB. A larger one is refused with 413 rather than truncated:
// forwarding a body we only partly read would let the unread part through.
const maxInspectBody = 1 << 20 // 1 MiB

// bodyKind says which schema an inspected request body follows.
type bodyKind int

const (
	bodyNone            bodyKind = iota
	bodyContainerCreate          // POST /containers/create
	bodyExecCreate               // POST /containers/{id}/exec
	bodyContainerUpdate          // POST /containers/{id}/update
	bodyVolumeCreate             // POST /volumes/create
)

// denyDangerousCreates refuses Docker API calls that would let a caller
// escape a container onto the host. Endpoints that cannot be made safe are
// refused outright; the endpoints whose body carries host-reaching
// configuration are buffered, parsed and checked. A body the filter cannot
// parse is refused: if we cannot read it, we cannot say it is safe.
// Everything else (reads, start/stop, logs, pulls) passes through untouched.
func denyDangerousCreates(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := dockerPath(r.URL.Path)
		if reason := refusedEndpoint(r.Method, p); reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
		kind := inspectedBody(r.Method, p)
		if kind == bodyNone {
			next.ServeHTTP(w, r)
			return
		}

		if r.ContentLength > maxInspectBody {
			deny(w, r, http.StatusRequestEntityTooLarge, "request body over the 1 MiB inspection limit")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxInspectBody+1))
		_ = r.Body.Close()
		if err != nil {
			deny(w, r, http.StatusForbidden, "unreadable request body")
			return
		}
		if len(body) > maxInspectBody {
			deny(w, r, http.StatusRequestEntityTooLarge, "request body over the 1 MiB inspection limit")
			return
		}
		// Replay exactly the bytes that were inspected.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.TransferEncoding = nil

		if reason := inspectBody(kind, body); reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// swarmRoots are the Swarm and plugin APIs. Eight.ly OS uses none of them.
// Installing a plugin grants it host privileges; services, configs, secrets
// and nodes are the Swarm control plane (a service carries its own mounts and
// capabilities). Reads list metadata only and stay open, except /swarm: its
// inspect returns the join tokens and /swarm/unlockkey the unlock key.
var swarmRoots = []string{"plugins", "services", "configs", "secrets", "nodes"}

// refusedEndpoint returns a reason when the endpoint itself is refused,
// whatever the body says. p is the path as dockerPath returns it.
func refusedEndpoint(method, p string) string {
	if under(p, "/swarm") {
		return "swarm API rejected"
	}
	if method == http.MethodGet || method == http.MethodHead {
		return ""
	}
	for _, root := range swarmRoots {
		if under(p, "/"+root) {
			return root + " API rejected: " + method + " " + p
		}
	}
	return ""
}

// inspectedBody reports which endpoints carry host-reaching configuration in
// their body. Exec is created on /containers/{id}/exec (its body can ask for
// Privileged); /exec/{id}/start carries only Detach/Tty and is not inspected.
func inspectedBody(method, p string) bodyKind {
	if method != http.MethodPost {
		return bodyNone
	}
	switch {
	case p == "/containers/create":
		return bodyContainerCreate
	case strings.HasPrefix(p, "/containers/") && strings.HasSuffix(p, "/exec"):
		return bodyExecCreate
	case strings.HasPrefix(p, "/containers/") && strings.HasSuffix(p, "/update"):
		return bodyContainerUpdate
	case p == "/volumes/create", p == "/volumes":
		return bodyVolumeCreate
	}
	return bodyNone
}

func under(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

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
	Privileged   *bool  `json:"Privileged"`
	PidMode      string `json:"PidMode"`
	IpcMode      string `json:"IpcMode"`
	NetworkMode  string `json:"NetworkMode"`
	UsernsMode   string `json:"UsernsMode"`
	UTSMode      string `json:"UTSMode"`
	CgroupnsMode string `json:"CgroupnsMode"`

	CapAdd strOrList `json:"CapAdd"`
	// Binds is the legacy "src:dst[:opts]" form; Mounts the structured one.
	Binds  strOrList   `json:"Binds"`
	Mounts []mountSpec `json:"Mounts"`

	SecurityOpt strOrList `json:"SecurityOpt"`
	// A pointer, because presence is what matters: Docker reads [] as
	// "mask nothing", and null as "use the defaults".
	MaskedPaths   *[]string `json:"MaskedPaths"`
	ReadonlyPaths *[]string `json:"ReadonlyPaths"`

	Devices []struct {
		PathOnHost string `json:"PathOnHost"`
	} `json:"Devices"`
	DeviceCgroupRules strOrList `json:"DeviceCgroupRules"`

	Sysctls      map[string]string `json:"Sysctls"`
	CgroupParent string            `json:"CgroupParent"`
	Tmpfs        map[string]string `json:"Tmpfs"`
}

type mountSpec struct {
	Type          string `json:"Type"`
	Source        string `json:"Source"`
	Target        string `json:"Target"`
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
	HostConfig *hostConfig `json:"HostConfig"`
}

type volumeCreateBody struct {
	Driver     string            `json:"Driver"`
	DriverOpts map[string]string `json:"DriverOpts"`
}

// inspectBody returns a reason when the body asks for a host-escape
// capability or cannot be interpreted, and "" when it is safe to forward.
//
// This is a deny-list: it blocks the documented container-escape vectors but
// cannot be exhaustive. An allow-list (or an upstream socket-proxy that only
// passes vetted requests) is the stronger long-term model.
func inspectBody(kind bodyKind, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	switch kind {
	case bodyContainerCreate, bodyExecCreate:
		var c createBody
		if err := json.Unmarshal(body, &c); err != nil {
			return "request body could not be interpreted: " + err.Error()
		}
		if reason := checkHostConfig(&c.hostConfig); reason != "" {
			return reason
		}
		if c.HostConfig != nil {
			return checkHostConfig(c.HostConfig)
		}
	case bodyContainerUpdate:
		var hc hostConfig
		if err := json.Unmarshal(body, &hc); err != nil {
			return "request body could not be interpreted: " + err.Error()
		}
		return checkHostConfig(&hc)
	case bodyVolumeCreate:
		var v volumeCreateBody
		if err := json.Unmarshal(body, &v); err != nil {
			return "request body could not be interpreted: " + err.Error()
		}
		return checkVolumeOptions(v.Driver, v.DriverOpts)
	}
	return ""
}

// dangerousCaps are Linux capabilities that effectively grant host control or
// break the container boundary.
var dangerousCaps = map[string]struct{}{
	"ALL":             {},
	"SYS_ADMIN":       {},
	"SYS_PTRACE":      {},
	"SYS_MODULE":      {},
	"SYS_RAWIO":       {},
	"DAC_READ_SEARCH": {},
	"DAC_OVERRIDE":    {},
	"NET_ADMIN":       {},
	"SYS_BOOT":        {},
	"SYS_CHROOT":      {},
}

func checkHostConfig(hc *hostConfig) string {
	if hc.Privileged != nil && *hc.Privileged {
		return "Privileged=true rejected"
	}
	if isHostNamespace(hc.PidMode) {
		return "host PID namespace rejected"
	}
	if isHostNamespace(hc.IpcMode) {
		return "host IPC namespace rejected"
	}
	if isHostNamespace(hc.NetworkMode) {
		return "host network namespace rejected"
	}
	if isHostNamespace(hc.UsernsMode) {
		return "host user namespace rejected"
	}
	if isHostNamespace(hc.UTSMode) {
		return "host UTS namespace rejected"
	}
	if isHostNamespace(hc.CgroupnsMode) {
		return "host cgroup namespace rejected"
	}
	for _, c := range hc.CapAdd {
		norm := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
		if _, bad := dangerousCaps[norm]; bad {
			return "dangerous capability rejected: " + c
		}
	}
	for _, bind := range hc.Binds {
		// "source:target[:opts]". A source that is not absolute is a named
		// volume, not a host path.
		src, _, _ := strings.Cut(bind, ":")
		if isSensitiveHostPath(src) {
			return "sensitive host bind mount rejected: " + src
		}
	}
	for _, m := range hc.Mounts {
		switch strings.ToLower(strings.TrimSpace(m.Type)) {
		case "bind":
			if isSensitiveHostPath(m.Source) {
				return "sensitive host bind mount rejected: " + m.Source
			}
		case "volume":
			if m.VolumeOptions != nil && m.VolumeOptions.DriverConfig != nil {
				dc := m.VolumeOptions.DriverConfig
				if reason := checkVolumeOptions(dc.Name, dc.Options); reason != "" {
					return reason
				}
			}
		case "tmpfs":
			if isSensitiveTmpfsTarget(m.Target) {
				return "tmpfs over sensitive path rejected: " + m.Target
			}
		}
	}
	for _, opt := range hc.SecurityOpt {
		if !allowedSecurityOpt(opt) {
			return "SecurityOpt rejected: " + opt
		}
	}
	if hc.MaskedPaths != nil {
		return "MaskedPaths override rejected"
	}
	if hc.ReadonlyPaths != nil {
		return "ReadonlyPaths override rejected"
	}
	for _, d := range hc.Devices {
		if strings.TrimSpace(d.PathOnHost) != "" {
			return "host device passthrough rejected: " + d.PathOnHost
		}
	}
	if len(hc.DeviceCgroupRules) > 0 {
		return "DeviceCgroupRules rejected"
	}
	for key := range hc.Sysctls {
		if !isSafeSysctl(key) {
			return "non-namespaced sysctl rejected: " + key
		}
	}
	if strings.TrimSpace(hc.CgroupParent) != "" {
		return "CgroupParent rejected: " + hc.CgroupParent
	}
	for target := range hc.Tmpfs {
		if isSensitiveTmpfsTarget(target) {
			return "tmpfs over sensitive path rejected: " + target
		}
	}
	return ""
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

// checkVolumeOptions refuses volume driver options that mount a host path or
// host block device: the local driver's type=none/bind, o=bind, or a device
// that is a local path. Network filesystems (nfs, nfs4, cifs, smb3) and tmpfs
// stay allowed. Options for any other driver are refused, because what they
// mean is up to the plugin.
func checkVolumeOptions(driver string, opts map[string]string) string {
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
	for _, opt := range strings.Split(o, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(opt), "=")
		if n := strings.ToLower(name); n == "bind" || n == "rbind" {
			return "bind volume options rejected"
		}
	}
	if strings.HasPrefix(device, "/") && !strings.HasPrefix(device, "//") {
		return "volume device on a host path rejected: " + device
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

// safeKernelSysctls are the kernel sysctls Docker namespaces by default. Only
// net.*, fs.mqueue.* and this IPC set are container-scoped; anything else
// (general kernel.*, vm.*, ...) affects the host.
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

// isHostNamespace reports whether a namespace mode shares the host's.
func isHostNamespace(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode == "host" || strings.HasPrefix(mode, "host:")
}

// sensitivePaths are host paths that must never be bind-mounted, nor any path
// under them, nor any parent of them (a bind of /var exposes /var/run).
var sensitivePaths = []string{
	"/",
	"/etc",
	"/root",
	"/proc",
	"/sys",
	"/dev",
	"/boot",
	"/home",
	"/run",
	"/var/run",
	"/var/lib/docker",
	"/var/run/docker.sock",
	"/run/docker.sock",
}

// isSensitiveHostPath reports whether an absolute host path equals, lies
// under, or is a parent of a sensitive path, after path.Clean resolves "..",
// "." and repeated slashes. A path that is not absolute is not a host path.
func isSensitiveHostPath(p string) bool {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		return false
	}
	clean := path.Clean(p)
	if clean == "/" {
		return true
	}
	for _, s := range sensitivePaths {
		if s == "/" {
			continue // only the root itself, handled above
		}
		if clean == s || strings.HasPrefix(clean, s+"/") || strings.HasPrefix(s, clean+"/") {
			return true
		}
	}
	return false
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

func deny(w http.ResponseWriter, r *http.Request, status int, reason string) {
	log.Warn().Msgf("[SECURITY] proxy.denied method=%s path=%s ip=%s status=%d reason=%q",
		r.Method, r.URL.Path, r.RemoteAddr, status, reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encoded, not concatenated: the reason may echo attacker-supplied text.
	msg := "forbidden: "
	if status == http.StatusRequestEntityTooLarge {
		msg = "too large: "
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg + reason})
}
