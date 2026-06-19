//go:build windows

// SPDX-License-Identifier: Apache-2.0

package register

import "os"

// permsEnforced is false on Windows, where the unix mode bits are synthesized
// from the read-only attribute and do not reflect a real ACL.
const permsEnforced = false

// fileUID reports no uid on Windows; ownership is not enforced there and the
// unix-style permission bits are not meaningful.
func fileUID(os.FileInfo) (int, bool) {
	return 0, false
}
