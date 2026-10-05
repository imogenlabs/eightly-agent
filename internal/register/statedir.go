// SPDX-License-Identifier: Apache-2.0

package register

import (
	"fmt"
	"os"
	"path/filepath"
)

// chmodDir is os.Chmod, a variable so a test can make tightening fail the way
// a read-only or foreign-owned mount does without needing root.
var chmodDir = os.Chmod

// PrepareStateDir brings the state file's directory down to 0700 when the agent
// owns it and it is broader than that, and reports whether it changed anything.
//
// EOS-2534: Docker creates a new volume directory 0755. The agent saved its
// registration there, and on the next start LoadState refused the directory, so
// the agent discarded its saved identity and tried to enroll again with a
// single-use token that was already spent. Tightening a directory the agent
// owns closes the same exposure the guard exists for, so it is the fix, not a
// bypass. It acts only once a state file exists. When the directory belongs to someone else, or the chmod fails (a
// read-only mount), it returns an error saying so and changes nothing, and
// LoadState goes on refusing exactly as before. The file check is untouched:
// a state file broader than 0600 is still refused.
func PrepareStateDir(statePath, dataPath string) (bool, error) {
	if !permsEnforced {
		return false, nil
	}
	// No saved state means no credential to protect, so the directory is left
	// as it is: it may be shared (a home directory) and is not ours to narrow.
	// A Docker volume is repaired on the next start, once the state is there.
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	dir := filepath.Dir(statePath)
	fi, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	perm := fi.Mode().Perm()
	if perm&^0o700 == 0 {
		return false, nil
	}
	if uid, ok := fileUID(fi); ok && uid != os.Getuid() {
		return false, fmt.Errorf("cannot tighten %s (mode %04o): it is owned by uid %d, not the agent uid %d; make it 0700 and owned by uid %d", dir, perm, uid, os.Getuid(), os.Getuid())
	}
	if err := chmodDir(dir, 0o700); err != nil {
		return false, fmt.Errorf("cannot tighten %s from mode %04o to 0700: %w", dir, perm, err)
	}
	return true, nil
}
