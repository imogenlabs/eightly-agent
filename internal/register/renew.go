// SPDX-License-Identifier: Apache-2.0

package register

import "time"

// clock is what renewal decisions read, a variable so a test can drive an
// agent through months (EOS-2536).
var clock = time.Now

type tokenLifetime struct{ issued, expires time.Time }

func readTokenLifetime(string) (tokenLifetime, bool) { return tokenLifetime{}, false }

// TokenExpired is a stub until EOS-2536 lands.
func TokenExpired(string, time.Time) bool { return false }
