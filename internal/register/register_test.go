package register

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
		_ = json.Unmarshal(body, &received)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

// TestRegister_ErrorDoesNotLeakBody covers fix #6: the server's response body
// must not be embedded in the returned error (only status code + fixed message).
func TestRegister_ErrorDoesNotLeakBody(t *testing.T) {
	secret := "SUPER-SECRET-INTERNAL-STACK-TRACE"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(secret))
	}))
	defer srv.Close()

	_, err := Register(srv.URL, "", AgentInfo{Hostname: "h"}, nil)
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error must not embed server response body, got: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should report the status code, got: %v", err)
	}
}

func TestReRegister_ErrorDoesNotLeakBody(t *testing.T) {
	secret := "DB-PASSWORD-IN-ERROR-BODY"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(secret))
	}))
	defer srv.Close()

	st := &State{AgentID: "a", Token: "t", ServerURL: srv.URL}
	_, err := ReRegister(st, AgentInfo{Hostname: "h"}, nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error must not embed server response body, got: %v", err)
	}
}

func TestRegister_200WithSuccessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

func TestReRegister_SendsBearerToken(t *testing.T) {
	var authHeader string
	var received reRegisterPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]string{
				"agent_id": "agent-renewed",
				"token":    "tok-renewed",
			},
		})
	}))
	defer srv.Close()

	state := &State{
		AgentID:   "agent-original",
		Token:     "tok-original-secret",
		ServerURL: srv.URL,
	}
	info := AgentInfo{Hostname: "rehost", AgentVersion: "2.0.0"}

	newState, err := ReRegister(state, info, nil)
	if err != nil {
		t.Fatalf("ReRegister failed: %v", err)
	}

	if authHeader != "Bearer tok-original-secret" {
		t.Errorf("Authorization header: got %q, want %q", authHeader, "Bearer tok-original-secret")
	}
	if received.AgentID != "agent-original" {
		t.Errorf("agent_id in payload: got %q, want %q", received.AgentID, "agent-original")
	}
	if received.Hostname != "rehost" {
		t.Errorf("hostname in payload: got %q, want %q", received.Hostname, "rehost")
	}
	if newState.AgentID != "agent-renewed" {
		t.Errorf("returned AgentID: got %q, want %q", newState.AgentID, "agent-renewed")
	}
	if newState.Token != "tok-renewed" {
		t.Errorf("returned Token: got %q, want %q", newState.Token, "tok-renewed")
	}
}

func TestReRegister_ServerErrorReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden"))
	}))
	defer srv.Close()

	state := &State{
		AgentID:   "agent-1",
		Token:     "tok-expired",
		ServerURL: srv.URL,
	}
	info := AgentInfo{Hostname: "h"}
	_, err := ReRegister(state, info, nil)
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestDetectInfo_ReturnsNonEmptyHostnameAndVersion(t *testing.T) {
	info := DetectInfo("/nonexistent.sock", "9999", "2.0.0", "")

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

func TestDetectInfo_AdvertiseIPOverridesDetection(t *testing.T) {
	info := DetectInfo("/nonexistent.sock", "9001", "1.0.0", "10.99.99.1")
	if info.IP != "10.99.99.1" {
		t.Errorf("expected advertise IP 10.99.99.1, got %q", info.IP)
	}
}

func TestDetectInfo_EmptyAdvertiseIPFallsBackToDetection(t *testing.T) {
	info := DetectInfo("/nonexistent.sock", "9001", "1.0.0", "")
	// IP may be empty (no network) or a real IP -- either is fine as long as it didn't panic
	_ = info.IP
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
	// t.TempDir() is 0755; LoadState now refuses a state file in a group/world
	// accessible directory, so tighten it to 0700 first.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
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

func TestBuildServerTLSConfig_InsecureTrue(t *testing.T) {
	log := zerolog.Nop()
	cfg, err := BuildServerTLSConfig("", true, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true when insecure=true")
	}
}

func TestBuildServerTLSConfig_DefaultPinsViaTOFU(t *testing.T) {
	log := zerolog.Nop()
	cfg, err := BuildServerTLSConfig("", false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Pin-only TOFU: chain verification is off (a self-signed appliance server
	// can't be vouched for by a CA), and a VerifyConnection callback enforces
	// the leaf SPKI pin instead.
	if !cfg.InsecureSkipVerify {
		t.Error("default (no CA) config should disable chain verification and rely on the pin")
	}
	if cfg.VerifyConnection == nil {
		t.Error("default config must install a VerifyConnection callback for TOFU pinning")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %d, want tls.VersionTLS12 (%d)", cfg.MinVersion, tls.VersionTLS12)
	}
}

func TestBuildServerTLSConfig_NonExistentCA_ReturnsError(t *testing.T) {
	log := zerolog.Nop()
	_, err := BuildServerTLSConfig("/nonexistent/path/ca.pem", false, log)
	if err == nil {
		t.Fatal("expected error for non-existent CA path")
	}
}

func TestBuildServerTLSConfig_ValidCA_SetsRootCAs(t *testing.T) {
	// Generate a self-signed CA cert
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(caFile, pemBytes, 0600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	log := zerolog.Nop()
	cfg, err := BuildServerTLSConfig(caFile, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false when a valid CA is provided")
	}
	if cfg.RootCAs == nil {
		t.Fatal("RootCAs should not be nil when a valid CA is provided")
	}
}

func TestBuildServerTLSConfig_InvalidPEM_ReturnsError(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "bad-ca.pem")
	if err := os.WriteFile(caFile, []byte("not a valid PEM certificate"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	log := zerolog.Nop()
	_, err := BuildServerTLSConfig(caFile, false, log)
	if err == nil {
		t.Fatal("expected error for invalid PEM content")
	}
}

// A 200 with success=true but no agent ID or token used to be taken as a
// registration: the agent saved an empty identity, and with no ID the token
// subject check was off.
func TestRegisterAndReRegister_RejectEmptyIdentity(t *testing.T) {
	for _, data := range []map[string]string{
		{"agent_id": "", "token": "tok"},
		{"agent_id": "agent-1", "token": ""},
		{},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
		}))
		if st, err := Register(srv.URL, "", AgentInfo{Hostname: "h"}, nil); err == nil || st != nil {
			t.Errorf("Register accepted %v: state %+v, err %v", data, st, err)
		}
		old := &State{AgentID: "agent-1", Token: "old", ServerURL: srv.URL}
		if st, err := ReRegister(old, AgentInfo{Hostname: "h"}, nil); err == nil || st != nil {
			t.Errorf("ReRegister accepted %v: state %+v, err %v", data, st, err)
		}
		srv.Close()
	}
}

func TestCheckWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "state.json")
	if err := CheckWritable(path); err != nil {
		t.Fatalf("writable path refused: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("probe file left behind: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("CheckWritable created the state file: %v", err)
	}

	// The probe must not touch an existing state file.
	if err := os.WriteFile(path, []byte(`{"agent_id":"keep"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckWritable(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"agent_id":"keep"}` {
		t.Errorf("state file changed to %q", b)
	}
}

func TestCheckWritable_ReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only mode bit does not stop writes into a folder on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root can write a read-only directory")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := CheckWritable(filepath.Join(dir, "state.json")); err == nil {
		t.Fatal("a read-only directory was reported writable")
	}
}
