//go:build !windows

package config

const (
	DefaultSocketPath = "/var/run/docker.sock"
	DefaultDataPath   = "/data"
	DefaultStatePath  = "/data/eightly-agent.json"
)
