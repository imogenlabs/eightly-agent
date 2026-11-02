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

// What the Eight.ly OS server puts in every agent token it signs
// (internal/agent/token.go there): this issuer, this type, an iat and an exp
// exactly AgentTokenTTL (30 days) after it.
const (
	tokenIssuer      = "eightly-server"
	tokenType        = "agent"
	maxTokenLifetime = 30 * 24 * time.Hour
	// maxClockSkew is how far in the future an iat may be and still be
	// believed: the server's clock and this host's are not the same clock.
	maxClockSkew = 5 * time.Minute
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
//
// A token must carry the issuer and type the server sets, an iat no more than
// five minutes in the future and an exp no more than 30 days after it, and must
// not have expired. A token without an
// exp never lapses, so it is refused.
//
// agentID reports this agent's own registered ID and is called on every request,
// because an agent learns its ID only once registration succeeds and the
// heartbeat replaces it on renewal. "" means the agent has no ID, which is only
// the case in local-only mode (no server URL): the caller must not route
// requests here while an agent that registers is still waiting for its ID.
func Middleware(secret string, insecureNoAuth bool, agentID func() string) func(http.Handler) http.Handler {
	if agentID == nil {
		agentID = func() string { return "" }
	}
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
			if err := json.Unmarshal(payload, &claims); err != nil || claims.Iss != tokenIssuer {
				logSecurityEvent(r, "auth.unauthorized", "bad payload or wrong issuer")
				unauthorized(w)
				return
			}
			if claims.Type != tokenType {
				logSecurityEvent(r, "auth.unauthorized", "wrong token type")
				unauthorized(w)
				return
			}
			if reason := lifetimeProblem(claims, time.Now()); reason != "" {
				logSecurityEvent(r, "auth.unauthorized", reason)
				unauthorized(w)
				return
			}

			// A token is only good for the agent it names. Every agent in
			// a fleet verifies with the same shared server secret, so without this
			// check any agent's token — including an enrollment token, which is
			// pasted into install commands — opened every other agent's API, and
			// this API fronts the Docker socket.
			//
			// The identity is read per request, not captured at startup: the
			// agent learns it on registration and it can change on renewal.
			if id := agentID(); id != "" && claims.Sub != id {
				logSecurityEvent(r, "auth.unauthorized", "token issued for a different agent")
				unauthorized(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// lifetimeProblem says why the token's lifetime is unacceptable at now, or ""
// when it is fine. The server sets exp exactly 30 days after iat, so a longer
// span was not minted by it as an agent token.
//
// Everything is compared in whole seconds. Converting a claimed span to a
// time.Duration first overflows for a large exp and wraps to a short one,
// which then passed the 30-day check.
func lifetimeProblem(c Claims, now time.Time) string {
	nowSec := now.Unix()
	switch {
	case c.Exp <= 0:
		return "token has no expiry"
	case c.Iat <= 0:
		return "token has no issue time"
	case c.Exp <= c.Iat:
		return "token expires before it was issued"
	case c.Exp-c.Iat > int64(maxTokenLifetime/time.Second):
		return "token lifetime is longer than 30 days"
	case c.Iat > nowSec && c.Iat-nowSec > int64(maxClockSkew/time.Second):
		return "token issued in the future"
	case nowSec >= c.Exp:
		return "expired token"
	}
	return ""
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
