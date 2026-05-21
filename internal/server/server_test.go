package server

import (
	"context"
	"crypto/tls"
	"fmt"
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
