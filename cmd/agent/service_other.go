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

func handleServiceCommand(cmd string) {
	messages := map[string]string{
		"install":   "On Linux, install the systemd service file: sudo cp deploy/eightly-agent.service /etc/systemd/system/ && sudo systemctl enable eightly-agent",
		"uninstall": "sudo systemctl disable eightly-agent && sudo rm /etc/systemd/system/eightly-agent.service",
		"start":     "sudo systemctl start eightly-agent",
		"stop":      "sudo systemctl stop eightly-agent",
		"status":    "sudo systemctl status eightly-agent",
	}
	if msg, ok := messages[cmd]; ok {
		fmt.Println(msg)
	} else {
		fmt.Printf("unknown service command: %s\n", cmd)
	}
}
