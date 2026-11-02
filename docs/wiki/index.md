---
description: "Eight.ly Agent is a small Go daemon that lets Eight.ly OS manage the Docker hosts it runs on, over authenticated TLS."
---

# Eight.ly Agent

Eight.ly Agent is a lightweight, cross-platform Docker management daemon written in Go. It runs on remote Docker hosts (Linux, macOS, and Windows) and lets [Eight.ly OS](https://eight.ly) manage them from a single dashboard. Deploy the agent on any machine with Docker, and EOS gains full container management, WebSocket console access, volume file browsing, and host monitoring -- all over authenticated TLS.

**One Go binary. Apache 2.0 license. Hardened through 6 rounds of security review.** The newest release is on the [Releases](https://github.com/imogenlabs/eightly-agent/releases) page.

![Remote Environments — connected agents with heartbeat status](https://imogenlabs.ai/images/eightly/agent/agent-environments.webp)

| | |
|---|---|
| ![Container list on local host](https://imogenlabs.ai/images/eightly/agent/agent-containers-local.webp) | ![Containers on remote agent host](https://imogenlabs.ai/images/eightly/agent/agent-containers-remote.webp) |

## Key Capabilities

| Feature | Description |
|---------|-------------|
| Docker API Proxy | Forwards authenticated requests to the local Docker socket over HTTPS, refusing requests that would reach the host |
| WebSocket Console | Interactive container attach and exec via WebSocket |
| File Browser | List, upload, download, delete, and rename files in Docker volumes |
| Host Info | Reports PCI devices, physical disks, and system metadata |
| Registration | Auto-registers with Eight.ly OS and sends 30-second heartbeats |
| Auto-TLS | Generates self-signed certificates on first start if none are provided |
| Prometheus Metrics | Optional `/metrics` endpoint for monitoring (behind `--metrics` flag) |

## Architecture

```mermaid
flowchart TB
    subgraph clients["Clients"]
        EOS["Eight.ly OS Dashboard"]
        CLI["curl / SDK / Mobile"]
    end

    subgraph agent["Eight.ly Agent :9001"]
        direction TB
        TLS["TLS Termination"]
        AUTH["HMAC-SHA256 Auth"]
        subgraph endpoints["Endpoints"]
            direction LR
            HEALTH["/ping /healthz /readyz /version"]
            METRICS["/metrics"]
            PROXY["Docker API Proxy"]
            WS["WebSocket Console"]
            BROWSE["File Browser"]
            HOST["Host Info"]
        end
        REG["Registration + Heartbeat"]
    end

    subgraph docker["Docker Engine"]
        SOCK["Unix Socket / Named Pipe"]
    end

    clients -->|"HTTPS + Bearer Token"| TLS
    TLS --> AUTH
    AUTH --> endpoints
    PROXY --> SOCK
    WS -->|"WebSocket"| SOCK
    BROWSE --> SOCK
    REG -->|"HTTPS"| EOS
```

The agent sits between the EOS dashboard and the local Docker daemon. All communication from EOS to the agent is over HTTPS with HMAC-SHA256 Bearer token authentication. The agent communicates with Docker via the Unix socket on Linux/macOS or the named pipe (`//./pipe/docker_engine`) on Windows.

## Package Layout

```
cmd/agent/
  main.go               -- entry point, subcommands, signal handling
  healthcheck.go        -- `eightly-agent healthcheck` subcommand for container health checks
  service_windows.go    -- Windows service (install/uninstall/start/stop/status)
  service_other.go      -- stubs for Linux and macOS
internal/auth/jwt.go    -- HMAC shared-secret token validation middleware
internal/browse/        -- file browser (ls, get, put, delete, rename), confined to each volume
internal/config/
  config.go             -- CLI flags + env var parsing and validation
  secretfile.go         -- --secret-file reading
  defaults_other.go     -- Linux/macOS default paths (unix socket, /data)
  defaults_windows.go   -- Windows default paths (named pipe, ProgramData)
internal/hostinfo/      -- PCI devices and physical disks (sysfs, system_profiler, PowerShell), cached 30s
internal/metrics/       -- Prometheus counters and gauges
internal/proxy/docker.go -- reverse proxy to the Docker socket and its request filter
internal/register/
  register.go           -- EOS registration + 30s heartbeat loop
  renew.go              -- token renewal from half-life
  pin.go                -- first-use pinning of the EOS server certificate
  holder.go             -- registration state shared with request handlers
  statedir.go           -- state file and directory permission checks
internal/server/
  server.go             -- HTTP mux, TLS listener, registration gate, graceful shutdown
  healthcheck.go        -- health check pinned to the agent's own certificate
internal/tlsutil/tls.go -- self-signed cert generation and reuse
internal/websocket/     -- WebSocket attach + exec handlers
internal/winacl/        -- Windows access lists for the data folder, state, secret and TLS key
```

About 4,800 lines of Go, not counting tests.

## Dependencies

| Module | Purpose |
|--------|---------|
| `github.com/gorilla/websocket` | WebSocket upgrade for container console |
| `github.com/rs/zerolog` | Structured JSON logging |
| `github.com/prometheus/client_golang` | Prometheus metrics exposition |
| `golang.org/x/sys` | Windows service management and access lists |

No frameworks, no ORM, no config libraries.

## Documentation

**Getting Started**

- [Installation](Installation.md) -- Docker, binary, and build-from-source instructions
- [Configuration](Configuration.md) -- All flags and environment variables

**Core Concepts**

- [Authentication](Authentication.md) -- HMAC-SHA256 token validation
- [Registration and Heartbeat](Registration-and-Heartbeat.md) -- How agents register with EOS and maintain connectivity
- [TLS](TLS.md) -- Auto-generated certificates and custom cert support

**Features**

- [API Reference](API-Reference.md) -- Full endpoint documentation with examples
- [File Browser](File-Browser.md) -- Volume file management operations
- [WebSocket Console](WebSocket-Console.md) -- Interactive container console access

**Help**

- [Troubleshooting](Troubleshooting.md) -- Common issues and debug procedures

## Links

- [Eight.ly OS](https://eight.ly)
- [Source on GitHub](https://github.com/imogenlabs/eightly-agent)
- [GitHub Container Registry](https://github.com/imogenlabs/eightly-agent/pkgs/container/eightly-agent)
- [Docker Hub mirror](https://hub.docker.com/r/smashingtags/eightly-agent)
- [Imogen Labs](https://imogenlabs.ai)
