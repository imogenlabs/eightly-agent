// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/imogenlabs/eightly-agent/internal/winacl"
)

// loadSecretFile fills Secret from --secret-file / EIGHTLY_AGENT_SECRET_FILE.
//
// A file keeps the fleet-wide secret out of the process environment, which on
// Windows meant a Machine variable every local user can read, and out of the
// command line. EIGHTLY_AGENT_SECRET still works (containers set it), but the
// two together are refused: a leftover variable would otherwise keep leaking a
// secret the operator believes now lives only in the file.
//
// It runs in Validate, not ParseArgs, so the healthcheck subcommand, which
// needs no secret, never fails on a file it cannot read.
func (c *Config) loadSecretFile() error {
	if c.SecretFile == "" {
		return nil
	}
	if c.Secret != "" {
		return errors.New("both a secret (--secret or EIGHTLY_AGENT_SECRET) and a secret file (--secret-file or EIGHTLY_AGENT_SECRET_FILE) are set; keep only the file. On Windows remove the machine variable: [Environment]::SetEnvironmentVariable('EIGHTLY_AGENT_SECRET', $null, 'Machine')")
	}
	secret, err := ReadSecretFile(c.SecretFile)
	if err != nil {
		return err
	}
	c.Secret = secret
	return nil
}

// ReadSecretFile returns the secret held in path, without surrounding
// whitespace (a trailing newline from an editor is not part of it). On Windows
// the file is first closed to everyone but SYSTEM, Administrators and the
// agent, and refused if that does not hold. On unix keep it 0600 or 0400 and
// owned by the agent's user; it is not refused there, because container
// secrets are commonly mounted 0444 inside a single-user container.
func ReadSecretFile(path string) (string, error) {
	if err := winacl.Secure(path); err != nil {
		return "", fmt.Errorf("refusing the secret file: %w", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the admin-set --secret-file flag
	if err != nil {
		return "", fmt.Errorf("cannot read the secret file: %w", err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("the secret file %s is empty", path)
	}
	return secret, nil
}
