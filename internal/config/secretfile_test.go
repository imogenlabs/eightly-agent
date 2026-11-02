// SPDX-License-Identifier: Apache-2.0

package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecret(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func parseValidate(t *testing.T, args ...string) (*Config, error) {
	t.Helper()
	c, err := ParseArgs(flag.NewFlagSet("t", flag.ContinueOnError), args, "test")
	if err != nil {
		t.Fatal(err)
	}
	return c, c.Validate()
}

func TestSecretFileFlag(t *testing.T) {
	t.Setenv("EIGHTLY_AGENT_SECRET", "")
	t.Setenv("EIGHTLY_AGENT_SECRET_FILE", "")
	c, err := parseValidate(t, "--secret-file", writeSecret(t, "  fleet-secret\r\n"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Secret != "fleet-secret" {
		t.Errorf("Secret = %q, want the file's contents without whitespace", c.Secret)
	}
}

func TestSecretFileEnv(t *testing.T) {
	t.Setenv("EIGHTLY_AGENT_SECRET", "")
	t.Setenv("EIGHTLY_AGENT_SECRET_FILE", writeSecret(t, "from-env-file\n"))
	c, err := parseValidate(t)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Secret != "from-env-file" {
		t.Errorf("Secret = %q", c.Secret)
	}
}

func TestSecretEnvStillWorks(t *testing.T) {
	t.Setenv("EIGHTLY_AGENT_SECRET", "container-secret")
	t.Setenv("EIGHTLY_AGENT_SECRET_FILE", "")
	c, err := parseValidate(t)
	if err != nil || c.Secret != "container-secret" {
		t.Fatalf("Secret = %q, %v", c.Secret, err)
	}
}

func TestSecretAndSecretFileTogetherRefused(t *testing.T) {
	t.Setenv("EIGHTLY_AGENT_SECRET", "leftover-machine-variable")
	t.Setenv("EIGHTLY_AGENT_SECRET_FILE", "")
	_, err := parseValidate(t, "--secret-file", writeSecret(t, "file-secret"))
	if err == nil || !strings.Contains(err.Error(), "keep only the file") {
		t.Fatalf("a secret set twice was accepted: %v", err)
	}
}

func TestSecretFileProblemsAreRefused(t *testing.T) {
	t.Setenv("EIGHTLY_AGENT_SECRET", "")
	t.Setenv("EIGHTLY_AGENT_SECRET_FILE", "")
	if _, err := parseValidate(t, "--secret-file", writeSecret(t, " \n")); err == nil {
		t.Error("an empty secret file was accepted")
	}
	if _, err := parseValidate(t, "--secret-file", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing secret file was accepted")
	}
}

// The healthcheck parses flags without Validate; a secret file it cannot read
// must not make the liveness probe fail.
func TestParseArgsDoesNotReadSecretFile(t *testing.T) {
	t.Setenv("EIGHTLY_AGENT_SECRET", "")
	t.Setenv("EIGHTLY_AGENT_SECRET_FILE", "")
	c, err := ParseArgs(flag.NewFlagSet("t", flag.ContinueOnError), []string{"--secret-file", filepath.Join(t.TempDir(), "missing")}, "test")
	if err != nil || c.Secret != "" {
		t.Fatalf("ParseArgs read the secret file: %q, %v", c.Secret, err)
	}
}
