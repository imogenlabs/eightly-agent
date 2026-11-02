// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestCanonicalWindowsPath(t *testing.T) {
	for in, want := range map[string]string{
		`C:\AppData`:             `C:\AppData`,
		`C:/AppData`:             `C:\AppData`,
		`c:\AppData\`:            `C:\AppData`,
		`d:/media//films/`:       `D:\media\films`,
		`C:\Program Files (x86)`: `C:\Program Files (x86)`,
		`C:\`:                    `C:\`,
	} {
		got, err := CanonicalWindowsPath(in)
		if err != nil || got != want {
			t.Errorf("CanonicalWindowsPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		`AppData`, `/srv`, `C:AppData`, `\AppData`, `\\server\share\x`, `\\?\C:\x`, `//server/share`,
		`C:\a\..\Windows`, `C:\a\.\b`, `C:\Windows.`, `C:\Windows \x`, `C:\PROGRA~1`,
		`C:\data:stream`, `C:\a*b`, "C:\\a\x01b", `1:\x`,
	} {
		if got, err := CanonicalWindowsPath(bad); err == nil {
			t.Errorf("CanonicalWindowsPath(%q) = %q; want a refusal", bad, got)
		}
	}
}

func TestWindowsUnder(t *testing.T) {
	cases := []struct {
		p, root string
		want    bool
	}{
		{`C:\AppData`, `C:\AppData`, true},
		{`c:\appdata`, `C:\AppData`, true},
		{`C:\AppData\x`, `C:\AppData`, true},
		{`C:\AppDataX`, `C:\AppData`, false},
		{`D:\AppData`, `C:\AppData`, false},
		{`C:\x`, `C:\`, true},
	}
	for _, c := range cases {
		if got := windowsUnder(c.p, c.root); got != c.want {
			t.Errorf("windowsUnder(%q, %q) = %v, want %v", c.p, c.root, got, c.want)
		}
	}
}

func noEnv(string) string { return "" }

// Codex round 3: on native Windows C:\AppData and C:/AppData were refused as
// bind roots. Both are taken there now, in one canonical form.
func TestBindRootsWindows(t *testing.T) {
	roots, err := bindRoots(` C:\AppData, c:/Media/ ,/srv`, true, noEnv)
	if err != nil {
		t.Fatalf("bindRoots: %v", err)
	}
	if want := []string{`C:\AppData`, `C:\Media`, `/srv`}; strings.Join(roots, "|") != strings.Join(want, "|") {
		t.Fatalf("roots = %q, want %q", roots, want)
	}
	for _, bad := range []string{
		`C:\`, `C:/`, `C:\Windows`, `c:\windows\system32`, `C:\Program Files\x`, `C:\Program Files (x86)`,
		`C:\ProgramData`, `C:\ProgramData\docker\volumes`, `C:\ProgramData\eightly-agent`,
		`C:\System Volume Information`, `C:\Windows.`, `C:\PROGRA~1`, `\\server\share`, `C:AppData`, `C:\a\..\Windows`,
	} {
		if roots, err := bindRoots(bad, true, noEnv); err == nil {
			t.Errorf("bindRoots(%q) on Windows = %q; want a refusal", bad, roots)
		}
	}
	// Windows installed on another drive is read from the environment.
	env := func(k string) string {
		return map[string]string{"SystemDrive": "D:", "SystemRoot": `D:\Win`, "ProgramData": `D:\ProgramData`}[k]
	}
	if _, err := bindRoots(`D:\Win\x`, true, env); err == nil {
		t.Error("a root inside SystemRoot on another drive was accepted")
	}
	if _, err := bindRoots(`C:\Windows`, true, env); err != nil {
		t.Errorf("C:\\Windows is not a system folder when Windows lives on D: but was refused: %v", err)
	}
}

// Elsewhere a drive path stays refused: the daemon would read it as a
// relative path.
func TestBindRootsDrivePathOutsideWindows(t *testing.T) {
	for _, r := range []string{`C:\AppData`, `C:/AppData`} {
		if _, err := bindRoots(r, false, noEnv); err == nil {
			t.Errorf("bindRoots(%q) accepted a drive path off Windows", r)
		}
	}
}

func TestAgentFiles(t *testing.T) {
	c := &Config{StatePath: "/d/s.json", DataPath: "/d", SecretFile: "/d/secret", EnrollTokenFile: "/t/tok"}
	f := c.AgentFiles()
	if f.State != c.StatePath || f.Data != c.DataPath || f.Secret != c.SecretFile || f.EnrollToken != c.EnrollTokenFile {
		t.Fatalf("AgentFiles = %+v", f)
	}
}
