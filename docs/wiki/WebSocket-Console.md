---
description: "Attach to a running container or exec a command in one over Eight.ly Agent's WebSocket console."
---

# WebSocket Console

The WebSocket console provides interactive terminal access to Docker containers through the agent. It supports two modes: **attach** (connect to a running container's existing streams) and **exec** (start a new command inside a container).

## Modes

### Attach Mode

**Endpoint**: `GET /websocket/attach?id=<container-id>`

Attaches to a running container's stdin, stdout, and stderr streams. This is equivalent to `docker attach` -- you see the container's main process output and can send input to it.

The agent:

1. Upgrades the HTTP connection to a WebSocket.
2. Opens a raw TCP connection to the Docker daemon Unix socket.
3. Sends a raw HTTP request to Docker:
   ```
   POST /containers/<id>/attach?stdin=1&stdout=1&stderr=1&stream=1 HTTP/1.1
   Host: docker
   Connection: Upgrade
   Upgrade: tcp
   ```
4. Waits for Docker to respond with `101 Switching Protocols`.
5. Bridges the WebSocket and Docker TCP streams bidirectionally.

Use attach mode when you want to interact with the container's main process (e.g., a shell that was started as the container's entrypoint).

### Exec Mode

**Endpoint**: `GET /websocket/exec?id=<exec-id>`

Starts a previously created exec instance and streams its I/O. This is the equivalent of `docker exec -it <container> /bin/bash` split into two steps.

The agent:

1. Upgrades the HTTP connection to a WebSocket.
2. Opens a raw TCP connection to the Docker daemon Unix socket.
3. Sends a raw HTTP request to Docker:
   ```
   POST /exec/<id>/start HTTP/1.1
   Host: docker
   Connection: Upgrade
   Upgrade: tcp
   Content-Type: application/json
   Content-Length: 27

   {"Tty":true,"Detach":false}
   ```
4. Waits for Docker to respond with `101 Switching Protocols`.
5. Bridges the WebSocket and Docker TCP streams bidirectionally.

**Important**: The exec instance must be created first via the Docker API proxy. The typical flow is:

1. Create an exec instance:
   ```bash
   curl -k -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"Cmd":["/bin/bash"],"AttachStdin":true,"AttachStdout":true,"AttachStderr":true,"Tty":true}' \
     https://localhost:9001/containers/<container-id>/exec
   ```
   Response: `{"Id":"exec-instance-id"}`

2. Connect a WebSocket to `/websocket/exec?id=<exec-instance-id>`.

Exec mode always starts with `Tty: true` and `Detach: false`, meaning the session is interactive with a pseudo-TTY allocated.

## WebSocket Protocol

### Connection

The WebSocket endpoint uses the standard HTTP upgrade mechanism (gorilla/websocket). The `Origin` header is checked on every upgrade (see [Security Model](#security-model)). The connection URL scheme is `wss://` since the agent always runs over TLS.

```
wss://agent-host:9001/websocket/attach?id=<container-id>
wss://agent-host:9001/websocket/exec?id=<exec-id>
```

### Message Format

- **Client to agent**: Any WebSocket message type. The raw message bytes are forwarded to Docker's stdin. A single message larger than 64 KB closes the session.
- **Agent to client**: Binary WebSocket messages. Each message contains raw terminal output bytes from Docker's stdout/stderr.

There is no framing protocol, escape sequences, or multiplexing. The WebSocket carries raw terminal bytes in both directions.

### Buffer Size

The agent reads from Docker in 32 KB chunks (`make([]byte, 32*1024)`). Data is forwarded to the WebSocket as soon as it is read -- there is no buffering or batching.

### Connection Lifecycle

```
Client                    Agent                     Docker
  |                         |                         |
  |-- WS Upgrade ---------->|                         |
  |                         |-- TCP connect --------->|
  |                         |-- HTTP attach/exec ---->|
  |                         |<-- 101 Switching -------|
  |<-- WS Accepted ---------|                         |
  |                         |                         |
  |== Bidirectional bridge ===========================|
  |                         |                         |
  |-- WS message (stdin) -->|-- write to Docker ----->|
  |                         |                         |
  |                         |<-- read from Docker ----|
  |<-- WS binary (stdout) --|                         |
  |                         |                         |
  |-- WS close ------------>|-- close Docker conn --->|
  |                         |                         |
```

### Termination

The bridge closes both the WebSocket and the Docker connection when any of these happens:

- **Client closes the WebSocket** or the request is cancelled.
- **Docker closes the stream**, for example because the container or the exec'd process exited.
- **No pong for 60 seconds.** The agent pings the client every 30 seconds; a pong, or any client message, resets the 60-second read deadline.
- **Idle for 15 minutes.** No message from the client in that time. Output from the container does not count as activity.
- **8 hours since the session started**, whatever the activity.
- **A write to the client takes longer than 10 seconds.** Every write (terminal output and pings) has a 10-second deadline, so a client that stops reading cannot hold a session open. The idle and 8-hour limits are watched separately from the writes, so they fire even while a write is stuck.

Before the bridge starts, connecting to the Docker socket is bounded at 5 seconds and Docker's `101 Switching Protocols` reply at 10 seconds.

## Connecting from a Browser

Use the standard WebSocket API:

```javascript
const ws = new WebSocket('wss://agent-host:9001/websocket/attach?id=abc123');

ws.binaryType = 'arraybuffer';

ws.onmessage = function(event) {
  // event.data is an ArrayBuffer containing terminal output
  const text = new TextDecoder().decode(event.data);
  terminal.write(text);  // e.g., write to xterm.js
};

// Send keystrokes
terminal.onData(function(data) {
  ws.send(data);
});
```

For a full terminal experience, use [xterm.js](https://xtermjs.org/) or a similar terminal emulator library. The EOS dashboard uses xterm.js with a WebSocket addon.

### Terminal Sizing

The exec mode creates sessions with `Tty: true`, but the agent does not send resize signals automatically. To resize the terminal, send a resize request via the Docker API proxy:

```bash
curl -k -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"h": 24, "w": 80}' \
  "https://localhost:9001/exec/<exec-id>/resize"
```

For attach mode, resize the container's TTY:

```bash
curl -k -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"h": 24, "w": 80}' \
  "https://localhost:9001/containers/<container-id>/resize"
```

## Security Model

- **Authentication**: Both WebSocket endpoints require a valid Bearer token (see [Authentication](Authentication.md)). The token is validated during the initial HTTP request before the WebSocket upgrade occurs.
- **Origin checking**: Checked on every upgrade, whether or not authentication is enabled, to stop cross-site WebSocket hijacking. With `--allowed-origins` (`EIGHTLY_ALLOWED_ORIGINS`, comma-separated) the `Origin` must exactly match one entry (scheme, host and port). Without it, the `Origin` must be `http` or `https` and match the request's host (the TLS server name, or the `Host` header), with default ports treated as equal. A request with no `Origin` is accepted only if it carries a Bearer token (a server-to-agent call, not a browser).
- **Session limit**: At most 50 WebSocket sessions run at once across attach and exec. Further requests get `503 Service Unavailable` "too many sessions" before the upgrade.
- **Timeouts**: See [Termination](#termination). The HTTP server's `WriteTimeout` does not apply once the connection is upgraded; the session limits above do.
- **No audit logging**: WebSocket sessions are not logged beyond the initial HTTP request. Commands entered in the terminal are not recorded by the agent.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Missing `id` parameter | Returns `400 Bad Request` with "missing id param" (before WebSocket upgrade) |
| `id` is not 12-64 letters and digits | Returns `400 Bad Request` with "invalid container id" or "invalid exec id" |
| 50 sessions already open | Returns `503 Service Unavailable` with "too many sessions" |
| `Origin` not allowed | Upgrade refused with `403 Forbidden` |
| WebSocket upgrade fails | Connection is dropped silently |
| Cannot connect to Docker socket (5 s) | Sends WebSocket close frame with code 1011 and reason "docker connect failed" |
| Writing the request to Docker fails | Sends WebSocket close frame with code 1011 and reason "docker write failed" |
| Docker attach/exec fails | Sends WebSocket close frame with code 1011 and reason "docker attach failed" or "docker exec start failed" |
| Container does not exist | Docker returns an error during attach/exec, resulting in a 1011 close frame |

## See Also

- [API Reference](API-Reference.md) -- Endpoint specifications
- [Authentication](Authentication.md) -- Token requirements for WebSocket access
- [Troubleshooting](Troubleshooting.md) -- WebSocket connection issues
