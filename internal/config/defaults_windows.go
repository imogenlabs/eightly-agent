//go:build windows

package config

const (
	DefaultSocketPath = `//./pipe/docker_engine`
	DefaultDataPath   = `C:\ProgramData\eightly-agent`
	DefaultStatePath  = `C:\ProgramData\eightly-agent\state.json`
)
