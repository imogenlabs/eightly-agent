//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package register

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/imogenlabs/eightly-agent/internal/winacl"
)

// volumeDir makes a directory with the mode Docker gives a new volume, holding
// a correctly locked-down state file.
func volumeDir(t *testing.T) (dir, statePath string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	statePath = filepath.Join(dir, "eightly-agent.json")
	if err := (&State{AgentID: "agent-1", Token: "tok"}).Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	return dir, statePath
}

func dirPerm(t *testing.T, dir string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return fi.Mode().Perm()
}

// An owned 0755 directory is tightened, after which the saved
// state loads.
func TestPrepareStateDir_TightensOwnedVolumeDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)

	tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath})
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	if !tightened {
		t.Error("reported nothing changed on a 0755 directory")
	}
	if perm := dirPerm(t, dir); perm != 0o700 {
		t.Fatalf("directory is %04o after tightening, want 0700", perm)
	}
	s, err := LoadState(statePath)
	if err != nil || s == nil || s.AgentID != "agent-1" {
		t.Fatalf("saved state did not load after tightening: %+v, %v", s, err)
	}
}

func TestPrepareStateDir_LeavesTightDirAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	chmodDir = func(string, os.FileMode) error {
		t.Error("chmod called on a directory that was already 0700")
		return nil
	}
	t.Cleanup(func() { chmodDir = os.Chmod })

	statePath := filepath.Join(dir, "eightly-agent.json")
	if err := (&State{AgentID: "agent-1", Token: "tok"}).Save(statePath); err != nil {
		t.Fatalf("save: %v", err)
	}
	tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath})
	if err != nil || tightened {
		t.Fatalf("PrepareStateDir on a 0700 dir = (%v, %v), want (false, nil)", tightened, err)
	}
}

func TestPrepareStateDir_MissingDirIsFirstStart(t *testing.T) {
	tightened, err := PrepareStateDir(winacl.AgentFiles{State: filepath.Join(t.TempDir(), "absent", "eightly-agent.json")})
	if err != nil || tightened {
		t.Fatalf("PrepareStateDir on a missing dir = (%v, %v), want (false, nil)", tightened, err)
	}
}

// AC1, the other half: when the directory cannot be tightened (read-only
// mount), say so, leave it as it is, and keep refusing the saved state.
func TestPrepareStateDir_CannotTightenKeepsRefusal(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	chmodDir = func(string, os.FileMode) error { return &os.PathError{Op: "chmod", Path: dir, Err: syscall.EROFS} }
	t.Cleanup(func() { chmodDir = os.Chmod })

	tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath})
	if err == nil {
		t.Fatal("a failed chmod was reported as success")
	}
	if tightened {
		t.Error("reported tightened when the chmod failed")
	}
	if !errors.Is(err, syscall.EROFS) || !strings.Contains(err.Error(), "cannot tighten") {
		t.Errorf("error does not say plainly what failed: %v", err)
	}
	if perm := dirPerm(t, dir); perm != 0o755 {
		t.Errorf("directory changed to %04o despite the failure", perm)
	}
	if _, err := LoadState(statePath); err == nil || !strings.Contains(err.Error(), "broader than 0700") {
		t.Fatalf("LoadState must still refuse the 0755 directory, got %v", err)
	}
}

// A directory another user owns is never chmodded, even though the call
// might succeed for root.
func TestPrepareStateDir_ForeignOwnedDirIsNotTouched(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: every directory is the agent's own")
	}
	chmodDir = func(string, os.FileMode) error {
		t.Error("chmod called on a directory the agent does not own")
		return nil
	}
	t.Cleanup(func() { chmodDir = os.Chmod })

	// /etc is root-owned and 0755 on every unix this runs on, and /etc/hosts
	// exists, so this reaches the ownership check rather than the
	// missing-file early return.
	_, err := PrepareStateDir(winacl.AgentFiles{State: "/etc/hosts"})
	if err == nil || !strings.Contains(err.Error(), "owned by uid 0") {
		t.Fatalf("expected a plain ownership refusal, got %v", err)
	}
}

// The file guard is not weakened: a 0644 state file in a directory the agent
// just tightened is still refused.
func TestPrepareStateDir_DoesNotLoosenFileGuard(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks behave differently")
	}
	_, statePath := volumeDir(t)
	if err := os.Chmod(statePath, 0o644); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	if _, err := PrepareStateDir(winacl.AgentFiles{State: statePath}); err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	if _, err := LoadState(statePath); err == nil || !strings.Contains(err.Error(), "broader than 0600") {
		t.Fatalf("a 0644 state file must still be refused, got %v", err)
	}
}

// A missing state file has no credential to protect, so its directory is left
// exactly as it is. A first run pointed at a file in a shared directory (for
// example --state-path /home/agent/eightly-agent.json) must not take that
// directory away from other users and services.
func TestPrepareStateDir_MissingFileLeavesDirAlone(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	chmodDir = func(string, os.FileMode) error {
		t.Error("chmod called on a directory holding no state file")
		return nil
	}
	t.Cleanup(func() { chmodDir = os.Chmod })

	tightened, err := PrepareStateDir(winacl.AgentFiles{State: filepath.Join(dir, "eightly-agent.json")})
	if err != nil || tightened {
		t.Fatalf("PrepareStateDir with no state file = (%v, %v), want (false, nil)", tightened, err)
	}
	if perm := dirPerm(t, dir); perm != 0o755 {
		t.Fatalf("directory changed to %04o with no state file in it", perm)
	}
}

// --state-path can point anywhere, for example into a home
// directory. The first restart used to narrow that whole directory to 0700,
// locking everything else in it away from every other user and process. Only
// a directory holding nothing but the agent's own files is the agent's to
// tighten; anything else is left alone, with the reason, and the saved state
// stays refused.
func TestPrepareStateDir_PopulatedFolderIsNotTightened(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	chmodDir = func(string, os.FileMode) error {
		t.Error("chmod called on a folder that holds other files")
		return nil
	}
	t.Cleanup(func() { chmodDir = os.Chmod })

	tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath})
	if tightened || err == nil {
		t.Fatalf("PrepareStateDir on a populated folder = (%v, %v), want (false, a reason)", tightened, err)
	}
	if !strings.Contains(err.Error(), "notes.txt") {
		t.Errorf("the reason does not name what else is there: %v", err)
	}
	if perm := dirPerm(t, dir); perm != 0o755 {
		t.Errorf("folder changed to %04o", perm)
	}
	if _, err := LoadState(statePath); err == nil {
		t.Fatal("the saved state must still be refused in a 0755 folder")
	}
}

// The agent's data folder holds its TLS key pair beside the state file; those
// are its own files too, so a Docker volume is still repaired.
func TestPrepareStateDir_DataFolderWithOwnFilesIsTightened(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	for _, name := range []string{"cert.pem", "key.pem", "eightly-agent.json.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath, Data: dir})
	if err != nil || !tightened {
		t.Fatalf("PrepareStateDir on the agent's own data folder = (%v, %v), want (true, nil)", tightened, err)
	}
	if perm := dirPerm(t, dir); perm != 0o700 {
		t.Errorf("folder left at %04o", perm)
	}
}

// cert.pem and key.pem are only the agent's when the folder is its data
// folder; in some other folder they are somebody else's.
func TestPrepareStateDir_CertFilesOutsideTheDataFolderAreNotOurs(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	elsewhere := t.TempDir() // a real folder, so the comparison is actually made
	if tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath, Data: elsewhere}); tightened || err == nil {
		t.Fatalf("got (%v, %v); a cert.pem outside the data folder is not the agent's", tightened, err)
	}
}

// Codex on #35: --data and the state folder can name the same directory by
// different spellings (a symlink, a relative path). The TLS pair is still the
// agent's own, so the folder is still repaired.
func TestPrepareStateDir_DataFolderNamedDifferentlyIsStillOurs(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	for _, name := range []string{"cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	link := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	tightened, err := PrepareStateDir(winacl.AgentFiles{State: statePath, Data: link})
	if err != nil || !tightened {
		t.Fatalf("data folder given through a symlink = (%v, %v), want (true, nil)", tightened, err)
	}
}

// Codex round 3: with --secret-file inside the data folder, the secret was
// counted as somebody else's, so a restart refused to repair the folder and
// then refused the saved state. The configured secret and enrollment token,
// and the backup a server switch leaves, are the agent's own.
func TestPrepareStateDir_SecretAndTokenFilesAreOurs(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	for _, name := range []string{"secret", "enroll-token", "eightly-agent.json.bak", "eightly-agent.json.bak.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	files := winacl.AgentFiles{
		State:       statePath,
		Data:        dir,
		Secret:      filepath.Join(dir, "secret"),
		EnrollToken: filepath.Join(dir, "enroll-token"),
	}
	tightened, err := PrepareStateDir(files)
	if err != nil || !tightened {
		t.Fatalf("PrepareStateDir with the secret in the data folder = (%v, %v), want (true, nil)", tightened, err)
	}
	if _, err := LoadState(statePath); err != nil {
		t.Fatalf("the saved state is still refused after the repair: %v", err)
	}
}

// A secret file somewhere else does not make a file of the same name in the
// state folder the agent's.
func TestPrepareStateDir_SecretElsewhereIsNotOurs(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	files := winacl.AgentFiles{State: statePath, Secret: filepath.Join(t.TempDir(), "secret")}
	if tightened, err := PrepareStateDir(files); tightened || err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("got (%v, %v); a secret file that is not the configured one is not the agent's", tightened, err)
	}
}

// Before enrolling, an agent-only 0755 folder is tightened, so the
// registration saved there loads on the next start.
func TestCheckStateDir_TightensBeforeEnrolling(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "eightly-agent.json")
	files := winacl.AgentFiles{State: statePath, Data: dir, Secret: filepath.Join(dir, "secret")}
	if err := CheckStateDir(files); err != nil {
		t.Fatalf("CheckStateDir on the agent's own 0755 folder: %v", err)
	}
	if perm := dirPerm(t, dir); perm != 0o700 {
		t.Fatalf("folder left at %04o", perm)
	}
	if err := (&State{AgentID: "a", Token: "t"}).Save(statePath); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(statePath); err != nil {
		t.Fatalf("the registration saved after the check is refused: %v", err)
	}
}

// A shared 0755 folder is not tightened, and enrolling into it is refused
// before the token is spent rather than after.
func TestCheckStateDir_RefusesASharedFolder(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := CheckStateDir(winacl.AgentFiles{State: filepath.Join(dir, "eightly-agent.json")})
	if err == nil || !strings.Contains(err.Error(), "notes.txt") {
		t.Fatalf("CheckStateDir on a shared 0755 folder = %v, want a refusal naming notes.txt", err)
	}
	if perm := dirPerm(t, dir); perm != 0o755 {
		t.Errorf("shared folder changed to %04o", perm)
	}
}

// A folder already at 0700 passes untouched.
func TestCheckStateDir_TightFolderPasses(t *testing.T) {
	dir := t.TempDir()
	if err := CheckStateDir(winacl.AgentFiles{State: filepath.Join(dir, "eightly-agent.json")}); err != nil {
		t.Fatalf("CheckStateDir on a 0700 folder: %v", err)
	}
}
