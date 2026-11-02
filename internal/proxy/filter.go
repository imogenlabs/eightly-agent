// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// maxInspectBody caps how much of an inspected body we buffer. Real create
// bodies are a few KB. A larger one is refused with 413 rather than truncated:
// forwarding a body we only partly read would let the unread part through.
const maxInspectBody = 1 << 20 // 1 MiB

// filter is the allow-list in front of the Docker socket. An endpoint that is
// not on the list is refused. The endpoints whose body carries host-reaching
// configuration are buffered, parsed and checked, and a body the filter cannot
// parse is refused: if we cannot read it, we cannot say it is safe. Calls
// that act on an existing container (exec, attach, archive upload) are
// checked against what that container already has, read from the daemon.
type filter struct {
	next   http.Handler
	roots  []string
	daemon daemon
	// hostFS is true when the agent sees the host's own filesystem, so bind
	// sources can be checked for symlinks. Inside a container it sees only
	// the roots mounted into it at the same path (see visible).
	hostFS bool
	lstat  func(string) (fs.FileInfo, error)
	// mountPoints lists where things are mounted in the agent's own
	// filesystem (/proc/self/mountinfo).
	mountPoints func() []string
	// selfIDs identify the agent's own container: full IDs, or the 12-char
	// prefix Docker uses as the default hostname.
	selfIDs []string
	// protectedPaths are host files holding the agent's secrets and state.
	protectedPaths []string

	selfMu sync.Mutex
	self   *selfInfo
}

// newChecker is the filter without a next handler, as both the proxy and
// the WebSocket target checker use it.
func newChecker(roots []string, d daemon, opts []Option) *filter {
	f := &filter{
		roots:       roots,
		daemon:      d,
		hostFS:      seesHostFS(),
		lstat:       os.Lstat,
		mountPoints: ownMountPoints,
	}
	f.selfIDs = selfContainerIDs(!f.hostFS)
	for _, o := range opts {
		o(f)
	}
	return f
}

func newFilter(next http.Handler, roots []string, d daemon, opts ...Option) *filter {
	f := newChecker(roots, d, opts)
	f.next = next
	var hidden []string
	for _, r := range roots {
		if !f.visible(r) {
			hidden = append(hidden, r)
		}
	}
	if len(hidden) > 0 {
		log.Warn().Strs("roots", hidden).Msg("[SECURITY] proxy: these bind roots are not mounted into the agent's container at the same path, so binds under them cannot be checked for symlinks, and a restart policy is refused on containers that mount them; mount each root into the agent at the same path, read-only")
	}
	return f
}

// visible reports whether the agent can check paths under root itself:
// always on the host, and in a container when the root is mounted into it at
// the same path. A directory that merely exists in the image is not the
// host's, so the root must lie at or under a mount point other than "/".
func (f *filter) visible(root string) bool {
	if f.hostFS {
		return true
	}
	if f.lstat == nil || f.mountPoints == nil {
		return false
	}
	fi, err := f.lstat(root)
	if err != nil || !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return false
	}
	for _, m := range f.mountPoints() {
		if m != "/" && (root == m || strings.HasPrefix(root, m+"/")) {
			return true
		}
	}
	return false
}

// ownMountPoints reads the agent's mount points. Only Linux has them to
// read; elsewhere a container install sees no roots.
func ownMountPoints() []string {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		// Fields: id parent major:minor root mountpoint ...
		if fields := strings.Fields(line); len(fields) >= 5 {
			out = append(out, unescapeMount(fields[4]))
		}
	}
	return out
}

// unescapeMount undoes mountinfo's octal escapes (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (f *filter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := dockerPath(r.URL.Path)
	rt, id, ok := matchRoute(r.Method, p)
	if !ok {
		deny(w, r, http.StatusForbidden, "endpoint not allowed: "+r.Method+" "+p)
		return
	}
	ctx := r.Context()

	switch rt.check {
	case checkContainerCreate, checkContainerUpdate, checkExecCreate, checkVolumeCreate, checkNetworkCreate:
		body, status, reason := readBody(r)
		if reason == "" {
			reason = f.checkBody(ctx, rt.check, body)
			status = http.StatusForbidden
		}
		if reason != "" {
			deny(w, r, status, reason)
			return
		}
	case checkImagePull, checkImageTag:
		// Both read their parameters from the form, and the daemon reads a
		// form body ahead of the query. Neither has any use for a body.
		body, status, reason := readBody(r)
		if reason == "" && len(body) > 0 {
			status, reason = http.StatusForbidden, "a request body is not accepted here"
		}
		if reason != "" {
			deny(w, r, status, reason)
			return
		}
	}

	var full, reason string
	switch rt.check {
	case checkExecCreate, checkTarget:
		full, reason = f.checkTargetContainer(ctx, id)
	case checkStart:
		full, reason = f.checkStart(ctx, id, strings.HasSuffix(p, "/restart"))
	case checkPrivateRead:
		full, reason = f.checkPrivateRead(ctx, id)
	case checkNotSelf:
		full, reason = f.checkNotSelf(ctx, id, strings.HasSuffix(p, "/rename"))
	case checkContainerUpdate:
		full, reason = f.checkUpdateTarget(ctx, id, r)
	case checkExecStart:
		reason = f.checkExecStart(ctx, id)
	case checkNetworkConnect:
		reason = f.checkNetwork(ctx, id)
	case checkImagePull:
		reason = checkPull(r.URL.Query())
	case checkImageTag:
		reason = f.checkTag(ctx, r.URL.Query())
	}
	if reason != "" {
		deny(w, r, statusFor(reason), reason)
		return
	}
	// Forward the immutable ID that was checked, not the caller's name: a
	// name can be moved to another container between check and use.
	if full != "" {
		r.URL.Path = pinContainer(p, id, full)
		r.URL.RawPath = ""
	}
	f.next.ServeHTTP(w, r)
}

// noSuchContainer is the start of the reason given when a checked container
// does not exist. It is answered 404, as the daemon would, and the request
// goes no further: forwarding it by name would let a container renamed to
// that name in the meantime be reached unchecked.
const noSuchContainer = "No such container: "

func statusFor(reason string) int {
	if strings.HasPrefix(reason, noSuchContainer) {
		return http.StatusNotFound
	}
	return http.StatusForbidden
}

// pinContainer replaces the {id} segment of a /containers/{id}... path.
func pinContainer(p, id, full string) string {
	segs := strings.Split(p, "/")
	if len(segs) > 2 && segs[1] == "containers" && segs[2] == id {
		segs[2] = full
	}
	return strings.Join(segs, "/")
}

// readBody buffers the request body for inspection and puts back exactly the
// bytes that were read, so what is checked is what is forwarded.
func readBody(r *http.Request) ([]byte, int, string) {
	if r.ContentLength > maxInspectBody {
		return nil, http.StatusRequestEntityTooLarge, "request body over the 1 MiB inspection limit"
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInspectBody+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, http.StatusForbidden, "unreadable request body"
	}
	if len(body) > maxInspectBody {
		return nil, http.StatusRequestEntityTooLarge, "request body over the 1 MiB inspection limit"
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.TransferEncoding = nil
	return body, 0, ""
}

// checkBody returns a reason when the body asks for something that reaches
// the host or cannot be interpreted, and "" when it is safe to forward.
func (f *filter) checkBody(ctx context.Context, c check, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	bad := func(err error) string { return "request body could not be interpreted: " + err.Error() }
	if err := checkDuplicateKeys(body); err != nil {
		return bad(err)
	}
	switch c {
	case checkContainerCreate, checkExecCreate:
		var b createBody
		if err := json.Unmarshal(body, &b); err != nil {
			return bad(err)
		}
		if reason := f.checkHostConfig(ctx, &b.hostConfig, fromRequest); reason != "" {
			return reason
		}
		if reason := f.checkRestartOnRoots(ctx, &b.hostConfig); reason != "" {
			return reason
		}
		if b.HostConfig != nil {
			if reason := f.checkHostConfig(ctx, b.HostConfig, fromRequest); reason != "" {
				return reason
			}
			if reason := f.checkRestartOnRoots(ctx, b.HostConfig); reason != "" {
				return reason
			}
		}
		if b.NetworkingConfig != nil {
			for name := range b.NetworkingConfig.EndpointsConfig {
				if reason := f.checkNetwork(ctx, name); reason != "" {
					return reason
				}
			}
		}
	case checkContainerUpdate:
		var hc hostConfig
		if err := json.Unmarshal(body, &hc); err != nil {
			return bad(err)
		}
		return f.checkHostConfig(ctx, &hc, fromRequest)
	case checkVolumeCreate:
		var v volumeCreateBody
		if err := json.Unmarshal(body, &v); err != nil {
			return bad(err)
		}
		return f.checkVolumeOptions(ctx, v.Driver, v.DriverOpts)
	case checkNetworkCreate:
		var n networkCreateBody
		if err := json.Unmarshal(body, &n); err != nil {
			return bad(err)
		}
		switch strings.ToLower(strings.TrimSpace(n.Driver)) {
		case "", "bridge", "macvlan", "ipvlan":
		default:
			return "network driver rejected: " + n.Driver
		}
	}
	return ""
}

// inspect reads the container a call acts on. A container the daemon does
// not know is refused with noSuchContainer.
func (f *filter) inspect(ctx context.Context, id string) (*containerInfo, string) {
	c, err := f.daemon.container(ctx, id)
	switch {
	case errors.Is(err, errNotFound):
		return nil, noSuchContainer + id
	case err != nil:
		return nil, "container " + id + " could not be checked: " + err.Error()
	}
	return c, ""
}

// checkTargetContainer reads the container a call acts on and refuses it
// when getting inside it reaches the host: the agent's own container, or one
// that is privileged, shares a host or another container's namespace, holds
// the Docker socket, mounts a host path outside the bind roots or a volume
// of the agent's own, has a capability outside the default set, or has a
// host device. It returns the container's full ID.
func (f *filter) checkTargetContainer(ctx context.Context, id string) (string, string) {
	c, reason := f.inspect(ctx, id)
	if reason != "" {
		return "", reason
	}
	if f.isSelf(c.ID) {
		return "", "the agent's own container is off limits"
	}
	if reason := f.checkHostConfig(ctx, &c.HostConfig, fromInspect); reason != "" {
		return "", "container " + id + ": " + reason
	}
	for _, m := range c.Mounts {
		if path.Base(m.Destination) == "docker.sock" || path.Base(m.Source) == "docker.sock" {
			return "", "container " + id + " holds the Docker socket"
		}
		switch strings.ToLower(m.Type) {
		case "bind":
			if reason := f.checkBindSource(ctx, m.Source, !m.RW); reason != "" {
				return "", "container " + id + ": " + reason
			}
		case "volume":
			if m.Name != "" {
				if reason := f.checkNamedVolume(ctx, m.Name); reason != "" {
					return "", "container " + id + ": " + reason
				}
			}
		case "tmpfs", "image":
		default:
			return "", "container " + id + ": mount type " + m.Type + " rejected"
		}
	}
	return c.ID, ""
}

// checkExecStart finds the container an exec instance belongs to and checks
// it as checkTargetContainer does. An exec created on the host with
// --privileged is refused too. An exec ID is not a name and cannot be moved,
// so an unknown one is left to the daemon's 404.
func (f *filter) checkExecStart(ctx context.Context, id string) string {
	e, err := f.daemon.exec(ctx, id)
	switch {
	case errors.Is(err, errNotFound):
		return ""
	case err != nil:
		return "exec " + id + " could not be checked: " + err.Error()
	case e.ProcessConfig.Privileged:
		return "privileged exec rejected"
	}
	_, reason := f.checkTargetContainer(ctx, e.ContainerID)
	return reason
}

// checkPrivateRead refuses reads that show a container's command line on the
// agent's own container and on any container holding the Docker socket.
func (f *filter) checkPrivateRead(ctx context.Context, id string) (string, string) {
	c, reason := f.inspect(ctx, id)
	if reason != "" {
		return "", reason
	}
	if f.isSelf(c.ID) || holdsSocket(c.Mounts) {
		return "", "container " + id + " holds the Docker socket"
	}
	return c.ID, ""
}

// checkNotSelf refuses stopping, killing, pausing, removing, updating or
// renaming the agent's own container: each one cuts Eight.ly OS off from
// this host, or starves the agent. A rename is refused as well on any
// container holding the Docker socket, whose name an operator relies on.
func (f *filter) checkNotSelf(ctx context.Context, id string, rename bool) (string, string) {
	c, reason := f.inspect(ctx, id)
	if reason != "" {
		return "", reason
	}
	if f.isSelf(c.ID) {
		return "", "the agent's own container is off limits"
	}
	if rename && holdsSocket(c.Mounts) {
		return "", "container " + id + " holds the Docker socket and keeps its name"
	}
	return c.ID, ""
}

// checkUpdateTarget refuses an update of the agent's own container, and a
// restart policy on a container whose mounts reach a bind root when the
// agent cannot see the host's files (see checkRestartOnRoots). The body has
// been checked and put back already.
func (f *filter) checkUpdateTarget(ctx context.Context, id string, r *http.Request) (string, string) {
	c, reason := f.inspect(ctx, id)
	if reason != "" {
		return "", reason
	}
	if f.isSelf(c.ID) {
		return "", "the agent's own container is off limits"
	}
	var hc hostConfig
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 || json.Unmarshal(body, &hc) != nil || !hc.RestartPolicy.restarts() || len(f.roots) == 0 {
		return c.ID, ""
	}
	for _, m := range c.Mounts {
		var root string
		switch strings.ToLower(m.Type) {
		case "bind":
			root = f.rootFor(path.Clean(m.Source))
		case "volume":
			if m.Name == "" {
				continue
			}
			var reason string
			if root, reason = f.volumeRoot(ctx, m.Name); reason != "" {
				return "", reason
			}
		}
		if root != "" && !f.visible(root) {
			return "", restartRefused(root)
		}
	}
	return c.ID, ""
}

// checkStart re-checks bind sources for symlinks when a container is
// started, because that is when the daemon mounts them: a link planted after
// create would otherwise be followed. That holds for a local volume bound to
// a path under a root as much as for a bind. Binds and volumes outside the
// roots were made on the host, not through the agent, and are left alone.
// Restarting the agent's own container is refused.
func (f *filter) checkStart(ctx context.Context, id string, restart bool) (string, string) {
	c, reason := f.inspect(ctx, id)
	if reason != "" {
		return "", reason
	}
	if restart && f.isSelf(c.ID) {
		return "", "the agent's own container is off limits"
	}
	if len(f.roots) == 0 {
		return c.ID, ""
	}
	for _, m := range c.Mounts {
		var src string
		switch strings.ToLower(m.Type) {
		case "bind":
			src = m.Source
		case "volume":
			if m.Name == "" {
				continue
			}
			v, err := f.daemon.volume(ctx, m.Name)
			switch {
			case errors.Is(err, errNotFound):
				continue
			case err != nil:
				return "", "volume " + m.Name + " could not be checked: " + err.Error()
			}
			src, _ = localBindDevice(v.Options)
		}
		if !strings.HasPrefix(src, "/") {
			continue
		}
		clean := path.Clean(src)
		if root := f.rootFor(clean); root != "" {
			if reason := f.checkSymlinks(root, clean); reason != "" {
				return "", "container " + id + ": " + reason
			}
		}
	}
	return c.ID, ""
}

// restartRefused explains the refusal of a restart policy on a container
// mounting a path under a root the agent cannot see.
func restartRefused(root string) string {
	return "a restart policy is refused on a container that mounts a path under the bind root " + root +
		", which is not mounted into the agent's container: the daemon restarts the container on its own, and the agent cannot check that path for symlinks. Mount " +
		root + " into the agent's container at the same path, read-only, or use RestartPolicy \"no\""
}

// checkRestartOnRoots refuses a restart policy on a container whose binds or
// local bind volumes lie under a bind root the agent cannot see. Such a
// container is mounted again on every automatic restart without passing
// through /start, and nothing was checked for symlinks at create either.
// Under a root the agent can see, the checks at create and start are the
// same on the host and in a container, and so is what a daemon-driven
// restart can still slip past them.
func (f *filter) checkRestartOnRoots(ctx context.Context, hc *hostConfig) string {
	if len(f.roots) == 0 || !hc.RestartPolicy.restarts() {
		return ""
	}
	hidden := func(src string) string {
		src = strings.TrimSpace(src)
		if !strings.HasPrefix(src, "/") {
			return ""
		}
		if root := f.rootFor(path.Clean(src)); root != "" && !f.visible(root) {
			return restartRefused(root)
		}
		return ""
	}
	volume := func(name string) string {
		root, reason := f.volumeRoot(ctx, name)
		if reason != "" {
			return reason
		}
		if root != "" && !f.visible(root) {
			return restartRefused(root)
		}
		return ""
	}
	for _, b := range hc.Binds {
		src, _, _ := strings.Cut(b, ":")
		if reason := hidden(src); reason != "" {
			return reason
		}
		if dockerVolumeName.MatchString(src) {
			if reason := volume(src); reason != "" {
				return reason
			}
		}
	}
	for _, m := range hc.Mounts {
		switch strings.ToLower(strings.TrimSpace(m.Type)) {
		case "bind":
			if reason := hidden(m.Source); reason != "" {
				return reason
			}
		case "volume":
			if m.VolumeOptions != nil && m.VolumeOptions.DriverConfig != nil {
				if dev, ok := localBindDevice(m.VolumeOptions.DriverConfig.Options); ok {
					if reason := hidden(dev); reason != "" {
						return reason
					}
				}
			}
			if m.Source != "" {
				if reason := volume(m.Source); reason != "" {
					return reason
				}
			}
		}
	}
	return ""
}

// volumeRoot returns the bind root an existing named volume's local bind
// lies under, or "".
func (f *filter) volumeRoot(ctx context.Context, name string) (string, string) {
	v, err := f.daemon.volume(ctx, name)
	switch {
	case errors.Is(err, errNotFound):
		return "", ""
	case err != nil:
		return "", "volume " + name + " could not be checked: " + err.Error()
	}
	dev, ok := localBindDevice(v.Options)
	if !ok || !strings.HasPrefix(dev, "/") {
		return "", ""
	}
	return f.rootFor(path.Clean(dev)), ""
}

// checkPull allows a registry pull and nothing else. The daemon imports an
// image instead whenever fromImage is empty: from fromSrc, a URL it fetches
// or "-" for the request body. The body itself has been refused already, so
// the query is the whole form.
func checkPull(q url.Values) string {
	if q.Has("fromSrc") {
		return "image import (fromSrc) rejected"
	}
	if strings.TrimSpace(q.Get("fromImage")) == "" {
		return "image import rejected: only a registry pull (fromImage) is allowed"
	}
	return ""
}

// checkTag refuses tagging an image into the agent's own repository: the
// next pull or update of the agent would run what was tagged there.
func (f *filter) checkTag(ctx context.Context, q url.Values) string {
	repo := normalizeRepo(q.Get("repo"))
	if repo == "" {
		return ""
	}
	self, reason := f.selfInfo(ctx)
	if reason != "" {
		return reason
	}
	for _, r := range self.repos {
		if r == repo {
			return "tagging into the agent's own image repository rejected: " + repo
		}
	}
	return ""
}

// checkSymlinks refuses a bind source when any component below its root is a
// symlink: the kernel follows it, so a link a container planted under the
// root would mount wherever it points. The root itself and its parents belong
// to the host's administrator. A component that does not exist yet ends the
// walk: the daemon creates it as a directory.
func (f *filter) checkSymlinks(root, p string) string {
	if !f.visible(root) {
		return ""
	}
	cur := root
	for _, seg := range strings.Split(strings.TrimPrefix(p, root), "/") {
		if seg == "" {
			continue
		}
		cur += "/" + seg
		fi, err := f.lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return ""
		case err != nil:
			return "bind source " + p + " could not be checked: " + err.Error()
		case fi.Mode()&fs.ModeSymlink != 0:
			return "bind source " + p + " runs through the symlink " + cur
		}
	}
	return ""
}

func (f *filter) isSelf(id string) bool {
	if id == "" {
		return false
	}
	for _, s := range f.selfIDs {
		if id == s || (len(s) == 12 && strings.HasPrefix(id, s)) {
			return true
		}
	}
	return false
}

func holdsSocket(mounts []mountPoint) bool {
	for _, m := range mounts {
		if path.Base(m.Source) == "docker.sock" || path.Base(m.Destination) == "docker.sock" {
			return true
		}
	}
	return false
}

// seesHostFS reports whether the agent runs directly on the host, where the
// paths in a bind are the paths it can stat.
func seesHostFS() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return false
		}
	}
	return true
}

var (
	containerIDInPath = regexp.MustCompile(`/containers/([0-9a-f]{64})/hostname$`)
	shortContainerID  = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// selfContainerIDs finds the agent's own container ID when it runs in one:
// from the file Docker bind-mounts over /etc/hostname, and from the hostname,
// which Docker sets to the ID's first 12 characters unless told otherwise.
// On the host itself neither applies, and every running container's mounts
// show in the mount table, so nothing is read there.
func selfContainerIDs(inContainer bool) []string {
	if !inContainer || runtime.GOOS != "linux" {
		return nil
	}
	var ids []string
	if fh, err := os.Open("/proc/self/mountinfo"); err == nil {
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			// Fields: id parent major:minor root mountpoint ...
			fields := strings.Fields(sc.Text())
			if len(fields) < 5 || fields[4] != "/etc/hostname" {
				continue
			}
			if m := containerIDInPath.FindStringSubmatch(fields[3]); m != nil {
				ids = append(ids, m[1])
			}
		}
		_ = fh.Close()
	}
	if h, err := os.Hostname(); err == nil && shortContainerID.MatchString(h) {
		ids = append(ids, h)
	}
	return ids
}
