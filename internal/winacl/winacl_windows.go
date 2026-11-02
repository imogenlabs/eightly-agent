//go:build windows

// SPDX-License-Identifier: Apache-2.0

package winacl

import (
	"fmt"
	"os"
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
// start out closed too.
func Protect(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	self, err := selfSID()
	if err != nil {
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

// Check refuses path unless only SYSTEM, Administrators and the agent's own
// account are granted access to it.
func Check(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot read the access list of %s: %w", path, err)
	}
	trusted, err := trustedSIDs()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err == windows.ERROR_OBJECT_NOT_FOUND || (err == nil && dacl == nil) {
		return verdict(path, false, nil, trusted)
	}
	if err != nil {
		return fmt.Errorf("cannot read the access list of %s: %w", path, err)
	}
	entries := make([]ace, 0, dacl.AceCount)
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &a); err != nil {
			return fmt.Errorf("cannot read the access list of %s: %w", path, err)
		}
		switch a.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE, windows.ACCESS_DENIED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&a.SidStart)) // #nosec G103 -- the SID starts at SidStart inside the ACE, as the Win32 ACE layout defines
			entries = append(entries, ace{
				sid:   sid.String(),
				allow: a.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE,
				known: true,
				mask:  uint32(a.Mask),
			})
		default:
			entries = append(entries, ace{})
		}
	}
	return verdict(path, true, entries, trusted)
}
