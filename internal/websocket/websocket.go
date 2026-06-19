// SPDX-License-Identifier: Apache-2.0

package websocket

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/smashingtags/eightly-agent/internal/metrics"
)

var dockerIDRe = regexp.MustCompile(`^[a-zA-Z0-9]{12,64}$`)

func validDockerID(id string) bool {
	return dockerIDRe.MatchString(id)
}

// DoS / liveness bounds for the WebSocket<->Docker bridge.
const (
	// maxFrameBytes caps a single inbound WebSocket frame, matching the OS
	// terminal's 64KB read limit. Frames larger than this tear down the session.
	maxFrameBytes = 64 * 1024
	// pongWait is how long we wait for a pong before considering the peer dead.
	pongWait = 60 * time.Second
	// pingInterval must be shorter than pongWait so a keepalive lands in time.
	pingInterval = 30 * time.Second
	// idleTimeout closes a session that has seen no client traffic for this long.
	idleTimeout = 15 * time.Minute
	// absoluteTimeout is the hard ceiling on a single session regardless of activity.
	absoluteTimeout = 8 * time.Hour
	// dockerDialTimeout bounds connecting to the Docker unix socket.
	dockerDialTimeout = 5 * time.Second
	// dockerHandshakeTimeout bounds reading Docker's upgrade response.
	dockerHandshakeTimeout = 10 * time.Second
	// maxConcurrentSessions caps simultaneous bridges across both handlers.
	maxConcurrentSessions = 50
)

// activeSessions tracks the number of in-flight bridges for the concurrency cap.
var activeSessions int64

// dbgClose logs an error from a best-effort Close at debug level (gosec G104).
func dbgClose(err error, what string) {
	if err != nil {
		log.Debug().Err(err).Str("conn", what).Msg("websocket: close error")
	}
}

// requestHost returns the authoritative host[:port] for the request, preferring
// the TLS server name (SNI) the client negotiated over the spoofable Host header.
func requestHost(r *http.Request) string {
	if r.TLS != nil && r.TLS.ServerName != "" {
		return r.TLS.ServerName
	}
	if r.Host != "" {
		return r.Host
	}
	return r.Header.Get("Host")
}

// hostMatches compares two host strings, treating a bare host as matching a
// host:port form on the default port for the given scheme. This lets an Origin
// of "https://example.com" match a request Host of "example.com:443".
func hostMatches(originHost, reqHost, scheme string) bool {
	if strings.EqualFold(originHost, reqHost) {
		return true
	}
	oh, op := splitHostPort(originHost, defaultPort(scheme))
	rh, rp := splitHostPort(reqHost, defaultPort(scheme))
	return strings.EqualFold(oh, rh) && op == rp
}

func defaultPort(scheme string) string {
	if scheme == "https" || scheme == "wss" {
		return "443"
	}
	return "80"
}

// splitHostPort splits host:port, returning defPort when no port is present.
func splitHostPort(hostport, defPort string) (string, string) {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		// No port present (SplitHostPort errors): treat the whole string as host.
		return hostport, defPort
	}
	if p == "" {
		p = defPort
	}
	return h, p
}

// hasToken reports whether the request carries a non-empty Bearer token. By the
// time CheckOrigin runs, the auth middleware has already validated it; presence
// here distinguishes a legit server-to-agent call from a browser-shaped request.
func hasToken(r *http.Request) bool {
	authz := r.Header.Get("Authorization")
	return strings.HasPrefix(authz, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(authz, "Bearer ")) != ""
}

// checkOrigin enforces CSWSH protection. Origin verification runs in EVERY
// branch and is orthogonal to authentication — auth being enabled never causes
// the origin check to be skipped.
//
//   - allowedOrigins non-empty: the Origin must exactly match an allowlist entry
//     (scheme + host + port). Absent/empty Origin is allowed only with a token.
//   - allowedOrigins empty: strict same-origin. A present Origin must match the
//     request's authoritative host (TLS SNI, falling back to Host) on a web
//     scheme. An absent/empty Origin is allowed only with a token (legit
//     non-browser server-to-agent calls), never unconditionally.
func checkOrigin(r *http.Request, allowedOrigins []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))

	if len(allowedOrigins) > 0 {
		if origin == "" {
			// No Origin and an allowlist is configured: only a non-browser
			// authenticated caller may pass.
			return hasToken(r)
		}
		for _, allowed := range allowedOrigins {
			if strings.EqualFold(origin, allowed) {
				return true
			}
		}
		return false
	}

	// No allowlist: enforce strict same-origin regardless of authEnabled.
	if origin == "" {
		// Empty/absent Origin: allow only the authenticated server-to-agent path.
		return hasToken(r)
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return hostMatches(u.Host, requestHost(r), scheme)
}

func newUpgrader(authEnabled bool, allowedOrigins []string) websocket.Upgrader {
	_ = authEnabled // origin is orthogonal to auth; param retained for wiring compatibility
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return checkOrigin(r, allowedOrigins)
		},
	}
}

// AttachHandler returns an HTTP handler that upgrades to WebSocket and attaches to a running container's stdio.
func AttachHandler(socketPath string, authEnabled bool, allowedOrigins []string) http.HandlerFunc {
	up := newUpgrader(authEnabled, allowedOrigins)
	return func(w http.ResponseWriter, r *http.Request) {
		containerID := r.URL.Query().Get("id")
		if containerID == "" {
			http.Error(w, "missing id param", http.StatusBadRequest)
			return
		}
		if !validDockerID(containerID) {
			http.Error(w, "invalid container id", http.StatusBadRequest)
			return
		}

		raw := fmt.Sprintf("POST /containers/%s/attach?stdin=1&stdout=1&stderr=1&stream=1 HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n", containerID)
		serveBridge(w, r, up, socketPath, raw, "docker attach failed")
	}
}

// ExecHandler returns an HTTP handler that upgrades to WebSocket and starts an exec session in a container.
func ExecHandler(socketPath string, authEnabled bool, allowedOrigins []string) http.HandlerFunc {
	up := newUpgrader(authEnabled, allowedOrigins)
	return func(w http.ResponseWriter, r *http.Request) {
		execID := r.URL.Query().Get("id")
		if execID == "" {
			http.Error(w, "missing id param", http.StatusBadRequest)
			return
		}
		if !validDockerID(execID) {
			http.Error(w, "invalid exec id", http.StatusBadRequest)
			return
		}

		body := `{"Tty":true,"Detach":false}`
		raw := fmt.Sprintf("POST /exec/%s/start HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", execID, len(body), body)
		serveBridge(w, r, up, socketPath, raw, "docker exec start failed")
	}
}

// serveBridge upgrades the connection, enforces the concurrency cap, dials
// Docker with bounded timeouts, performs the hijack handshake under a read
// deadline, and bridges the two conns with DoS protections.
func serveBridge(w http.ResponseWriter, r *http.Request, up websocket.Upgrader, socketPath, raw, hsErrMsg string) {
	// Concurrency cap, checked before allocating the upgrade.
	if n := atomic.AddInt64(&activeSessions, 1); n > maxConcurrentSessions {
		atomic.AddInt64(&activeSessions, -1)
		http.Error(w, "too many sessions", http.StatusServiceUnavailable)
		return
	}
	defer atomic.AddInt64(&activeSessions, -1)

	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	metrics.WSClientsConnected.Inc()
	defer func() {
		metrics.WSClientsConnected.Dec()
		dbgClose(ws.Close(), "ws")
	}()

	docker, err := net.DialTimeout("unix", socketPath, dockerDialTimeout)
	if err != nil {
		dbgClose(ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker connect failed")), "ws")
		return
	}
	defer func() { dbgClose(docker.Close(), "docker") }()

	// Propagate client disconnect: cancelling r.Context() closes the docker conn.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		<-ctx.Done()
		dbgClose(docker.Close(), "docker")
	}()

	if err := docker.SetWriteDeadline(time.Now().Add(dockerHandshakeTimeout)); err != nil {
		dbgClose(ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker write failed")), "ws")
		return
	}
	if _, err := docker.Write([]byte(raw)); err != nil {
		dbgClose(ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker write failed")), "ws")
		return
	}

	// Bound the handshake response read.
	if err := docker.SetReadDeadline(time.Now().Add(dockerHandshakeTimeout)); err != nil {
		dbgClose(ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, hsErrMsg)), "ws")
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(docker), nil) //nolint:bodyclose // hijacked connection, not a standard HTTP response
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		dbgClose(ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, hsErrMsg)), "ws")
		return
	}
	// Clear handshake deadlines now that the stream is established; the bridge
	// manages its own liveness via ws pings and the session timers.
	dbgClose(docker.SetReadDeadline(time.Time{}), "docker")
	dbgClose(docker.SetWriteDeadline(time.Time{}), "docker")

	bridge(ctx, ws, docker)
}

func bridge(ctx context.Context, ws *websocket.Conn, docker net.Conn) {
	// DoS bounds on the WebSocket side: cap frame size + require periodic pongs.
	ws.SetReadLimit(maxFrameBytes)
	resetReadDeadline := func() {
		dbgClose(ws.SetReadDeadline(time.Now().Add(pongWait)), "ws")
	}
	resetReadDeadline()
	ws.SetPongHandler(func(string) error {
		resetReadDeadline()
		return nil
	})

	// gorilla/websocket writes are not concurrency-safe; serialize control
	// (ping) and data (binary) frames behind one mutex.
	var wsMu sync.Mutex
	wsWrite := func(mt int, data []byte) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		return ws.WriteMessage(mt, data)
	}

	idle := time.NewTimer(idleTimeout)
	absolute := time.NewTimer(absoluteTimeout)
	defer idle.Stop()
	defer absolute.Stop()

	done := make(chan struct{})
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			dbgClose(ws.Close(), "ws")
			dbgClose(docker.Close(), "docker")
			close(done)
		})
	}

	// Keepalive + timeout supervisor: pings the client, and tears the whole
	// session down on client disconnect, idle, or absolute deadline.
	go func() {
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				closeBoth()
				return
			case <-idle.C:
				closeBoth()
				return
			case <-absolute.C:
				closeBoth()
				return
			case <-ping.C:
				if err := wsWrite(websocket.PingMessage, nil); err != nil {
					closeBoth()
					return
				}
			}
		}
	}()

	// docker->ws: reads from Docker, writes to the WebSocket.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := docker.Read(buf)
			if n > 0 {
				if wErr := wsWrite(websocket.BinaryMessage, buf[:n]); wErr != nil {
					closeBoth()
					return
				}
			}
			if err != nil {
				closeBoth()
				return
			}
		}
	}()

	// ws->docker: reads from the WebSocket, writes to Docker. Client traffic
	// refreshes both the idle window and the pong read deadline.
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				closeBoth()
				return
			}
			idle.Reset(idleTimeout)
			resetReadDeadline()
			if _, err := docker.Write(msg); err != nil {
				closeBoth()
				return
			}
		}
	}()

	<-done
}
