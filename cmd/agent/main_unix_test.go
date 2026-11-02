//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/imogenlabs/eightly-agent/internal/server"
	"github.com/imogenlabs/eightly-agent/internal/tlsutil"
)

// Off Windows the service words print the systemd command that does the job,
// and never start the agent.
func TestMain_ServiceCommandsPrintSystemdSteps(t *testing.T) {
	clearAgentEnv(t)
	want := map[string]string{
		"install":   "systemctl enable eightly-agent",
		"uninstall": "systemctl disable eightly-agent",
		"start":     "systemctl start eightly-agent",
		"stop":      "systemctl stop eightly-agent",
		"status":    "systemctl status eightly-agent",
	}
	for cmd, line := range want {
		withArgs(t, cmd)
		got := captureStdout(t, main)
		if !strings.Contains(got, line) {
			t.Errorf("eightly-agent %s printed %q, want it to contain %q", cmd, got, line)
		}
	}
	if got := captureStdout(t, func() { handleServiceCommand("restart") }); got != "unknown service command: restart\n" {
		t.Errorf("handleServiceCommand(restart) printed %q", got)
	}
	if isWindowsService() {
		t.Error("isWindowsService is true off Windows")
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// main() in interactive mode serves until SIGINT or SIGTERM and then returns,
// so a service manager's stop is a clean exit rather than a kill.
func TestMain_ServesUntilInterrupted(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		warnsNoAuth  bool
		wantLogLevel zerolog.Level
	}{
		{
			name:         "insecure-no-auth at debug level",
			args:         []string{"--insecure-no-auth", "--log-level", "debug"},
			warnsNoAuth:  true,
			wantLogLevel: zerolog.DebugLevel,
		},
		{
			name:         "a secret and a log level it cannot read",
			args:         []string{"--secret", "s3cret", "--log-level", "loud"},
			wantLogLevel: zerolog.InfoLevel,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentEnv(t)
			origLogger, origLevel := log.Logger, zerolog.GlobalLevel()
			t.Cleanup(func() { log.Logger = origLogger; zerolog.SetGlobalLevel(origLevel) })
			var logs lockedBuffer
			log.Logger = zerolog.New(&logs)

			data := t.TempDir()
			port := freePort(t)
			withArgs(t, append([]string{
				"--addr", "127.0.0.1", "--port", port, "--data", data,
				"--socket", filepath.Join(data, "docker.sock"),
			}, tc.args...)...)

			done := make(chan struct{})
			go func() {
				defer close(done)
				main()
			}()

			// /healthz answering means main() is past signal.NotifyContext,
			// so the signal below is caught rather than killing the test.
			deadline := time.Now().Add(15 * time.Second)
			for {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				err := server.Healthcheck(ctx, "127.0.0.1", port, tlsutil.CertPath(data))
				cancel()
				if err == nil {
					break
				}
				select {
				case <-done:
					t.Fatalf("main() returned before serving; logs:\n%s", logs.String())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatalf("agent never answered /healthz: %v; logs:\n%s", err, logs.String())
				}
				time.Sleep(50 * time.Millisecond)
			}

			if got := zerolog.GlobalLevel(); got != tc.wantLogLevel {
				t.Errorf("log level %s, want %s", got, tc.wantLogLevel)
			}
			if warned := strings.Contains(logs.String(), "Authentication is DISABLED"); warned != tc.warnsNoAuth {
				t.Errorf("auth-disabled warning logged = %v, want %v; logs:\n%s", warned, tc.warnsNoAuth, logs.String())
			}

			if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("main() did not return after SIGINT")
			}
		})
	}
}
