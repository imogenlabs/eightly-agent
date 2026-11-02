//go:build windows

// SPDX-License-Identifier: Apache-2.0

package winacl

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// selfSID is the account the agent runs as. As a service that is SYSTEM, already
// trusted; run by hand it is the user who started it, who needs to read back
// what it wrote.
func selfSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func trustedSIDs() (map[string]bool, error) {
	self, err := selfSID()
	if err != nil {
		return nil, err
	}
	return map[string]bool{sidSystem: true, sidAdministrators: true, self: true}, nil
}

// Protect replaces path's DACL with full control for SYSTEM, Administrators and
// the agent's own account, marked protected so nothing is inherited from the
// parent. On a folder the entries are inheritable, so files created in it later
// start out closed too. An owner outside that set is replaced first: the owner
// may rewrite the DACL at any time, and a hostile DACL denies changing it until
// the owner has changed.
func Protect(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	self, err := selfSID()
	if err != nil {
		return err
	}
	if err := fixOwner(path, self); err != nil {
		return err
	}
	flags := ""
	if fi.IsDir() {
		flags = "OICI"
	}
	sddl := fmt.Sprintf("D:P(A;%[1]s;FA;;;SY)(A;%[1]s;FA;;;BA)", flags)
	if self != sidSystem && self != sidAdministrators {
		sddl += fmt.Sprintf("(A;%s;FA;;;%s)", flags, self)
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// owner returns the SID that owns path.
func owner(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return "", fmt.Errorf("cannot read the owner of %s: %w", path, err)
	}
	o, _, err := sd.Owner()
	if err != nil {
		return "", fmt.Errorf("cannot read the owner of %s: %w", path, err)
	}
	if o == nil {
		return "", nil
	}
	return o.String(), nil
}

// fixOwner hands path to BUILTIN\Administrators (or, run by hand without
// elevation, to the agent's own account) unless a trusted account already owns
// it. Taking a file from another account needs SeTakeOwnershipPrivilege, which
// SYSTEM and elevated administrators hold; it is enabled for this call only.
func fixOwner(path, self string) error {
	cur, err := owner(path)
	if err != nil {
		return err
	}
	trusted := map[string]bool{sidSystem: true, sidAdministrators: true, self: true}
	if cur != "" && trusted[cur] {
		return nil
	}
	target, err := ownerFor(self)
	if err != nil {
		return err
	}
	err = withPrivileges([]string{"SeTakeOwnershipPrivilege", "SeRestorePrivilege"}, func() error {
		return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION, target, nil, nil, nil)
	})
	if err != nil {
		if cur == "" {
			cur = "an unreadable owner"
		}
		return fmt.Errorf("%s is owned by %s and the agent cannot take it over (%w); run the agent as the service (SYSTEM) or from an elevated prompt, or delete %s so the agent creates it afresh", path, cur, err, path)
	}
	return nil
}

// ownerFor picks the owner the agent gives its files: Administrators when it
// runs as SYSTEM or elevated, its own account otherwise, which is the only
// owner an unprivileged process may set.
func ownerFor(self string) (*windows.SID, error) {
	if self == sidSystem || windows.GetCurrentProcessToken().IsElevated() {
		return windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	}
	return windows.StringToSid(self)
}

// withPrivileges runs fn with the named privileges enabled on this thread only.
// The process token is left alone: Go opens every file with backup semantics,
// so SeRestorePrivilege enabled process-wide would let any open in the agent
// pass straight through access lists. A privilege the account does not hold is
// skipped; fn then fails on its own with the reason.
func withPrivileges(names []string, fn func() error) error {
	runtime.LockOSThread()
	if err := windows.ImpersonateSelf(windows.SecurityImpersonation); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("cannot impersonate self: %w", err)
	}
	reverted := false
	defer func() {
		// A thread still impersonating must never run other goroutines; left
		// locked, it is thrown away when this goroutine exits.
		if reverted {
			runtime.UnlockOSThread()
		}
	}()
	defer func() { reverted = windows.RevertToSelf() == nil }()

	var tok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, true, &tok); err != nil {
		return fmt.Errorf("cannot open the thread token: %w", err)
	}
	defer func() { _ = tok.Close() }()
	for _, name := range names {
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
			continue
		}
		tp := windows.Tokenprivileges{PrivilegeCount: 1}
		tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
		// ERROR_NOT_ALL_ASSIGNED: the account lacks it; fn reports the outcome.
		_ = windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
	}
	return fn()
}

// guardsFolders makes Secure check the folder a file lives in: on Windows
// nothing else does.
const guardsFolders = true

// inheritOnlyACE marks an entry that only passes access down to children.
const inheritOnlyACE = 0x8

// readACL returns path's owner and access list, reduced to what the verdicts
// need. present is false for a NULL DACL, which grants everyone everything.
func readACL(path string) (owner string, present bool, entries []ace, err error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", false, nil, fmt.Errorf("cannot read the access list of %s: %w", path, err)
	}
	if o, _, err := sd.Owner(); err == nil && o != nil {
		owner = o.String()
	}
	dacl, _, err := sd.DACL()
	if err == windows.ERROR_OBJECT_NOT_FOUND || (err == nil && dacl == nil) {
		return owner, false, nil, nil
	}
	if err != nil {
		return "", false, nil, fmt.Errorf("cannot read the access list of %s: %w", path, err)
	}
	entries = make([]ace, 0, dacl.AceCount)
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &a); err != nil {
			return "", false, nil, fmt.Errorf("cannot read the access list of %s: %w", path, err)
		}
		switch a.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE, windows.ACCESS_DENIED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&a.SidStart)) // #nosec G103 -- the SID starts at SidStart inside the ACE, as the Win32 ACE layout defines
			entries = append(entries, ace{
				sid:         sid.String(),
				allow:       a.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE,
				known:       true,
				inheritOnly: a.Header.AceFlags&inheritOnlyACE != 0,
				mask:        uint32(a.Mask),
			})
		default:
			entries = append(entries, ace{})
		}
	}
	return owner, true, entries, nil
}

// Check refuses path unless SYSTEM, Administrators or the agent's own account
// owns it and only those are granted access to it.
func Check(path string) error {
	trusted, err := trustedSIDs()
	if err != nil {
		return err
	}
	o, present, entries, err := readACL(path)
	if err != nil {
		return err
	}
	if err := ownerVerdict(path, o, trusted); err != nil {
		return err
	}
	return verdict(path, present, entries, trusted)
}

// folderTrust is who may own or change a folder holding the agent's files:
// the accounts trusted with the files, and TrustedInstaller, which owns the
// system drive's root.
func folderTrust() (map[string]bool, error) {
	trusted, err := trustedSIDs()
	if err != nil {
		return nil, err
	}
	trusted[sidTrustedInstaller] = true
	return trusted, nil
}

// sharedFix is what an operator does about a shared folder others can change.
const sharedFix = "keep the agent's state, secret, enrollment token and TLS files in a folder of their own (the default is C:\\ProgramData\\eightly-agent) or remove that access"

// ancestorFix is what an operator does about a folder above the agent's that
// others can change.
func ancestorFix(dir string) string {
	return "it holds " + dir + ", so move the agent's folder somewhere only SYSTEM and Administrators can change, such as C:\\ProgramData\\eightly-agent"
}

// checkShared refuses a folder that holds other files when anyone untrusted
// owns it or may add, delete or rename what is in it.
func checkShared(dir string) error {
	trusted, err := folderTrust()
	if err != nil {
		return err
	}
	o, present, entries, err := readACL(dir)
	if err != nil {
		return err
	}
	return mutationVerdict(dir, o, present, entries, trusted, sharedDirMask, sharedFix)
}

// checkAncestors walks from dir's parent to the volume root and refuses any
// folder there that an untrusted account owns or may delete children of,
// rename, or re-permission: with any of those the agent's whole folder can be
// moved aside and replaced. A volume root cannot itself be renamed.
func checkAncestors(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	trusted, err := folderTrust()
	if err != nil {
		return err
	}
	for child := abs; ; {
		parent := filepath.Dir(child)
		if parent == child {
			return nil
		}
		mask := uint32(ancestorMask)
		if filepath.Dir(parent) == parent {
			mask &^= deleteRight
		}
		o, present, entries, err := readACL(parent)
		if err != nil {
			return err
		}
		if err := mutationVerdict(parent, o, present, entries, trusted, mask, ancestorFix(abs)); err != nil {
			return err
		}
		child = parent
	}
}
