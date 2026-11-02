//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/register"
)

// Docker creates a new volume directory 0755. The agent saved its
// registration there, and on the next start its own permission guard refused
// the directory, so it threw the saved identity away and enrolled again with a
// single-use token that was already spent: "register failed: status 403".
// A restart must resume from the saved state instead.
func TestResume_RestartResumesSavedStateInFreshVolumeDir(t *testing.T) {
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

	state, _, _ := resume(cfg, tlsCfg)

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
func TestResume_FirstStartEnrolls(t *testing.T) {
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

	state, _, _ := resume(cfg, tlsCfg)

	if n := calls.Load(); n != 1 {
		t.Fatalf("first start made %d register calls, want 1", n)
	}
	if state == nil || state.AgentID != "agent-new" {
		t.Fatalf("first start did not take the new registration, got %+v", state)
	}
}

// Codex round 3: with --secret-file in a fresh 0755 data folder the agent
// enrolled, then the next start refused the folder (the secret counted as
// somebody else's) and the saved state with it. The folder is made right
// before the token is spent, and the restart resumes.
func TestResume_SecretFileInDataFolderSurvivesRestart(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	srv, calls := registerCounter(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ServerURL:   srv.URL,
		EnrollToken: "eos_enroll_fresh",
		DataPath:    dir,
		StatePath:   filepath.Join(dir, "eightly-agent.json"),
		SecretFile:  secret,
		SocketPath:  "/nonexistent.sock",
		Port:        "9001",
		Version:     "test",
	}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}
	if state, _, err := resume(cfg, tlsCfg); err != nil || state == nil {
		t.Fatalf("first start: %+v, %v", state, err)
	}
	state, _, err := resume(cfg, tlsCfg)
	if err != nil || state == nil || state.AgentID != "agent-new" {
		t.Fatalf("restart did not resume the saved registration: %+v, %v", state, err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d register calls, want 1", n)
	}
}

// A state folder shared with other files that the next start would refuse is
// refused before enrolling, so the single-use token is not spent.
func TestResume_SharedStateFolderDoesNotSpendTheToken(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	srv, calls := registerCounter(t)
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ServerURL:   srv.URL,
		EnrollToken: "eos_enroll_fresh",
		StatePath:   filepath.Join(dir, "eightly-agent.json"),
		SocketPath:  "/nonexistent.sock",
		Port:        "9001",
		Version:     "test",
	}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}
	if state, _, _ := resume(cfg, tlsCfg); state != nil {
		t.Fatalf("enrolled into a folder the next start refuses: %+v", state)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d register calls; the token must not be spent", n)
	}
}

// Re-enrolling an agent whose token lapsed means running the install
// command again with a fresh enrollment token. If the saved state survived in
// the volume, the agent used to load it and never look at the new token, so
// re-enrolling could not work. A lapsed saved token plus an enrollment token
// means enroll.
func TestResume_ExpiredSavedTokenWithEnrollTokenEnrollsAgain(t *testing.T) {
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

	state, _, _ := resume(cfg, tlsCfg)

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
func TestResume_ValidSavedTokenIgnoresLeftoverEnrollToken(t *testing.T) {
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

	if state, _, _ := resume(cfg, tlsCfg); state == nil || state.AgentID != "agent-saved" || calls.Load() != 0 {
		t.Fatalf("a valid saved registration was replaced (%d register calls)", calls.Load())
	}
}

// Re-enrolling a lapsed agent against the SAME server must
// still hold the server to the pin saved at first enrollment. Before, the
// expired state was dropped before anything seeded the pin, so in the default
// pin-only mode re-enrollment trusted whatever certificate answered and sent
// the fresh single-use enrollment token to it. A server whose key does not
// match the saved pin is refused, and the token never leaves the agent.
func TestResume_ReEnrollSameServerRefusesCertNotMatchingSavedPin(t *testing.T) {
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

	state, _, _ := resume(cfg, tlsCfg)

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
func TestResume_ReEnrollSameServerMatchingSavedPinKeepsPin(t *testing.T) {
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

	state, _, _ := resume(cfg, tlsCfg)

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
func TestResume_VolumeWithTLSPairIsStillRepaired(t *testing.T) {
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

	state, _, _ := resume(cfg, tlsCfg)

	if calls.Load() != 0 || state == nil || state.AgentID != "agent-saved" {
		t.Fatalf("the agent's own volume was not repaired: %d register calls, state %+v", calls.Load(), state)
	}
}

// A state file the agent cannot read is not a missing one: refuse to start
// rather than enroll over the identity it holds.
func TestResume_UnreadableStateRefusesToStart(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	srv, calls := registerCounter(t)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	saved := &register.State{AgentID: "agent-saved", Token: "saved-token", ServerURL: srv.URL}
	if err := saved.Save(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(statePath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(statePath, 0o600) })
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_x", StatePath: statePath, SocketPath: "/nonexistent.sock"}
	tlsCfg, _ := register.BuildServerTLSConfig("", true, zerolog.Nop())

	state, _, err := resume(cfg, tlsCfg)
	if err == nil || state != nil || calls.Load() != 0 {
		t.Fatalf("got state %+v, err %v, %d register calls; want a startup error and no enrollment", state, err, calls.Load())
	}
}
