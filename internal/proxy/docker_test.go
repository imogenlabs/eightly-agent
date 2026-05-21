package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewDockerProxy_ReturnsNonNilHandler(t *testing.T) {
	h := NewDockerProxy("/var/run/docker.sock")
	if h == nil {
		t.Fatal("NewDockerProxy returned nil")
	}
}

func TestNewDockerProxy_NonexistentSocket_ReturnsError(t *testing.T) {
	h := NewDockerProxy("/tmp/nonexistent-docker-test.sock")

	req := httptest.NewRequest("GET", "/containers/json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 when socket doesn't exist")
	}
}

func TestRewriteDockerPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/containers", "/containers/json"},
		{"/images", "/images/json"},
		{"/volumes", "/volumes"},
		{"/networks", "/networks"},
		{"/containers/abc123/start", "/containers/abc123/start"},
		{"/images/abc123/tag", "/images/abc123/tag"},
		{"/something-else", "/something-else"},
	}
	for _, tt := range tests {
		got := rewriteDockerPath(tt.input)
		if got != tt.want {
			t.Errorf("rewriteDockerPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVersionPrefixStripping(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/v1.41/containers/json", "/containers/json"},
		{"/v1.45/images/json", "/images/json"},
		{"/containers/json", "/containers/json"},
	}
	for _, tt := range tests {
		got := versionPrefix.ReplaceAllString(tt.input, "")
		if got != tt.want {
			t.Errorf("versionPrefix strip %q = %q, want %q", tt.input, got, tt.want)
		}
	}
}
