package websocket

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// floodingDocker is a fake Docker socket that completes the hijack handshake
// and then writes output as fast as it can, like `yes` in a container.
func floodingDocker(t *testing.T) string {
	t.Helper()
	// Under /tmp to stay inside the sun_path limit on macOS.
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
		_, _ = c.Write([]byte("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"))
		out := []byte(strings.Repeat("y\n", 16*1024))
		for {
			if _, err := c.Write(out); err != nil {
				return
			}
		}
	}()
	return sock
}

// setLimits shortens the session limits for one test and restores them after
// the session has ended.
func setLimits(t *testing.T, ping, write, idle, absolute time.Duration) {
	t.Helper()
	old := [4]time.Duration{pingInterval, writeWait, idleTimeout, absoluteTimeout}
	pingInterval, writeWait, idleTimeout, absoluteTimeout = ping, write, idle, absolute
	t.Cleanup(func() {
		pingInterval, writeWait, idleTimeout, absoluteTimeout = old[0], old[1], old[2], old[3]
	})
}

// dialNeverRead connects a client that never reads a byte, so the agent's
// writes to it stall once the socket buffers are full.
func dialNeverRead(t *testing.T, sock string) {
	t.Helper()
	srv := httptest.NewServer(AttachHandler(sock, allowAll{}, false, nil))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/websocket/attach?id=abcdef123456"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, ReadBufferSize: 1024}
	hdr := http.Header{}
	hdr.Set("Origin", srv.URL)
	conn, resp, err := dialer.Dial(wsURL, hdr)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4096)
	}
}

// waitSessionsGone waits for every bridge to end and frees its slot.
func waitSessionsGone(t *testing.T, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for atomic.LoadInt64(&activeSessions) != 0 {
		if time.Since(start) > within {
			t.Fatalf("session still holds a slot after %v (active=%d)", within, atomic.LoadInt64(&activeSessions))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Since(start)
}

// TestBridge_AbsoluteLimitWithStalledClient: the data write is stuck on a
// client that never reads and the write deadline is far away. Pings are due
// constantly. The absolute limit must still end the session on time; before
// the fix the ping waited on the stuck writer's lock and the timers were
// never checked again.
func TestBridge_AbsoluteLimitWithStalledClient(t *testing.T) {
	setLimits(t, 20*time.Millisecond, time.Hour, time.Hour, 1500*time.Millisecond)
	dialNeverRead(t, floodingDocker(t))
	waitSessionsGone(t, 8*time.Second)
}

// TestBridge_IdleLimitWithStalledClient is the same for the idle limit.
func TestBridge_IdleLimitWithStalledClient(t *testing.T) {
	setLimits(t, 20*time.Millisecond, time.Hour, 1500*time.Millisecond, time.Hour)
	dialNeverRead(t, floodingDocker(t))
	waitSessionsGone(t, 8*time.Second)
}

// TestBridge_WriteDeadlineEndsStalledSession: with both limits far away, the
// write deadline alone ends a session whose client stopped reading.
func TestBridge_WriteDeadlineEndsStalledSession(t *testing.T) {
	setLimits(t, time.Hour, time.Second, time.Hour, time.Hour)
	dialNeverRead(t, floodingDocker(t))
	waitSessionsGone(t, 8*time.Second)
}
