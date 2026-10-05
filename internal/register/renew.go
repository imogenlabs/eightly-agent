// SPDX-License-Identifier: Apache-2.0

package register

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// clock is what renewal decisions read, a variable so a test can drive an
// agent through months (EOS-2536).
var clock = time.Now

// legacyTokenLifetime matches the server's grace for a token minted before
// tokens carried an expiry: 30 days from issue.
const legacyTokenLifetime = 30 * 24 * time.Hour

// tokenLifetime is when the agent's bearer token was issued and when it lapses.
type tokenLifetime struct{ issued, expires time.Time }

// readTokenLifetime reads iat/exp out of the server's token
// (`base64url(claims).signature`). It does not and cannot check the signature;
// it only decides WHEN to renew. The server still verifies every token.
func readTokenLifetime(token string) (tokenLifetime, bool) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.SplitN(token, ".", 2)[0])
	if err != nil {
		return tokenLifetime{}, false
	}
	var c struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &c) != nil {
		return tokenLifetime{}, false
	}
	switch {
	case c.Iat > 0 && c.Exp > c.Iat:
		return tokenLifetime{time.Unix(c.Iat, 0), time.Unix(c.Exp, 0)}, true
	case c.Iat > 0 && c.Exp == 0:
		issued := time.Unix(c.Iat, 0)
		return tokenLifetime{issued, issued.Add(legacyTokenLifetime)}, true
	}
	return tokenLifetime{}, false
}

// renewDue is true from half-life on, leaving the second half of the token's
// life for a renewal that fails to be retried on later beats.
func (l tokenLifetime) renewDue(now time.Time) bool {
	return !now.Before(l.issued.Add(l.expires.Sub(l.issued) / 2))
}

func (l tokenLifetime) expired(now time.Time) bool { return !now.Before(l.expires) }

// TokenExpired reports whether token has certainly lapsed at now. A token
// whose lifetime cannot be read is "cannot tell", which is not expired.
func TokenExpired(token string, now time.Time) bool {
	lt, ok := readTokenLifetime(token)
	return ok && lt.expired(now)
}
