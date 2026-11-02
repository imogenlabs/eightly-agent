// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"strings"
)

// IsWindowsDrivePath reports whether p starts like an absolute Windows drive
// path: a letter, a colon and a slash either way round (C:\x, c:/x).
func IsWindowsDrivePath(p string) bool {
	return len(p) >= 3 && isASCIILetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func isASCIILetter(b byte) bool { return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' }

// CanonicalWindowsPath returns the one spelling the agent compares Windows
// drive paths in: an upper-case drive letter, backslashes, no empty, '.' or
// '..' segment and no trailing backslash (the drive root is "C:\"). NTFS
// ignores case, so two canonical paths are the same path when
// strings.EqualFold says so, and p is under root when it equals root or,
// compared without case, starts with root + `\`.
//
// It refuses what would let one folder go by two names that compare
// differently: a segment ending in a dot or a space (Windows drops them, so
// "Windows." opens Windows), one holding '~' (an 8.3 short name such as
// PROGRA~1), and ':' past the drive (an alternate data stream), along with
// the other characters Windows refuses in a name. UNC (\\server\share),
// device (\\?\C:\) and drive-relative (C:x) paths are not drive paths and are
// refused.
func CanonicalWindowsPath(p string) (string, error) {
	if !IsWindowsDrivePath(p) {
		return "", fmt.Errorf("%q is not an absolute Windows drive path such as C:\\AppData", p)
	}
	var segs []string
	for _, seg := range strings.FieldsFunc(p[3:], func(r rune) bool { return r == '\\' || r == '/' }) {
		switch {
		case seg == "." || seg == "..":
			return "", fmt.Errorf("%q has a '.' or '..' segment", p)
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return "", fmt.Errorf("%q has a segment ending in a dot or a space, which Windows drops", p)
		case strings.ContainsAny(seg, "~:*?\"<>|"):
			return "", fmt.Errorf("%q has a segment holding one of ~ : * ? \" < > |; spell it out in full", p)
		case strings.IndexFunc(seg, func(r rune) bool { return r < 0x20 }) >= 0:
			return "", fmt.Errorf("%q holds a control character", p)
		}
		segs = append(segs, seg)
	}
	return strings.ToUpper(p[:1]) + `:\` + strings.Join(segs, `\`), nil
}

// windowsUnder reports whether canonical path p is root or inside it.
func windowsUnder(p, root string) bool {
	if strings.EqualFold(p, root) {
		return true
	}
	prefix := strings.TrimSuffix(root, `\`) + `\`
	return len(p) > len(prefix) && strings.EqualFold(p[:len(prefix)], prefix)
}

// windowsSystemPaths are the Windows folders a bind root may not be, lie
// under or hold: Windows itself, the program folders, ProgramData (Docker's
// and containerd's state, the agent's own secrets, OpenSSH's host keys) and
// the system drive's recovery folders. They are read from the environment,
// where an install on another drive says where they are, with the usual
// locations as a fallback.
func windowsSystemPaths(getenv func(string) string) []string {
	or := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	drive := or("SystemDrive", "C:")
	candidates := []string{
		or("SystemRoot", drive+`\Windows`),
		or("ProgramFiles", drive+`\Program Files`),
		or("ProgramFiles(x86)", drive+`\Program Files (x86)`),
		or("ProgramW6432", drive+`\Program Files`),
		or("ProgramData", drive+`\ProgramData`),
		drive + `\System Volume Information`,
		drive + `\Recovery`,
		drive + `\Boot`,
		drive + `\$Recycle.Bin`,
	}
	var out []string
	for _, c := range candidates {
		if p, err := CanonicalWindowsPath(c); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// windowsBindRoot checks one --allowed-bind-roots entry given as a Windows
// drive path and returns its canonical form.
func windowsBindRoot(r string, getenv func(string) string) (string, error) {
	p, err := CanonicalWindowsPath(r)
	if err != nil {
		return "", fmt.Errorf("--allowed-bind-roots: %w", err)
	}
	if len(p) == len(`C:\`) {
		return "", fmt.Errorf("--allowed-bind-roots: %q is a whole drive; name a folder on it", r)
	}
	for _, s := range windowsSystemPaths(getenv) {
		if windowsUnder(p, s) || windowsUnder(s, p) {
			return "", fmt.Errorf("--allowed-bind-roots: %q is, holds or is inside the system directory %s", r, s)
		}
	}
	return p, nil
}
