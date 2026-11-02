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

func TestOnlyAgentFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	if ok, err := OnlyAgentFiles(missing); !ok || err != nil {
		t.Errorf("missing folder: %v %v, want true", ok, err)
	}
	for _, name := range []string{"cert.pem", "key.pem", "secret", "state.json", "state.json.tmp", "eightly-agent.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
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
	if err := os.WriteFile(filepath.Join(shared, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := OnlyAgentFiles(shared); ok {
		t.Error("a folder holding someone else's file was treated as the agent's own")
	}
	if done, err := ProtectDir(shared); done || err != nil {
		t.Errorf("ProtectDir on a shared folder = %v, %v; want it left alone", done, err)
	}
}
