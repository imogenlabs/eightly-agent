// SPDX-License-Identifier: Apache-2.0

package dockerdial

import (
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPipePath(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{`//./pipe/docker_engine`, `\\.\pipe\docker_engine`, true},
		{`\\.\pipe\docker_engine`, `\\.\pipe\docker_engine`, true},
		{`\\.\PIPE\dockerDesktopLinuxEngine`, `\\.\pipe\dockerDesktopLinuxEngine`, true},
		{`//./pipe/docker_engine_windows`, `\\.\pipe\docker_engine_windows`, true},
		{`\\./pipe\mixed`, `\\.\pipe\mixed`, true},
		{`//./pipe/`, "", false},
		{`//./pipe//`, "", false},
		{`//./pipe`, "", false},
		{`/var/run/docker.sock`, "", false},
		{`C:\ProgramData\docker\docker.sock`, "", false},
		{`\\server\pipe\docker_engine`, "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := pipePath(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("pipePath(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDialTimeoutUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable here: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		if c, err := ln.Accept(); err == nil {
			_, _ = c.Write([]byte("ok"))
			_ = c.Close()
		}
	}()
	conn, err := DialTimeout(sock, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", sock, err)
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 2)
	if _, err := conn.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("read %q, %v", buf, err)
	}
}

func TestDialTimeoutMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sock")
	if runtime.GOOS == "windows" {
		path = `//./pipe/eightly-agent-test-missing`
	}
	if conn, err := DialTimeout(path, 500*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("dialing %s succeeded", path)
	}
}
