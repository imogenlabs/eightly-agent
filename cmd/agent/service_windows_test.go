//go:build windows

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/imogenlabs/eightly-agent/internal/config"
)

// These drive the service's Execute loop with channels, the way the service
// control manager would, so the stop and failure handling are tested without
// installing a service.

func serviceConfig(t *testing.T) *config.Config {
	t.Helper()
	data := t.TempDir()
	return &config.Config{
		Addr:           "127.0.0.1",
		Port:           freePort(t),
		DataPath:       data,
		StatePath:      filepath.Join(data, "eightly-agent.json"),
		SocketPath:     filepath.Join(data, "docker.sock"),
		LogLevel:       "info",
		InsecureNoAuth: true,
	}
}

func nextStatus(t *testing.T, changes <-chan svc.Status) svc.State {
	t.Helper()
	select {
	case s := <-changes:
		return s.State
	case <-time.After(15 * time.Second):
		t.Fatal("the service reported no status")
		return 0
	}
}

func TestExecute_StopsWhenAsked(t *testing.T) {
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 8)
	code := make(chan uint32, 1)
	go func() {
		_, c := (&agentService{cfg: serviceConfig(t)}).Execute(nil, requests, changes)
		code <- c
	}()

	if s := nextStatus(t, changes); s != svc.StartPending {
		t.Fatalf("first status %d, want StartPending", s)
	}
	if s := nextStatus(t, changes); s != svc.Running {
		t.Fatalf("second status %d, want Running", s)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: svc.Status{State: svc.Running}}
	if s := nextStatus(t, changes); s != svc.Running {
		t.Fatalf("interrogate answered %d, want Running", s)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if s := nextStatus(t, changes); s != svc.StopPending {
		t.Fatalf("after stop %d, want StopPending", s)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit code %d after a requested stop, want 0", c)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Execute did not return after stop")
	}
}

// A server that cannot start must stop the service with a non-zero code, so
// the service manager's recovery actions see a failure.
func TestExecute_ReportsServerFailure(t *testing.T) {
	cfg := serviceConfig(t)
	cfg.ServerCA = filepath.Join(cfg.DataPath, "missing-ca.pem")
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 8)
	_, c := (&agentService{cfg: cfg}).Execute(nil, requests, changes)
	if c != 1 {
		t.Fatalf("exit code %d when the server cannot start, want 1", c)
	}
}

func TestProtectDataDirs_CreatesTheDataFolder(t *testing.T) {
	cfg := serviceConfig(t)
	cfg.DataPath = filepath.Join(cfg.DataPath, "nested", "data")
	cfg.StatePath = filepath.Join(cfg.DataPath, "eightly-agent.json")
	protectDataDirs(cfg)
	if fi, err := os.Stat(cfg.DataPath); err != nil || !fi.IsDir() {
		t.Fatalf("data folder not created: %v", err)
	}
}

// install reads its own flags before it touches the service manager, so a
// mistyped flag fails there and changes nothing.
func TestInstallService_RejectsUnknownFlag(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"eightly-agent", "install", "--no-such-flag"}
	if err := installService(); err == nil {
		t.Fatal("install accepted an unknown flag")
	}
}
