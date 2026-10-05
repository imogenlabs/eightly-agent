//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/smashingtags/eightly-agent/internal/config"
	"github.com/smashingtags/eightly-agent/internal/register"
)

// registerCounter is an EOS server that counts enrollment calls and accepts
// every one, so a test can tell "resumed" from "enrolled again".
func registerCounter(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/register" {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]string{"agent_id": "agent-new", "token": "new-token"},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// EOS-2534. Docker creates a new volume directory 0755. The agent saved its
// registration there, and on the next start its own permission guard refused
// the directory, so it threw the saved identity away and enrolled again with a
// single-use token that was already spent: "register failed: status 403".
// A restart must resume from the saved state instead.
func TestLoadOrRegister_RestartResumesSavedStateInFreshVolumeDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	srv, calls := registerCounter(t)

	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // defeat the umask: this is the mode Docker gives a volume
		t.Fatalf("chmod: %v", err)
	}
	statePath := filepath.Join(dir, "eightly-agent.json")
	saved := &register.State{AgentID: "agent-saved", Token: "saved-token", ServerURL: srv.URL}
	if err := saved.Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}

	cfg := &config.Config{
		ServerURL:   srv.URL,
		EnrollToken: "eos_enroll_already_spent",
		StatePath:   statePath,
		SocketPath:  "/nonexistent.sock",
		Port:        "9001",
		Version:     "test",
	}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	state := loadOrRegister(cfg, tlsCfg)

	if n := calls.Load(); n != 0 {
		t.Fatalf("a restart with saved state enrolled again (%d register calls); it must resume", n)
	}
	if state == nil || state.AgentID != "agent-saved" || state.Token != "saved-token" {
		t.Fatalf("restart did not resume the saved registration, got %+v", state)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("state directory left at %04o, want 0700", perm)
	}
}

// The other half: with nothing saved, the agent still enrolls exactly once.
func TestLoadOrRegister_FirstStartEnrolls(t *testing.T) {
	srv, calls := registerCounter(t)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	cfg := &config.Config{
		ServerURL:   srv.URL,
		EnrollToken: "eos_enroll_fresh",
		StatePath:   statePath,
		SocketPath:  "/nonexistent.sock",
		Port:        "9001",
		Version:     "test",
	}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	state := loadOrRegister(cfg, tlsCfg)

	if n := calls.Load(); n != 1 {
		t.Fatalf("first start made %d register calls, want 1", n)
	}
	if state == nil || state.AgentID != "agent-new" {
		t.Fatalf("first start did not take the new registration, got %+v", state)
	}
}
