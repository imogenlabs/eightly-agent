// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMainEnv tells the test binary to run the real main() instead of the tests.
// The paths that end the process (log.Fatal, a bad flag, -h) can only be
// observed from outside it.
const runMainEnv = "EIGHTLY_AGENT_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// agentEnvKeys are every environment variable config.ParseArgs reads. A
// developer's or runner's own settings must not leak into a test.
var agentEnvKeys = []string{
	"AGENT_PORT", "DATA_PATH", "LOG_LEVEL", "DOCKER_SOCKET",
	"EIGHTLY_AGENT_SECRET", "EIGHTLY_AGENT_SECRET_FILE", "EIGHTLY_SERVER_URL",
	"EIGHTLY_ENROLL_TOKEN", "EIGHTLY_STATE_PATH", "EIGHTLY_SERVER_CA",
	"EIGHTLY_ADVERTISE_IP", "EIGHTLY_TLS_SANS", "EIGHTLY_ALLOWED_ORIGINS",
	"EIGHTLY_BROWSE_READ_ONLY", "EIGHTLY_METRICS_ENABLED", "EIGHTLY_INSECURE_NO_AUTH",
}

func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, k := range agentEnvKeys {
		t.Setenv(k, "")
	}
}

// runAgent runs main() in a child process with args and the extra environment,
// and returns its exit code, stdout and stderr.
func runAgent(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...) // #nosec G204 -- the test binary re-running itself
	cmd.Env = append([]string{runMainEnv + "=1"}, cleanEnviron()...)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("run agent: %v", err)
	}
	return code, stdout.String(), stderr.String()
}

func cleanEnviron() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, drop := range agentEnvKeys {
			if strings.EqualFold(k, drop) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	out := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	fn()
	_ = w.Close()
	return <-out
}

// withArgs points os.Args and a fresh flag.CommandLine at args for one call of
// main(); config.Parse registers its flags on flag.CommandLine, and a second
// registration on the same set panics.
func withArgs(t *testing.T, args ...string) {
	t.Helper()
	origArgs, origFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = origArgs, origFlags })
	os.Args = append([]string{"eightly-agent"}, args...)
	flag.CommandLine = flag.NewFlagSet("eightly-agent", flag.ContinueOnError)
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	return port
}

func TestMain_VersionSubcommandAndFlag(t *testing.T) {
	clearAgentEnv(t)
	for _, args := range [][]string{{"version"}, {"--version"}, {"-version"}} {
		withArgs(t, args...)
		if got := captureStdout(t, main); got != version+"\n" {
			t.Errorf("eightly-agent %s printed %q, want %q", strings.Join(args, " "), got, version+"\n")
		}
	}
}

// Every way the agent can refuse to start ends the process with a reason. A
// refusal that exits 0, or exits without saying why, leaves a service manager
// restarting it forever and an operator with nothing to read.
func TestAgent_RefusesToStart(t *testing.T) {
	data := t.TempDir()
	cases := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{
			name: "no secret and no explicit opt-out",
			args: []string{"--data", data},
			want: "EIGHTLY_AGENT_SECRET is required",
		},
		{
			name: "a boolean variable it cannot read",
			env:  []string{"EIGHTLY_METRICS_ENABLED=maybe"},
			args: []string{"--data", data, "--insecure-no-auth"},
			want: "is not a boolean",
		},
		{
			name: "a plain-http server URL",
			args: []string{"--data", data, "--secret", "s", "--server-url", "http://eos.example"},
			want: "--server-url must use https",
		},
		{
			name: "a secret file that does not exist",
			env:  []string{"EIGHTLY_AGENT_SECRET_FILE=" + filepath.Join(data, "missing")},
			args: []string{"--data", data},
			want: "missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runAgent(t, tc.env, tc.args...)
			if code != 1 {
				t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "invalid configuration") || !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not explain the refusal (want %q):\n%s", tc.want, stderr)
			}
		})
	}
}

func TestAgent_FlagErrors(t *testing.T) {
	t.Run("-h lists the flags and exits 0", func(t *testing.T) {
		code, _, stderr := runAgent(t, nil, "-h")
		if code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
		for _, f := range []string{"-secret-file", "-server-url", "-enroll-token", "-insecure-no-auth"} {
			if !strings.Contains(stderr, f) {
				t.Errorf("usage does not list %s:\n%s", f, stderr)
			}
		}
	})
	t.Run("an unknown flag exits 2", func(t *testing.T) {
		code, _, stderr := runAgent(t, nil, "--no-such-flag")
		if code != 2 || !strings.Contains(stderr, "no-such-flag") {
			t.Fatalf("exit %d, want 2 naming the flag; stderr:\n%s", code, stderr)
		}
	})
}

// The healthcheck subcommand is what the image's HEALTHCHECK runs; its exit
// status is the whole interface.
func TestAgent_HealthcheckSubcommand(t *testing.T) {
	code, _, stderr := runAgent(t, nil, "healthcheck", "--addr", "127.0.0.1", "--port", freePort(t), "--data", t.TempDir())
	if code != 1 || !strings.Contains(stderr, "unhealthy") {
		t.Fatalf("nothing listening: exit %d, want 1 with a reason; stderr:\n%s", code, stderr)
	}
	code, _, _ = runAgent(t, nil, "healthcheck", "--no-such-flag")
	if code != 1 {
		t.Fatalf("bad healthcheck flag: exit %d, want 1", code)
	}
}
