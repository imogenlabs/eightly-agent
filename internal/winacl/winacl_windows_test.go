//go:build windows

// SPDX-License-Identifier: Apache-2.0

package winacl

import (
	"os"
	"path/filepath"
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
