//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package config

const (
	DefaultSocketPath = "/var/run/docker.sock"
	// dockerVolumesPath is where the Docker engine keeps named volumes. On
	// Docker Desktop it is inside the VM and does not exist here.
	dockerVolumesPath = "/var/lib/docker/volumes"
	DefaultDataPath   = "/data"
	DefaultStatePath  = "/data/eightly-agent.json"
)
