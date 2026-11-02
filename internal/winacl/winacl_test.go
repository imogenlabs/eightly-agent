// SPDX-License-Identifier: Apache-2.0

package winacl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const usersSID = "S-1-5-32-545"

var trusted = map[string]bool{sidSystem: true, sidAdministrators: true}

func allow(sid string) ace { return ace{sid: sid, allow: true, known: true, mask: 0x1F01FF} }

func TestVerdict(t *testing.T) {
	cases := []struct {
		name    string
		present bool
		entries []ace
		wantErr string
	}{
		{"system and admins only", true, []ace{allow(sidSystem), allow(sidAdministrators)}, ""},
		{"users can read", true, []ace{allow(sidSystem), allow(usersSID)}, usersSID},
		{"no dacl means everyone", false, nil, "every user"},
		{"empty dacl grants nothing", true, nil, ""},
		{"deny entries are fine", true, []ace{{sid: usersSID, known: true, mask: 0x1F01FF}, allow(sidSystem)}, ""},
		{"empty mask grants nothing", true, []ace{{sid: usersSID, allow: true, known: true}}, ""},
		{"unknown entry types are refused", true, []ace{allow(sidSystem), {}}, "cannot check"},
	}
	for _, c := range cases {
		err := verdict(`C:\x\state.json`, c.present, c.entries, trusted)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected refusal: %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: got %v, want an error containing %q", c.name, err, c.wantErr)
		}
	}
}

func TestOwnerVerdict(t *testing.T) {
	cases := []struct {
		owner, wantErr string
	}{
		{sidSystem, ""},
		{sidAdministrators, ""},
		{usersSID, "owned by " + usersSID},
		{"S-1-5-21-1-2-3-1001", "owned by"},
		{"", "no owner"},
	}
	for _, c := range cases {
		err := ownerVerdict(`C:\x\secret`, c.owner, trusted)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("owner %q: unexpected refusal: %v", c.owner, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("owner %q: got %v, want an error containing %q", c.owner, err, c.wantErr)
		}
	}
}

// useFiles records f as the agent's files for one test.
func useFiles(t *testing.T, f AgentFiles) {
	t.Helper()
	prev := agentFiles()
	SetAgentFiles(f)
	t.Cleanup(func() { SetAgentFiles(prev) })
}

func touch(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOnlyAgentFiles(t *testing.T) {
	dir := t.TempDir()
	useFiles(t, AgentFiles{
		State:       filepath.Join(dir, "state.json"),
		Data:        dir,
		Secret:      filepath.Join(dir, "secret"),
		EnrollToken: filepath.Join(dir, "enroll-token"),
	})
	missing := filepath.Join(dir, "nope")
	if ok, err := OnlyAgentFiles(missing); !ok || err != nil {
		t.Errorf("missing folder: %v %v, want true", ok, err)
	}
	touch(t, dir, "cert.pem", "key.pem", "secret", "enroll-token", "state.json", "state.json.tmp", "state.json.bak", "state.json.bak.tmp")
	if ok, err := OnlyAgentFiles(dir); !ok || err != nil {
		t.Errorf("agent-only folder: %v %v, want true", ok, err)
	}

	withVolume := t.TempDir()
	if err := os.Mkdir(filepath.Join(withVolume, "vol1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, _ := OnlyAgentFiles(withVolume); ok {
		t.Error("a folder holding a subfolder was treated as the agent's own")
	}

	shared := t.TempDir()
	touch(t, shared, "notes.txt")
	if ok, _ := OnlyAgentFiles(shared); ok {
		t.Error("a folder holding someone else's file was treated as the agent's own")
	}
	if done, err := ProtectDir(shared); done || err != nil {
		t.Errorf("ProtectDir on a shared folder = %v, %v; want it left alone", done, err)
	}
}

// Codex round 3: any .json made a folder the agent's, so a secret file put
// beside another program's app.json re-permissioned that program's folder.
// Only the configured files count.
func TestOnlyAgentFilesCountsConfiguredFilesOnly(t *testing.T) {
	dir := t.TempDir()
	useFiles(t, AgentFiles{Secret: filepath.Join(dir, "secret"), State: filepath.Join(t.TempDir(), "state.json")})
	touch(t, dir, "secret", "app.json")
	if ok, err := OnlyAgentFiles(dir); ok || err != nil {
		t.Errorf("a folder holding app.json beside the secret = %v, %v; want it treated as shared", ok, err)
	}
	// cert.pem and key.pem are the agent's only in its data folder.
	other := t.TempDir()
	touch(t, other, "cert.pem", "key.pem")
	if ok, _ := OnlyAgentFiles(other); ok {
		t.Error("a TLS pair outside the data folder was treated as the agent's")
	}
	// A state file not named *.json is still the agent's.
	custom := t.TempDir()
	useFiles(t, AgentFiles{State: filepath.Join(custom, "agent-state")})
	touch(t, custom, "agent-state", "agent-state.tmp")
	if ok, err := OnlyAgentFiles(custom); !ok || err != nil {
		t.Errorf("a custom-named state file = %v, %v; want it the agent's", ok, err)
	}
}

func TestAgentFilesNamesInFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	f := AgentFiles{Data: link, Secret: filepath.Join(link, "secret")}
	if !f.Owns(dir, "cert.pem") || !f.Owns(dir, "secret") {
		t.Errorf("files named through a symlink are not recognised in %s: %v", dir, f.NamesIn(dir))
	}
	if f.Owns(dir, "state.json") {
		t.Error("an unconfigured state file was recognised")
	}
	if (AgentFiles{}).Owns(dir, "cert.pem") {
		t.Error("an empty configuration owns cert.pem")
	}
	// Folders that do not exist yet compare by their spelling.
	gone := filepath.Join(dir, "later")
	if !(AgentFiles{Secret: filepath.Join(gone, "secret")}).Owns(gone, "secret") {
		t.Error("a missing folder is not compared by name")
	}
}

func TestMutationVerdict(t *testing.T) {
	const self = "S-1-5-21-1-2-3-1001"
	trusted := map[string]bool{sidSystem: true, sidAdministrators: true, self: true, sidTrustedInstaller: true}
	grant := func(sid string, mask uint32) ace { return ace{sid: sid, allow: true, known: true, mask: mask} }
	cases := []struct {
		name    string
		owner   string
		present bool
		mask    uint32
		entries []ace
		wantErr string
	}{
		{"read only for users", sidSystem, true, sharedDirMask, []ace{allow(sidSystem), grant(usersSID, 0x1200A9)}, ""},
		{"users may add files", sidSystem, true, sharedDirMask, []ace{allow(sidSystem), grant(usersSID, fileAddFile)}, usersSID},
		{"users may delete children", sidSystem, true, sharedDirMask, []ace{grant(usersSID, fileDeleteChild)}, usersSID},
		{"the review's case", sidSystem, true, sharedDirMask, []ace{allow(sidSystem), grant(usersSID, fileAddFile|fileDeleteChild)}, usersSID},
		{"generic write", sidSystem, true, sharedDirMask, []ace{grant(usersSID, genericWrite)}, usersSID},
		{"inherit-only grants nothing here", sidSystem, true, sharedDirMask, []ace{{sid: usersSID, allow: true, known: true, inheritOnly: true, mask: 0x1F01FF}}, ""},
		{"deny entries are fine", sidSystem, true, sharedDirMask, []ace{{sid: usersSID, known: true, mask: 0x1F01FF}}, ""},
		{"trusted accounts may change it", self, true, sharedDirMask, []ace{allow(sidSystem), allow(sidAdministrators), allow(self)}, ""},
		{"foreign owner", usersSID, true, sharedDirMask, nil, "owned by " + usersSID},
		{"no owner", "", true, sharedDirMask, nil, "no owner"},
		{"no dacl", sidSystem, false, sharedDirMask, nil, "every user"},
		{"unknown entry", sidSystem, true, sharedDirMask, []ace{{}}, "cannot check"},
		{"adding to an ancestor is fine", sidTrustedInstaller, true, ancestorMask, []ace{grant(usersSID, fileAddFile|fileAddSubdir)}, ""},
		{"deleting children of an ancestor is not", sidSystem, true, ancestorMask, []ace{grant(usersSID, fileDeleteChild)}, usersSID},
		{"renaming an ancestor is not", sidSystem, true, ancestorMask, []ace{grant("S-1-5-11", deleteRight)}, "S-1-5-11"},
		{"re-permissioning an ancestor is not", sidSystem, true, ancestorMask, []ace{grant(usersSID, writeDAC)}, usersSID},
	}
	for _, c := range cases {
		err := mutationVerdict(`C:\x`, c.owner, c.present, c.entries, trusted, c.mask, "fix it")
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected refusal: %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr) || !strings.Contains(err.Error(), "fix it")):
			t.Errorf("%s: got %v, want an error containing %q and the fix", c.name, err, c.wantErr)
		}
	}
}

func TestSecureOutsideWindows(t *testing.T) {
	if guardsFolders {
		t.Skip("Windows checks the folder; see winacl_windows_test.go")
	}
	path := filepath.Join(t.TempDir(), "secret")
	touch(t, filepath.Dir(path), "secret")
	if err := Secure(path); err != nil {
		t.Fatalf("Secure: %v", err)
	}
}
