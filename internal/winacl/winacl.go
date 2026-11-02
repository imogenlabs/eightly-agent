// SPDX-License-Identifier: Apache-2.0

// Package winacl locks the agent's secrets down on Windows. There the unix mode
// bits the rest of the agent sets are ignored, and a file under ProgramData
// inherits an ACL that lets every local user read it. Protect replaces that
// with an explicit, protected DACL (SYSTEM and Administrators full control, plus
// the agent's own account when it is not SYSTEM, nothing inherited), and Check
// refuses a file anyone else can open. On other platforms both do nothing.
package winacl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Well-known SIDs that may always hold access.
const (
	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
)

// ace is one entry of a DACL, reduced to what the verdict needs.
type ace struct {
	sid   string
	allow bool // an access-allowed entry
	known bool // a type this package can read; anything else is refused
	mask  uint32
}

// verdict decides whether a DACL keeps path to trusted principals. A missing
// (NULL) DACL grants everyone everything. An entry type we cannot read might
// grant access we cannot see, so it is refused too. Deny entries only take
// access away and are fine.
func verdict(path string, present bool, entries []ace, trusted map[string]bool) error {
	if !present {
		return fmt.Errorf("%s has no access control list, so every user can open it", path)
	}
	var others []string
	for _, e := range entries {
		switch {
		case !e.known:
			return fmt.Errorf("%s has an access entry of a type the agent cannot check; reset it with: icacls %q /reset", path, path)
		case !e.allow || e.mask == 0:
			continue
		case !trusted[e.sid]:
			others = append(others, e.sid)
		}
	}
	if len(others) > 0 {
		return fmt.Errorf("%s is readable by %s, not only SYSTEM and Administrators", path, strings.Join(others, ", "))
	}
	return nil
}

// OnlyAgentFiles reports whether dir holds nothing but the agent's own files:
// its TLS pair, its secret, and its state (*.json and the *.json.tmp written
// beside it). A protected DACL on a folder is pushed down to everything in it,
// so a folder holding anything else (a volume, a home directory) is not
// re-permissioned. A missing folder holds nothing.
func OnlyAgentFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !isAgentFile(e.Name()) {
			return false, nil
		}
	}
	return true, nil
}

func isAgentFile(name string) bool {
	switch strings.ToLower(name) {
	case "cert.pem", "key.pem", "secret":
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".tmp" {
		ext = strings.ToLower(filepath.Ext(strings.TrimSuffix(name, filepath.Ext(name))))
	}
	return ext == ".json"
}

// ProtectDir protects dir when it holds only the agent's files and reports
// whether it did. Files inside are protected one by one by their owners
// regardless, so skipping a shared folder never leaves a secret open.
func ProtectDir(dir string) (bool, error) {
	ok, err := OnlyAgentFiles(dir)
	if err != nil || !ok {
		return false, err
	}
	if err := Protect(dir); err != nil {
		return false, err
	}
	return true, nil
}

// Secure protects path and then checks the result, so a caller can rely on the
// file being closed to everyone but SYSTEM, Administrators and the agent.
func Secure(path string) error {
	if err := Protect(path); err != nil {
		return fmt.Errorf("cannot restrict access to %s: %w", path, err)
	}
	return Check(path)
}
