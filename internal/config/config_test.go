package config

import (
	"flag"
	"io"
	"os"
	"testing"
)

// helper: create a fresh FlagSet and parse args via ParseArgs.
func parseTestArgs(t *testing.T, args []string, version string) *Config {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c, err := ParseArgs(fs, args, version)
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	return c
}

// helper: clear all env vars that ParseArgs reads.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AGENT_PORT", "EIGHTLY_AGENT_SECRET", "EIGHTLY_SERVER_URL",
		"EIGHTLY_ENROLL_TOKEN", "EIGHTLY_STATE_PATH", "DATA_PATH",
		"LOG_LEVEL", "DOCKER_SOCKET", "EIGHTLY_SERVER_CA",
		"EIGHTLY_ADVERTISE_IP", "EIGHTLY_TLS_SANS",
		"EIGHTLY_ALLOWED_ORIGINS",
		"EIGHTLY_INSECURE_NO_AUTH", "EIGHTLY_INSECURE_SERVER_TLS",
	} {
		os.Unsetenv(key)
	}
}

func TestParseArgsFlagParsing(t *testing.T) {
	clearEnv(t)
	c := parseTestArgs(t, []string{"--port", "8080", "--secret", "mysecret"}, "v1.0.0")

	if c.Port != "8080" {
		t.Errorf("Port: expected 8080, got %q", c.Port)
	}
	if c.Secret != "mysecret" {
		t.Errorf("Secret: expected mysecret, got %q", c.Secret)
	}
	if c.Version != "v1.0.0" {
		t.Errorf("Version: expected v1.0.0, got %q", c.Version)
	}
}

func TestParseArgsEnvPrecedence(t *testing.T) {
	clearEnv(t)
	t.Setenv("AGENT_PORT", "3000")

	c := parseTestArgs(t, []string{"--port", "8080"}, "v1.0.0")

	if c.Port != "3000" {
		t.Errorf("Port: env should win over flag; expected 3000, got %q", c.Port)
	}
}

func TestParseArgsDefaults(t *testing.T) {
	clearEnv(t)
	c := parseTestArgs(t, nil, "v0.0.0")

	if c.Addr != "0.0.0.0" {
		t.Errorf("Addr: expected 0.0.0.0, got %q", c.Addr)
	}
	if c.Port != "9001" {
		t.Errorf("Port: expected 9001, got %q", c.Port)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel: expected info, got %q", c.LogLevel)
	}
	if c.StatePath != DefaultStatePath {
		t.Errorf("StatePath: expected %q, got %q", DefaultStatePath, c.StatePath)
	}
	if c.DataPath != DefaultDataPath {
		t.Errorf("DataPath: expected %q, got %q", DefaultDataPath, c.DataPath)
	}
	if c.SocketPath != DefaultSocketPath {
		t.Errorf("SocketPath: expected %q, got %q", DefaultSocketPath, c.SocketPath)
	}
	if c.Secret != "" {
		t.Errorf("Secret: expected empty, got %q", c.Secret)
	}
	if c.InsecureNoAuth {
		t.Error("InsecureNoAuth: expected false by default")
	}
	if c.InsecureServerTLS {
		t.Error("InsecureServerTLS: expected false by default")
	}
	if c.ShowVersion {
		t.Error("ShowVersion: expected false by default")
	}
}

func TestParseArgsVersionFlag(t *testing.T) {
	clearEnv(t)
	c := parseTestArgs(t, []string{"--version"}, "v2.0.0")

	if !c.ShowVersion {
		t.Fatal("ShowVersion: expected true when --version is passed")
	}
}

func TestParseArgsAllFlags(t *testing.T) {
	clearEnv(t)
	c := parseTestArgs(t, []string{
		"--addr", "127.0.0.1",
		"--port", "7777",
		"--secret", "s3cret",
		"--server-url", "https://eos.local",
		"--enroll-token", "tok-abc",
		"--state-path", "/tmp/state.json",
		"--data", "/tmp/data",
		"--log-level", "debug",
		"--socket", "/tmp/docker.sock",
		"--server-ca", "/etc/ssl/ca.pem",
		"--advertise-ip", "10.0.0.5",
		"--tls-san", "node1.local,node2.local",
		"--insecure-no-auth",
		"--insecure-server-tls",
	}, "v3.0.0")

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Addr", c.Addr, "127.0.0.1"},
		{"Port", c.Port, "7777"},
		{"Secret", c.Secret, "s3cret"},
		{"ServerURL", c.ServerURL, "https://eos.local"},
		{"EnrollToken", c.EnrollToken, "tok-abc"},
		{"StatePath", c.StatePath, "/tmp/state.json"},
		{"DataPath", c.DataPath, "/tmp/data"},
		{"LogLevel", c.LogLevel, "debug"},
		{"SocketPath", c.SocketPath, "/tmp/docker.sock"},
		{"ServerCA", c.ServerCA, "/etc/ssl/ca.pem"},
		{"AdvertiseIP", c.AdvertiseIP, "10.0.0.5"},
		{"TLSSANs", c.TLSSANs, "node1.local,node2.local"},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s: expected %q, got %q", tc.name, tc.want, tc.got)
		}
	}
	if !c.InsecureNoAuth {
		t.Error("InsecureNoAuth: expected true")
	}
	if !c.InsecureServerTLS {
		t.Error("InsecureServerTLS: expected true")
	}
}

func TestParseArgsEnvOverridesMultiple(t *testing.T) {
	clearEnv(t)
	t.Setenv("AGENT_PORT", "8080")
	t.Setenv("EIGHTLY_AGENT_SECRET", "env-secret")
	t.Setenv("EIGHTLY_SERVER_URL", "https://env.eos")
	t.Setenv("EIGHTLY_ENROLL_TOKEN", "env-tok")
	t.Setenv("EIGHTLY_STATE_PATH", "/env/state.json")
	t.Setenv("DATA_PATH", "/env/data")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DOCKER_SOCKET", "/env/docker.sock")
	t.Setenv("EIGHTLY_SERVER_CA", "/env/ca.pem")
	t.Setenv("EIGHTLY_ADVERTISE_IP", "10.0.0.50")
	t.Setenv("EIGHTLY_TLS_SANS", "a.local,b.local")

	c := parseTestArgs(t, nil, "v1.0.0")

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Port", c.Port, "8080"},
		{"Secret", c.Secret, "env-secret"},
		{"ServerURL", c.ServerURL, "https://env.eos"},
		{"EnrollToken", c.EnrollToken, "env-tok"},
		{"StatePath", c.StatePath, "/env/state.json"},
		{"DataPath", c.DataPath, "/env/data"},
		{"LogLevel", c.LogLevel, "debug"},
		{"SocketPath", c.SocketPath, "/env/docker.sock"},
		{"ServerCA", c.ServerCA, "/env/ca.pem"},
		{"AdvertiseIP", c.AdvertiseIP, "10.0.0.50"},
		{"TLSSANs", c.TLSSANs, "a.local,b.local"},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s: expected %q, got %q", tc.name, tc.want, tc.got)
		}
	}
}

func TestParseArgsBoolEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("EIGHTLY_INSECURE_NO_AUTH", "true")
	t.Setenv("EIGHTLY_INSECURE_SERVER_TLS", "1")

	c := parseTestArgs(t, nil, "v1.0.0")

	if !c.InsecureNoAuth {
		t.Error("InsecureNoAuth: expected true with env=true")
	}
	if !c.InsecureServerTLS {
		t.Error("InsecureServerTLS: expected true with env=1")
	}
}

func TestParseArgsBoolEnvFalse(t *testing.T) {
	clearEnv(t)
	// Set flag to true via args, then override to false via env
	t.Setenv("EIGHTLY_INSECURE_NO_AUTH", "false")

	c := parseTestArgs(t, []string{"--insecure-no-auth"}, "v1.0.0")

	if c.InsecureNoAuth {
		t.Error("InsecureNoAuth: env=false should override flag to false")
	}
}

func TestParseArgsInvalidFlag(t *testing.T) {
	clearEnv(t)
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := ParseArgs(fs, []string{"--bogus"}, "v1.0.0")
	if err == nil {
		t.Fatal("expected error for unknown flag --bogus")
	}
}

func TestEnvOverride(t *testing.T) {
	target := "original"
	t.Setenv("TEST_OVERRIDE_KEY", "replaced")
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
	t.Setenv("TEST_EMPTY_KEY", "")
	envOverride(&target, "TEST_EMPTY_KEY")
	if target != "original" {
		t.Fatalf("expected 'original' (empty env should not override), got %q", target)
	}
}

func TestEnvOverrideBoolValues(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"off", false},
	}
	for _, tc := range tests {
		t.Run(tc.env, func(t *testing.T) {
			val := !tc.want // start opposite
			t.Setenv("TEST_BOOL", tc.env)
			envOverrideBool(&val, "TEST_BOOL")
			if val != tc.want {
				t.Errorf("envOverrideBool(%q): expected %v, got %v", tc.env, tc.want, val)
			}
		})
	}
}

func TestEnvOverrideBoolEmpty(t *testing.T) {
	val := true
	os.Unsetenv("TEST_BOOL_EMPTY")
	envOverrideBool(&val, "TEST_BOOL_EMPTY")
	if !val {
		t.Error("envOverrideBool should not change value when env is unset")
	}
}

func TestValidateRequiresSecret(t *testing.T) {
	c := &Config{Secret: "", InsecureNoAuth: false}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when secret is empty and insecure-no-auth is false")
	}
}

func TestValidateAcceptsInsecureNoAuth(t *testing.T) {
	c := &Config{Secret: "", InsecureNoAuth: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected no error with insecure-no-auth, got: %v", err)
	}
}

func TestValidateAcceptsSecret(t *testing.T) {
	c := &Config{Secret: "something", InsecureNoAuth: false}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected no error with secret set, got: %v", err)
	}
}

func FuzzParseArgs(f *testing.F) {
	f.Add("9001", "secret123", "info")
	f.Add("", "", "")
	f.Add("0", "a", "debug")
	f.Fuzz(func(t *testing.T, port, secret, logLevel string) {
		fs := flag.NewFlagSet("fuzz", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		cfg, err := ParseArgs(fs, []string{"--port", port, "--secret", secret, "--log-level", logLevel}, "fuzz")
		if err != nil {
			return // invalid flags are expected
		}
		if cfg.Port != port {
			t.Errorf("port: got %q, want %q", cfg.Port, port)
		}
	})
}
