// SPDX-License-Identifier: Apache-2.0

package websocket

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// SNI carries no port. An agent served on :9001 is reached as
// https://agent.example:9001, and its own page sends that Origin, so the port
// from Host must be kept when Host names the same host as SNI.
func TestCheckOrigin_SNIKeepsExplicitPort(t *testing.T) {
	cases := []struct {
		name, host, origin string
		want               bool
	}{
		{"same origin on 9001", "agent.example:9001", "https://agent.example:9001", true},
		{"host case differs from SNI", "AGENT.example:9001", "https://agent.example:9001", true},
		{"origin on default port, request on 9001", "agent.example:9001", "https://agent.example", false},
		{"origin on another port", "agent.example:9001", "https://agent.example:9002", false},
		{"no port in Host", "agent.example", "https://agent.example", true},
		{"no port in Host, origin on 9001", "agent.example", "https://agent.example:9001", false},
		// Host naming another host is spoofed: neither its name nor its port counts.
		{"spoofed host lends no port", "evil.example:9001", "https://agent.example:9001", false},
		{"spoofed host lends no name", "evil.example:9001", "https://evil.example:9001", false},
		{"spoofed host, SNI default port", "evil.example:9001", "https://agent.example", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://agent.example:9001/websocket/attach?id=abcdef123456", nil)
			r.TLS = &tls.ConnectionState{ServerName: "agent.example"}
			r.Host = c.host
			r.Header.Set("Origin", c.origin)
			if got := checkOrigin(r, nil); got != c.want {
				t.Errorf("checkOrigin(SNI=agent.example, Host=%q, Origin=%q) = %v, want %v", c.host, c.origin, got, c.want)
			}
		})
	}
}

// eagerDocker is a fake Docker socket that sends the 101 headers and the first
// terminal output in a single write, as the real daemon can.
func eagerDocker(t *testing.T, output string) string {
	t.Helper()
	dir, err := os.MkdirTemp(socketBase(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n" + output))
		// Hold the stream open until the agent hangs up.
		_, _ = c.Read(buf)
	}()
	return sock
}

// The handshake reader may prefetch output that arrived with the 101. Before
// the fix that reader was dropped and the client never saw the first prompt.
func TestBridge_KeepsOutputSentWithHandshake(t *testing.T) {
	const prompt = "root@abcdef123456:/# "
	srv := httptest.NewServer(AttachHandler(eagerDocker(t, prompt), allowAll{}, false, nil))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/websocket/attach?id=abcdef123456"
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)
	conn, resp, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).Dial(wsURL, hdr)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var got strings.Builder
	for got.Len() < len(prompt) {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read after %q: %v (output sent with the 101 was lost)", got.String(), err)
		}
		got.Write(msg)
	}
	if got.String() != prompt {
		t.Fatalf("got %q, want %q", got.String(), prompt)
	}
	_ = conn.Close()
	waitSessionsGone(t, 5*time.Second)
}
