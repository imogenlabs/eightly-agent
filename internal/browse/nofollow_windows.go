// SPDX-License-Identifier: Apache-2.0

//go:build windows

package browse

import "os"

// openNoFollow refuses a symlink at the final component. Windows has no
// O_NOFOLLOW, so the open is verified after the fact (openVerified).
func openNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	return openVerified(root, name, flag, perm)
}
