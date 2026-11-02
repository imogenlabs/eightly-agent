package websocket

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNewUpgrader_AuthDisabled_RejectsCrossOrigin(t *testing.T) {
	up := newUpgrader(false, nil)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	r.Header.Set("Origin", "http://evil.example.com")

	if up.CheckOrigin(r) {
		t.Error("expected cross-origin request to be rejected when auth is disabled")
	}
}

func TestNewUpgrader_AuthDisabled_AllowsSameOrigin(t *testing.T) {
	up := newUpgrader(false, nil)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	r.Header.Set("Origin", "http://localhost:9001")

	if !up.CheckOrigin(r) {
		t.Error("expected same-origin request to be allowed when auth is disabled")
	}
}

// Empty Origin with no token must NOT pass (browser-shaped requests can omit
// Origin; only authenticated server-to-agent calls may pass).
func TestNewUpgrader_NoOrigin_NoToken_Rejected(t *testing.T) {
	up := newUpgrader(false, nil)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	// No Origin header, no Authorization header.

	if up.CheckOrigin(r) {
		t.Error("expected no-Origin/no-token request to be rejected")
	}
}

// Empty Origin WITH a Bearer token represents a legit server-to-agent call.
func TestNewUpgrader_NoOrigin_WithToken_Allowed(t *testing.T) {
	up := newUpgrader(true, nil)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	r.Header.Set("Authorization", "Bearer some.token.value")

	if !up.CheckOrigin(r) {
		t.Error("expected no-Origin request with a Bearer token to be allowed")
	}
}

// SABOTAGE-PROVEN (fix #1): with an EMPTY allowlist and auth ENABLED, a
// cross-origin upgrade MUST still be rejected. The old code returned true
// unconditionally when authEnabled, so this assertion failed (red-on-vuln).
func TestNewUpgrader_AuthEnabled_EmptyAllowlist_RejectsCrossOrigin(t *testing.T) {
	up := newUpgrader(true, nil)

	r := httptest.NewRequest("GET", "https://os.eight.ly/websocket/attach?id=abcdef123456", nil)
	r.TLS = &tls.ConnectionState{ServerName: "os.eight.ly"}
	r.Host = "os.eight.ly"
	r.Header.Set("Origin", "https://evil.example.com")
	// A token is present, mimicking the realistic CSWSH: a browser auto-attaches
	// the agent's credential, but Origin betrays the cross-site source.
	r.Header.Set("Authorization", "Bearer some.token.value")

	if up.CheckOrigin(r) {
		t.Error("CSWSH: cross-origin upgrade must be rejected even with auth enabled and empty allowlist")
	}
}

func TestNewUpgrader_AuthEnabled_EmptyAllowlist_AllowsSameOrigin(t *testing.T) {
	up := newUpgrader(true, nil)

	r := httptest.NewRequest("GET", "https://os.eight.ly/websocket/attach?id=abcdef123456", nil)
	r.TLS = &tls.ConnectionState{ServerName: "os.eight.ly"}
	r.Host = "os.eight.ly"
	r.Header.Set("Origin", "https://os.eight.ly")
	r.Header.Set("Authorization", "Bearer some.token.value")

	if !up.CheckOrigin(r) {
		t.Error("expected same-origin upgrade to be allowed when auth is enabled")
	}
}

// Fix #2: the same-origin check must compare against the TLS server name, not a
// spoofable Host header. An attacker-controlled Host must not let a foreign
// Origin pass; the SNI is authoritative.
func TestCheckOrigin_PrefersTLSServerNameOverHost(t *testing.T) {
	r := httptest.NewRequest("GET", "https://os.eight.ly/websocket/attach?id=abcdef123456", nil)
	r.TLS = &tls.ConnectionState{ServerName: "os.eight.ly"}
	r.Host = "evil.example.com" // spoofed Host header
	r.Header.Set("Origin", "https://evil.example.com")

	if checkOrigin(r, nil) {
		t.Error("expected Origin matching only the spoofed Host (not the TLS SNI) to be rejected")
	}

	// Origin matching the authoritative TLS SNI is allowed.
	r.Header.Set("Origin", "https://os.eight.ly")
	if !checkOrigin(r, nil) {
		t.Error("expected Origin matching the TLS server name to be allowed")
	}
}

// Default-port equivalence: Origin "https://host" matches request Host "host:443".
func TestCheckOrigin_DefaultPortEquivalence(t *testing.T) {
	r := httptest.NewRequest("GET", "https://os.eight.ly:443/x?id=abcdef123456", nil)
	r.TLS = &tls.ConnectionState{ServerName: "os.eight.ly:443"}
	r.Header.Set("Origin", "https://os.eight.ly")

	if !checkOrigin(r, nil) {
		t.Error("expected https origin without port to match host:443")
	}
}

func TestCheckOrigin_Allowlist(t *testing.T) {
	allow := []string{"https://os.eight.ly", "https://os-dev.eight.ly"}

	cases := []struct {
		name   string
		origin string
		token  bool
		want   bool
	}{
		{"in allowlist", "https://os.eight.ly", false, true},
		{"in allowlist case-insensitive", "https://OS.EIGHT.LY", false, true},
		{"not in allowlist", "https://evil.example.com", false, false},
		{"not in allowlist even with token", "https://evil.example.com", true, false},
		{"empty origin no token", "", false, false},
		{"empty origin with token", "", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://os.eight.ly/x?id=abcdef123456", nil)
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if c.token {
				r.Header.Set("Authorization", "Bearer t.t.t")
			}
			if got := checkOrigin(r, allow); got != c.want {
				t.Errorf("checkOrigin(origin=%q, token=%v) = %v, want %v", c.origin, c.token, got, c.want)
			}
		})
	}
}

// Non-web schemes (e.g. file://, chrome-extension://) must never pass the
// same-origin check.
func TestCheckOrigin_RejectsNonWebScheme(t *testing.T) {
	r := httptest.NewRequest("GET", "https://os.eight.ly/x?id=abcdef123456", nil)
	r.TLS = &tls.ConnectionState{ServerName: "os.eight.ly"}
	r.Header.Set("Origin", "file://os.eight.ly")

	if checkOrigin(r, nil) {
		t.Error("expected non-web-scheme Origin to be rejected")
	}
}

func TestAttachHandler_CrossOriginRejectedWithoutAuth(t *testing.T) {
	handler := AttachHandler("/nonexistent.sock", allowAll{}, false, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/websocket/attach?id=abcdef123456", nil) //nolint:noctx // test helper, no meaningful context
	req.Header.Set("Origin", "http://evil.example.com")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Error("expected cross-origin WebSocket upgrade to be rejected, but got 101")
	}
}

// Fix #4 (read limit): a frame larger than maxFrameBytes must tear the session
// down. We stand up the attach handler against a fake Docker unix socket that
// completes the hijack handshake, then drive an oversized client frame and
// assert the server closes the connection.
func TestBridge_ReadLimitClosesOnOversizedFrame(t *testing.T) {
	if maxFrameBytes != 64*1024 {
		t.Fatalf("maxFrameBytes = %d, want 65536 (parity with OS terminal)", maxFrameBytes)
	}

	// The socket lives under /tmp to stay inside the sun_path limit (104 bytes
	// on macOS, where t.TempDir() is already too long).
	dir, err := os.MkdirTemp(socketBase(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Fake Docker: accept, read the request, reply 101, then drain.
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"))
		_, _ = io.Copy(io.Discard, c) // drain until close
	}()

	handler := AttachHandler(sock, allowAll{}, false, nil)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/websocket/attach?id=abcdef123456"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL) // same-origin so the upgrade passes
	conn, resp, derr := dialer.Dial(wsURL, hdr)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if derr != nil {
		t.Fatalf("ws dial: %v", derr)
	}
	defer conn.Close()

	// Send a frame larger than the server read limit.
	big := make([]byte, maxFrameBytes+1024)
	if err := conn.WriteMessage(websocket.BinaryMessage, big); err != nil {
		t.Fatalf("write big frame: %v", err)
	}

	// The server should close the connection (read limit exceeded -> close).
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, _, rerr := conn.ReadMessage(); rerr == nil {
		t.Error("expected connection to be closed after oversized frame, got nil error")
	}
}

func TestValidDockerID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"valid 12-char hex", "abcdef123456", true},
		{"valid 64-char hex", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", true},
		{"valid 12-char mixed case", "aBcDeF123456", true},
		{"empty string", "", false},
		{"too short 11 chars", "abcdef12345", false},
		{"too long 65 chars", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678901", false},
		{"contains CRLF", "abcdef1234\r\n", false},
		{"contains newline", "abcdef12345\n", false},
		{"contains path separator", "abcdef/12345", false},
		{"contains backslash", "abcdef\\12345", false},
		{"contains spaces", "abcdef 12345", false},
		{"contains dots", "abcdef.12345", false},
		{"contains dashes", "abcdef-12345", false},
		{"contains underscore", "abcdef_12345", false},
		{"exactly 12 chars", "aB3cD4eF5678", true},
		{"exactly 64 chars all digits", "1234567890123456789012345678901234567890123456789012345678901234", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validDockerID(tt.id)
			if got != tt.want {
				t.Errorf("validDockerID(%q) = %v, want %v", tt.id, got, tt.want)
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
