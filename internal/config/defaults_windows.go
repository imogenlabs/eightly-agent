//go:build windows

// SPDX-License-Identifier: Apache-2.0

package config

const (
	DefaultSocketPath = `//./pipe/docker_engine`
	// dockerVolumesPath is where the Docker engine keeps named volumes. On
	// Docker Desktop it is inside the VM and does not exist here.
	dockerVolumesPath = `C:\ProgramData\docker\volumes`
	DefaultDataPath   = `C:\ProgramData\eightly-agent`
	DefaultStatePath  = `C:\ProgramData\eightly-agent\state.json`
	// DefaultSecretPath is where `eightly-agent.exe install` points the
	// service's --secret-file. It is not read unless that flag names it.
	DefaultSecretPath = `C:\ProgramData\eightly-agent\secret` // #nosec G101 -- a file path, not a credential
)
