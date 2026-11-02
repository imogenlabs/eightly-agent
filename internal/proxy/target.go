// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"net/http"
	"os"
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
func NewTargetChecker(socketPath string, bindRoots []string) *TargetChecker {
	f := &filter{
		roots:  bindRoots,
		daemon: &dockerAPI{client: &http.Client{Transport: dockerTransport(socketPath)}},
		hostFS: seesHostFS(),
		lstat:  os.Lstat,
	}
	f.selfIDs = selfContainerIDs(!f.hostFS)
	return &TargetChecker{f: f}
}

// Container checks the container id names. It returns the container's full
// ID, which the caller should use from then on (a name can be moved to
// another container between the check and the use), or the reason it is
// refused. A container that does not exist is not refused: the daemon
// answers that itself, and fullID is then "".
func (t *TargetChecker) Container(ctx context.Context, id string) (fullID, reason string) {
	return t.f.checkTargetContainer(ctx, id)
}

// Exec checks the container an exec instance runs in, and refuses an exec
// created privileged. It returns "" when the exec may be started.
func (t *TargetChecker) Exec(ctx context.Context, id string) (reason string) {
	return t.f.checkExecStart(ctx, id)
}
