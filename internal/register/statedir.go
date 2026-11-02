// SPDX-License-Identifier: Apache-2.0

package register

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// chmodDir is os.Chmod, a variable so a test can make tightening fail the way
// a read-only or foreign-owned mount does without needing root.
var chmodDir = os.Chmod

// PrepareStateDir brings the state file's directory down to 0700 when the agent
// owns it and it is broader than that, and reports whether it changed anything.
//
// Docker creates a new volume directory 0755. The agent saved its
// registration there, and on the next start LoadState refused the directory, so
// the agent discarded its saved identity and tried to enroll again with a
// single-use token that was already spent. Tightening a directory the agent
// owns closes the same exposure the guard exists for, so it is the fix, not a
// bypass. It acts only once a state file exists. When the directory belongs to someone else, or the chmod fails (a
// read-only mount), it returns an error saying so and changes nothing, and
// LoadState goes on refusing exactly as before. The file check is untouched:
// a state file broader than 0600 is still refused.
//
// It also tightens only a folder that holds nothing but the agent's
// own files (the state file and its .tmp, and the TLS key pair when the folder
// is dataPath). A shared folder such as a home directory is left alone with
// the reason, and the saved state stays refused.
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
	if other, err := otherFiles(dir, statePath, dataPath); err != nil {
		return false, fmt.Errorf("cannot tighten %s (mode %04o): its contents could not be listed: %w", dir, perm, err)
	} else if len(other) > 0 {
		// --state-path can point into a shared folder such as a home
		// directory. Narrowing it would lock everything else there away from
		// every other user, so only a folder that is wholly the agent's is.
		return false, fmt.Errorf("not tightening %s (mode %04o): it also holds %s, which are not the agent's; point --state-path at a folder of its own", dir, perm, strings.Join(other, ", "))
	}
	if err := chmodDir(dir, 0o700); err != nil {
		return false, fmt.Errorf("cannot tighten %s from mode %04o to 0700: %w", dir, perm, err)
	}
	return true, nil
}

// otherFiles lists what in dir is not the agent's own: the state file and its
// .tmp, plus the TLS key pair when dir is also the agent's data folder. The
// list is capped so a home directory makes a readable log line.
func otherFiles(dir, statePath, dataPath string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	own := map[string]bool{filepath.Base(statePath): true, filepath.Base(statePath) + ".tmp": true}
	if sameDir(dataPath, dir) {
		own["cert.pem"], own["key.pem"] = true, true
	}
	var other []string
	for _, e := range entries {
		if !own[e.Name()] {
			other = append(other, e.Name())
		}
	}
	sort.Strings(other)
	if len(other) > 5 {
		other = append(other[:5], fmt.Sprintf("and %d more", len(other)-5))
	}
	return other, nil
}

// sameDir reports whether a and b are the same directory on disk, however they
// are spelled (a symlink, a relative path). A path that cannot be read is not.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}
