//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package register

import (
	"os"
	"syscall"
)

// permsEnforced gates the unix-style mode check, which is only meaningful on
// platforms with real POSIX permission bits.
const permsEnforced = true

// fileUID returns the owning uid of fi on unix platforms.
func fileUID(fi os.FileInfo) (int, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), true
	}
	return 0, false
}
