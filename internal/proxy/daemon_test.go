package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// unixDaemon serves handler on a unix socket, as the Docker daemon does, and
// returns the socket's path.
func unixDaemon(t *testing.T, handler http.Handler) string {
	t.Helper()
	base := "/tmp" // inside the sun_path limit on macOS, where t.TempDir() is too long
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "px")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// dockerAPI reads what the filter needs from the daemon, tells a missing
// object (the daemon will refuse the request itself) from a failure (the
// filter must refuse), and never trusts a body it cannot decode.
func TestDockerAPI_ReadsTheDaemon(t *testing.T) {
	sock := unixDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/volumes/data":
			_, _ = w.Write([]byte(`{"Driver":"local","Options":{"type":"nfs"}}`))
		case "/networks/lan":
			_, _ = w.Write([]byte(`{"Driver":"macvlan"}`))
		case "/networks/broken":
			_, _ = w.Write([]byte(`{"Driver":`))
		case "/volumes/busy":
			http.Error(w, "daemon trouble", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	d := &dockerAPI{client: &http.Client{Transport: dockerTransport(sock)}}
	ctx := context.Background()

	if v, err := d.volume(ctx, "data"); err != nil || v.Driver != "local" || v.Options["type"] != "nfs" {
		t.Errorf("volume: %+v, %v", v, err)
	}
	if n, err := d.network(ctx, "lan"); err != nil || n.Driver != "macvlan" {
		t.Errorf("network: %+v, %v", n, err)
	}
	if _, err := d.volume(ctx, "missing"); !errors.Is(err, errNotFound) {
		t.Errorf("missing volume: %v, want errNotFound", err)
	}
	if _, err := d.volume(ctx, "busy"); err == nil || errors.Is(err, errNotFound) || !strings.Contains(err.Error(), "500") {
		t.Errorf("daemon error: %v, want a failure naming the status", err)
	}
	if _, err := d.network(ctx, "broken"); err == nil {
		t.Error("a body that cannot be decoded was accepted")
	}
}

// failingDaemon answers every question with an error that is not "not found".
type failingDaemon struct{}

var errDaemonDown = errors.New("daemon down")

func (failingDaemon) container(context.Context, string) (*containerInfo, error) {
	return nil, errDaemonDown
}
func (failingDaemon) exec(context.Context, string) (*execInfo, error)     { return nil, errDaemonDown }
func (failingDaemon) volume(context.Context, string) (*volumeInfo, error) { return nil, errDaemonDown }
func (failingDaemon) network(context.Context, string) (*networkInfo, error) {
	return nil, errDaemonDown
}

// Every check that asks the daemon refuses when it cannot get an answer: a
// question left unanswered is not a yes.
func TestFilter_RefusesWhatItCannotCheck(t *testing.T) {
	f := &filter{roots: testRoots, daemon: failingDaemon{}, hostFS: true, lstat: os.Lstat}
	ctx := context.Background()
	for name, reason := range map[string]string{
		"target container": func() string { _, r := f.checkTargetContainer(ctx, "app"); return r }(),
		"exec":             f.checkExecStart(ctx, "exec-app"),
		"private read":     func() string { _, r := f.checkPrivateRead(ctx, "app"); return r }(),
		"start":            func() string { _, r := f.checkStart(ctx, "app", false); return r }(),
		"not self":         func() string { _, r := f.checkNotSelf(ctx, "app", false); return r }(),
		"network":          f.checkNetwork(ctx, "mynet"),
		"named volume":     f.checkNamedVolume(ctx, "cache"),
	} {
		if !strings.Contains(reason, "could not be checked") {
			t.Errorf("%s: reason %q, want a refusal that says it could not be checked", name, reason)
		}
	}
}

// oneContainer answers for a single container and nothing else.
type oneContainer struct{ c containerInfo }

func (d oneContainer) container(_ context.Context, id string) (*containerInfo, error) {
	if id == d.c.ID {
		return &d.c, nil
	}
	return nil, errNotFound
}
func (oneContainer) exec(context.Context, string) (*execInfo, error)       { return nil, errNotFound }
func (oneContainer) volume(context.Context, string) (*volumeInfo, error)   { return nil, errNotFound }
func (oneContainer) network(context.Context, string) (*networkInfo, error) { return nil, errNotFound }

func TestFilter_TargetEdges(t *testing.T) {
	ctx := context.Background()
	sockByDest := containerInfo{ID: "sockdest", Mounts: []mountPoint{{Type: "bind", Source: "/srv/run", Destination: "/var/run/docker.sock"}}}
	pipe := containerInfo{ID: "pipe", Mounts: []mountPoint{{Type: "npipe", Source: `\\.\pipe\docker_engine`, Destination: "/p"}}}

	f := &filter{roots: testRoots, daemon: oneContainer{sockByDest}}
	if _, reason := f.checkTargetContainer(ctx, "sockdest"); !strings.Contains(reason, "Docker socket") {
		t.Errorf("socket mounted by destination: %q, want refused", reason)
	}
	if _, reason := f.checkPrivateRead(ctx, "sockdest"); reason == "" {
		t.Error("private read of a socket holder allowed")
	}
	// A missing container is refused, never forwarded by name: a container
	// renamed to that name in the meantime would be reached unchecked.
	if _, reason := f.checkPrivateRead(ctx, "missing"); statusFor(reason) != http.StatusNotFound {
		t.Errorf("private read of a missing container: %q, want 404", reason)
	}
	if reason := f.checkExecStart(ctx, "missing"); reason != "" {
		t.Errorf("missing exec: %q, want it left to the daemon (an exec ID cannot be moved)", reason)
	}

	f.daemon = oneContainer{pipe}
	if _, reason := f.checkTargetContainer(ctx, "pipe"); !strings.Contains(reason, "mount type npipe rejected") {
		t.Errorf("unknown mount type: %q, want refused", reason)
	}

	// Start re-checks symlinks only where the agent sees the host's files
	// and there are roots to check against, but always pins the ID.
	if full, reason := f.checkStart(ctx, "pipe", false); full != "pipe" || reason != "" {
		t.Errorf("start without the host's files: (%q, %q), want the ID and nothing checked", full, reason)
	}
	f.hostFS = true
	if _, reason := f.checkStart(ctx, "missing", false); statusFor(reason) != http.StatusNotFound {
		t.Errorf("start of a missing container: %q, want 404", reason)
	}

	if f.isSelf("") {
		t.Error("an empty ID matched the agent's own container")
	}
}
