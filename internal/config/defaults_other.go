//go:build !windows
// SPDX-License-Identifier: Apache-2.0


package config

const (
	DefaultSocketPath = "/var/run/docker.sock"
	DefaultDataPath   = "/data"
	DefaultStatePath  = "/data/eightly-agent.json"
)
