// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"flag"
	"os"
	"strings"
)

// Config holds all agent configuration from CLI flags and environment variables.
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
	AllowedOrigins    string
	MetricsEnabled    bool
	InsecureNoAuth    bool
	InsecureServerTLS bool
	ShowVersion       bool
}

// ParseArgs registers all flags on the provided FlagSet, parses the given
// args, then applies environment variable overrides. Testable — callers
// supply a fresh flag.NewFlagSet and synthetic args.
func ParseArgs(fs *flag.FlagSet, args []string, version string) (*Config, error) {
	c := &Config{Version: version}

	fs.StringVar(&c.Addr, "addr", "0.0.0.0", "listen address")
	fs.StringVar(&c.Port, "port", "9001", "listen port")
	fs.StringVar(&c.Secret, "secret", "", "HMAC shared secret")
	fs.StringVar(&c.ServerURL, "server-url", "", "EOS server URL for registration")
	fs.StringVar(&c.EnrollToken, "enroll-token", "", "first-time registration token")
	fs.StringVar(&c.StatePath, "state-path", DefaultStatePath, "agent state file")
	fs.StringVar(&c.DataPath, "data", DefaultDataPath, "data directory")
	fs.StringVar(&c.LogLevel, "log-level", "info", "log level")
	fs.StringVar(&c.SocketPath, "socket", DefaultSocketPath, "Docker socket path")
	fs.StringVar(&c.ServerCA, "server-ca", "", "path to CA cert file for validating EOS server TLS")
	fs.StringVar(&c.AdvertiseIP, "advertise-ip", "", "IP address to advertise instead of auto-detecting")
	fs.StringVar(&c.TLSSANs, "tls-san", "", "comma-separated extra SANs for the self-signed TLS cert")
	fs.StringVar(&c.AllowedOrigins, "allowed-origins", "", "comma-separated WebSocket origin allowlist (empty = same-origin check)")
	fs.BoolVar(&c.MetricsEnabled, "metrics", false, "enable Prometheus /metrics endpoint")
	fs.BoolVar(&c.InsecureNoAuth, "insecure-no-auth", false, "disable authentication (NOT recommended for production)")
	fs.BoolVar(&c.InsecureServerTLS, "insecure-server-tls", false, "skip TLS verification for EOS server connections")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

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
	envOverride(&c.AllowedOrigins, "EIGHTLY_ALLOWED_ORIGINS")
	envOverrideBool(&c.MetricsEnabled, "EIGHTLY_METRICS_ENABLED")
	envOverrideBool(&c.InsecureNoAuth, "EIGHTLY_INSECURE_NO_AUTH")
	envOverrideBool(&c.InsecureServerTLS, "EIGHTLY_INSECURE_SERVER_TLS")

	return c, nil
}

// Parse is the production entry point. Delegates to ParseArgs with
// flag.CommandLine and os.Args[1:].
func Parse(version string) *Config {
	c, err := ParseArgs(flag.CommandLine, os.Args[1:], version)
	if err != nil {
		// flag.CommandLine defaults to ExitOnError, so Parse never
		// returns an error in practice.
		panic("config: " + err.Error())
	}
	return c
}

func envOverride(target *string, key string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}

func envOverrideBool(target *bool, key string) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		*target = true
	case "false", "0", "no", "off":
		*target = false
	}
}

// Validate checks that required configuration fields are set.
func (c *Config) Validate() error {
	if c.Secret == "" && !c.InsecureNoAuth {
		return errors.New("EIGHTLY_AGENT_SECRET is required; use --insecure-no-auth to explicitly disable authentication")
	}
	return nil
}
