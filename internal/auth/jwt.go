// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Claims represents the payload of an HMAC-signed agent token.
type Claims struct {
	Sub  string `json:"sub"`
	Iss  string `json:"iss"`
	Type string `json:"type"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp,omitempty"`
}

// Middleware returns an HTTP middleware that validates HMAC-SHA256 Bearer tokens.
//
// Security contract (fail-closed):
//   - If secret != "": every request must present a valid token, else 401.
//   - If secret == "" AND insecureNoAuth == true: authentication is EXPLICITLY
//     disabled and all requests pass through. This is the only path that allows
//     unauthenticated access, and it requires the operator to opt in.
//   - If secret == "" AND insecureNoAuth == false: the agent is misconfigured
//     (no secret, no explicit opt-out). Rather than silently serving the Docker
//     socket — i.e. host root — unauthenticated, we FAIL CLOSED and reject every
//     request with 503. "No auth" must always be a deliberate choice.
func Middleware(secret string, insecureNoAuth bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Explicit, operator-acknowledged no-auth mode.
		if secret == "" && insecureNoAuth {
			return next
		}

		// Misconfiguration: no secret and no explicit opt-out. Fail closed.
		if secret == "" {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logSecurityEvent(r, "auth.misconfigured", "no secret configured and insecure-no-auth not set; failing closed")
				serviceUnavailable(w)
			})
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" || token == r.Header.Get("Authorization") {
				logSecurityEvent(r, "auth.unauthorized", "missing or malformed Bearer token")
				unauthorized(w)
				return
			}

			parts := strings.SplitN(token, ".", 2)
			if len(parts) != 2 {
				logSecurityEvent(r, "auth.unauthorized", "token not in payload.signature form")
				unauthorized(w)
				return
			}

			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(parts[0]))
			sig, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil || !hmac.Equal(mac.Sum(nil), sig) {
				logSecurityEvent(r, "auth.unauthorized", "invalid signature")
				unauthorized(w)
				return
			}

			payload, err := base64.RawURLEncoding.DecodeString(parts[0])
			if err != nil {
				logSecurityEvent(r, "auth.unauthorized", "undecodable payload")
				unauthorized(w)
				return
			}
			var claims Claims
			if err := json.Unmarshal(payload, &claims); err != nil || claims.Iss != "eightly-server" {
				logSecurityEvent(r, "auth.unauthorized", "bad payload or wrong issuer")
				unauthorized(w)
				return
			}

			if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
				logSecurityEvent(r, "auth.unauthorized", "expired token")
				unauthorized(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// logSecurityEvent writes a single, greppable [SECURITY] detection line for an
// auth failure or fail-closed event. Format is fixed so log pipelines can match
// on the `[SECURITY]` prefix and parse the key=value fields.
func logSecurityEvent(r *http.Request, event, reason string) {
	log.Warn().Msgf("[SECURITY] %s method=%s path=%s ip=%s reason=%q",
		event, r.Method, r.URL.Path, clientIP(r), reason)
}

// clientIP returns the best-effort remote address for logging. RemoteAddr is
// host:port; we keep it whole rather than risk mis-parsing IPv6.
func clientIP(r *http.Request) string {
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"message":"unauthorized"}`)) // #nosec G104 -- HTTP response write
}

func serviceUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(`{"message":"service unavailable: authentication not configured"}`)) // #nosec G104 -- HTTP response write
}
