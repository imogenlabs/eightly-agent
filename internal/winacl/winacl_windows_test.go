//go:build windows

// SPDX-License-Identifier: Apache-2.0

package winacl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// openToUsers gives BUILTIN\Users read access, the way a file under ProgramData
// inherits it.
func openToUsers(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRefusesAFileUsersCanRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	openToUsers(t, path)
	if err := Check(path); err == nil {
		t.Fatal("a file BUILTIN\\Users can read passed the check")
	}
	if err := Secure(path); err != nil {
		t.Fatalf("Secure did not close the file: %v", err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("the agent can no longer read its own file: %v", err)
	}
}

func TestProtectDirClosesNewFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	openToUsers(t, dir)
	if done, err := ProtectDir(dir); !done || err != nil {
		t.Fatalf("ProtectDir = %v, %v", done, err)
	}
	if err := Check(dir); err != nil {
		t.Fatalf("protected folder failed the check: %v", err)
	}
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(path); err != nil {
		t.Fatalf("a file created in the protected folder is open: %v", err)
	}
}

// giveTo makes sid the owner of path, the way a non-admin who pre-creates the
// data folder owns it. Setting another account as owner needs
// SeRestorePrivilege, so this needs an elevated test run.
func giveTo(t *testing.T, path, sid string) {
	t.Helper()
	s, err := windows.StringToSid(sid)
	if err != nil {
		t.Fatal(err)
	}
	err = withPrivileges([]string{"SeRestorePrivilege"}, func() error {
		return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, s, nil, nil, nil)
	})
	if err != nil {
		t.Fatalf("cannot hand %s to %s: %v", path, sid, err)
	}
}

func requireElevated(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("needs an elevated run to hand a file to another owner")
	}
}

func TestCheckRefusesAForeignOwnerAndProtectTakesItBack(t *testing.T) {
	requireElevated(t)
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Secure(path); err != nil {
		t.Fatalf("Secure: %v", err)
	}
	giveTo(t, path, usersSID)
	if err := Check(path); err == nil || !strings.Contains(err.Error(), "owned by "+usersSID) {
		t.Fatalf("a file owned by BUILTIN\\Users passed the check: %v", err)
	}
	if err := Secure(path); err != nil {
		t.Fatalf("Secure did not take the file back: %v", err)
	}
	if o, err := owner(path); err != nil || o != sidAdministrators {
		t.Fatalf("owner after Secure = %q, %v; want Administrators", o, err)
	}
}

func TestProtectDirTakesBackAForeignOwnedFolder(t *testing.T) {
	requireElevated(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A hostile owner also leaves an access list that shuts everyone else out.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	giveTo(t, dir, usersSID)
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if done, err := ProtectDir(dir); !done || err != nil {
		t.Fatalf("ProtectDir = %v, %v; want the folder taken back", done, err)
	}
	if err := Check(dir); err != nil {
		t.Fatalf("taken-back folder fails the check: %v", err)
	}
}

func TestProtectDirRefusesASharedFolderSomeoneElseOwns(t *testing.T) {
	requireElevated(t)
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	giveTo(t, dir, usersSID)
	if done, err := ProtectDir(dir); done || err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("ProtectDir on a foreign-owned shared folder = %v, %v; want a refusal", done, err)
	}
}
