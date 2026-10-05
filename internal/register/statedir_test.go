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

// EOS-2534 AC1: an owned 0755 directory is tightened, after which the saved
// state loads.
func TestPrepareStateDir_TightensOwnedVolumeDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; ownership checks behave differently")
	}
	dir, statePath := volumeDir(t)

	tightened, err := PrepareStateDir(statePath)
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
	tightened, err := PrepareStateDir(statePath)
	if err != nil || tightened {
		t.Fatalf("PrepareStateDir on a 0700 dir = (%v, %v), want (false, nil)", tightened, err)
	}
}

func TestPrepareStateDir_MissingDirIsFirstStart(t *testing.T) {
	tightened, err := PrepareStateDir(filepath.Join(t.TempDir(), "absent", "eightly-agent.json"))
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

	tightened, err := PrepareStateDir(statePath)
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
	_, err := PrepareStateDir("/etc/hosts")
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
	if _, err := PrepareStateDir(statePath); err != nil {
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

	tightened, err := PrepareStateDir(filepath.Join(dir, "eightly-agent.json"))
	if err != nil || tightened {
		t.Fatalf("PrepareStateDir with no state file = (%v, %v), want (false, nil)", tightened, err)
	}
	if perm := dirPerm(t, dir); perm != 0o755 {
		t.Fatalf("directory changed to %04o with no state file in it", perm)
	}
}
