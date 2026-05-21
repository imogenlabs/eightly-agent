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
func Middleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if secret == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" || token == r.Header.Get("Authorization") {
				unauthorized(w)
				return
			}

			parts := strings.SplitN(token, ".", 2)
			if len(parts) != 2 {
				unauthorized(w)
				return
			}

			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(parts[0]))
			sig, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil || !hmac.Equal(mac.Sum(nil), sig) {
				unauthorized(w)
				return
			}

			payload, err := base64.RawURLEncoding.DecodeString(parts[0])
			if err != nil {
				unauthorized(w)
				return
			}
			var claims Claims
			if err := json.Unmarshal(payload, &claims); err != nil || claims.Iss != "eightly-server" {
				unauthorized(w)
				return
			}

			if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
				unauthorized(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"message":"unauthorized"}`)) // #nosec G104 -- HTTP response write
}
