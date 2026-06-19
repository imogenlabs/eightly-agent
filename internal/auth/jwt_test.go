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

func makeToken(claims Claims, secret string) string {
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return encoded + "." + sig
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
})

func TestValidToken(t *testing.T) {
	secret := "test-secret-key"
	token := makeToken(Claims{Sub: "admin", Iss: "eightly-server", Type: "api", Iat: 1700000000}, secret)

	mw := Middleware(secret, false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("expected body 'ok', got %q", rr.Body.String())
	}
}

func TestMissingAuthorizationHeader(t *testing.T) {
	mw := Middleware("some-secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMissingBearerPrefix(t *testing.T) {
	mw := Middleware("some-secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Token abc.def")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMalformedTokenNoDot(t *testing.T) {
	mw := Middleware("some-secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer nodothere")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestInvalidSignature(t *testing.T) {
	token := makeToken(Claims{Sub: "admin", Iss: "eightly-server", Type: "api", Iat: 1700000000}, "correct-secret")

	mw := Middleware("wrong-secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestInvalidPayloadNotJSON(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := payload + "." + sig

	mw := Middleware("secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestWrongIssuer(t *testing.T) {
	secret := "secret"
	token := makeToken(Claims{Sub: "admin", Iss: "wrong-issuer", Type: "api", Iat: 1700000000}, secret)

	mw := Middleware(secret, false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong issuer, got %d", rr.Code)
	}
}

// TestEmptySecretWithExplicitInsecureDisablesAuth: empty secret is only a
// no-auth pass-through when the operator EXPLICITLY opts in via insecureNoAuth.
func TestEmptySecretWithExplicitInsecureDisablesAuth(t *testing.T) {
	mw := Middleware("", true)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header at all

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when secret empty and insecureNoAuth=true, got %d", rr.Code)
	}
}

// TestEmptySecretWithoutOptInFailsClosed is the SABOTAGE-PROVEN test for fix #1.
//
// Contract: empty secret + insecureNoAuth=false must REJECT every request
// (fail closed), never silently serve the Docker socket (host root)
// unauthenticated.
//
// Sabotage proof: the OLD implementation was `if secret == "" { return next }`,
// which would call okHandler and return 200 here — this test would FAIL on it.
// The NEW implementation returns a fail-closed handler (503), so this passes.
// To re-prove: temporarily change Middleware's misconfig branch back to
// `return next` and this assertion goes red.
func TestEmptySecretWithoutOptInFailsClosed(t *testing.T) {
	served := false
	guard := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("LEAKED")) // would only run if auth failed open
	})

	mw := Middleware("", false)(guard)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if served {
		t.Fatal("FAIL-OPEN: next handler ran with no secret and no insecureNoAuth opt-in")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 fail-closed, got %d (body %q)", rr.Code, rr.Body.String())
	}
	if rr.Body.String() == "LEAKED" {
		t.Fatal("FAIL-OPEN: leaked response body from protected handler")
	}
}

// TestEmptySecretWithoutOptInRejectsEvenWithToken proves the fail-closed path
// ignores any presented credential — there is no secret to validate against,
// so nothing can authenticate.
func TestEmptySecretWithoutOptInRejectsEvenWithToken(t *testing.T) {
	token := makeToken(Claims{Sub: "admin", Iss: "eightly-server", Type: "api", Iat: 1700000000}, "whatever")
	mw := Middleware("", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 even with a token present, got %d", rr.Code)
	}
}

func TestMiddlewareCallsNextHandler(t *testing.T) {
	secret := "test"
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	})

	token := makeToken(Claims{Sub: "user", Iss: "eightly-server", Type: "api", Iat: 1700000000}, secret)
	mw := Middleware(secret, false)(handler)

	req := httptest.NewRequest(http.MethodPost, "/action", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatal("next handler was not called on valid auth")
	}
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 from next handler, got %d", rr.Code)
	}
}

func TestUnauthorizedResponseBody(t *testing.T) {
	mw := Middleware("secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	expected := `{"message":"unauthorized"}`
	if rr.Body.String() != expected {
		t.Fatalf("expected body %q, got %q", expected, rr.Body.String())
	}
}

func TestCorruptedSignatureBase64(t *testing.T) {
	claims := Claims{Sub: "admin", Iss: "eightly-server", Type: "api", Iat: 1700000000}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := encoded + ".!!!invalid-base64!!!"

	mw := Middleware("secret", false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for corrupted sig base64, got %d", rr.Code)
	}
}

func TestExpiredToken(t *testing.T) {
	secret := "test-secret"
	expired := Claims{
		Sub:  "admin",
		Iss:  "eightly-server",
		Type: "api",
		Iat:  1700000000,
		Exp:  time.Now().Unix() - 3600, // expired 1 hour ago
	}
	token := makeToken(expired, secret)

	mw := Middleware(secret, false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", rr.Code)
	}
}

func TestTokenWithoutExpStillWorks(t *testing.T) {
	secret := "test-secret"
	noExp := Claims{
		Sub:  "admin",
		Iss:  "eightly-server",
		Type: "api",
		Iat:  1700000000,
		// Exp is zero (omitted) — backwards compatible
	}
	token := makeToken(noExp, secret)

	mw := Middleware(secret, false)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for token without exp, got %d", rr.Code)
	}
}
