// SPDX-License-Identifier: Apache-2.0

// Package winacl locks the agent's secrets down on Windows. There the unix mode
// bits the rest of the agent sets are ignored, and a file under ProgramData
// inherits an ACL that lets every local user read it. Protect replaces that
// with an explicit, protected DACL (SYSTEM and Administrators full control, plus
// the agent's own account when it is not SYSTEM, nothing inherited) and takes
// ownership back from any other account, since an owner can always rewrite the
// DACL. Check refuses a file anyone else can open or owns. ProtectDir and
// Secure also refuse a folder, or any folder above it, that lets anyone else
// swap the agent's files. On other platforms the checks do nothing.
package winacl

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Well-known SIDs that may always hold access.
const (
	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
	// sidTrustedInstaller owns the system drive's root and Windows itself. It
	// may own a folder above the agent's; it never holds the agent's files.
	sidTrustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
)

// Access rights that change what a folder holds or who controls it.
const (
	fileAddFile     = 0x2
	fileAddSubdir   = 0x4
	fileDeleteChild = 0x40
	deleteRight     = 0x10000
	writeDAC        = 0x40000
	writeOwner      = 0x80000
	maximumAllowed  = 0x2000000
	genericAll      = 0x10000000
	genericWrite    = 0x40000000

	// sharedDirMask is what nobody else may hold on a folder the agent shares
	// with other files: adding a file lets them plant one before the agent
	// writes it (and keep it open), deleting a child lets them swap one the
	// agent already protected.
	sharedDirMask = fileAddFile | fileAddSubdir | fileDeleteChild | deleteRight | writeDAC | writeOwner | maximumAllowed | genericAll | genericWrite
	// ancestorMask is what nobody else may hold on a folder above the agent's:
	// any of it lets them move the agent's folder aside and put their own in
	// its place. Adding files to an ancestor is harmless and is the default on
	// C:\ and C:\ProgramData.
	ancestorMask = fileDeleteChild | deleteRight | writeDAC | writeOwner | maximumAllowed | genericAll
)

// ace is one entry of a DACL, reduced to what the verdict needs.
type ace struct {
	sid         string
	allow       bool // an access-allowed entry
	known       bool // a type this package can read; anything else is refused
	inheritOnly bool // passed down to children only; grants nothing on this object
	mask        uint32
}

// verdict decides whether a DACL keeps path to trusted principals. A missing
// (NULL) DACL grants everyone everything. An entry type we cannot read might
// grant access we cannot see, so it is refused too. Deny entries only take
// access away and are fine.
func verdict(path string, present bool, entries []ace, trusted map[string]bool) error {
	if !present {
		return fmt.Errorf("%s has no access control list, so every user can open it", path)
	}
	var others []string
	for _, e := range entries {
		switch {
		case !e.known:
			return fmt.Errorf("%s has an access entry of a type the agent cannot check; reset it with: icacls %q /reset", path, path)
		case !e.allow || e.mask == 0:
			continue
		case !trusted[e.sid]:
			others = append(others, e.sid)
		}
	}
	if len(others) > 0 {
		return fmt.Errorf("%s is readable by %s, not only SYSTEM and Administrators", path, strings.Join(others, ", "))
	}
	return nil
}

// ownerVerdict refuses an owner outside the trusted set: whoever owns a file
// can rewrite its access list whatever the list says today.
func ownerVerdict(path, owner string, trusted map[string]bool) error {
	if owner == "" {
		return fmt.Errorf("%s has no owner the agent can read", path)
	}
	if !trusted[owner] {
		return fmt.Errorf("%s is owned by %s, not SYSTEM, Administrators or the agent's account, so that account can reopen it", path, owner)
	}
	return nil
}

// mutationVerdict refuses a folder that an account outside trusted owns or may
// change in the ways mask names. Entries that only pass access down to
// children grant nothing on the folder itself and are skipped; the agent's
// own files never inherit, since Protect marks them protected.
func mutationVerdict(path, owner string, present bool, entries []ace, trusted map[string]bool, mask uint32, fix string) error {
	if err := ownerVerdict(path, owner, trusted); err != nil {
		return fmt.Errorf("%w; %s", err, fix)
	}
	if !present {
		return fmt.Errorf("%s has no access control list, so every user can change what it holds; %s", path, fix)
	}
	var others []string
	for _, e := range entries {
		switch {
		case !e.known:
			return fmt.Errorf("%s has an access entry of a type the agent cannot check; %s", path, fix)
		case !e.allow || e.inheritOnly || e.mask&mask == 0:
			continue
		case !trusted[e.sid]:
			others = append(others, e.sid)
		}
	}
	if len(others) > 0 {
		return fmt.Errorf("%s lets %s add, delete or rename what it holds, so the agent's files there could be swapped for others; %s", path, strings.Join(others, ", "), fix)
	}
	return nil
}

// AgentFiles names the files the agent keeps: its state (with the .tmp, .bak
// and .bak.tmp written beside it), its TLS pair in the data folder, its secret
// and its enrollment token. Empty fields name nothing.
type AgentFiles struct {
	State       string
	Data        string
	Secret      string
	EnrollToken string
}

// NamesIn lists the names of the agent's files that live in dir, however dir
// and the configured paths are spelled.
func (f AgentFiles) NamesIn(dir string) []string {
	var names []string
	if f.State != "" && sameDir(filepath.Dir(f.State), dir) {
		b := filepath.Base(f.State)
		names = append(names, b, b+".tmp", b+".bak", b+".bak.tmp")
	}
	if f.Data != "" && sameDir(f.Data, dir) {
		names = append(names, "cert.pem", "key.pem")
	}
	for _, p := range []string{f.Secret, f.EnrollToken} {
		if p != "" && sameDir(filepath.Dir(p), dir) {
			names = append(names, filepath.Base(p))
		}
	}
	return names
}

// Owns reports whether name, in dir, is one of the agent's files. Windows file
// names are compared without case, as NTFS does.
func (f AgentFiles) Owns(dir, name string) bool {
	for _, n := range f.NamesIn(dir) {
		if n == name || runtime.GOOS == "windows" && strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// sameDir reports whether a and b are the same folder, however they are
// spelled (a symlink, a relative path, another case on Windows). Two paths
// that cannot both be read are compared by their absolute spelling.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if ai, err := os.Stat(a); err == nil {
		if bi, err := os.Stat(b); err == nil {
			return os.SameFile(ai, bi)
		}
	}
	aa, err1 := filepath.Abs(a)
	ba, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aa, ba)
	}
	return aa == ba
}

var (
	filesMu sync.RWMutex
	files   AgentFiles
)

// SetAgentFiles records which files are the agent's, from its configuration.
// Call it before any folder is protected: a folder holding anything else is
// never re-permissioned.
func SetAgentFiles(f AgentFiles) {
	filesMu.Lock()
	files = f
	filesMu.Unlock()
}

func agentFiles() AgentFiles {
	filesMu.RLock()
	defer filesMu.RUnlock()
	return files
}

// OnlyAgentFiles reports whether dir holds nothing but the agent's own files,
// as SetAgentFiles named them. A protected DACL on a folder is pushed down to
// everything in it, so a folder holding anything else (another program's
// settings, a volume, a home directory) is not re-permissioned. A missing
// folder holds nothing.
func OnlyAgentFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	own := agentFiles()
	for _, e := range entries {
		if !e.Type().IsRegular() || !own.Owns(dir, e.Name()) {
			return false, nil
		}
	}
	return true, nil
}

// ProtectDir makes dir safe to keep the agent's files in, and reports whether
// it changed dir's access list. A folder holding only the agent's files is
// protected outright. A folder holding anything else keeps its access list,
// since rewriting it would change other programs' permissions, and is refused
// unless only trusted accounts own it and may add, delete or rename what it
// holds: anyone else could replace a protected file with their own, and the
// agent would then trust it. Either way every folder above it must not let
// anyone else move it aside.
func ProtectDir(dir string) (bool, error) {
	ok, err := OnlyAgentFiles(dir)
	if err != nil {
		return false, err
	}
	if !ok {
		if err := checkShared(dir); err != nil {
			return false, err
		}
		return false, checkAncestors(dir)
	}
	if err := Protect(dir); err != nil {
		return false, err
	}
	if err := Check(dir); err != nil {
		return false, err
	}
	return true, checkAncestors(dir)
}

// Secure makes path's folder safe (see ProtectDir), protects path and then
// checks the result, so a caller can rely on the file being closed to
// everyone but SYSTEM, Administrators and the agent, and on nobody else being
// able to swap it.
func Secure(path string) error {
	if guardsFolders {
		if _, err := ProtectDir(filepath.Dir(path)); err != nil {
			return fmt.Errorf("cannot keep %s safe: %w", path, err)
		}
	}
	if err := Protect(path); err != nil {
		return fmt.Errorf("cannot restrict access to %s: %w", path, err)
	}
	return Check(path)
}
