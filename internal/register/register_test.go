package register

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRegister_SendsCorrectPayload(t *testing.T) {
	var received registerPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/register" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}

		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]string{
				"agent_id": "agent-001",
				"token":    "tok-abc",
			},
		})
	}))
	defer srv.Close()

	info := AgentInfo{
		Hostname:      "testhost",
		IP:            "10.0.0.1",
		Port:          "9001",
		DockerVersion: "24.0.0",
		OS:            "linux",
		Arch:          "amd64",
		AgentVersion:  "1.0.0",
	}

	_, err := Register(srv.URL, "my-enroll-token", info, nil)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if received.Hostname != "testhost" {
		t.Errorf("hostname: got %q, want %q", received.Hostname, "testhost")
	}
	if received.IP != "10.0.0.1" {
		t.Errorf("ip: got %q, want %q", received.IP, "10.0.0.1")
	}
	if received.Port != "9001" {
		t.Errorf("port: got %q, want %q", received.Port, "9001")
	}
	if received.DockerVersion != "24.0.0" {
		t.Errorf("docker_version: got %q, want %q", received.DockerVersion, "24.0.0")
	}
	if received.OS != "linux" {
		t.Errorf("os: got %q, want %q", received.OS, "linux")
	}
	if received.Arch != "amd64" {
		t.Errorf("arch: got %q, want %q", received.Arch, "amd64")
	}
	if received.AgentVersion != "1.0.0" {
		t.Errorf("agent_version: got %q, want %q", received.AgentVersion, "1.0.0")
	}
}

func TestRegister_IncludesEnrollToken(t *testing.T) {
	var received registerPayload
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]string{
				"agent_id": "a1",
				"token":    "t1",
			},
		})
	}))
	defer srv.Close()

	info := AgentInfo{Hostname: "h", AgentVersion: "1.0.0"}
	_, err := Register(srv.URL, "secret-enroll-123", info, nil)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if received.EnrollToken != "secret-enroll-123" {
		t.Errorf("enroll_token in body: got %q, want %q", received.EnrollToken, "secret-enroll-123")
	}
	if authHeader != "Bearer secret-enroll-123" {
		t.Errorf("Authorization header: got %q, want %q", authHeader, "Bearer secret-enroll-123")
	}
}

func TestRegister_SuccessReturnsValidState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]string{
				"agent_id": "agent-xyz",
				"token":    "tok-xyz",
			},
		})
	}))
	defer srv.Close()

	info := AgentInfo{Hostname: "h"}
	state, err := Register(srv.URL, "", info, nil)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if state.AgentID != "agent-xyz" {
		t.Errorf("AgentID: got %q, want %q", state.AgentID, "agent-xyz")
	}
	if state.Token != "tok-xyz" {
		t.Errorf("Token: got %q, want %q", state.Token, "tok-xyz")
	}
	if state.ServerURL != srv.URL {
		t.Errorf("ServerURL: got %q, want %q", state.ServerURL, srv.URL)
	}
	if state.RegisteredAt == "" {
		t.Error("RegisteredAt should not be empty")
	}
}

func TestRegister_ServerErrorReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server exploded"))
	}))
	defer srv.Close()

	info := AgentInfo{Hostname: "h"}
	state, err := Register(srv.URL, "", info, nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if state != nil {
		t.Error("state should be nil on error")
	}
}

func TestRegister_200WithSuccessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
		})
	}))
	defer srv.Close()

	info := AgentInfo{Hostname: "h"}
	_, err := Register(srv.URL, "", info, nil)
	if err == nil {
		t.Fatal("expected error when success=false")
	}
}

func TestDetectInfo_ReturnsNonEmptyHostnameAndVersion(t *testing.T) {
	info := DetectInfo("/nonexistent.sock", "9999", "2.0.0")

	if info.Hostname == "" {
		t.Error("Hostname should not be empty")
	}
	if info.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion: got %q, want %q", info.AgentVersion, "2.0.0")
	}
	if info.Port != "9999" {
		t.Errorf("Port: got %q, want %q", info.Port, "9999")
	}
	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.Arch == "" {
		t.Error("Arch should not be empty")
	}
}

func TestLoadState_NonexistentFileReturnsNil(t *testing.T) {
	state, err := LoadState("/tmp/definitely-does-not-exist-register-test.json")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if state != nil {
		t.Error("expected nil state for nonexistent file")
	}
}

func TestStateSave_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := &State{
		AgentID:      "agent-123",
		Token:        "tok-abc",
		ServerURL:    "https://example.com",
		RegisteredAt: "2026-01-01T00:00:00Z",
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
	if parsed["agent_id"] != "agent-123" {
		t.Errorf("agent_id: got %v, want %q", parsed["agent_id"], "agent-123")
	}
}

func TestLoadState_AfterSave_ReturnsSameData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	original := &State{
		AgentID:      "agent-rt",
		Token:        "tok-rt",
		ServerURL:    "https://eos.test",
		RegisteredAt: "2026-05-20T12:00:00Z",
	}
	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded state should not be nil")
	}
	if loaded.AgentID != original.AgentID {
		t.Errorf("AgentID: got %q, want %q", loaded.AgentID, original.AgentID)
	}
	if loaded.Token != original.Token {
		t.Errorf("Token: got %q, want %q", loaded.Token, original.Token)
	}
	if loaded.ServerURL != original.ServerURL {
		t.Errorf("ServerURL: got %q, want %q", loaded.ServerURL, original.ServerURL)
	}
	if loaded.RegisteredAt != original.RegisteredAt {
		t.Errorf("RegisteredAt: got %q, want %q", loaded.RegisteredAt, original.RegisteredAt)
	}
}

func TestStateRoundTrip_PreservesAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "state.json")

	original := &State{
		AgentID:      "round-trip-id",
		Token:        "round-trip-tok",
		ServerURL:    "https://roundtrip.eight.ly",
		RegisteredAt: "2026-12-31T23:59:59Z",
	}
	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	if *loaded != *original {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", loaded, original)
	}
}

func TestStateSave_CreatesIntermediateDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "state.json")

	s := &State{AgentID: "test"}
	if err := s.Save(path); err != nil {
		t.Fatalf("Save should create intermediate dirs, got: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should exist at %s", path)
	}
}
