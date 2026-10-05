// SPDX-License-Identifier: Apache-2.0

//go:build windows

package browse

import (
	"errors"
	"os"
)

// openNoFollow refuses a symlink at the final component with a check before
// the open; Windows has no O_NOFOLLOW. A link swapped in after the check can
// still only point inside the volume, because the Root refuses one that leads
// out. The agent is deployed on Linux; this keeps cross-builds working.
func openNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	if info, err := root.Lstat(name); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: errors.New("is a symlink")}
	}
	return root.OpenFile(name, flag, perm)
}
