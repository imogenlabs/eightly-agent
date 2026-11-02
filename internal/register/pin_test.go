// SPDX-License-Identifier: Apache-2.0

package register

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// newTLSLeaf builds a self-signed TLS leaf cert for use as a test server cert.
func newTLSLeaf(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"127.0.0.1", "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: mustParse(t, der)}
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsecert: %v", err)
	}
	return c
}

func registerOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]string{"agent_id": "a1", "token": "t1"},
		})
	})
}

// TestRegister_CapturesPin_OnFirstConnect proves the TOFU capture half of fix
// #1: the leaf SPKI seen during the first successful register is persisted on
// the returned State.
func TestRegister_CapturesPin_OnFirstConnect(t *testing.T) {
	leaf := newTLSLeaf(t, "eos-server")
	srv := httptest.NewUnstartedServer(registerOKHandler())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{leaf}}
	srv.StartTLS()
	defer srv.Close()

	cfg, err := BuildServerTLSConfig("", false, zerolog.Nop())
	if err != nil {
		t.Fatalf("build tls: %v", err)
	}

	state, err := Register(srv.URL, "", AgentInfo{Hostname: "h"}, cfg)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	want := spkiFingerprint(leaf.Leaf)
	if state.ServerPin != want {
		t.Fatalf("ServerPin: got %q, want %q", state.ServerPin, want)
	}
	if !strings.HasPrefix(state.ServerPin, "sha256/") {
		t.Errorf("pin should be sha256/<base64>, got %q", state.ServerPin)
	}
}

// TestPinnedConfig_RejectsDifferentCert is the SABOTAGE-PROVEN test for fix #1.
// After the pin is captured against server A's cert, a connection to server B
// (different cert/SPKI) using the SAME pinned config must be rejected.
//
// Sabotage check: reverting the fix (removing VerifyConnection from
// BuildServerTLSConfig, i.e. not installing verifyPinned) makes this test go
// RED because the second connection would succeed. Verified red-on-vuln.
func TestPinnedConfig_RejectsDifferentCert(t *testing.T) {
	// Server A — establishes the pin.
	leafA := newTLSLeaf(t, "server-A")
	srvA := httptest.NewUnstartedServer(registerOKHandler())
	srvA.TLS = &tls.Config{Certificates: []tls.Certificate{leafA}}
	srvA.StartTLS()
	defer srvA.Close()

	// Server B — different cert, impersonating the same endpoint.
	leafB := newTLSLeaf(t, "server-B")
	srvB := httptest.NewUnstartedServer(registerOKHandler())
	srvB.TLS = &tls.Config{Certificates: []tls.Certificate{leafB}}
	srvB.StartTLS()
	defer srvB.Close()

	cfg, err := BuildServerTLSConfig("", false, zerolog.Nop())
	if err != nil {
		t.Fatalf("build tls: %v", err)
	}

	// First connect to A captures the pin.
	state, err := Register(srvA.URL, "", AgentInfo{Hostname: "h"}, cfg)
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	if state.ServerPin != spkiFingerprint(leafA.Leaf) {
		t.Fatalf("pin not captured from A")
	}

	// A heartbeat to B with the same pinned config must fail on the pin mismatch.
	stateB := &State{AgentID: "a1", Token: "t1", ServerURL: srvB.URL, ServerPin: state.ServerPin}
	err = sendHeartbeat(context.Background(), stateB, cfg)
	if err == nil {
		t.Fatal("VULNERABLE: heartbeat to a server with a DIFFERENT cert succeeded — pin not enforced")
	}
	if !strings.Contains(err.Error(), "pin mismatch") {
		t.Errorf("expected a pin-mismatch error, got: %v", err)
	}

	// Sanity: a heartbeat back to A (matching cert) still works.
	stateA := &State{AgentID: "a1", Token: "t1", ServerURL: srvA.URL, ServerPin: state.ServerPin}
	if err := sendHeartbeat(context.Background(), stateA, cfg); err != nil {
		t.Errorf("heartbeat to the pinned server A should succeed, got: %v", err)
	}
}

// TestSeedPin_EnforcesPersistedPinAcrossRestart proves the restart path: a fresh
// config seeded from persisted state enforces the saved pin on the very first
// dial, even though it never observed the original handshake.
func TestSeedPin_EnforcesPersistedPinAcrossRestart(t *testing.T) {
	leaf := newTLSLeaf(t, "server-A")
	srv := httptest.NewUnstartedServer(registerOKHandler())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{leaf}}
	srv.StartTLS()
	defer srv.Close()

	cfg, err := BuildServerTLSConfig("", false, zerolog.Nop())
	if err != nil {
		t.Fatalf("build tls: %v", err)
	}

	// Persisted pin from a *different* prior server. Seeding it must make the
	// first dial to this server fail (pin mismatch), simulating an MITM after
	// restart.
	otherLeaf := newTLSLeaf(t, "attacker")
	bogusPin := spkiFingerprint(otherLeaf.Leaf)
	seedPin(cfg, bogusPin)

	st := &State{AgentID: "a1", Token: "t1", ServerURL: srv.URL, ServerPin: bogusPin}
	if err := sendHeartbeat(context.Background(), st, cfg); err == nil {
		t.Fatal("VULNERABLE: persisted pin not enforced across restart — first dial accepted a mismatched cert")
	}
}

// TestInsecureServerTLS_SkipsPinning confirms the escape hatch still bypasses
// pinning entirely (no VerifyConnection installed).
func TestInsecureServerTLS_SkipsPinning(t *testing.T) {
	cfg, err := BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("build tls: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("insecure config must set InsecureSkipVerify")
	}
	if cfg.VerifyConnection != nil {
		t.Error("insecure config must NOT install pinning (escape hatch)")
	}
}

// TestLoadState_RejectsBroadFilePerms covers fix #5: a state file looser than
// 0600 is refused.
func TestLoadState_RejectsBroadFilePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores unix modes; the access-list check covers it (internal/winacl)")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks behave differently")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	path := filepath.Join(dir, "state.json")
	s := &State{AgentID: "x"}
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	_, err := LoadState(path)
	if err == nil {
		t.Fatal("expected refusal to load a 0644 state file")
	}
	if !strings.Contains(err.Error(), "broader") {
		t.Errorf("error should mention broad perms, got: %v", err)
	}
}

// TestLoadState_RejectsBroadDirPerms covers fix #5 for the parent dir.
func TestLoadState_RejectsBroadDirPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores unix modes; the access-list check covers it (internal/winacl)")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks behave differently")
	}
	dir := t.TempDir() // 0755 by default — too broad
	path := filepath.Join(dir, "state.json")
	s := &State{AgentID: "x"}
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	_, err := LoadState(path)
	if err == nil {
		t.Fatal("expected refusal to load a state file in a 0755 directory")
	}
}

// TestLoadState_AcceptsTightPerms confirms a correctly-locked-down state file loads.
func TestLoadState_AcceptsTightPerms(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	path := filepath.Join(dir, "state.json")
	s := &State{AgentID: "ok", Token: "t", ServerPin: "sha256/abc"}
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("expected tight-perm state to load, got: %v", err)
	}
	if loaded == nil || loaded.AgentID != "ok" || loaded.ServerPin != "sha256/abc" {
		t.Errorf("unexpected loaded state: %+v", loaded)
	}
}
