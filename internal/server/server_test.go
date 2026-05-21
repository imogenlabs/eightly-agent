package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/smashingtags/eightly-agent/internal/config"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return fmt.Sprintf("%d", port)
}

func TestRun_PingEndpoint(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)

	cfg := &config.Config{
		Addr:       "127.0.0.1",
		Port:       port,
		Secret:     "",
		ServerURL:  "",
		StatePath:  dir + "/state.json",
		DataPath:   dir,
		SocketPath: "/var/run/docker.sock",
		Version:    "test-0.0.1",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg)
	}()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	var resp *http.Response
	var lastErr error
	for i := 0; i < 30; i++ {
		resp, lastErr = client.Get(fmt.Sprintf("https://127.0.0.1:%s/ping", port))
		if lastErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("server never became ready: %v", lastErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("/ping: got status %d, want 200", resp.StatusCode)
	}

	agentHeader := resp.Header.Get("Eightly-Agent")
	if agentHeader != "test-0.0.1" {
		t.Errorf("Eightly-Agent header: got %q, want %q", agentHeader, "test-0.0.1")
	}

	apiHeader := resp.Header.Get("Eightly-Agent-API-Version")
	if apiHeader != "2" {
		t.Errorf("Eightly-Agent-API-Version header: got %q, want %q", apiHeader, "2")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within 10 seconds")
	}
}

func TestDetectCertSANs_NoNetworkCalls(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "10.0.0.42",
		TLSSANs:     "node1.local, node2.local",
	}

	sans := detectCertSANs(cfg)

	// Should contain hostname (whatever the test machine returns)
	hostname, _ := os.Hostname()
	found := false
	for _, s := range sans {
		if s == hostname {
			found = true
			break
		}
	}
	if hostname != "" && !found {
		t.Errorf("expected hostname %q in SANs %v", hostname, sans)
	}

	// Should contain advertise IP
	foundIP := false
	for _, s := range sans {
		if s == "10.0.0.42" {
			foundIP = true
			break
		}
	}
	if !foundIP {
		t.Errorf("expected advertise IP 10.0.0.42 in SANs %v", sans)
	}

	// Should contain the two TLS SANs
	foundNode1, foundNode2 := false, false
	for _, s := range sans {
		if s == "node1.local" {
			foundNode1 = true
		}
		if s == "node2.local" {
			foundNode2 = true
		}
	}
	if !foundNode1 || !foundNode2 {
		t.Errorf("expected node1.local and node2.local in SANs %v", sans)
	}
}

func TestDetectCertSANs_EmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	sans := detectCertSANs(cfg)

	// Should still have hostname at minimum (unless hostname fails)
	hostname, _ := os.Hostname()
	if hostname != "" && len(sans) == 0 {
		t.Error("expected at least hostname in SANs")
	}
}

func TestDetectCertSANs_EmptyAdvertiseAndSANs(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "",
		TLSSANs:     "",
	}
	sans := detectCertSANs(cfg)

	hostname, _ := os.Hostname()
	if hostname != "" {
		if len(sans) != 1 || sans[0] != hostname {
			t.Errorf("expected only hostname %q, got %v", hostname, sans)
		}
	} else {
		if len(sans) != 0 {
			t.Errorf("expected empty SANs when hostname fails, got %v", sans)
		}
	}
}

func TestDetectCertSANs_AdvertiseIPOnly(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "192.168.1.100",
		TLSSANs:     "",
	}
	sans := detectCertSANs(cfg)

	found := false
	for _, s := range sans {
		if s == "192.168.1.100" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected advertise IP 192.168.1.100 in SANs %v", sans)
	}
}

func TestDetectCertSANs_TLSSANsOnly(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "",
		TLSSANs:     "foo.local,10.0.0.1",
	}
	sans := detectCertSANs(cfg)

	foundFoo, foundIP := false, false
	for _, s := range sans {
		if s == "foo.local" {
			foundFoo = true
		}
		if s == "10.0.0.1" {
			foundIP = true
		}
	}
	if !foundFoo {
		t.Errorf("expected foo.local in SANs %v", sans)
	}
	if !foundIP {
		t.Errorf("expected 10.0.0.1 in SANs %v", sans)
	}
}

func TestRun_CancelledContextShutsDown(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)

	cfg := &config.Config{
		Addr:       "127.0.0.1",
		Port:       port,
		Secret:     "",
		ServerURL:  "",
		StatePath:  dir + "/state.json",
		DataPath:   dir,
		SocketPath: "/var/run/docker.sock",
		Version:    "test-0.0.1",
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg)
	}()

	// Wait for server to start listening
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%s/ping", port))
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run should return nil on clean shutdown, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// testToken builds an HMAC-SHA256 signed token matching the format in
// internal/auth. Duplicated here to avoid exporting test helpers from auth.
func testToken(sub, secret string, exp int64) string {
	type claims struct {
		Sub  string `json:"sub"`
		Iss  string `json:"iss"`
		Type string `json:"type"`
		Iat  int64  `json:"iat"`
		Exp  int64  `json:"exp,omitempty"`
	}
	c := claims{Sub: sub, Iss: "eightly-server", Type: "api", Iat: time.Now().Unix(), Exp: exp}
	payload, _ := json.Marshal(c)
	encoded := base64.RawURLEncoding.EncodeToString(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return encoded + "." + sig
}

func waitForServer(t *testing.T, client *http.Client, port string) {
	t.Helper()
	for i := 0; i < 30; i++ {
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%s/ping", port))
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server never became ready")
}

func TestIntegration_AuthEndpoints(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	secret := "integration-test-secret"

	cfg := &config.Config{
		Addr:       "127.0.0.1",
		Port:       port,
		Secret:     secret,
		ServerURL:  "",
		StatePath:  dir + "/state.json",
		DataPath:   dir,
		SocketPath: "/var/run/docker.sock",
		Version:    "test-integration",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg)
	}()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	waitForServer(t, client, port)
	base := fmt.Sprintf("https://127.0.0.1:%s", port)

	// 1. GET /ping — no auth required, should return 200
	t.Run("ping_no_auth", func(t *testing.T) {
		resp, err := client.Get(base + "/ping")
		if err != nil {
			t.Fatalf("GET /ping: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /ping: got %d, want 200", resp.StatusCode)
		}
	})

	// 2. GET /host without auth — should return 401
	t.Run("host_no_auth", func(t *testing.T) {
		resp, err := client.Get(base + "/host")
		if err != nil {
			t.Fatalf("GET /host (no auth): %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET /host (no auth): got %d, want 401", resp.StatusCode)
		}
	})

	// 3. GET /host with valid token — should return 200 + valid JSON
	t.Run("host_with_auth", func(t *testing.T) {
		token := testToken("admin", secret, time.Now().Add(5*time.Minute).Unix())
		req, _ := http.NewRequest(http.MethodGet, base+"/host", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /host (authed): %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET /host (authed): got %d, body: %s", resp.StatusCode, body)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("GET /host: response is not valid JSON: %v", err)
		}
		if _, ok := result["pci_devices"]; !ok {
			t.Error("GET /host: response missing pci_devices field")
		}
		if _, ok := result["physical_disks"]; !ok {
			t.Error("GET /host: response missing physical_disks field")
		}
	})

	// Clean shutdown
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within 10 seconds")
	}
}
