// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearEnrollEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"EIGHTLY_AGENT_SECRET_FILE", "EIGHTLY_ENROLL_TOKEN", "EIGHTLY_ENROLL_TOKEN_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("EIGHTLY_AGENT_SECRET", "s")
}

func TestEnrollTokenFile(t *testing.T) {
	clearEnrollEnv(t)
	path := filepath.Join(t.TempDir(), "enroll-token")
	if err := os.WriteFile(path, []byte("eos_enroll_abc\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := parseValidate(t, "--enroll-token-file", path)
	if err != nil || c.EnrollToken != "eos_enroll_abc" {
		t.Fatalf("--enroll-token-file: %q, %v", c.EnrollToken, err)
	}
	t.Setenv("EIGHTLY_ENROLL_TOKEN_FILE", path)
	if c, err := parseValidate(t); err != nil || c.EnrollToken != "eos_enroll_abc" {
		t.Fatalf("EIGHTLY_ENROLL_TOKEN_FILE: %q, %v", c.EnrollToken, err)
	}
}

// The token is spent at the first registration and the file is deleted; the
// service, still pointed at it, must keep starting from its saved state.
func TestEnrollTokenFile_MissingOrEmptyIsNoToken(t *testing.T) {
	clearEnrollEnv(t)
	dir := t.TempDir()
	if c, err := parseValidate(t, "--enroll-token-file", filepath.Join(dir, "gone")); err != nil || c.EnrollToken != "" {
		t.Fatalf("missing file: %q, %v; want no token and no error", c.EnrollToken, err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := parseValidate(t, "--enroll-token-file", empty); err != nil || c.EnrollToken != "" {
		t.Fatalf("empty file: %q, %v; want no token and no error", c.EnrollToken, err)
	}
}

// A leftover machine variable would keep the token readable by every user.
func TestEnrollTokenAndFileTogetherRefused(t *testing.T) {
	clearEnrollEnv(t)
	t.Setenv("EIGHTLY_ENROLL_TOKEN", "eos_enroll_leftover")
	path := filepath.Join(t.TempDir(), "enroll-token")
	if err := os.WriteFile(path, []byte("eos_enroll_abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseValidate(t, "--enroll-token-file", path); err == nil || !strings.Contains(err.Error(), "keep only the file") {
		t.Fatalf("a token set twice was accepted: %v", err)
	}
}

// A file the agent cannot read is an error, not a silent "no token".
func TestEnrollTokenFile_UnreadableIsRefused(t *testing.T) {
	clearEnrollEnv(t)
	if _, err := parseValidate(t, "--enroll-token-file", t.TempDir()); err == nil {
		t.Fatal("a directory given as the token file was accepted")
	}
}
