// SPDX-License-Identifier: Apache-2.0

package websocket

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/smashingtags/eightly-agent/internal/metrics"
)

var dockerIDRe = regexp.MustCompile(`^[a-zA-Z0-9]{12,64}$`)

func validDockerID(id string) bool {
	return dockerIDRe.MatchString(id)
}

func newUpgrader(authEnabled bool, allowedOrigins []string) websocket.Upgrader {
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if len(allowedOrigins) > 0 {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				for _, allowed := range allowedOrigins {
					if origin == allowed {
						return true
					}
				}
				return false
			}
			if authEnabled {
				return true
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			host := r.Host
			if host == "" {
				host = r.Header.Get("Host")
			}
			trimmed := origin
			trimmed = strings.TrimPrefix(trimmed, "https://")
			trimmed = strings.TrimPrefix(trimmed, "http://")
			return trimmed == host
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

		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		metrics.WSClientsConnected.Inc()
		defer func() {
			metrics.WSClientsConnected.Dec()
			ws.Close()
		}()

		docker, err := net.Dial("unix", socketPath)
		if err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker connect failed")) // #nosec G104 -- best-effort close message
			return
		}
		defer docker.Close()

		raw := fmt.Sprintf("POST /containers/%s/attach?stdin=1&stdout=1&stderr=1&stream=1 HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n", containerID)
		if _, err := docker.Write([]byte(raw)); err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker write failed")) // #nosec G104 -- best-effort close message
			return
		}

		resp, err := http.ReadResponse(bufio.NewReader(docker), nil) //nolint:bodyclose // hijacked connection, not a standard HTTP response
		if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker attach failed")) // #nosec G104 -- best-effort close message
			return
		}

		bridge(ws, docker)
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

		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		metrics.WSClientsConnected.Inc()
		defer func() {
			metrics.WSClientsConnected.Dec()
			ws.Close()
		}()

		docker, err := net.Dial("unix", socketPath)
		if err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker connect failed")) // #nosec G104 -- best-effort close message
			return
		}
		defer docker.Close()

		body := `{"Tty":true,"Detach":false}`
		raw := fmt.Sprintf("POST /exec/%s/start HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", execID, len(body), body)
		if _, err := docker.Write([]byte(raw)); err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker write failed")) // #nosec G104 -- best-effort close message
			return
		}

		resp, err := http.ReadResponse(bufio.NewReader(docker), nil) //nolint:bodyclose // hijacked connection, not a standard HTTP response
		if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker exec start failed")) // #nosec G104 -- best-effort close message
			return
		}

		bridge(ws, docker)
	}
}

func bridge(ws *websocket.Conn, docker net.Conn) {
	done := make(chan struct{})

	// docker->ws: reads from the Docker connection and writes to the WebSocket.
	// Exits when docker.Read returns an error (connection closed by peer or by
	// the ws->docker goroutine calling docker.Close). Signals completion via done.
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := docker.Read(buf)
			if n > 0 {
				if wErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); wErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// ws->docker: reads from the WebSocket and writes to the Docker connection.
	// Exits when ws.ReadMessage returns an error (client disconnect or ws.Close
	// called below). Calls docker.Close to unblock the docker->ws goroutine.
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				docker.Close() // #nosec G104 -- best-effort cleanup to unblock reader goroutine
				return
			}
			if _, err := docker.Write(msg); err != nil {
				return
			}
		}
	}()

	<-done
	ws.Close()    // #nosec G104 -- best-effort cleanup, connection is already closing
	docker.Close() // #nosec G104 -- best-effort cleanup, connection is already closing
}

