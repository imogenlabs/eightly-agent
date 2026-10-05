// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smashingtags/eightly-agent/internal/tlsutil"
)

func tlsTarget(t *testing.T, h http.HandlerFunc) (host, port string) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	return host, port
}

func TestHealthcheck_HealthyListenerPasses(t *testing.T) {
	var path string
	host, port := tlsTarget(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := Healthcheck(context.Background(), host, port); err != nil {
		t.Fatalf("healthy listener reported unhealthy: %v", err)
	}
	if path != "/healthz" {
		t.Errorf("asked %q, want /healthz", path)
	}
}

func TestHealthcheck_ErrorStatusFails(t *testing.T) {
	host, port := tlsTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := Healthcheck(context.Background(), host, port); err == nil {
		t.Fatal("a 503 was reported healthy")
	}
}

// EOS-2533: the listener that went bad on ms01 accepted connections and never
// answered. The check must give up on its own deadline and say unhealthy, in
// this process, with nothing left behind.
func TestHealthcheck_SilentListenerFailsWithinDeadline(t *testing.T) {
	release := make(chan struct{})
	host, port := tlsTarget(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Healthcheck(ctx, host, port)
	if err == nil {
		t.Fatal("a listener that never answers was reported healthy")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("check took %v to give up, deadline was 300ms", took)
	}
}

func TestHealthcheck_NothingListeningFails(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	if err := Healthcheck(context.Background(), host, port); err == nil {
		t.Fatal("a closed port was reported healthy")
	}
}

func TestHealthcheckHost_WildcardBindDialsLoopback(t *testing.T) {
	for in, want := range map[string]string{
		"":            "127.0.0.1",
		"0.0.0.0":     "127.0.0.1",
		"::":          "127.0.0.1",
		"[::]":        "127.0.0.1",
		"[::1]":       "::1",
		"::1":         "::1",
		"10.0.0.5":    "10.0.0.5",
		"agent.local": "agent.local",
	} {
		if got := healthcheckHost(in); got != want {
			t.Errorf("healthcheckHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeCertPEM writes cert to dir/cert.pem, where the agent keeps its own.
func writeCertPEM(t *testing.T, dir string, cert *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

// The probe must be talking to this agent, not to whatever answers the port:
// a listener presenting any other certificate is unhealthy.
func TestHealthcheck_RejectsAnotherCertificate(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	ours := httptest.NewTLSServer(http.HandlerFunc(ok))
	t.Cleanup(ours.Close)
	writeCertPEM(t, t.TempDir(), ours.Certificate())

	other := httptest.NewUnstartedServer(http.HandlerFunc(ok))
	otherDir := t.TempDir()
	certPath, keyPath, err := tlsutil.EnsureCert(otherDir)
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load pair: %v", err)
	}
	other.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	other.StartTLS()
	t.Cleanup(other.Close)
	host, port, _ := net.SplitHostPort(other.Listener.Addr().String())

	if err := Healthcheck(context.Background(), host, port); err == nil {
		t.Fatal("a listener presenting a different certificate was reported healthy")
	}
}
