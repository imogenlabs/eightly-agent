// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package browse

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// openNoFollow opens name inside root and refuses a symlink at the final
// component in the same system call. os.Root.OpenFile resolves a final symlink
// itself even when O_NOFOLLOW is passed (measured on Go 1.26), so a check
// before it leaves a window. Instead the parent directory is opened through
// the Root, which keeps it inside the volume, and the last component is opened
// relative to that directory with O_NOFOLLOW.
func openNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	dir, err := root.Open(filepath.Dir(name))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), filepath.Base(name), flag|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm.Perm())) // #nosec G115 -- a directory fd fits in an int
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}
