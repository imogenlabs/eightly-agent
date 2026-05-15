package config

import (
	"flag"
	"os"
)

type Config struct {
	Addr        string
	Port        string
	Secret      string
	ServerURL   string
	EnrollToken string
	StatePath   string
	DataPath    string
	LogLevel    string
	SocketPath  string
	Version     string
}

func Parse(version string) *Config {
	c := &Config{Version: version}

	flag.StringVar(&c.Addr, "addr", "0.0.0.0", "listen address")
	flag.StringVar(&c.Port, "port", "9001", "listen port")
	flag.StringVar(&c.Secret, "secret", "", "HMAC shared secret")
	flag.StringVar(&c.ServerURL, "server-url", "", "EOS server URL for registration")
	flag.StringVar(&c.EnrollToken, "enroll-token", "", "first-time registration token")
	flag.StringVar(&c.StatePath, "state-path", "/data/eightly-agent.json", "agent state file")
	flag.StringVar(&c.DataPath, "data-path", "/data", "data directory")
	flag.StringVar(&c.LogLevel, "log-level", "info", "log level")
	flag.StringVar(&c.SocketPath, "socket-path", "/var/run/docker.sock", "Docker socket path")
	flag.Parse()

	envOverride(&c.Port, "AGENT_PORT")
	envOverride(&c.Secret, "EIGHTLY_AGENT_SECRET")
	envOverride(&c.ServerURL, "EIGHTLY_SERVER_URL")
	envOverride(&c.EnrollToken, "EIGHTLY_ENROLL_TOKEN")
	envOverride(&c.StatePath, "EIGHTLY_STATE_PATH")
	envOverride(&c.DataPath, "DATA_PATH")
	envOverride(&c.LogLevel, "LOG_LEVEL")
	envOverride(&c.SocketPath, "DOCKER_SOCKET")

	return c
}

func envOverride(target *string, key string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}
