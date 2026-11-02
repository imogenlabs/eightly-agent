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
	token := makeToken(serverClaims("admin"), secret)

	mw := Middleware(secret, false, nil)(okHandler)
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
	mw := Middleware("some-secret", false, nil)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMissingBearerPrefix(t *testing.T) {
	mw := Middleware("some-secret", false, nil)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Token abc.def")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMalformedTokenNoDot(t *testing.T) {
	mw := Middleware("some-secret", false, nil)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer nodothere")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestInvalidSignature(t *testing.T) {
	token := makeToken(serverClaims("admin"), "correct-secret")

	mw := Middleware("wrong-secret", false, nil)(okHandler)
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

	mw := Middleware("secret", false, nil)(okHandler)
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
	token := makeToken(withIssuer(serverClaims("admin"), "wrong-issuer"), secret)

	mw := Middleware(secret, false, nil)(okHandler)
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
	mw := Middleware("", true, nil)(okHandler)
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

	mw := Middleware("", false, nil)(guard)
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
	token := makeToken(serverClaims("admin"), "whatever")
	mw := Middleware("", false, nil)(okHandler)
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

	token := makeToken(serverClaims("user"), secret)
	mw := Middleware(secret, false, nil)(handler)

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
	mw := Middleware("secret", false, nil)(okHandler)
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
	claims := serverClaims("admin")
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := encoded + ".!!!invalid-base64!!!"

	mw := Middleware("secret", false, nil)(okHandler)
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
	expired := serverClaims("admin")
	expired.Iat = time.Now().Add(-maxTokenLifetime - time.Hour).Unix()
	expired.Exp = time.Now().Add(-time.Hour).Unix()
	token := makeToken(expired, secret)

	mw := Middleware(secret, false, nil)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", rr.Code)
	}
}

// serverClaims is what the Eight.ly OS server signs for an agent: type
// "agent", exp exactly 30 days after iat (internal/agent/token.go there).
func serverClaims(sub string) Claims {
	now := time.Now()
	return Claims{Sub: sub, Iss: "eightly-server", Type: "agent", Iat: now.Unix(), Exp: now.Add(30 * 24 * time.Hour).Unix()}
}

func withIssuer(c Claims, iss string) Claims {
	c.Iss = iss
	return c
}

func statusFor(t *testing.T, c Claims) int {
	t.Helper()
	secret := "test-secret"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken(c, secret))
	rr := httptest.NewRecorder()
	Middleware(secret, false, nil)(okHandler).ServeHTTP(rr, req)
	return rr.Code
}

// The token the current server issues must keep working.
func TestServerIssuedTokenIsAccepted(t *testing.T) {
	if code := statusFor(t, serverClaims("agent-1")); code != http.StatusOK {
		t.Fatalf("a token shaped like the server's got %d, want 200", code)
	}
}

// A token without exp never lapses. The server has put exp in every agent
// token for a long time, so one without it was not minted as an agent token.
func TestTokenWithoutExpIsRejected(t *testing.T) {
	c := serverClaims("agent-1")
	c.Exp = 0
	if code := statusFor(t, c); code != http.StatusUnauthorized {
		t.Fatalf("token without exp got %d, want 401", code)
	}
}

func TestTokenWithoutIatIsRejected(t *testing.T) {
	c := serverClaims("agent-1")
	c.Iat = 0
	if code := statusFor(t, c); code != http.StatusUnauthorized {
		t.Fatalf("token without iat got %d, want 401", code)
	}
}

func TestTokenLongerThan30DaysIsRejected(t *testing.T) {
	c := serverClaims("agent-1")
	c.Exp = time.Unix(c.Iat, 0).Add(maxTokenLifetime + time.Second).Unix()
	if code := statusFor(t, c); code != http.StatusUnauthorized {
		t.Fatalf("token living 30 days and a second got %d, want 401", code)
	}
}

func TestTokenExpiringBeforeIssueIsRejected(t *testing.T) {
	c := serverClaims("agent-1")
	c.Exp = c.Iat
	if code := statusFor(t, c); code != http.StatusUnauthorized {
		t.Fatalf("token with exp == iat got %d, want 401", code)
	}
}

func TestTokenOfAnotherTypeIsRejected(t *testing.T) {
	for _, typ := range []string{"", "api", "enroll", "Agent"} {
		c := serverClaims("agent-1")
		c.Type = typ
		if code := statusFor(t, c); code != http.StatusUnauthorized {
			t.Errorf("token of type %q got %d, want 401", typ, code)
		}
	}
}

// The server treats a token as dead from its exp second on; so does the agent.
func TestLifetimeProblemAtExactExpiry(t *testing.T) {
	c := serverClaims("agent-1")
	at := time.Unix(c.Exp, 0)
	if lifetimeProblem(c, at) == "" {
		t.Error("a token was accepted at its exp second")
	}
	if p := lifetimeProblem(c, at.Add(-time.Second)); p != "" {
		t.Errorf("a token was refused a second before exp: %s", p)
	}
}

// exp - iat used to be multiplied into a time.Duration, which wraps: a span of
// 18446744074 seconds came out as about 0.7 seconds and passed the 30-day check.
func TestTokenWithOverflowingLifetimeIsRejected(t *testing.T) {
	c := serverClaims("agent-1")
	c.Exp = c.Iat + 18446744074
	if p := lifetimeProblem(c, time.Unix(c.Iat, 0)); p == "" {
		t.Fatal("a token whose lifetime overflows a Duration was accepted")
	}
	if code := statusFor(t, c); code != http.StatusUnauthorized {
		t.Fatalf("overflowing lifetime got %d, want 401", code)
	}
}

func TestTokenIssuedInTheFutureIsRejected(t *testing.T) {
	now := time.Now()
	c := serverClaims("agent-1")
	c.Iat = now.Add(time.Hour).Unix()
	c.Exp = time.Unix(c.Iat, 0).Add(maxTokenLifetime).Unix()
	if p := lifetimeProblem(c, now); p == "" {
		t.Fatal("a token issued an hour from now was accepted")
	}
	if code := statusFor(t, c); code != http.StatusUnauthorized {
		t.Fatalf("future iat got %d, want 401", code)
	}
}

// Two clocks never agree exactly; a token minted a moment "ahead" of this host
// is still the server's.
func TestTokenIssuedWithinClockSkewIsAccepted(t *testing.T) {
	now := time.Now()
	c := serverClaims("agent-1")
	c.Iat = now.Add(maxClockSkew).Unix()
	c.Exp = time.Unix(c.Iat, 0).Add(maxTokenLifetime).Unix()
	if p := lifetimeProblem(c, now); p != "" {
		t.Fatalf("a token issued within the clock skew was refused: %s", p)
	}
	c.Iat = now.Add(maxClockSkew + time.Second).Unix()
	if lifetimeProblem(c, now) == "" {
		t.Fatal("a token issued just past the clock skew was accepted")
	}
}

func TestTokenOfExactly30DaysIsAccepted(t *testing.T) {
	c := serverClaims("agent-1")
	c.Exp = c.Iat + int64(maxTokenLifetime/time.Second)
	if p := lifetimeProblem(c, time.Unix(c.Iat, 0)); p != "" {
		t.Fatalf("a 30-day token was refused: %s", p)
	}
}
