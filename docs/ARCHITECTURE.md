# Architecture

Eight.ly Agent is a single Go binary (~2,050 LOC) organized into focused internal packages.

## Package Layout

```
cmd/agent/
  main.go              Entry point, subcommand dispatch, service mode detection
  service_windows.go   Windows SCM service handler (install/uninstall/start/stop/status)
  service_other.go     Linux/macOS: prints systemd instructions for service subcommands

internal/
  auth/                HMAC-SHA256 token validation middleware
  browse/              Docker volume file browser (ls, get, put, delete, rename)
  config/              CLI flag + env var parsing, platform-specific defaults, validation
  hostinfo/            Host info collection (PCI devices, disks) — platform-specific via build tags
  proxy/               Reverse proxy to Docker daemon socket
  register/            EOS registration, heartbeat loop, re-registration, server TLS config
  server/              HTTP server lifecycle, route wiring, TLS cert generation, middleware
  tlsutil/             Self-signed certificate generation with configurable SANs
  websocket/           WebSocket attach/exec handlers with Docker socket bridging
```

## Request Flow

1. Client sends HTTPS request with `Authorization: Bearer <token>`
2. `auth.Middleware` validates HMAC-SHA256 signature and claims (issuer, expiry)
3. Request is routed to the appropriate handler:
   - `/ping`, `/healthz`, `/readyz`, `/version` — no auth required
   - `/browse/*` — file browser with path containment validation
   - `/websocket/attach`, `/websocket/exec` — WebSocket upgrade with Docker ID validation
   - `/host` — host info collection
   - `/*` — Docker API pass-through via reverse proxy
4. Response headers include `Eightly-Agent` version and `Strict-Transport-Security`

## Trust Boundaries

- **Agent ↔ Docker**: Unix socket (Linux/macOS) or named pipe (Windows). Trusted — the agent has full Docker access.
- **Agent ↔ EOS Server**: HTTPS with server certificate verification by default (system trust store). Use `--server-ca` to pin a custom CA, or `--insecure-server-tls` to disable verification (development only). Authentication via enrollment token (first registration) or existing agent token (re-registration/heartbeat).
- **Agent ↔ Client**: HTTPS with self-signed or custom TLS cert. Authentication via HMAC-SHA256 Bearer token. A valid token grants root-equivalent access via the Docker socket.

## Platform Abstraction

Build tags handle platform differences:
- `config/defaults_linux.go` / `defaults_windows.go` — socket path, data directory
- `hostinfo/hostinfo_linux.go` / `hostinfo_darwin.go` / `hostinfo_windows.go` — PCI + disk detection
- `cmd/agent/service_windows.go` / `service_other.go` — service management
