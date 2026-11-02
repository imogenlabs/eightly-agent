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
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"

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
	// sources can be checked for symlinks. Inside a container it does not.
	hostFS bool
	lstat  func(string) (fs.FileInfo, error)
	// selfIDs identify the agent's own container: full IDs, or the 12-char
	// prefix Docker uses as the default hostname.
	selfIDs []string
}

func newFilter(next http.Handler, roots []string, d daemon) *filter {
	f := &filter{
		next:   next,
		roots:  roots,
		daemon: d,
		hostFS: seesHostFS(),
		lstat:  os.Lstat,
	}
	f.selfIDs = selfContainerIDs(!f.hostFS)
	if len(roots) > 0 && !f.hostFS {
		log.Warn().Strs("roots", roots).Msg("[SECURITY] proxy: the agent runs in a container and cannot see the host's filesystem, so bind sources cannot be checked for symlinks; keep --allowed-bind-roots to directories no container can write to")
	}
	return f
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
	}

	switch rt.check {
	case checkExecCreate, checkTarget:
		full, reason := f.checkTargetContainer(ctx, id)
		if reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
		// Forward the immutable ID that was checked, not the caller's name:
		// a name can be moved to another container between check and use.
		if full != "" {
			r.URL.Path = strings.Replace(p, "/containers/"+id+"/", "/containers/"+full+"/", 1)
			r.URL.RawPath = ""
		}
	case checkExecStart:
		if reason := f.checkExecStart(ctx, id); reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
	case checkPrivateRead:
		if reason := f.checkPrivateRead(ctx, id); reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
	case checkStart:
		if reason := f.checkStartMounts(ctx, id); reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
	case checkNetworkConnect:
		if reason := f.checkNetwork(ctx, id); reason != "" {
			deny(w, r, http.StatusForbidden, reason)
			return
		}
	case checkImagePull:
		// fromSrc imports a tarball, or has the daemon fetch a URL of the
		// caller's choosing. Pulls from a registry use fromImage.
		if r.URL.Query().Get("fromSrc") != "" {
			deny(w, r, http.StatusForbidden, "image import (fromSrc) rejected")
			return
		}
	}
	f.next.ServeHTTP(w, r)
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
	switch c {
	case checkContainerCreate, checkExecCreate:
		var b createBody
		if err := json.Unmarshal(body, &b); err != nil {
			return bad(err)
		}
		if reason := f.checkHostConfig(ctx, &b.hostConfig, fromRequest); reason != "" {
			return reason
		}
		if b.HostConfig != nil {
			if reason := f.checkHostConfig(ctx, b.HostConfig, fromRequest); reason != "" {
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
		return f.checkVolumeOptions(v.Driver, v.DriverOpts)
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

// checkTargetContainer reads the container a call acts on and refuses it
// when getting inside it reaches the host: the agent's own container, or one
// that is privileged, shares a host or another container's namespace, holds
// the Docker socket, mounts a host path outside the bind roots, has a
// capability outside the default set, or has a host device. It returns the
// container's full ID.
func (f *filter) checkTargetContainer(ctx context.Context, id string) (string, string) {
	c, err := f.daemon.container(ctx, id)
	switch {
	case errors.Is(err, errNotFound):
		return "", "" // the daemon answers 404 itself
	case err != nil:
		return "", "container " + id + " could not be checked: " + err.Error()
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
			if reason := f.checkBindSource(m.Source, !m.RW); reason != "" {
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
// --privileged is refused too.
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
func (f *filter) checkPrivateRead(ctx context.Context, id string) string {
	c, err := f.daemon.container(ctx, id)
	switch {
	case errors.Is(err, errNotFound):
		return ""
	case err != nil:
		return "container " + id + " could not be checked: " + err.Error()
	}
	if f.isSelf(c.ID) || holdsSocket(c.Mounts) {
		return "container " + id + " holds the Docker socket"
	}
	return ""
}

// checkStartMounts re-checks bind sources for symlinks when a container is
// started, because that is when the daemon mounts them: a link planted after
// create would otherwise be followed. Binds outside the roots were made on
// the host, not through the agent, and are left alone.
func (f *filter) checkStartMounts(ctx context.Context, id string) string {
	if !f.hostFS || len(f.roots) == 0 {
		return ""
	}
	c, err := f.daemon.container(ctx, id)
	switch {
	case errors.Is(err, errNotFound):
		return ""
	case err != nil:
		return "container " + id + " could not be checked: " + err.Error()
	}
	for _, m := range c.Mounts {
		if !strings.EqualFold(m.Type, "bind") || !strings.HasPrefix(m.Source, "/") {
			continue
		}
		clean := path.Clean(m.Source)
		if root := f.rootFor(clean); root != "" {
			if reason := f.checkSymlinks(root, clean); reason != "" {
				return "container " + id + ": " + reason
			}
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
	if !f.hostFS {
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
