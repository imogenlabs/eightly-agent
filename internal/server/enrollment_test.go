// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

func tokenExpiringAt(exp time.Time) string {
	claims, _ := json.Marshal(map[string]int64{"iat": exp.Add(-30 * 24 * time.Hour).Unix(), "exp": exp.Unix()})
	return base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// spentTokenEOS answers every register call the way the server answers an
// enrollment token it has already consumed.
func spentTokenEOS(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/register" {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "valid enrollment token or agent token required"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func insecureTLS(t *testing.T) *tls.Config {
	t.Helper()
	c, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The server consumed the token but its answer was lost; every retry is now
// refused. The agent used to retry that forever. It stops after the first
// refusal instead of waiting out the context.
func TestRegisterWithRetry_StopsOnSpentEnrollToken(t *testing.T) {
	fastRetry(t)
	srv, calls := spentTokenEOS(t)
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_spent", StatePath: filepath.Join(t.TempDir(), "data", "eightly-agent.json"), SocketPath: "/nonexistent.sock"}
	tlsCfg := insecureTLS(t)

	if _, _, err := resume(cfg, tlsCfg); !errors.Is(err, register.ErrEnrollmentRefused) {
		t.Fatalf("resume got %v, want ErrEnrollmentRefused", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if state, _ := registerWithRetry(ctx, cfg, tlsCfg); state != nil {
		t.Fatalf("got a registration from a server that refuses the token: %+v", state)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("registerWithRetry kept retrying a refused token for %v", took)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d register calls, want 2 (one from resume, one from the retry loop)", n)
	}
}

// End to end: the agent still serves, answers 503 on authenticated routes,
// and does not keep sending the refused token.
func TestRun_RefusedEnrollmentKeepsServing503WithoutRetrying(t *testing.T) {
	fastRetry(t)
	srv, calls := spentTokenEOS(t)
	dir := t.TempDir()
	port := freePort(t)
	cfg := &config.Config{
		Addr: "127.0.0.1", Port: port, Secret: idSecret,
		ServerURL: srv.URL, InsecureServerTLS: true, EnrollToken: "eos_enroll_spent",
		StatePath: filepath.Join(dir, "state", "eightly-agent.json"), DataPath: dir,
		SocketPath: "/nonexistent.sock", Version: "test",
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, cfg) }()
	defer func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop")
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- test server
	waitForServer(t, client, port)
	time.Sleep(200 * time.Millisecond) // several retry periods at fastRetry pacing

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%s/host", port), nil) //nolint:noctx // test
	req.Header.Set("Authorization", "Bearer "+agentToken("agent-anyone"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /host: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/host answered %d, want 503", resp.StatusCode)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d register calls after a refusal, want 1", n)
	}
}

func writeRawState(t *testing.T, data []byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "eightly-agent.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A damaged state file still names this agent and its server pin. Enrolling
// over it threw both away; the agent now refuses to start and leaves it alone.
func TestResume_MalformedStateRefusesToStartAndKeepsFile(t *testing.T) {
	srv, calls := registerCounter(t)
	bad := []byte(`{"agent_id":"agent-saved","token":`)
	path := writeRawState(t, bad)
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_x", StatePath: path, SocketPath: "/nonexistent.sock"}

	state, _, err := resume(cfg, insecureTLS(t))
	if err == nil || errors.Is(err, register.ErrEnrollmentRefused) || state != nil {
		t.Fatalf("got state %+v, err %v; want a startup error", state, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("enrolled over a damaged state file (%d register calls)", calls.Load())
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, bad) {
		t.Fatalf("the damaged state file was changed: %q", got)
	}
}

func TestRun_MalformedStateRefusesToStart(t *testing.T) {
	srv, calls := registerCounter(t)
	path := writeRawState(t, []byte("not json"))
	cfg := &config.Config{
		Addr: "127.0.0.1", Port: freePort(t), Secret: idSecret,
		ServerURL: srv.URL, InsecureServerTLS: true, EnrollToken: "eos_enroll_x",
		StatePath: path, DataPath: t.TempDir(), SocketPath: "/nonexistent.sock", Version: "test",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, cfg); err == nil || ctx.Err() != nil {
		t.Fatalf("Run with a damaged state file returned %v (ctx %v); want an immediate error", err, ctx.Err())
	}
	if calls.Load() != 0 {
		t.Fatalf("%d register calls", calls.Load())
	}
}

// A saved registration for server A must not be sent to server B.
func TestResume_StateForAnotherServerWithoutEnrollTokenRefuses(t *testing.T) {
	srv, calls := registerCounter(t)
	path := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	saved := &register.State{AgentID: "agent-a", Token: tokenExpiringAt(time.Now().Add(20 * 24 * time.Hour)), ServerURL: "https://server-a.example:8443"}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	cfg := &config.Config{ServerURL: srv.URL, StatePath: path, SocketPath: "/nonexistent.sock"}

	state, _, err := resume(cfg, insecureTLS(t))
	if err == nil || errors.Is(err, register.ErrEnrollmentRefused) || state != nil {
		t.Fatalf("got state %+v, err %v; want a startup error", state, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("%d register calls", calls.Load())
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("the saved registration was changed")
	}
}

// The operator moves the agent: a new install command carries an enrollment
// token for the new server. The old state is backed up, the old server's pin
// is not applied to the new server, and the new registration is saved.
func TestResume_StateForAnotherServerWithEnrollTokenSwitches(t *testing.T) {
	srv, calls := registerCounter(t)
	path := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	saved := &register.State{
		AgentID: "agent-a", Token: tokenExpiringAt(time.Now().Add(20 * 24 * time.Hour)),
		ServerURL: "https://server-a.example", ServerPin: "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_new", StatePath: path, SocketPath: "/nonexistent.sock"}
	tlsCfg, err := register.BuildServerTLSConfig("", false, zerolog.Nop()) // pin-only TOFU
	if err != nil {
		t.Fatal(err)
	}

	state, _, err := resume(cfg, tlsCfg)
	if err != nil || state == nil || state.AgentID != "agent-new" || calls.Load() != 1 {
		t.Fatalf("did not enroll with the new server: state %+v, err %v, %d calls", state, err, calls.Load())
	}
	if state.ServerPin == saved.ServerPin || state.ServerPin == "" {
		t.Errorf("new registration pin %q; want the new server's own", state.ServerPin)
	}
	onDisk, err := register.LoadState(path)
	if err != nil || onDisk == nil || onDisk.ServerURL != srv.URL {
		t.Fatalf("new registration not saved: %+v, %v", onDisk, err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil || !bytes.Equal(bak, before) {
		t.Fatalf("backup holds %q (%v), want the old state", bak, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path + ".bak"); fi.Mode().Perm() != 0o600 {
			t.Errorf("backup mode %04o, want 0600", fi.Mode().Perm())
		}
	}
}

// The same server written differently is still the same server.
func TestResume_SameServerSpelledDifferentlyResumes(t *testing.T) {
	srv, calls := registerCounter(t)
	path := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	saved := &register.State{AgentID: "agent-saved", Token: tokenExpiringAt(time.Now().Add(20 * 24 * time.Hour)), ServerURL: srv.URL + "/"}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ServerURL: srv.URL, StatePath: path, SocketPath: "/nonexistent.sock"}
	state, _, err := resume(cfg, insecureTLS(t))
	if err != nil || state == nil || state.AgentID != "agent-saved" || calls.Load() != 0 {
		t.Fatalf("got state %+v, err %v, %d calls; want the saved registration", state, err, calls.Load())
	}
}

func TestSameServer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://eos.example", "https://eos.example/", true},
		{"https://EOS.Example", "https://eos.example", true},
		{"HTTPS://eos.example", "https://eos.example", true},
		{"https://eos.example:443", "https://eos.example", true},
		{"http://eos.example:80/", "http://eos.example", true},
		{"https://eos.example:8443", "https://eos.example:8443/", true},
		{"https://[::1]:443", "https://[::1]", true},
		{"https://eos.example/eos", "https://eos.example/eos/", true},
		{"https://eos.example", "https://other.example", false},
		{"https://eos.example:8443", "https://eos.example", false},
		{"http://eos.example", "https://eos.example", false},
		{"https://eos.example/a", "https://eos.example/b", false},
		{"", "", false},
		{"", "https://eos.example", false},
		{"eos.example", "eos.example", false},
	}
	for _, c := range cases {
		if got := sameServer(c.a, c.b); got != c.want {
			t.Errorf("sameServer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
