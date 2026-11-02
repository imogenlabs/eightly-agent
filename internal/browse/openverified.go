// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"errors"
	"io/fs"
	"os"
)

var (
	errNotSameFile = errors.New("path changed while it was opened")
	// errNotRegular refuses a pipe, socket, device or directory where a
	// regular file is required. Reading a named pipe hangs the request, and a
	// device node a container made in its volume would reach a host disk.
	errNotRegular = errors.New("not a regular file")
)

// checkRegular returns errNotRegular for any mode but a regular file.
func checkRegular(mode fs.FileMode) error {
	if !mode.IsRegular() {
		return errNotRegular
	}
	return nil
}

// openVerified opens name inside root and keeps the file only if, after the
// open, name (not followed) is an entry for the very file that was opened. A
// symlink is never the same file as its target, so this refuses one too.
// It is the no-follow open for platforms without O_NOFOLLOW (Windows): if a
// symlink was swapped in for the open, the file reached through it is not
// the file name now names, and the open is refused. O_TRUNC is applied only
// after that check, and O_CREATE only with O_EXCL, so a refused open neither
// truncates nor creates the file a link points at. Anything that is not a
// regular file is refused with errNotRegular, again before O_TRUNC.
func openVerified(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	// Open an existing file first, creating nothing. Only if there is none is
	// it created, with O_EXCL, which fails on any existing entry, a dangling
	// symlink included, so nothing is ever created through a link.
	base := flag &^ (os.O_TRUNC | os.O_CREATE | os.O_EXCL)
	f, err := root.OpenFile(name, base, perm)
	if errors.Is(err, fs.ErrNotExist) && flag&os.O_CREATE != 0 {
		f, err = root.OpenFile(name, base|os.O_CREATE|os.O_EXCL, perm)
	}
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	entry, err := root.Lstat(name)
	if err != nil || !os.SameFile(opened, entry) {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: name, Err: errNotSameFile}
	}
	if err := checkRegular(opened.Mode()); err != nil {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	if flag&os.O_TRUNC != 0 {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}
