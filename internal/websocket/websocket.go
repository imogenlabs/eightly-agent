// SPDX-License-Identifier: Apache-2.0

package websocket

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"regexp"

	"github.com/gorilla/websocket"
)

var dockerIDRe = regexp.MustCompile(`^[a-zA-Z0-9]{12,64}$`)

func validDockerID(id string) bool {
	return dockerIDRe.MatchString(id)
}

// CheckOrigin accepts all origins. Access control is handled by HMAC token
// authentication on the HTTP upgrade request. Browsers cannot set the
// Authorization header on WebSocket handshakes, so CSRF via cross-origin
// WS is not exploitable when authentication is enabled.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func AttachHandler(socketPath string) http.HandlerFunc {
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

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()

		docker, err := net.Dial("unix", socketPath)
		if err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker connect failed"))
			return
		}
		defer docker.Close()

		raw := fmt.Sprintf("POST /containers/%s/attach?stdin=1&stdout=1&stderr=1&stream=1 HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n", containerID)
		if _, err := docker.Write([]byte(raw)); err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker write failed"))
			return
		}

		resp, err := http.ReadResponse(bufio.NewReader(docker), nil)
		if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker attach failed"))
			return
		}

		bridge(ws, docker)
	}
}

func ExecHandler(socketPath string) http.HandlerFunc {
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

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()

		docker, err := net.Dial("unix", socketPath)
		if err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker connect failed"))
			return
		}
		defer docker.Close()

		body := `{"Tty":true,"Detach":false}`
		raw := fmt.Sprintf("POST /exec/%s/start HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", execID, len(body), body)
		if _, err := docker.Write([]byte(raw)); err != nil {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker write failed"))
			return
		}

		resp, err := http.ReadResponse(bufio.NewReader(docker), nil)
		if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
			ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "docker exec start failed"))
			return
		}

		bridge(ws, docker)
	}
}

func bridge(ws *websocket.Conn, docker net.Conn) {
	done := make(chan struct{})

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

	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				docker.Close()
				return
			}
			if _, err := docker.Write(msg); err != nil {
				return
			}
		}
	}()

	<-done
	ws.Close()
	docker.Close()
}

