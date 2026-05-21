# Eight.ly Agent

Eight.ly Agent is a lightweight, cross-platform Docker management daemon written in Go. It runs on remote Docker hosts (Linux, macOS, and Windows) and lets [Eight.ly OS](https://eight.ly) manage them from a single dashboard. Deploy the agent on any machine with Docker, and EOS gains full container management, WebSocket console access, volume file browsing, and host monitoring -- all over authenticated TLS.

**~1,400 lines of Go. Two dependencies. Zero config files.**

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
                     Unix Socket / Named Pipe
                             |
                  +----------v----------+
                  |   Docker Daemon     |
                  |  /var/run/docker.sock|
                  |  (or named pipe on  |
                  |   Windows)          |
                  +---------------------+
```

The agent sits between the EOS dashboard and the local Docker daemon. All communication from EOS to the agent is over HTTPS with HMAC-SHA256 Bearer token authentication. The agent communicates with Docker via the Unix socket on Linux/macOS or the named pipe (`//./pipe/docker_engine`) on Windows.

## Package Layout

```
cmd/agent/main.go                  128 LOC -- entry point, HTTP mux, TLS, graceful shutdown
cmd/agent/service_linux.go          -- Linux service (systemd) integration
cmd/agent/service_darwin.go         -- macOS service (launchd) integration
cmd/agent/service_windows.go        -- Windows service (SCM) integration
internal/auth/jwt.go                65 LOC -- HMAC shared-secret token validation middleware
internal/browse/browse.go          143 LOC -- file browser (ls, get, put, delete, rename)
internal/config/config.go           51 LOC -- CLI flags + env var parsing
internal/config/defaults_linux.go   -- Linux platform defaults
internal/config/defaults_darwin.go  -- macOS platform defaults
internal/config/defaults_windows.go -- Windows platform defaults
internal/hostinfo/hostinfo.go       87 LOC -- PCI device and physical disk detection
internal/proxy/docker.go            50 LOC -- reverse proxy to Docker socket
internal/register/register.go      268 LOC -- EOS registration + 30s heartbeat loop
internal/server/server.go           -- HTTP server lifecycle (used by service wrappers)
internal/tlsutil/tls.go             75 LOC -- self-signed cert generation
internal/websocket/ws.go           125 LOC -- WebSocket attach + exec handlers
                                   -----
                                  ~1,400 LOC total
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
