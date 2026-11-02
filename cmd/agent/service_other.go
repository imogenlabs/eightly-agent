//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/imogenlabs/eightly-agent/internal/config"
)

func isWindowsService() bool {
	return false
}

func runAsService(_ *config.Config) {}

// serviceSteps are what each service word prints off Windows, where systemd
// runs the agent and these words only say how.
var serviceSteps = map[string]string{
	"install": `On Linux the agent runs as the systemd service eightly-agent.

The .deb and .rpm packages install it, with its settings file
/etc/eightly-agent/agent.env (root-owned, mode 0600, read by systemd before it
starts the agent). Put EIGHTLY_AGENT_SECRET, EIGHTLY_SERVER_URL and
EIGHTLY_ENROLL_TOKEN there, as Eight.ly OS shows them, then start it:

  sudoedit /etc/eightly-agent/agent.env
  sudo systemctl enable eightly-agent && sudo systemctl start eightly-agent

Without a package, with the release binary and a checkout of the source, first:

  sudo install -m 0755 eightly-agent /usr/bin/eightly-agent
  sudo install -m 0644 deploy/eightly-agent.service /etc/systemd/system/
  sudo sh deploy/postinstall.sh   # the eightly-agent user, /var/lib/eightly-agent and agent.env`,
	"uninstall": "sudo systemctl disable eightly-agent && sudo rm /etc/systemd/system/eightly-agent.service (a package: remove it with apt or dnf; /etc/eightly-agent/agent.env holds the secret, delete it too)",
	"start":     "sudo systemctl start eightly-agent",
	"stop":      "sudo systemctl stop eightly-agent",
	"status":    "sudo systemctl status eightly-agent",
}

func handleServiceCommand(cmd string) {
	if msg, ok := serviceSteps[cmd]; ok {
		fmt.Println(msg)
	} else {
		fmt.Printf("unknown service command: %s\n", cmd)
	}
}
