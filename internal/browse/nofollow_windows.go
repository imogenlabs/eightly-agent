// SPDX-License-Identifier: Apache-2.0

//go:build windows

package browse

import (
	"errors"
	"os"
)

// openNoFollow refuses a symlink at the final component. Windows has no
// O_NOFOLLOW, so the open is verified after the fact (openVerified), which
// also refuses anything that is not a regular file.
func openNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	return openVerified(root, name, flag, perm)
}

var errNotDir = errors.New("not a directory")

// openDir opens a directory for listing and refuses anything else.
func openDir(root *os.Root, name string) (*os.File, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: name, Err: errNotDir}
	}
	return f, nil
}
