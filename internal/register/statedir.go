// SPDX-License-Identifier: Apache-2.0

package register

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imogenlabs/eightly-agent/internal/winacl"
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
// It also tightens only a folder that holds nothing but the agent's own files
// as files names them (the state file with its .tmp and .bak, the TLS pair
// when the folder is the data folder, the secret and enrollment token files).
// A shared folder such as a home directory is left alone with the reason, and
// the saved state stays refused.
func PrepareStateDir(files winacl.AgentFiles) (bool, error) {
	if !permsEnforced {
		return false, nil
	}
	// No saved state means no credential to protect yet; CheckStateDir
	// handles the folder before the agent enrolls.
	if _, err := os.Stat(files.State); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return tightenStateDir(files)
}

// CheckStateDir refuses a state folder that LoadState would refuse once a
// registration is saved there, tightening it first when PrepareStateDir would.
// Call it before spending a single-use enrollment token: a registration saved
// into a folder the next start refuses is lost, and the token with it. The
// folder must exist (CheckWritable creates it).
func CheckStateDir(files winacl.AgentFiles) error {
	dir := filepath.Dir(files.State)
	if !permsEnforced {
		return checkStateDirACL(dir)
	}
	if _, err := tightenStateDir(files); err != nil {
		return err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	return assertPerm(dir, fi, 0o700)
}

// tightenStateDir chmods the state folder to 0700 when it is broader, the
// agent's uid owns it and it holds only the agent's files.
func tightenStateDir(files winacl.AgentFiles) (bool, error) {
	dir := filepath.Dir(files.State)
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
	if other, err := otherFiles(dir, files); err != nil {
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

// otherFiles lists what in dir is not the agent's own. The list is capped so
// a home directory makes a readable log line.
func otherFiles(dir string, files winacl.AgentFiles) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var other []string
	for _, e := range entries {
		if !files.Owns(dir, e.Name()) {
			other = append(other, e.Name())
		}
	}
	sort.Strings(other)
	if len(other) > 5 {
		other = append(other[:5], fmt.Sprintf("and %d more", len(other)-5))
	}
	return other, nil
}
