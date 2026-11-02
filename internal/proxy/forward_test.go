package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type seen struct {
	method, path, rawQuery, host, body, requestURI string
	header                                         http.Header
}

// dockerBackend starts a fake Docker daemon on a Unix socket and records what
// it receives. The socket lives under /tmp to stay inside the sun_path limit.
func dockerBackend(t *testing.T) (string, <-chan seen) {
	t.Helper()
	dir, err := os.MkdirTemp(socketBase(), "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan seen, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Host, string(b), r.RequestURI, r.Header.Clone()}
		w.Header().Set("X-Backend", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("from-daemon"))
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return sock, got
}

func forward(t *testing.T, req *http.Request) (seen, *httptest.ResponseRecorder) {
	t.Helper()
	sock, got := dockerBackend(t)
	rr := httptest.NewRecorder()
	NewDockerProxy(sock).ServeHTTP(rr, req)
	select {
	case s := <-got:
		return s, rr
	default:
		t.Fatalf("backend never saw the request; proxy answered %d %q", rr.Code, rr.Body.String())
		return seen{}, nil
	}
}

func TestForward_PathQueryAndResponse(t *testing.T) {
	cases := []struct{ in, wantPath, wantQuery string }{
		{"/v1.45/containers/json?all=1&filters=%7B%22status%22%3A%5B%22running%22%5D%7D", "/containers/json", "all=1&filters=%7B%22status%22%3A%5B%22running%22%5D%7D"},
		{"/containers", "/containers/json", ""},
		{"/v1.41/images", "/images/json", ""},
		{"/volumes", "/volumes", ""},
		{"/containers/json?a=1;b=2", "/containers/json", "a=1;b=2"},
		{"/containers/abc/logs?stdout=1", "/containers/abc/logs", "stdout=1"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.in, nil)
			s, rr := forward(t, req)
			if s.path != c.wantPath || s.rawQuery != c.wantQuery {
				t.Errorf("backend got %q ? %q, want %q ? %q", s.path, s.rawQuery, c.wantPath, c.wantQuery)
			}
			if rr.Code != http.StatusTeapot || rr.Body.String() != "from-daemon" || rr.Header().Get("X-Backend") != "yes" {
				t.Errorf("response not relayed: %d %q %v", rr.Code, rr.Body.String(), rr.Header())
			}
		})
	}
}

func TestForward_BodyAndMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1.45/containers/create", strings.NewReader(`{"Image":"alpine"}`))
	s, _ := forward(t, req)
	if s.method != http.MethodPost || s.path != "/containers/create" || s.body != `{"Image":"alpine"}` {
		t.Errorf("got %s %s body %q", s.method, s.path, s.body)
	}
}

func TestForward_Headers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/containers/json", nil)
	req.Host = "agent.example:9000"
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("X-Custom", "keep")
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Forwarded-Host", "client-said.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Forwarded", "for=198.51.100.1")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "drop-me")
	req.Header.Set("Keep-Alive", "timeout=5")
	s, _ := forward(t, req)

	want := map[string]string{
		"X-Custom":          "keep",
		"Authorization":     "Bearer t",
		"X-Forwarded-For":   "198.51.100.1, 203.0.113.7",
		"X-Forwarded-Host":  "client-said.example",
		"X-Forwarded-Proto": "https",
		"Forwarded":         "for=198.51.100.1",
	}
	for k, v := range want {
		if got := s.header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	for _, k := range []string{"X-Hop", "Keep-Alive", "Connection"} {
		if s.header.Get(k) != "" {
			t.Errorf("hop-by-hop header %s reached the daemon: %q", k, s.header.Get(k))
		}
	}
	if s.host != "agent.example:9000" {
		t.Errorf("backend Host = %q, want the client's Host to pass through", s.host)
	}
}

func TestForward_XForwardedForStartsFromClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/volumes", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	s, _ := forward(t, req)
	if got := s.header.Get("X-Forwarded-For"); got != "203.0.113.7" {
		t.Errorf("X-Forwarded-For = %q, want 203.0.113.7", got)
	}
	if s.header.Get("X-Forwarded-Host") != "" || s.header.Get("X-Forwarded-Proto") != "" {
		t.Errorf("proxy invented X-Forwarded-Host/Proto: %v", s.header)
	}
}

func TestForward_EncodedPath(t *testing.T) {
	cases := []struct{ in, wantURI string }{
		// Path untouched: the client's escaping goes out as sent.
		{"/containers/a%2Fb/json?x=1", "/containers/a%2Fb/json?x=1"},
		// Version prefix stripped: the old raw form no longer matches, so the
		// decoded path is what reaches the daemon.
		{"/v1.45/containers/a%2Fb/json?x=1", "/containers/a/b/json?x=1"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			s, _ := forward(t, httptest.NewRequest(http.MethodGet, c.in, nil))
			if s.requestURI != c.wantURI {
				t.Errorf("request URI on the wire = %q, want %q", s.requestURI, c.wantURI)
			}
		})
	}
}

// socketBase keeps the test socket path short: macOS caps a Unix socket path
// at 104 bytes, and its default temp folder is long. Windows has no /tmp.
func socketBase() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/tmp"
}
