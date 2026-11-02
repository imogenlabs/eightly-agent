// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imogenlabs/eightly-agent/internal/tlsutil"
)

// tlsTarget starts a TLS listener and writes the certificate it presents where
// the healthcheck will look for the agent's own.
func tlsTarget(t *testing.T, h http.HandlerFunc) (host, port, certPath string) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	return host, port, writeCertPEM(t, t.TempDir(), srv.Certificate())
}

// agentTarget starts a listener serving a certificate made by the agent's own
// tlsutil.EnsureCert in dir: self-signed and not a CA, unlike httptest's.
func agentTarget(t *testing.T, dir string) (host, port, certPath string) {
	t.Helper()
	certPath, keyPath, err := tlsutil.EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load pair: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	host, port, _ = net.SplitHostPort(srv.Listener.Addr().String())
	return host, port, certPath
}

func TestHealthcheck_HealthyListenerPasses(t *testing.T) {
	var path string
	host, port, certPath := tlsTarget(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := Healthcheck(context.Background(), host, port, certPath); err != nil {
		t.Fatalf("healthy listener reported unhealthy: %v", err)
	}
	if path != "/healthz" {
		t.Errorf("asked %q, want /healthz", path)
	}
}

func TestHealthcheck_ErrorStatusFails(t *testing.T) {
	host, port, certPath := tlsTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := Healthcheck(context.Background(), host, port, certPath); err == nil {
		t.Fatal("a 503 was reported healthy")
	}
}

// The listener that went bad in production accepted connections and never
// answered. The check must give up on its own deadline and say unhealthy, in
// this process, with nothing left behind.
func TestHealthcheck_SilentListenerFailsWithinDeadline(t *testing.T) {
	release := make(chan struct{})
	host, port, certPath := tlsTarget(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Healthcheck(ctx, host, port, certPath)
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
	_, _, certPath := agentTarget(t, t.TempDir())
	if err := Healthcheck(context.Background(), host, port, certPath); err == nil {
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
	_, _, ourCert := agentTarget(t, t.TempDir())
	host, port, _ := agentTarget(t, t.TempDir())

	if err := Healthcheck(context.Background(), host, port, ourCert); err == nil {
		t.Fatal("a listener presenting a different certificate was reported healthy")
	}
}

// The agent's real certificate (self-signed, not a CA) must verify against
// itself, or the check would fail on every healthy agent.
func TestHealthcheck_TrustsTheAgentsOwnCertificate(t *testing.T) {
	for _, addr := range []string{"", "0.0.0.0", "127.0.0.1"} {
		_, port, certPath := agentTarget(t, t.TempDir())
		if err := Healthcheck(context.Background(), addr, port, certPath); err != nil {
			t.Errorf("addr %q: the agent's own certificate was rejected: %v", addr, err)
		}
	}
}

// A certificate the probe cannot read is unhealthy, never a reason to stop
// checking who answered.
func TestHealthcheck_UnreadableCertificateFails(t *testing.T) {
	host, port, _ := agentTarget(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "cert.pem")
	err := Healthcheck(context.Background(), host, port, missing)
	if err == nil {
		t.Fatal("a missing certificate file was reported healthy")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("error does not say which certificate it could not read: %v", err)
	}

	garbage := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(garbage, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Healthcheck(context.Background(), host, port, garbage); err == nil {
		t.Fatal("a certificate file holding no certificate was reported healthy")
	}
}

// The agent reuses its self-signed certificate after it expires (Eight.ly OS
// pins it, so it is never reissued). An expired certificate the agent serves
// must still pass, and an expired one it does not serve must still fail.
func TestHealthcheck_ExpiredOwnCertificatePasses(t *testing.T) {
	expired := func() (*x509.Certificate, tls.Certificate) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "eightly-agent"},
			NotBefore:    time.Now().Add(-2 * 365 * 24 * time.Hour),
			NotAfter:     time.Now().Add(-365 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:     []string{"localhost"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return leaf, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	}
	serve := func(pair tls.Certificate) (string, string) {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		return host, port
	}

	ours, pair := expired()
	if !time.Now().After(ours.NotAfter) {
		t.Fatal("test certificate is not expired")
	}
	host, port := serve(pair)
	if err := Healthcheck(context.Background(), host, port, writeCertPEM(t, t.TempDir(), ours)); err != nil {
		t.Fatalf("the agent's own expired certificate was rejected: %v", err)
	}

	other, _ := expired()
	if err := Healthcheck(context.Background(), host, port, writeCertPEM(t, t.TempDir(), other)); err == nil {
		t.Fatal("a listener presenting another expired certificate was reported healthy")
	}
}

// A specific --addr the certificate does not name (a reused or supplied cert)
// is verified against a name the certificate does carry, not refused forever.
func TestCertServerName_FallsBackToANameTheCertCarries(t *testing.T) {
	certPath, _, err := tlsutil.EnsureCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(data)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{"127.0.0.1": "127.0.0.1", "localhost": "localhost", "10.9.9.9": "localhost"} {
		got, err := certServerName(leaf, host)
		if err != nil || got != want {
			t.Errorf("certServerName(%q) = %q, %v; want %q", host, got, err, want)
		}
	}
	if _, err := certServerName(&x509.Certificate{}, "127.0.0.1"); err == nil {
		t.Error("a certificate with no names was given a server name")
	}
}

func TestWithinValidity(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{NotBefore: start, NotAfter: start.AddDate(1, 0, 0)}
	mid := start.AddDate(0, 6, 0)
	for now, want := range map[time.Time]time.Time{
		mid:                     mid,
		start.AddDate(3, 0, 0):  leaf.NotAfter,
		start.AddDate(-1, 0, 0): leaf.NotBefore,
	} {
		if got := withinValidity(leaf, now); !got.Equal(want) {
			t.Errorf("withinValidity(%v) = %v, want %v", now, got, want)
		}
	}
}
