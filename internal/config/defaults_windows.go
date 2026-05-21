//go:build windows
// SPDX-License-Identifier: Apache-2.0


package config

const (
	DefaultSocketPath = `//./pipe/docker_engine`
	DefaultDataPath   = `C:\ProgramData\eightly-agent`
	DefaultStatePath  = `C:\ProgramData\eightly-agent\state.json`
)
