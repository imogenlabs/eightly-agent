// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Config holds all agent configuration from CLI flags and environment variables.
type Config struct {
	Addr              string
	Port              string
	Secret            string
	SecretFile        string
	ServerURL         string
	EnrollToken       string
	EnrollTokenFile   string
	StatePath         string
	DataPath          string
	VolumesPath       string
	LogLevel          string
	SocketPath        string
	Version           string
	ServerCA          string
	AdvertiseIP       string
	TLSSANs           string
	AllowedOrigins    string
	AllowedBindRoots  string
	MetricsEnabled    bool
	BrowseReadOnly    bool
	InsecureNoAuth    bool
	InsecureServerTLS bool
	ShowVersion       bool

	// envErrs are environment values that could not be read; Validate
	// reports them so a typo stops the agent instead of being ignored.
	envErrs []error
}

// ParseArgs registers all flags on the provided FlagSet, parses the given
// args, then applies environment variable overrides. Testable — callers
// supply a fresh flag.NewFlagSet and synthetic args.
func ParseArgs(fs *flag.FlagSet, args []string, version string) (*Config, error) {
	c := &Config{Version: version}

	fs.StringVar(&c.Addr, "addr", "0.0.0.0", "listen address")
	fs.StringVar(&c.Port, "port", "9001", "listen port")
	fs.StringVar(&c.Secret, "secret", "", "HMAC shared secret")
	fs.StringVar(&c.SecretFile, "secret-file", "", "file holding the HMAC shared secret (instead of --secret)")
	fs.StringVar(&c.ServerURL, "server-url", "", "EOS server URL for registration")
	fs.StringVar(&c.EnrollToken, "enroll-token", "", "first-time registration token")
	fs.StringVar(&c.EnrollTokenFile, "enroll-token-file", "", "file holding the first-time registration token (instead of --enroll-token); a missing or empty file means none")
	fs.StringVar(&c.StatePath, "state-path", "", "agent state file (default: "+filepath.Base(DefaultStatePath)+" in the data directory)")
	fs.StringVar(&c.DataPath, "data", DefaultDataPath, "data directory")
	fs.StringVar(&c.VolumesPath, "volumes-path", "", "folder holding Docker's named volumes, for the file browser (default: "+dockerVolumesPath+" where it exists)")
	fs.StringVar(&c.LogLevel, "log-level", "info", "log level")
	fs.StringVar(&c.SocketPath, "socket", DefaultSocketPath, "Docker socket path")
	fs.StringVar(&c.ServerCA, "server-ca", "", "path to CA cert file for validating EOS server TLS")
	fs.StringVar(&c.AdvertiseIP, "advertise-ip", "", "IP address to advertise instead of auto-detecting")
	fs.StringVar(&c.TLSSANs, "tls-san", "", "comma-separated extra SANs for the self-signed TLS cert")
	fs.StringVar(&c.AllowedOrigins, "allowed-origins", "", "comma-separated WebSocket origin allowlist (empty = same-origin check)")
	fs.StringVar(&c.AllowedBindRoots, "allowed-bind-roots", "", "comma-separated host directories containers may bind-mount (empty = named volumes only)")
	fs.BoolVar(&c.MetricsEnabled, "metrics", false, "enable Prometheus /metrics endpoint")
	fs.BoolVar(&c.BrowseReadOnly, "browse-read-only", false, "serve the file browser read-only (reject upload/delete/rename)")
	fs.BoolVar(&c.InsecureNoAuth, "insecure-no-auth", false, "disable authentication (NOT recommended for production)")
	fs.BoolVar(&c.InsecureServerTLS, "insecure-server-tls", false, "skip TLS verification for EOS server connections")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	envOverride(&c.Port, "AGENT_PORT")
	envOverride(&c.Secret, "EIGHTLY_AGENT_SECRET")
	envOverride(&c.SecretFile, "EIGHTLY_AGENT_SECRET_FILE")
	envOverride(&c.ServerURL, "EIGHTLY_SERVER_URL")
	envOverride(&c.EnrollToken, "EIGHTLY_ENROLL_TOKEN")
	envOverride(&c.EnrollTokenFile, "EIGHTLY_ENROLL_TOKEN_FILE")
	envOverride(&c.StatePath, "EIGHTLY_STATE_PATH")
	envOverride(&c.DataPath, "DATA_PATH")
	envOverride(&c.VolumesPath, "EIGHTLY_VOLUMES_PATH")
	envOverride(&c.LogLevel, "LOG_LEVEL")
	envOverride(&c.SocketPath, "DOCKER_SOCKET")
	envOverride(&c.ServerCA, "EIGHTLY_SERVER_CA")
	envOverride(&c.AdvertiseIP, "EIGHTLY_ADVERTISE_IP")
	envOverride(&c.TLSSANs, "EIGHTLY_TLS_SANS")
	envOverride(&c.AllowedOrigins, "EIGHTLY_ALLOWED_ORIGINS")
	envOverride(&c.AllowedBindRoots, "EIGHTLY_ALLOWED_BIND_ROOTS")
	for _, b := range []struct {
		target *bool
		key    string
	}{
		{&c.BrowseReadOnly, "EIGHTLY_BROWSE_READ_ONLY"},
		{&c.MetricsEnabled, "EIGHTLY_METRICS_ENABLED"},
		{&c.InsecureNoAuth, "EIGHTLY_INSECURE_NO_AUTH"},
	} {
		if err := envOverrideBool(b.target, b.key); err != nil {
			c.envErrs = append(c.envErrs, err)
		}
	}
	// InsecureServerTLS is intentionally flag-only: skipping server cert
	// verification + TOFU pinning is dangerous enough that it must be a
	// deliberate, operator-typed --insecure-server-tls, never silently enabled
	// by an inherited/leaked environment variable.

	// The state file lives in the data directory unless it is set on its
	// own. A fixed default under /data broke every install whose data
	// directory is elsewhere: the packaged service uses /var/lib/eightly-agent
	// and cannot write /data, so the agent spent its enrollment token on a
	// registration it could not save.
	if c.StatePath == "" {
		c.StatePath = DefaultStatePathFor(c.DataPath)
	}

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

// DefaultStatePathFor is the state file used when none is set: in dataPath,
// named as on this platform. For the default data directory it is
// DefaultStatePath.
func DefaultStatePathFor(dataPath string) string {
	return filepath.Join(dataPath, filepath.Base(DefaultStatePath))
}

// VolumesRoot is the folder the file browser reads Docker's named volumes
// from, laid out as Docker keeps it: <root>/<name>/_data. "" means there is
// none the agent can see, which is the case on Docker Desktop, where volumes
// live inside its virtual machine.
func (c *Config) VolumesRoot() string {
	return volumesRoot(c.VolumesPath, c.DataPath, dockerVolumesPath, dirExists)
}

// volumesRoot picks the volumes folder. A set path wins. Otherwise Docker's
// own folder where it exists, which is where docker-compose.yml binds it.
// Otherwise <data>/volumes when it exists: before --volumes-path the browser
// read only there, and the documented compose file bound the volumes there.
// Otherwise, on Linux, Docker's folder anyway, so a missing bind is reported
// by name; elsewhere none.
func volumesRoot(set, dataPath, dockerDefault string, exists func(string) bool) string {
	if set != "" {
		return set
	}
	if dockerDefault != "" && exists(dockerDefault) {
		return dockerDefault
	}
	if legacy := filepath.Join(dataPath, "volumes"); exists(legacy) {
		return legacy
	}
	if runtime.GOOS == "linux" {
		return dockerDefault
	}
	return ""
}

// dirExists reports whether p is a directory. One the agent may not look into
// still exists: Docker's own folder is root-only on a host, and an agent
// running there as another user is answered 403 by name rather than pointed
// somewhere else.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir() || errors.Is(err, fs.ErrPermission)
}

// envOverrideBool sets *target from the environment variable key when it is
// set. A value that is not a recognised yes or no is an error and leaves
// *target as it was.
func envOverrideBool(target *bool, key string) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		*target = true
	case "false", "0", "no", "off":
		*target = false
	default:
		return fmt.Errorf("%s=%q is not a boolean; use true or false", key, v)
	}
	return nil
}

// Validate checks that required configuration fields are set.
func (c *Config) Validate() error {
	if len(c.envErrs) > 0 {
		return errors.Join(c.envErrs...)
	}
	if err := c.loadSecretFile(); err != nil {
		return err
	}
	if err := c.loadEnrollTokenFile(); err != nil {
		return err
	}
	if _, err := c.BindRoots(); err != nil {
		return err
	}
	if c.Secret == "" && !c.InsecureNoAuth {
		return errors.New("EIGHTLY_AGENT_SECRET is required; use --insecure-no-auth to explicitly disable authentication")
	}
	if c.ServerURL != "" && !c.InsecureServerTLS {
		u, err := url.Parse(c.ServerURL)
		if err != nil {
			return fmt.Errorf("invalid --server-url %q: %w", c.ServerURL, err)
		}
		if u.Scheme != "https" {
			return fmt.Errorf("--server-url must use https (got scheme %q); use --insecure-server-tls to allow an unverified/non-https server", u.Scheme)
		}
	}
	return nil
}

// systemPaths may not be bind roots, nor lie under one, nor hold one: each
// holds the host's configuration, binaries or the container runtime's state,
// and a container that can write there owns the host.
var systemPaths = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32",
	"/opt/cni", "/opt/containerd", "/proc", "/root", "/run", "/sbin", "/sys",
	"/usr", "/var/lib/containerd", "/var/lib/docker", "/var/run",
}

// BindRoots returns the host directories a container may bind-mount, from
// --allowed-bind-roots. Each must be an absolute, clean path that is not a
// system directory, inside one, or a parent of one. An empty setting allows
// none: containers get named volumes only.
func (c *Config) BindRoots() ([]string, error) {
	var roots []string
	for _, r := range strings.Split(c.AllowedBindRoots, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !strings.HasPrefix(r, "/") || path.Clean(r) != r {
			return nil, fmt.Errorf("--allowed-bind-roots: %q must be an absolute path with no trailing slash, '.' or '..'", r)
		}
		for _, s := range systemPaths {
			if r == "/" || r == s || strings.HasPrefix(r, s+"/") || strings.HasPrefix(s, r+"/") {
				return nil, fmt.Errorf("--allowed-bind-roots: %q is, holds or is inside the system directory %s", r, s)
			}
		}
		roots = append(roots, r)
	}
	return roots, nil
}
