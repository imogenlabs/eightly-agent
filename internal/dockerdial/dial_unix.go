//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package dockerdial

import (
	"context"
	"net"
)

// DialContext connects to the Docker engine's Unix socket at path.
func DialContext(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
