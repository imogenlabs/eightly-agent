// SPDX-License-Identifier: Apache-2.0

package register

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/imogenlabs/eightly-agent/internal/auth"
)

func signedAgentToken(t *testing.T, secret, sub string) string {
	t.Helper()
	now := time.Now()
	payload, err := json.Marshal(auth.Claims{Sub: sub, Iss: "eightly-server", Type: "agent", Iat: now.Unix(), Exp: now.Add(tokenTTL).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// The heartbeat used to renew by writing the new state over the old one in
// place while every request read the agent ID from it without a lock. Run
// with -race: renewals and authenticated requests at the same time.
func TestRenewalWhileRequestsRunIsRaceFree(t *testing.T) {
	f, states, h := startFakeEOS(t)
	const secret = "race-secret"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	api := auth.Middleware(secret, false, states.AgentID)(next)
	token := signedAgentToken(t, secret, "agent-1")

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var failed sync.Once
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				req := httptest.NewRequest(http.MethodGet, "/containers/json", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				api.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					failed.Do(func() { t.Errorf("request during renewal got %d", rec.Code) })
				}
				_ = states.Load().Token
			}
		}()
	}

	before := states.Load()
	for i := 0; i < 20; i++ {
		f.advance(16 * 24 * time.Hour) // past half-life: every beat renews
		h.step(context.Background())
	}
	cancel()
	wg.Wait()

	if f.registers < 20 {
		t.Fatalf("%d renewals in 20 beats; the test did not renew while requests ran", f.registers)
	}
	if before.Token == states.Load().Token {
		t.Fatal("the held state was not replaced")
	}
}

func TestStateHolderEmpty(t *testing.T) {
	h := NewStateHolder(nil)
	if h.Load() != nil || h.AgentID() != "" {
		t.Fatal("an empty holder reported a registration")
	}
	h.Store(&State{AgentID: "a"})
	if h.AgentID() != "a" {
		t.Fatalf("AgentID %q after Store", h.AgentID())
	}
}
