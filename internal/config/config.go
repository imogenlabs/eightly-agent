// SPDX-License-Identifier: Apache-2.0

package config

import (
	"flag"
	"os"
)

type Config struct {
	Addr              string
	Port              string
	Secret            string
	ServerURL         string
	EnrollToken       string
	StatePath         string
	DataPath          string
	LogLevel          string
	SocketPath        string
	Version           string
	ServerCA          string
	AdvertiseIP       string
	TLSSANs           string
	InsecureNoAuth    bool
	InsecureServerTLS bool
	ShowVersion       bool
}

func Parse(version string) *Config {
	c := &Config{Version: version}

	flag.StringVar(&c.Addr, "addr", "0.0.0.0", "listen address")
	flag.StringVar(&c.Port, "port", "9001", "listen port")
	flag.StringVar(&c.Secret, "secret", "", "HMAC shared secret")
	flag.StringVar(&c.ServerURL, "server-url", "", "EOS server URL for registration")
	flag.StringVar(&c.EnrollToken, "enroll-token", "", "first-time registration token")
	flag.StringVar(&c.StatePath, "state-path", DefaultStatePath, "agent state file")
	flag.StringVar(&c.DataPath, "data", DefaultDataPath, "data directory")
	flag.StringVar(&c.LogLevel, "log-level", "info", "log level")
	flag.StringVar(&c.SocketPath, "socket", DefaultSocketPath, "Docker socket path")
	flag.StringVar(&c.ServerCA, "server-ca", "", "path to CA cert file for validating EOS server TLS")
	flag.StringVar(&c.AdvertiseIP, "advertise-ip", "", "IP address to advertise instead of auto-detecting")
	flag.StringVar(&c.TLSSANs, "tls-san", "", "comma-separated extra SANs for the self-signed TLS cert")
	flag.BoolVar(&c.InsecureNoAuth, "insecure-no-auth", false, "disable authentication (NOT recommended for production)")
	flag.BoolVar(&c.InsecureServerTLS, "insecure-server-tls", false, "skip TLS verification for EOS server connections")
	flag.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	flag.Parse()

	envOverride(&c.Port, "AGENT_PORT")
	envOverride(&c.Secret, "EIGHTLY_AGENT_SECRET")
	envOverride(&c.ServerURL, "EIGHTLY_SERVER_URL")
	envOverride(&c.EnrollToken, "EIGHTLY_ENROLL_TOKEN")
	envOverride(&c.StatePath, "EIGHTLY_STATE_PATH")
	envOverride(&c.DataPath, "DATA_PATH")
	envOverride(&c.LogLevel, "LOG_LEVEL")
	envOverride(&c.SocketPath, "DOCKER_SOCKET")
	envOverride(&c.ServerCA, "EIGHTLY_SERVER_CA")
	envOverride(&c.AdvertiseIP, "EIGHTLY_ADVERTISE_IP")
	envOverride(&c.TLSSANs, "EIGHTLY_TLS_SANS")
	if os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "true" || os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "1" {
		c.InsecureNoAuth = true
	}
	if os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "true" || os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "1" {
		c.InsecureServerTLS = true
	}

	return c
}

func envOverride(target *string, key string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}
