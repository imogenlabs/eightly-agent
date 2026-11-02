// SPDX-License-Identifier: Apache-2.0

// Package dockerdial connects to the Docker engine: a named pipe on Windows
// (//./pipe/docker_engine by default), a Unix socket everywhere else.
package dockerdial

import (
	"context"
	"net"
	"strings"
	"time"
)

const pipePrefix = `\\.\pipe\`

// pipePath reports whether path names a local named pipe, written either as
// //./pipe/name or \\.\pipe\name, and returns it in the \\.\pipe\name form
// Windows expects.
func pipePath(path string) (string, bool) {
	p := strings.ReplaceAll(path, "/", `\`)
	if len(p) <= len(pipePrefix) || !strings.EqualFold(p[:len(pipePrefix)], pipePrefix) {
		return "", false
	}
	name := p[len(pipePrefix):]
	if strings.Trim(name, `\`) == "" {
		return "", false
	}
	return pipePrefix + name, true
}

// DialTimeout connects to the Docker engine at path, giving up after timeout.
// The timeout covers only the connect, as with net.DialTimeout.
func DialTimeout(path string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return DialContext(ctx, path)
}
