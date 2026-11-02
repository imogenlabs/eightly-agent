// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// TargetChecker applies the proxy's target rules to sessions that reach
// Docker without passing through the proxy, such as the WebSocket attach and
// exec bridges. A container is refused when getting inside it reaches the
// host: the agent's own container, one holding the Docker socket, a
// privileged one, one sharing a host namespace, or one mounting a host path
// outside the bind roots.
type TargetChecker struct{ f *filter }

// NewTargetChecker asks the daemon on socketPath, with the same bind roots
// the proxy uses.
func NewTargetChecker(socketPath string, bindRoots []string, opts ...Option) *TargetChecker {
	return &TargetChecker{f: newChecker(bindRoots, &dockerAPI{client: &http.Client{Transport: dockerTransport(socketPath)}}, opts)}
}

// Container checks the container id names. It returns the container's full
// ID, which the caller must use from then on (a name can be moved to another
// container between the check and the use), or the reason it is refused.
// When no container has that name both are "", and the caller must answer
// 404 rather than go on by name.
func (t *TargetChecker) Container(ctx context.Context, id string) (fullID, reason string) {
	full, reason := t.f.checkTargetContainer(ctx, id)
	if strings.HasPrefix(reason, noSuchContainer) {
		return "", ""
	}
	return full, reason
}

// Exec checks the container an exec instance runs in, and refuses an exec
// created privileged or one the daemon does not know. It returns "" when the
// exec may be started.
func (t *TargetChecker) Exec(ctx context.Context, id string) (reason string) {
	if _, err := t.f.daemon.exec(ctx, id); errors.Is(err, errNotFound) {
		return "no such exec instance: " + id
	}
	return t.f.checkExecStart(ctx, id)
}

// Volume applies the volume policy to a named volume the file browser is
// asked to open: a volume of the agent's own, or a local volume bound to a
// host path outside the bind roots, is refused with 403. It answers 404 for
// a volume the daemon does not know, and 0 when the volume may be opened.
func (t *TargetChecker) Volume(ctx context.Context, name string) (status int, reason string) {
	if _, err := t.f.daemon.volume(ctx, name); errors.Is(err, errNotFound) {
		return http.StatusNotFound, "volume not found"
	}
	if reason := t.f.checkNamedVolume(ctx, name); reason != "" {
		if strings.Contains(reason, "could not be checked") {
			return http.StatusServiceUnavailable, reason
		}
		return http.StatusForbidden, reason
	}
	return 0, ""
}
