// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package browse

import "syscall"

// syscallNoFollow is O_NOFOLLOW on unix-like platforms. It is passed to
// os.Root.OpenFile, but measured on Go 1.26 the Root resolves a final-component
// symlink itself (inside the volume only), so the flag does not refuse one
// there: isSymlink in browse.go is the check that does.
const syscallNoFollow = syscall.O_NOFOLLOW
