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

The WebSocket endpoint uses the standard HTTP upgrade mechanism. The agent uses gorilla/websocket with `CheckOrigin` set to accept all origins. The connection URL scheme is `wss://` since the agent always runs over TLS.

```
wss://agent-host:9001/websocket/attach?id=<container-id>
wss://agent-host:9001/websocket/exec?id=<exec-id>
```

### Message Format

- **Client to agent**: Any WebSocket message type. The raw message bytes are forwarded to Docker's stdin.
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

The bridge terminates when either side closes:

- **Client closes WebSocket**: The agent's read loop gets an error, closes the Docker connection, and the bridge ends.
- **Docker closes stream**: The agent's Docker read loop gets an error (EOF), the `done` channel closes, and the bridge function returns. The WebSocket connection is then closed via `defer ws.Close()`.
- **Container exits**: Docker closes the stream, triggering the same flow as above.

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

- **Authentication**: Both WebSocket endpoints require a valid Bearer token (see [[Authentication]]). The token is validated during the initial HTTP request before the WebSocket upgrade occurs.
- **Origin checking**: The WebSocket upgrader accepts all origins (`CheckOrigin` returns `true`). Access control is handled entirely by the HMAC token.
- **No rate limiting**: There is no limit on the number of concurrent WebSocket sessions. Each session holds open a Docker connection and a goroutine pair.
- **No audit logging**: WebSocket sessions are not logged beyond the initial HTTP request. Commands entered in the terminal are not recorded by the agent.
- **Timeout**: The HTTP server's `WriteTimeout` is 30 minutes, which applies to the WebSocket connection. Sessions idle for more than 30 minutes may be terminated by the server.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Missing `id` parameter | Returns `400 Bad Request` with "missing id param" (before WebSocket upgrade) |
| WebSocket upgrade fails | Connection is dropped silently |
| Cannot connect to Docker socket | Sends WebSocket close frame with code 1011 and reason "docker connect failed" |
| Docker attach/exec fails | Sends WebSocket close frame with code 1011 and reason "docker attach failed" or "docker exec start failed" |
| Container does not exist | Docker returns an error during attach/exec, resulting in a 1011 close frame |

## See Also

- [[API-Reference]] -- Endpoint specifications
- [[Authentication]] -- Token requirements for WebSocket access
- [[Troubleshooting]] -- WebSocket connection issues
