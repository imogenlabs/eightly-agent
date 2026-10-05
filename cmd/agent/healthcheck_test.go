// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunHealthcheck_ExitCodes(t *testing.T) {
	t.Setenv("AGENT_PORT", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	var stderr bytes.Buffer
	if code := runHealthcheck([]string{"--port", port}, &stderr); code != 0 {
		t.Fatalf("healthy agent: exit %d, want 0 (%s)", code, stderr.String())
	}

	srv.Close()
	stderr.Reset()
	if code := runHealthcheck([]string{"--port", port}, &stderr); code != 1 {
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

	if code := runHealthcheck(nil, &bytes.Buffer{}); code != 0 {
		t.Fatalf("AGENT_PORT=%s: exit %d, want 0", port, code)
	}
}
