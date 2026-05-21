# Eight.ly Agent

Eight.ly Agent is a lightweight Docker management daemon written in Go. It runs on remote Docker hosts and lets [Eight.ly OS](https://eight.ly) manage them from a single dashboard. Deploy the agent on any Linux machine with Docker, and EOS gains full container management, WebSocket console access, volume file browsing, and host monitoring -- all over authenticated TLS.

**992 lines of Go. Two dependencies. Zero config files.**

## Key Capabilities

| Feature | Description |
|---------|-------------|
| Docker API Proxy | Forwards authenticated requests to the local Docker socket over HTTPS |
| WebSocket Console | Interactive container attach and exec via WebSocket |
| File Browser | List, upload, download, delete, and rename files in Docker volumes |
| Host Info | Reports PCI devices, physical disks, and system metadata |
| Registration | Auto-registers with Eight.ly OS and sends 30-second heartbeats |
| Auto-TLS | Generates self-signed certificates on first start if none are provided |

## Architecture

```
                  +---------------------+
                  |   Eight.ly OS UI    |
                  |    (Dashboard)      |
                  +----------+----------+
                             |
                   HTTPS + Bearer Token
                             |
                  +----------v----------+
                  |  Eight.ly Agent     |
                  |     :9001           |
                  |                     |
                  |  +---------------+  |
                  |  | Auth (HMAC)   |  |
                  |  | TLS (auto)    |  |
                  |  | Registration  |  |
                  |  | Heartbeat     |  |
                  |  +---------------+  |
                  +----------+----------+
                             |
                        Unix Socket
                             |
                  +----------v----------+
                  |   Docker Daemon     |
                  |  /var/run/docker.sock|
                  +---------------------+
```

The agent sits between the EOS dashboard and the local Docker daemon. All communication from EOS to the agent is over HTTPS with HMAC-SHA256 Bearer token authentication. The agent communicates with Docker via the Unix socket.

## Package Layout

```
cmd/agent/main.go             128 LOC -- entry point, HTTP mux, TLS, graceful shutdown
internal/auth/jwt.go           65 LOC -- HMAC shared-secret token validation middleware
internal/browse/browse.go     143 LOC -- file browser (ls, get, put, delete, rename)
internal/config/config.go      51 LOC -- CLI flags + env var parsing
internal/hostinfo/hostinfo.go  87 LOC -- PCI device and physical disk detection
internal/proxy/docker.go       50 LOC -- reverse proxy to Docker socket
internal/register/register.go 268 LOC -- EOS registration + 30s heartbeat loop
internal/tlsutil/tls.go        75 LOC -- self-signed cert generation
internal/websocket/ws.go      125 LOC -- WebSocket attach + exec handlers
                               -----
                               992 LOC total
```

## Dependencies

| Module | Purpose |
|--------|---------|
| `github.com/gorilla/websocket` | WebSocket upgrade for container console |
| `github.com/rs/zerolog` | Structured JSON logging |

No frameworks, no ORM, no config libraries.

## Wiki Pages

**Getting Started**

- [[Installation]] -- Docker, binary, and build-from-source instructions
- [[Configuration]] -- All flags and environment variables

**Core Concepts**

- [[Authentication]] -- HMAC-SHA256 token validation
- [[Registration-and-Heartbeat]] -- How agents register with EOS and maintain connectivity
- [[TLS]] -- Auto-generated certificates and custom cert support

**Features**

- [[API-Reference]] -- Full endpoint documentation with examples
- [[File-Browser]] -- Volume file management operations
- [[WebSocket-Console]] -- Interactive container console access

**Help**

- [[Troubleshooting]] -- Common issues and debug procedures

## Links

- [Eight.ly OS](https://eight.ly)
- [Docker Hub](https://hub.docker.com/r/smashingtags/eightly-agent)
- [GitHub Container Registry](https://github.com/smashingtags/eightly-agent/pkgs/container/eightly-agent)
