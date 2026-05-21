package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewUpgrader_AuthDisabled_RejectsCrossOrigin(t *testing.T) {
	up := newUpgrader(false)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	r.Header.Set("Origin", "http://evil.example.com")

	if up.CheckOrigin(r) {
		t.Error("expected cross-origin request to be rejected when auth is disabled")
	}
}

func TestNewUpgrader_AuthDisabled_AllowsSameOrigin(t *testing.T) {
	up := newUpgrader(false)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	r.Header.Set("Origin", "http://localhost:9001")

	if !up.CheckOrigin(r) {
		t.Error("expected same-origin request to be allowed when auth is disabled")
	}
}

func TestNewUpgrader_AuthDisabled_AllowsNoOrigin(t *testing.T) {
	up := newUpgrader(false)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	// No Origin header

	if !up.CheckOrigin(r) {
		t.Error("expected request with no Origin header to be allowed")
	}
}

func TestNewUpgrader_AuthEnabled_AllowsCrossOrigin(t *testing.T) {
	up := newUpgrader(true)

	r := httptest.NewRequest("GET", "http://localhost:9001/websocket/attach?id=abcdef123456", nil)
	r.Host = "localhost:9001"
	r.Header.Set("Origin", "http://evil.example.com")

	if !up.CheckOrigin(r) {
		t.Error("expected cross-origin request to be allowed when auth is enabled")
	}
}

func TestAttachHandler_CrossOriginRejectedWithoutAuth(t *testing.T) {
	handler := AttachHandler("/nonexistent.sock", false)

	// We need a real HTTP server to test the WS upgrade rejection
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	// Attempt a websocket upgrade with cross-origin
	req, _ := http.NewRequest("GET", srv.URL+"/websocket/attach?id=abcdef123456", nil)
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
