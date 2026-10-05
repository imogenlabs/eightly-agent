// SPDX-License-Identifier: Apache-2.0

package register

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

const tokenTTL = 30 * 24 * time.Hour

// fakeEOS applies eightly-os's agent-token rules on a fake clock: a token is
// `base64url(claims).sig` with iat/exp, lives 30 days, heartbeat refuses an
// expired one with 401, and register refuses one with 403 unless a valid
// enrollment token is sent.
type fakeEOS struct {
	mu        sync.Mutex
	now       time.Time
	serial    int
	registers int
	beats     []int
}

func (f *fakeEOS) advance(d time.Duration) { f.mu.Lock(); f.now = f.now.Add(d); f.mu.Unlock() }
func (f *fakeEOS) clock() time.Time        { f.mu.Lock(); defer f.mu.Unlock(); return f.now }

func (f *fakeEOS) mint() string {
	f.serial++
	claims, _ := json.Marshal(map[string]any{"sub": "agent-1", "iat": f.now.Unix(), "exp": f.now.Add(tokenTTL).Unix()})
	return base64.RawURLEncoding.EncodeToString(claims) + fmt.Sprintf(".sig%d", f.serial)
}

func (f *fakeEOS) valid(r *http.Request) bool {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	payload, err := base64.RawURLEncoding.DecodeString(strings.SplitN(tok, ".", 2)[0])
	if err != nil {
		return false
	}
	var c struct{ Exp int64 }
	if json.Unmarshal(payload, &c) != nil || c.Exp == 0 {
		return false
	}
	return f.now.Before(time.Unix(c.Exp, 0))
}

func (f *fakeEOS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/api/agents/register":
		f.registers++
		if !f.valid(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"agent_id": "agent-1", "token": f.mint()}})
	case strings.HasSuffix(r.URL.Path, "/heartbeat"):
		code := http.StatusOK
		if !f.valid(r) {
			code = http.StatusUnauthorized
		}
		f.beats = append(f.beats, code)
		w.WriteHeader(code)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func startFakeEOS(t *testing.T) (*fakeEOS, *State, *heartbeater) {
	t.Helper()
	f := &fakeEOS{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	prev := clock
	clock = f.clock
	t.Cleanup(func() { clock = prev })

	tlsCfg, err := BuildServerTLSConfig("", true, zerolog.Nop())
	if err != nil {
		t.Fatalf("tls: %v", err)
	}
	state := &State{AgentID: "agent-1", Token: f.mint(), ServerURL: srv.URL}
	statePath := filepath.Join(t.TempDir(), "data", "state.json") // Save creates it 0700
	h := newHeartbeater(state, statePath, "/nonexistent.sock", "test", "10.0.0.9", tlsCfg, zerolog.Nop())
	return f, state, h
}

// EOS-2536 AC1 + AC3. Driven through 100 days with a beat every day, the agent
// renews its token before it lapses and every heartbeat is accepted. Before
// this, nothing renewed it, and on day 31 it was refused for good.
func TestHeartbeat_RenewsBeforeExpiryAndStaysConnectedFor100Days(t *testing.T) {
	f, state, h := startFakeEOS(t)
	for day := 1; day <= 100; day++ {
		f.advance(24 * time.Hour)
		h.step(context.Background())
		if got := f.beats[len(f.beats)-1]; got != http.StatusOK {
			t.Fatalf("day %d: heartbeat answered %d; the agent did not keep its token alive", day, got)
		}
	}
	if f.registers < 3 || f.registers > 8 {
		t.Errorf("%d renewals over 100 days; renewing at half-life of 30 days should be about 6", f.registers)
	}
	saved, err := LoadState(h.statePath)
	if err != nil || saved == nil || saved.Token != state.Token {
		t.Fatalf("the renewed token was not saved, so a restart would lose it: %v", err)
	}
}

// Renewal waits for half-life: a token with most of its life left is not
// swapped on every beat.
func TestHeartbeat_DoesNotRenewEarly(t *testing.T) {
	f, _, h := startFakeEOS(t)
	for i := 0; i < 14; i++ {
		f.advance(24 * time.Hour)
		h.step(context.Background())
	}
	if f.registers != 0 {
		t.Fatalf("renewed %d times inside the first half of the token's life", f.registers)
	}
}

// AC2, agent side. A token that has already lapsed cannot be renewed: the
// server refuses it. The agent says plainly that it must be re-enrolled and
// does not keep throwing the dead token at the register endpoint.
func TestHeartbeat_ExpiredTokenDoesNotLoopOnRegister(t *testing.T) {
	f, _, h := startFakeEOS(t)
	var logs strings.Builder
	h.log = zerolog.New(&logs)
	f.advance(31 * 24 * time.Hour)

	for i := 0; i < 10; i++ {
		h.step(context.Background())
	}
	if f.registers != 0 {
		t.Fatalf("an expired token was sent to register %d times; it can only ever be refused", f.registers)
	}
	if n := strings.Count(logs.String(), "must be re-enrolled"); n != 1 {
		t.Fatalf("said 'must be re-enrolled' %d times, want once:\n%s", n, logs.String())
	}
	if h.interval != heartbeatMaxInterval {
		t.Errorf("interval %v, want the slowest (%v) while it waits to be re-enrolled", h.interval, heartbeatMaxInterval)
	}
}

func TestTokenLifetime_ReadsIssuedAndExpiry(t *testing.T) {
	f := &fakeEOS{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	lt, ok := readTokenLifetime(f.mint())
	if !ok || !lt.issued.Equal(f.now) || !lt.expires.Equal(f.now.Add(tokenTTL)) {
		t.Fatalf("got %+v, %v", lt, ok)
	}
	if _, ok := readTokenLifetime("not-a-token"); ok {
		t.Error("garbage read as a token")
	}
	if TokenExpired("not-a-token", f.now) {
		t.Error("a token whose life cannot be read was called expired; that is 'cannot tell', not 'expired'")
	}
}

// Codex P2. A renewal whose save fails (a full or read-only disk) used to be
// treated as done: the new token lived only in memory and nothing tried to
// save it again, so a restart after the old token lapsed loaded a dead
// credential. The renewed token stays pending and the next beat saves it.
func TestHeartbeat_RetriesSavingARenewedTokenAfterTheSaveFailed(t *testing.T) {
	f, state, h := startFakeEOS(t)
	if err := state.Save(h.statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	original := state.Token
	// A directory where Save writes its temp file makes the save fail, the
	// way a full or read-only disk does, without needing root.
	blocker := h.statePath + ".tmp"
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	f.advance(16 * 24 * time.Hour) // past half-life: this beat renews
	h.step(context.Background())
	if state.Token == original {
		t.Fatal("the token was not renewed past half-life")
	}
	if onDisk, _ := LoadState(h.statePath); onDisk == nil || onDisk.Token != original {
		t.Fatal("the save was meant to fail and did not; the test is not testing anything")
	}

	if err := os.Remove(blocker); err != nil { // the disk is writable again
		t.Fatalf("remove: %v", err)
	}
	f.advance(24 * time.Hour)
	h.step(context.Background())

	onDisk, err := LoadState(h.statePath)
	if err != nil || onDisk == nil || onDisk.Token != state.Token {
		t.Fatalf("the renewed token was never saved after the disk recovered, so a restart loads the old one: %v", err)
	}
}
