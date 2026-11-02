//go:build windows

// SPDX-License-Identifier: Apache-2.0

package register

import "os"

// permsEnforced is false on Windows, where the unix mode bits are synthesized
// from the read-only attribute and do not reflect a real ACL. The state file is
// protected by its DACL instead: see secureState in stateacl_windows.go.
const permsEnforced = false

// fileUID reports no uid on Windows; the unix-style owner and permission bits
// are not meaningful there, and secureState checks the DACL instead.
func fileUID(os.FileInfo) (int, bool) {
	return 0, false
}
