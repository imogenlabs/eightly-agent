//go:build windows

// SPDX-License-Identifier: Apache-2.0

package register

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/imogenlabs/eightly-agent/internal/winacl"
)

// secureState closes the saved state (it holds the agent's bearer token) to
// everyone but SYSTEM, Administrators and the agent, and refuses it if that does
// not hold. Under ProgramData the file and its folder otherwise inherit read
// access for every local user, and the unix mode check is meaningless here.
// An existing install is repaired on its next start rather than refused, so it
// keeps its identity instead of burning a single-use enrollment token.
func secureState(path string) error {
	if _, err := winacl.ProtectDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("cannot restrict access to %s: %w", filepath.Dir(path), err)
	}
	return winacl.Secure(path)
}

// prepareStateWrite creates the state's temporary file empty and closes it
// before any token is written into it; the rename keeps that access list.
func prepareStateWrite(tmp string) error {
	if _, err := winacl.ProtectDir(filepath.Dir(tmp)); err != nil {
		return fmt.Errorf("cannot restrict access to %s: %w", filepath.Dir(tmp), err)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- path comes from admin-configured --state-path flag
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return winacl.Secure(tmp)
}

// checkStateDirACL refuses a state folder that secureState would refuse once a
// registration is saved there: a shared folder others can change, or one under
// a folder that lets them move it aside.
func checkStateDirACL(dir string) error {
	if _, err := winacl.ProtectDir(dir); err != nil {
		return fmt.Errorf("cannot restrict access to %s: %w", dir, err)
	}
	return nil
}
