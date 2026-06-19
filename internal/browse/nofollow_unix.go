// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package browse

import "syscall"

// syscallNoFollow is O_NOFOLLOW on unix-like platforms — the final path
// component is opened without following a symlink, blocking symlink-redirect
// escapes at open() time.
const syscallNoFollow = syscall.O_NOFOLLOW
