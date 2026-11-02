// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
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

const idSecret = "identity-test-secret"

func agentToken(sub string) string {
	return testToken(sub, idSecret, time.Now().Add(time.Hour).Unix())
}

func callAuthed(h http.Handler, token string) int {
	req := httptest.NewRequest(http.MethodGet, "/containers/json", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

var okNext = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

// Before the fix, an agent that failed to register at startup had no ID, the
// subject check was off, and a token minted for ANY agent opened this one.
// Until it knows its ID it answers 503; afterwards only its own token works.
func TestAuthMiddleware_UnregisteredAgentAnswers503(t *testing.T) {
	states := register.NewStateHolder(nil)
	cfg := &config.Config{Secret: idSecret, ServerURL: "https://eos.example"}
	h := authMiddleware(cfg, states)(okNext)

	if code := callAuthed(h, agentToken("agent-other")); code != http.StatusServiceUnavailable {
		t.Fatalf("unregistered agent let another agent's token through with %d, want 503", code)
	}

	states.Store(&register.State{AgentID: "agent-me", Token: "t"})
	if code := callAuthed(h, agentToken("agent-me")); code != http.StatusOK {
		t.Fatalf("registered agent refused its own token with %d", code)
	}
	if code := callAuthed(h, agentToken("agent-other")); code != http.StatusUnauthorized {
		t.Fatalf("registered agent answered %d to another agent's token, want 401", code)
	}
}

// Local-only mode (no server URL) never registers and keeps working on the
// signature alone, as documented.
func TestAuthMiddleware_LocalOnlyModeUnchanged(t *testing.T) {
	cfg := &config.Config{Secret: idSecret}
	h := authMiddleware(cfg, register.NewStateHolder(nil))(okNext)
	if code := callAuthed(h, agentToken("admin")); code != http.StatusOK {
		t.Fatalf("local-only agent answered %d to a valid token", code)
	}
	if code := callAuthed(h, ""); code != http.StatusUnauthorized {
		t.Fatalf("local-only agent answered %d without a token", code)
	}
}

// --insecure-no-auth without a secret opens every route, registered or not.
func TestAuthMiddleware_InsecureNoAuthUnchanged(t *testing.T) {
	cfg := &config.Config{InsecureNoAuth: true, ServerURL: "https://eos.example"}
	h := authMiddleware(cfg, register.NewStateHolder(nil))(okNext)
	if code := callAuthed(h, ""); code != http.StatusOK {
		t.Fatalf("--insecure-no-auth answered %d", code)
	}
}

// flakyEOS refuses the first `fail` register calls with 503, then enrolls.
func flakyEOS(t *testing.T, fail int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/register" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if calls.Add(1) <= fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]string{"agent_id": "agent-me", "token": "new-token"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fastRetry(t *testing.T) {
	t.Helper()
	pmin, pmax := registerRetryMin, registerRetryMax
	registerRetryMin, registerRetryMax = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { registerRetryMin, registerRetryMax = pmin, pmax })
}

// "will retry via heartbeat" was never true: with no state the heartbeat never
// started. Registration now retries on its own until it gets through.
func TestRegisterWithRetry_KeepsTryingUntilRegistered(t *testing.T) {
	fastRetry(t)
	srv, calls := flakyEOS(t, 3)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_x", StatePath: statePath, SocketPath: "/nonexistent.sock", Port: "9001", Version: "test"}
	tlsCfg, err := register.BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, unsaved := registerWithRetry(ctx, cfg, tlsCfg)
	if state == nil || state.AgentID != "agent-me" || unsaved {
		t.Fatalf("got state %+v unsaved %v after %d calls", state, unsaved, calls.Load())
	}
	if n := calls.Load(); n != 4 {
		t.Errorf("%d register calls, want 4 (3 refused, then enrolled)", n)
	}
	if onDisk, err := register.LoadState(statePath); err != nil || onDisk == nil || onDisk.AgentID != "agent-me" {
		t.Errorf("registration not saved: %+v, %v", onDisk, err)
	}
}

func TestRegisterWithRetry_StopsWithContext(t *testing.T) {
	fastRetry(t)
	srv, _ := flakyEOS(t, 1<<30)
	cfg := &config.Config{ServerURL: srv.URL, StatePath: filepath.Join(t.TempDir(), "s.json"), SocketPath: "/nonexistent.sock"}
	tlsCfg, _ := register.BuildServerTLSConfig("", true, zerolog.Nop())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if state, _ := registerWithRetry(ctx, cfg, tlsCfg); state != nil {
		t.Fatalf("got a state from a server that refuses everything: %+v", state)
	}
}

// The packaged unit used to leave the state path at /data, which systemd made
// read-only: the agent spent its single-use enrollment token, could not save
// the result, and could never enroll again. It must not register at all when
// the state cannot be saved.
func TestResume_DoesNotSpendEnrollTokenWhenStateIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only mode bit does not stop writes into a folder on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root can write a read-only directory")
	}
	srv, calls := flakyEOS(t, 0)
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_once", StatePath: filepath.Join(ro, "eightly-agent.json"), SocketPath: "/nonexistent.sock"}
	tlsCfg, _ := register.BuildServerTLSConfig("", true, zerolog.Nop())

	if state, _, _ := resume(cfg, tlsCfg); state != nil {
		t.Fatalf("registered without a writable state file: %+v", state)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("sent the enrollment token %d times although the result could not be saved", n)
	}
}

// End to end: the server is down when the agent starts. The API answers 503
// to a valid token, the agent registers in the background, and from then on
// its own token works and another agent's does not.
func TestRun_RegistersInBackgroundAndGatesUntilThen(t *testing.T) {
	fastRetry(t)
	// The server refuses until the test has seen the gate. A fixed number of
	// refusals raced the agent's startup: on a loaded runner the certificate
	// took longer than the retries, the agent was registered before the first
	// request, and the gate was never observed.
	var held atomic.Bool
	held.Store(true)
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/register" {
			w.WriteHeader(http.StatusOK)
			return
		}
		calls.Add(1)
		if held.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]string{"agent_id": "agent-me", "token": "new-token"},
		})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	port := freePort(t)
	cfg := &config.Config{
		Addr: "127.0.0.1", Port: port, Secret: idSecret,
		ServerURL: srv.URL, InsecureServerTLS: true, EnrollToken: "eos_enroll_x",
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

	// The first /host after registration gathers hardware info; on Windows
	// that runs PowerShell and can take several seconds.
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- test server
	waitForServer(t, client, port)
	get := func(token string) int {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%s/host", port), nil) //nolint:noctx // test
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /host: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := get(agentToken("agent-anyone")); code != http.StatusServiceUnavailable {
		t.Fatalf("before registration /host answered %d, want 503", code)
	}
	held.Store(false)

	deadline := time.Now().Add(45 * time.Second)
	for get(agentToken("agent-me")) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatalf("never registered (%d register calls)", calls.Load())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code := get(agentToken("agent-anyone")); code != http.StatusUnauthorized {
		t.Fatalf("after registration another agent's token got %d, want 401", code)
	}
}

// A state file saved with an empty identity is not an identity.
func TestResume_IgnoresSavedStateWithoutIdentity(t *testing.T) {
	srv, calls := flakyEOS(t, 0)
	statePath := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	if err := (&register.State{Token: "tok", ServerURL: srv.URL}).Save(statePath); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ServerURL: srv.URL, EnrollToken: "eos_enroll_x", StatePath: statePath, SocketPath: "/nonexistent.sock"}
	tlsCfg, _ := register.BuildServerTLSConfig("", true, zerolog.Nop())
	state, _, _ := resume(cfg, tlsCfg)
	if state == nil || state.AgentID != "agent-me" || calls.Load() != 1 {
		t.Fatalf("got %+v after %d register calls; want a fresh enrollment", state, calls.Load())
	}
}
