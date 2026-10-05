// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"errors"
	"os"
)

var errNotSameFile = errors.New("path changed while it was opened")

// openVerified opens name inside root and keeps the file only if, after the
// open, name is still a non-symlink entry for the very file that was opened.
// It is the no-follow open for platforms without O_NOFOLLOW (Windows): if a
// symlink was swapped in for the open, the file reached through it is not
// the file name now names, and the open is refused. O_TRUNC is applied only
// after that check, so a refused open never truncates the file a link
// pointed at.
func openVerified(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := root.OpenFile(name, flag&^os.O_TRUNC, perm)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	entry, err := root.Lstat(name)
	if err != nil || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, entry) {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: name, Err: errNotSameFile}
	}
	if flag&os.O_TRUNC != 0 {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}
