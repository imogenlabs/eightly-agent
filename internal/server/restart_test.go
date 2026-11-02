//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/register"
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

// Docker creates a new volume directory 0755. The agent saved its
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

func tokenExpiringAt(exp time.Time) string {
	claims, _ := json.Marshal(map[string]int64{"iat": exp.Add(-30 * 24 * time.Hour).Unix(), "exp": exp.Unix()})
	return base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// Re-enrolling an agent whose token lapsed means running the install
// command again with a fresh enrollment token. If the saved state survived in
// the volume, the agent used to load it and never look at the new token, so
// re-enrolling could not work. A lapsed saved token plus an enrollment token
// means enroll.
func TestLoadOrRegister_ExpiredSavedTokenWithEnrollTokenEnrollsAgain(t *testing.T) {
	srv, calls := registerCounter(t)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	lapsed := &register.State{AgentID: "agent-saved", Token: tokenExpiringAt(time.Now().Add(-24 * time.Hour)), ServerURL: srv.URL}
	if err := lapsed.Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_fresh", StatePath: statePath, SocketPath: "/nonexistent.sock", Port: "9001", Version: "test"}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	state := loadOrRegister(cfg, tlsCfg)

	if n := calls.Load(); n != 1 {
		t.Fatalf("lapsed saved token + enrollment token made %d register calls, want 1", n)
	}
	if state == nil || state.Token != "new-token" {
		t.Fatalf("did not take the new enrollment, got %+v", state)
	}
}

// A saved token that is still valid is kept even when an enrollment token is
// still in the environment: the install command leaves it there, and it is
// spent.
func TestLoadOrRegister_ValidSavedTokenIgnoresLeftoverEnrollToken(t *testing.T) {
	srv, calls := registerCounter(t)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	live := &register.State{AgentID: "agent-saved", Token: tokenExpiringAt(time.Now().Add(20 * 24 * time.Hour)), ServerURL: srv.URL}
	if err := live.Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_spent", StatePath: statePath, SocketPath: "/nonexistent.sock", Port: "9001", Version: "test"}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	if state := loadOrRegister(cfg, tlsCfg); state == nil || state.AgentID != "agent-saved" || calls.Load() != 0 {
		t.Fatalf("a valid saved registration was replaced (%d register calls)", calls.Load())
	}
}

// Re-enrolling a lapsed agent against the SAME server must
// still hold the server to the pin saved at first enrollment. Before, the
// expired state was dropped before anything seeded the pin, so in the default
// pin-only mode re-enrollment trusted whatever certificate answered and sent
// the fresh single-use enrollment token to it. A server whose key does not
// match the saved pin is refused, and the token never leaves the agent.
func TestLoadOrRegister_ReEnrollSameServerRefusesCertNotMatchingSavedPin(t *testing.T) {
	srv, calls := registerCounter(t)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	lapsed := &register.State{
		AgentID:   "agent-saved",
		Token:     tokenExpiringAt(time.Now().Add(-24 * time.Hour)),
		ServerURL: srv.URL,
		// The pin of the server this agent first enrolled with. The server
		// answering now presents a different key.
		ServerPin: "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	if err := lapsed.Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_fresh", StatePath: statePath, SocketPath: "/nonexistent.sock", Port: "9001", Version: "test"}
	tlsCfg, err := register.BuildServerTLSConfig("", false, zerolog.Nop()) // default: pin-only TOFU
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	state := loadOrRegister(cfg, tlsCfg)

	if n := calls.Load(); n != 0 {
		t.Fatalf("re-enrollment sent the enrollment token to a server whose key does not match the saved pin (%d register calls)", n)
	}
	if state != nil {
		t.Fatalf("re-enrollment against a mismatched pin produced a registration: %+v", state)
	}
	onDisk, err := register.LoadState(statePath)
	if err != nil || onDisk == nil || onDisk.ServerPin != lapsed.ServerPin {
		t.Fatalf("the saved pin was not kept on disk: %+v, %v", onDisk, err)
	}
}

// The other half: the same server, presenting the key it was pinned to, is
// re-enrolled with, and the pin carries over into the new registration.
func TestLoadOrRegister_ReEnrollSameServerMatchingSavedPinKeepsPin(t *testing.T) {
	srv, calls := registerCounter(t)
	sum := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
	pin := "sha256/" + base64.StdEncoding.EncodeToString(sum[:])
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	lapsed := &register.State{AgentID: "agent-saved", Token: tokenExpiringAt(time.Now().Add(-24 * time.Hour)), ServerURL: srv.URL, ServerPin: pin}
	if err := lapsed.Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_fresh", StatePath: statePath, SocketPath: "/nonexistent.sock", Port: "9001", Version: "test"}
	tlsCfg, err := register.BuildServerTLSConfig("", false, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	state := loadOrRegister(cfg, tlsCfg)

	if n := calls.Load(); n != 1 || state == nil || state.Token != "new-token" {
		t.Fatalf("re-enrollment with a matching pin did not go through (%d register calls, state %+v)", n, state)
	}
	if state.ServerPin != pin {
		t.Fatalf("new registration pin %q, want the saved %q", state.ServerPin, pin)
	}
}

// In the image, /data is both --data and the state folder, so it
// holds the agent's TLS key pair beside the state file. That is still wholly
// the agent's folder, so a restart repairs it and resumes.
func TestLoadOrRegister_VolumeWithTLSPairIsStillRepaired(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	srv, calls := registerCounter(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	for _, name := range []string{"cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	statePath := filepath.Join(dir, "eightly-agent.json")
	saved := &register.State{AgentID: "agent-saved", Token: "saved-token", ServerURL: srv.URL}
	if err := saved.Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_spent", StatePath: statePath, DataPath: dir, SocketPath: "/nonexistent.sock", Port: "9001", Version: "test"}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	state := loadOrRegister(cfg, tlsCfg)

	if calls.Load() != 0 || state == nil || state.AgentID != "agent-saved" {
		t.Fatalf("the agent's own volume was not repaired: %d register calls, state %+v", calls.Load(), state)
	}
}
