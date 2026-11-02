//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The packages keep the secret in agent.env; the install steps must lead
// there, not to the drop-in or Environment= lines they used to describe.
func TestServiceSteps_InstallUsesTheSettingsFile(t *testing.T) {
	install := serviceSteps["install"]
	for _, want := range []string{"/etc/eightly-agent/agent.env", "EIGHTLY_AGENT_SECRET", "deploy/postinstall.sh", "systemctl enable eightly-agent"} {
		if !strings.Contains(install, want) {
			t.Errorf("install steps do not mention %s:\n%s", want, install)
		}
	}
}
