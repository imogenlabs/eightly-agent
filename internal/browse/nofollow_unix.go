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
//
// Only a regular file is ever returned. The open uses O_NONBLOCK so a named
// pipe cannot hang it, and O_NOCTTY so a terminal device cannot become the
// agent's controlling terminal. The descriptor is then checked with fstat and
// anything else (pipe, socket, device) is closed and refused with
// errNotRegular before a byte is read or written. O_TRUNC is applied only
// after that check, so a refused open never truncates anything.
func openNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	dir, err := root.Open(filepath.Dir(name))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	open := flag&^os.O_TRUNC | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY
	fd, err := unix.Openat(int(dir.Fd()), filepath.Base(name), open, uint32(perm.Perm())) // #nosec G115 -- a directory fd fits in an int
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	if err := checkFd(fd, flag); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil // #nosec G115 -- fd from a successful openat is non-negative
}

// checkFd refuses a descriptor that is not a regular file, then clears
// O_NONBLOCK and applies a deferred O_TRUNC.
func checkFd(fd, flag int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG { // Mode is uint16 on darwin, uint32 on linux
		return errNotRegular
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		return err
	}
	if flag&os.O_TRUNC != 0 {
		return unix.Ftruncate(fd, 0)
	}
	return nil
}

// openDir opens a directory for listing. O_DIRECTORY makes the kernel refuse
// anything else before opening it, so a named pipe cannot hang the request.
func openDir(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_DIRECTORY, 0)
}
