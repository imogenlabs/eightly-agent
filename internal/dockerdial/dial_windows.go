//go:build windows

// SPDX-License-Identifier: Apache-2.0

package dockerdial

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// DialContext connects to the Docker engine at path: a named pipe when path
// names one, otherwise a Unix socket (Windows supports those too).
func DialContext(ctx context.Context, path string) (net.Conn, error) {
	if pipe, ok := pipePath(path); ok {
		return winio.DialPipeContext(ctx, pipe)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
