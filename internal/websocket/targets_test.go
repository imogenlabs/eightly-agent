package websocket

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/imogenlabs/eightly-agent/internal/proxy"
)

// allowAll lets every session through, for tests about the bridge itself.
type allowAll struct{}

func (allowAll) Container(_ context.Context, id string) (string, string) { return id, "" }
func (allowAll) Exec(context.Context, string) string                     { return "" }

var (
	ordinaryID = strings.Repeat("a", 64)
	agentID    = strings.Repeat("b", 64)
	privID     = strings.Repeat("c", 64)
	hostNetID  = strings.Repeat("d", 64)
	sockID     = strings.Repeat("e", 64)
	outsideID  = strings.Repeat("f", 64)
)

// fakeContainers answer GET /containers/{id}/json, by full ID or 12-char prefix.
var fakeContainers = map[string]string{
	ordinaryID: `{"Id":"` + ordinaryID + `","HostConfig":{"NetworkMode":"bridge"},
		"Mounts":[{"Type":"volume","Name":"","Destination":"/data","RW":true}]}`,
	// The agent as docker-compose.yml runs it: it holds the socket.
	agentID: `{"Id":"` + agentID + `","HostConfig":{},
		"Mounts":[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true}]}`,
	privID:    `{"Id":"` + privID + `","HostConfig":{"Privileged":true}}`,
	hostNetID: `{"Id":"` + hostNetID + `","HostConfig":{"NetworkMode":"host","PidMode":"host"}}`,
	sockID: `{"Id":"` + sockID + `","HostConfig":{},
		"Mounts":[{"Type":"bind","Source":"/run/docker.sock","Destination":"/sock/docker.sock","RW":false}]}`,
	outsideID: `{"Id":"` + outsideID + `","HostConfig":{},
		"Mounts":[{"Type":"bind","Source":"/etc","Destination":"/host-etc","RW":true}]}`,
}

// fakeExecs answer GET /exec/{id}/json.
var fakeExecs = map[string]string{
	"0123456789ab": `{"ContainerID":"` + ordinaryID + `","ProcessConfig":{"privileged":false}}`,
	"1123456789ab": `{"ContainerID":"` + agentID + `","ProcessConfig":{"privileged":false}}`,
	"2123456789ab": `{"ContainerID":"` + privID + `","ProcessConfig":{"privileged":false}}`,
	"3123456789ab": `{"ContainerID":"` + ordinaryID + `","ProcessConfig":{"privileged":true}}`,
}

// fakeDaemon serves inspect calls and completes attach and exec start
// hijacks on a unix socket. Every hijacked request line is sent on started.
func fakeDaemon(t *testing.T) (sock string, started chan string) {
	t.Helper()
	dir, err := os.MkdirTemp(socketBase(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock = filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	started = make(chan string, 4)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.Method == http.MethodGet && len(segs) == 3 && segs[0] == "containers" && segs[2] == "json":
			for id, js := range fakeContainers {
				if id == segs[1] || (len(segs[1]) >= 12 && strings.HasPrefix(id, segs[1])) {
					_, _ = w.Write([]byte(js))
					return
				}
			}
			http.NotFound(w, r)
		case r.Method == http.MethodGet && len(segs) == 3 && segs[0] == "exec" && segs[2] == "json":
			if js, ok := fakeExecs[segs[1]]; ok {
				_, _ = w.Write([]byte(js))
				return
			}
			http.NotFound(w, r)
		case r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/attach") || strings.HasSuffix(r.URL.Path, "/start")):
			started <- r.Method + " " + r.URL.Path
			c, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer c.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buf.Flush()
			_, _ = bufio.NewReader(c).ReadByte() // hold until the bridge closes
		default:
			t.Errorf("unexpected Docker call %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock, started
}

// dialSession opens a session on handler and returns the HTTP status of the
// upgrade and the body when it was refused.
func dialSession(t *testing.T, handler http.Handler, path, id string) (int, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + path + "?id=" + id
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)
	conn, resp, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).Dial(wsURL, hdr)
	if err == nil {
		_ = conn.Close()
		// Wait for the bridge to end: it reads the session limits, which
		// later tests change.
		waitSessionsGone(t, 10*time.Second)
		return http.StatusSwitchingProtocols, ""
	}
	if resp == nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	_, _ = bufio.NewReader(resp.Body).WriteTo(&body)
	return resp.StatusCode, body.String()
}

// The bridges reach Docker without passing through the proxy, so they must
// apply the proxy's target rules themselves: attaching to or exec-ing into a
// container that reaches the host is a way out to the host.
func TestAttach_AppliesTheProxyTargetRules(t *testing.T) {
	sock, started := fakeDaemon(t)
	handler := AttachHandler(sock, proxy.NewTargetChecker(sock, nil), false, nil)

	for name, id := range map[string]string{
		"privileged":                  privID,
		"host network and PID":        hostNetID,
		"holds the Docker socket":     sockID,
		"the agent's own (socket)":    agentID,
		"binds a host path":           outsideID,
		"short ID of a socket holder": agentID[:12],
	} {
		t.Run(name, func(t *testing.T) {
			code, body := dialSession(t, handler, "/websocket/attach", id)
			if code != http.StatusForbidden || !strings.Contains(body, "forbidden") {
				t.Fatalf("attach to %s: %d %q, want 403", name, code, body)
			}
		})
	}
	select {
	case got := <-started:
		t.Fatalf("a refused attach still reached Docker: %s", got)
	default:
	}

	// An ordinary container is attached to, by the full ID that was
	// checked even when the caller gave a short one.
	if code, body := dialSession(t, handler, "/websocket/attach", ordinaryID[:12]); code != http.StatusSwitchingProtocols {
		t.Fatalf("attach to an ordinary container: %d %q, want 101", code, body)
	}
	select {
	case got := <-started:
		if want := "POST /containers/" + ordinaryID + "/attach"; got != want {
			t.Errorf("attached with %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attach never reached Docker")
	}
}

func TestExec_AppliesTheProxyTargetRules(t *testing.T) {
	sock, started := fakeDaemon(t)
	handler := ExecHandler(sock, proxy.NewTargetChecker(sock, nil), false, nil)

	for name, id := range map[string]string{
		"exec in the agent's container":  "1123456789ab",
		"exec in a privileged container": "2123456789ab",
		"privileged exec":                "3123456789ab",
	} {
		t.Run(name, func(t *testing.T) {
			code, body := dialSession(t, handler, "/websocket/exec", id)
			if code != http.StatusForbidden || !strings.Contains(body, "forbidden") {
				t.Fatalf("%s: %d %q, want 403", name, code, body)
			}
		})
	}
	select {
	case got := <-started:
		t.Fatalf("a refused exec still reached Docker: %s", got)
	default:
	}

	if code, body := dialSession(t, handler, "/websocket/exec", "0123456789ab"); code != http.StatusSwitchingProtocols {
		t.Fatalf("exec in an ordinary container: %d %q, want 101", code, body)
	}
	select {
	case got := <-started:
		if want := "POST /exec/0123456789ab/start"; got != want {
			t.Errorf("started %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the exec never reached Docker")
	}
}

// A daemon that cannot be asked is a refusal, not a pass.
func TestAttach_RefusesWhenTheTargetCannotBeChecked(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "missing.sock")
	handler := AttachHandler(sock, proxy.NewTargetChecker(sock, nil), false, nil)
	if code, body := dialSession(t, handler, "/websocket/attach", ordinaryID); code != http.StatusForbidden {
		t.Fatalf("unreachable daemon: %d %q, want 403", code, body)
	}
}
