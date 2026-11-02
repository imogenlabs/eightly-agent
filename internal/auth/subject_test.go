// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testSecret = "fleet-wide-shared-server-secret"

func mintToken(t *testing.T, sub string) string {
	t.Helper()
	claims := Claims{Sub: sub, Iss: "eightly-server", Type: "agent", Iat: time.Now().Unix(), Exp: time.Now().Add(time.Hour).Unix()}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func callWithToken(handler http.Handler, token string) int {
	req := httptest.NewRequest(http.MethodGet, "/containers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func handlerFor(agentID string) http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return Middleware(testSecret, false, func() string { return agentID })(ok)
}

// The whole point of per-agent token binding: every agent verifies with the same shared server
// secret, so a token minted for agent A used to open agent B's API — and this API
// fronts the Docker socket.
func TestTokenForAnotherAgentIsRejected(t *testing.T) {
	h := handlerFor("agent-bbbbbbbb")

	if code := callWithToken(h, mintToken(t, "agent-aaaaaaaa")); code != http.StatusUnauthorized {
		t.Fatalf("another agent's token got %d, want 401 — one captured token must not open the whole fleet", code)
	}
}

func TestAgentAcceptsItsOwnToken(t *testing.T) {
	h := handlerFor("agent-bbbbbbbb")

	if code := callWithToken(h, mintToken(t, "agent-bbbbbbbb")); code != http.StatusOK {
		t.Fatalf("an agent rejected its own token with %d; that would break every installed agent", code)
	}
}

// An enrollment token's subject is "enroll-...", never an agent ID, so binding
// the subject also shuts the door this credential opened — and it is the one that
// gets pasted into install commands, shell history and screenshots.
func TestEnrollmentSubjectIsRejected(t *testing.T) {
	h := handlerFor("agent-bbbbbbbb")

	if code := callWithToken(h, mintToken(t, "enroll-12345678")); code != http.StatusUnauthorized {
		t.Fatalf("an enrollment-subject token got %d, want 401", code)
	}
}

// No identity means local-only mode (no server URL): the signature is the
// check. An agent that registers never reaches the middleware without its ID;
// the server package answers 503 until it has one.
func TestUnknownIdentityKeepsPreviousBehaviour(t *testing.T) {
	h := handlerFor("")

	if code := callWithToken(h, mintToken(t, "agent-aaaaaaaa")); code != http.StatusOK {
		t.Fatalf("a local-only agent rejected a valid token with %d", code)
	}
}

// A nil accessor is treated as "identity unknown" rather than panicking, so an
// older caller that has not been rewired cannot crash the agent.
func TestNilAccessorIsSafe(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := Middleware(testSecret, false, nil)(ok)

	if code := callWithToken(h, mintToken(t, "agent-aaaaaaaa")); code != http.StatusOK {
		t.Fatalf("nil agentID accessor gave %d, want 200 (treat as unknown identity)", code)
	}
}

// The subject check must not become a way past the signature check.
func TestForgedTokenStillRejectedEvenWithMatchingSubject(t *testing.T) {
	h := handlerFor("agent-bbbbbbbb")

	forged := mintToken(t, "agent-bbbbbbbb") + "tampered"
	if code := callWithToken(h, forged); code != http.StatusUnauthorized {
		t.Fatalf("a tampered token got %d, want 401", code)
	}
}
