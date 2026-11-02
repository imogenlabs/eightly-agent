//go:build windows

// SPDX-License-Identifier: Apache-2.0

package config

const (
	DefaultSocketPath = `//./pipe/docker_engine`
	DefaultDataPath   = `C:\ProgramData\eightly-agent`
	DefaultStatePath  = `C:\ProgramData\eightly-agent\state.json`
	// DefaultSecretPath is where `eightly-agent.exe install` points the
	// service's --secret-file. It is not read unless that flag names it.
	DefaultSecretPath = `C:\ProgramData\eightly-agent\secret` // #nosec G101 -- a file path, not a credential
)
