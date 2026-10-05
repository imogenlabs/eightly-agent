// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// dataDirWithCert writes the certificate srv presents into a data directory,
// where the agent keeps its own, and returns the directory.
func dataDirWithCert(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	der := srv.Certificate().Raw
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return dir
}

func TestRunHealthcheck_ExitCodes(t *testing.T) {
	t.Setenv("AGENT_PORT", "")
	t.Setenv("DATA_PATH", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	data := dataDirWithCert(t, srv)

	var stderr bytes.Buffer
	if code := runHealthcheck([]string{"--port", port, "--data", data}, &stderr); code != 0 {
		t.Fatalf("healthy agent: exit %d, want 0 (%s)", code, stderr.String())
	}

	srv.Close()
	stderr.Reset()
	if code := runHealthcheck([]string{"--port", port, "--data", data}, &stderr); code != 1 {
		t.Fatalf("stopped agent: exit %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("an unhealthy result printed nothing to say why")
	}
}

// The probe honours AGENT_PORT the way the agent does, so a container started
// with a different port is asked on the port it actually serves.
func TestRunHealthcheck_HonoursAgentPortEnv(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("AGENT_PORT", port)
	t.Setenv("DATA_PATH", dataDirWithCert(t, srv))

	if code := runHealthcheck(nil, &bytes.Buffer{}); code != 0 {
		t.Fatalf("AGENT_PORT=%s: exit %d, want 0", port, code)
	}
}
