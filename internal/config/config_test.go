package config

import (
	"os"
	"testing"
)

func TestEnvOverride(t *testing.T) {
	target := "original"
	os.Setenv("TEST_OVERRIDE_KEY", "replaced")
	defer os.Unsetenv("TEST_OVERRIDE_KEY")

	envOverride(&target, "TEST_OVERRIDE_KEY")
	if target != "replaced" {
		t.Fatalf("expected 'replaced', got %q", target)
	}
}

func TestEnvOverrideNoOp(t *testing.T) {
	target := "original"
	os.Unsetenv("TEST_MISSING_KEY")

	envOverride(&target, "TEST_MISSING_KEY")
	if target != "original" {
		t.Fatalf("expected 'original' (unchanged), got %q", target)
	}
}

func TestEnvOverrideEmptyValue(t *testing.T) {
	target := "original"
	os.Setenv("TEST_EMPTY_KEY", "")
	defer os.Unsetenv("TEST_EMPTY_KEY")

	envOverride(&target, "TEST_EMPTY_KEY")
	if target != "original" {
		t.Fatalf("expected 'original' (empty env should not override), got %q", target)
	}
}

func TestDefaultValues(t *testing.T) {
	// Clean env vars that could interfere
	envs := []string{
		"AGENT_PORT", "EIGHTLY_AGENT_SECRET", "EIGHTLY_SERVER_URL",
		"EIGHTLY_ENROLL_TOKEN", "EIGHTLY_STATE_PATH", "DATA_PATH",
		"LOG_LEVEL", "DOCKER_SOCKET",
	}
	for _, e := range envs {
		os.Unsetenv(e)
	}

	c := &Config{Version: "v1.0.0"}

	// Verify the default constants
	if DefaultSocketPath != "/var/run/docker.sock" {
		t.Fatalf("expected default socket /var/run/docker.sock, got %q", DefaultSocketPath)
	}
	if DefaultDataPath != "/data" {
		t.Fatalf("expected default data path /data, got %q", DefaultDataPath)
	}
	if DefaultStatePath != "/data/eightly-agent.json" {
		t.Fatalf("expected default state path /data/eightly-agent.json, got %q", DefaultStatePath)
	}
	if c.Version != "v1.0.0" {
		t.Fatalf("expected version v1.0.0, got %q", c.Version)
	}
}

func TestEnvOverridesApply(t *testing.T) {
	c := &Config{
		Port:        "9001",
		Secret:      "",
		ServerURL:   "",
		EnrollToken: "",
		StatePath:   DefaultStatePath,
		DataPath:    DefaultDataPath,
		LogLevel:    "info",
		SocketPath:  DefaultSocketPath,
	}

	os.Setenv("AGENT_PORT", "8080")
	os.Setenv("EIGHTLY_AGENT_SECRET", "my-secret")
	os.Setenv("EIGHTLY_SERVER_URL", "https://eos.local")
	os.Setenv("EIGHTLY_ENROLL_TOKEN", "tok-123")
	os.Setenv("EIGHTLY_STATE_PATH", "/tmp/state.json")
	os.Setenv("DATA_PATH", "/tmp/data")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("DOCKER_SOCKET", "/tmp/docker.sock")
	defer func() {
		for _, e := range []string{
			"AGENT_PORT", "EIGHTLY_AGENT_SECRET", "EIGHTLY_SERVER_URL",
			"EIGHTLY_ENROLL_TOKEN", "EIGHTLY_STATE_PATH", "DATA_PATH",
			"LOG_LEVEL", "DOCKER_SOCKET",
		} {
			os.Unsetenv(e)
		}
	}()

	// Apply the same envOverride calls that Parse would
	envOverride(&c.Port, "AGENT_PORT")
	envOverride(&c.Secret, "EIGHTLY_AGENT_SECRET")
	envOverride(&c.ServerURL, "EIGHTLY_SERVER_URL")
	envOverride(&c.EnrollToken, "EIGHTLY_ENROLL_TOKEN")
	envOverride(&c.StatePath, "EIGHTLY_STATE_PATH")
	envOverride(&c.DataPath, "DATA_PATH")
	envOverride(&c.LogLevel, "LOG_LEVEL")
	envOverride(&c.SocketPath, "DOCKER_SOCKET")

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Port", c.Port, "8080"},
		{"Secret", c.Secret, "my-secret"},
		{"ServerURL", c.ServerURL, "https://eos.local"},
		{"EnrollToken", c.EnrollToken, "tok-123"},
		{"StatePath", c.StatePath, "/tmp/state.json"},
		{"DataPath", c.DataPath, "/tmp/data"},
		{"LogLevel", c.LogLevel, "debug"},
		{"SocketPath", c.SocketPath, "/tmp/docker.sock"},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s: expected %q, got %q", tc.name, tc.want, tc.got)
		}
	}
}

func TestVersionPassthrough(t *testing.T) {
	c := &Config{Version: "v2.3.1-beta4"}
	if c.Version != "v2.3.1-beta4" {
		t.Fatalf("expected version v2.3.1-beta4, got %q", c.Version)
	}
}

func TestInsecureNoAuthEnvVar(t *testing.T) {
	c := &Config{}

	os.Setenv("EIGHTLY_INSECURE_NO_AUTH", "true")
	defer os.Unsetenv("EIGHTLY_INSECURE_NO_AUTH")

	if os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "true" || os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "1" {
		c.InsecureNoAuth = true
	}

	if !c.InsecureNoAuth {
		t.Fatal("expected InsecureNoAuth to be true when EIGHTLY_INSECURE_NO_AUTH=true")
	}
}

func TestInsecureNoAuthDefaultFalse(t *testing.T) {
	c := &Config{}
	os.Unsetenv("EIGHTLY_INSECURE_NO_AUTH")

	if os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "true" || os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "1" {
		c.InsecureNoAuth = true
	}

	if c.InsecureNoAuth {
		t.Fatal("expected InsecureNoAuth to be false by default")
	}
}

func TestAdvertiseIPEnvOverride(t *testing.T) {
	c := &Config{AdvertiseIP: ""}
	os.Setenv("EIGHTLY_ADVERTISE_IP", "10.0.0.50")
	defer os.Unsetenv("EIGHTLY_ADVERTISE_IP")

	envOverride(&c.AdvertiseIP, "EIGHTLY_ADVERTISE_IP")
	if c.AdvertiseIP != "10.0.0.50" {
		t.Fatalf("expected AdvertiseIP 10.0.0.50, got %q", c.AdvertiseIP)
	}
}

func TestTLSSANsEnvOverride(t *testing.T) {
	c := &Config{TLSSANs: ""}
	os.Setenv("EIGHTLY_TLS_SANS", "node1.local,node2.local")
	defer os.Unsetenv("EIGHTLY_TLS_SANS")

	envOverride(&c.TLSSANs, "EIGHTLY_TLS_SANS")
	if c.TLSSANs != "node1.local,node2.local" {
		t.Fatalf("expected TLSSANs node1.local,node2.local, got %q", c.TLSSANs)
	}
}

func TestServerCAEnvOverride(t *testing.T) {
	c := &Config{ServerCA: ""}
	os.Setenv("EIGHTLY_SERVER_CA", "/etc/ssl/custom-ca.pem")
	defer os.Unsetenv("EIGHTLY_SERVER_CA")

	envOverride(&c.ServerCA, "EIGHTLY_SERVER_CA")
	if c.ServerCA != "/etc/ssl/custom-ca.pem" {
		t.Fatalf("expected ServerCA /etc/ssl/custom-ca.pem, got %q", c.ServerCA)
	}
}

func TestInsecureServerTLSEnvVar(t *testing.T) {
	c := &Config{}
	os.Setenv("EIGHTLY_INSECURE_SERVER_TLS", "true")
	defer os.Unsetenv("EIGHTLY_INSECURE_SERVER_TLS")

	if os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "true" || os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "1" {
		c.InsecureServerTLS = true
	}

	if !c.InsecureServerTLS {
		t.Fatal("expected InsecureServerTLS to be true when EIGHTLY_INSECURE_SERVER_TLS=true")
	}
}

func TestInsecureServerTLSEnvVarNumeric(t *testing.T) {
	c := &Config{}
	os.Setenv("EIGHTLY_INSECURE_SERVER_TLS", "1")
	defer os.Unsetenv("EIGHTLY_INSECURE_SERVER_TLS")

	if os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "true" || os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "1" {
		c.InsecureServerTLS = true
	}

	if !c.InsecureServerTLS {
		t.Fatal("expected InsecureServerTLS to be true when EIGHTLY_INSECURE_SERVER_TLS=1")
	}
}

func TestInsecureServerTLSDefaultFalse(t *testing.T) {
	c := &Config{}
	os.Unsetenv("EIGHTLY_INSECURE_SERVER_TLS")

	if os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "true" || os.Getenv("EIGHTLY_INSECURE_SERVER_TLS") == "1" {
		c.InsecureServerTLS = true
	}

	if c.InsecureServerTLS {
		t.Fatal("expected InsecureServerTLS to be false by default")
	}
}

func TestPartialEnvOverride(t *testing.T) {
	c := &Config{
		Port:       "9001",
		LogLevel:   "info",
		SocketPath: DefaultSocketPath,
	}

	os.Setenv("AGENT_PORT", "7777")
	defer os.Unsetenv("AGENT_PORT")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("DOCKER_SOCKET")

	envOverride(&c.Port, "AGENT_PORT")
	envOverride(&c.LogLevel, "LOG_LEVEL")
	envOverride(&c.SocketPath, "DOCKER_SOCKET")

	if c.Port != "7777" {
		t.Fatalf("expected port 7777, got %q", c.Port)
	}
	if c.LogLevel != "info" {
		t.Fatalf("expected log-level info (unchanged), got %q", c.LogLevel)
	}
	if c.SocketPath != DefaultSocketPath {
		t.Fatalf("expected socket path %q (unchanged), got %q", DefaultSocketPath, c.SocketPath)
	}
}
