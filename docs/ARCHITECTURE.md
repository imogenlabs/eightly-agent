# Architecture

Eight.ly Agent is a single Go binary (about 4,800 lines, tests excluded) organized into focused internal packages.

## Package Layout

```
cmd/agent/
  main.go              Entry point, subcommand dispatch, service mode detection
  healthcheck.go       `eightly-agent healthcheck`: in-process HTTPS check of /healthz
  service_windows.go   Windows SCM service handler (install/uninstall/start/stop/status)
  service_other.go     Linux/macOS: prints systemd instructions for service subcommands

internal/
  auth/                HMAC-SHA256 token validation middleware
  browse/              Docker volume file browser (ls, get, put, delete, rename)
  config/              CLI flag + env var parsing, platform-specific defaults, validation
  hostinfo/            Host info collection (PCI devices, disks) — platform-specific via build tags
  proxy/               Reverse proxy to Docker daemon socket
  register/            EOS registration, heartbeat loop, token renewal, TOFU pin, state file checks
  server/              HTTP server lifecycle, route wiring, middleware, registration gate, health check
  tlsutil/             Self-signed certificate generation with configurable SANs
  websocket/           WebSocket attach/exec handlers with Docker socket bridging
  winacl/              Windows access lists for the data folder, state, secret and TLS files (no-op elsewhere)
```

## Request Flow

1. Client sends HTTPS request with `Authorization: Bearer <token>`
2. When a secret and a server URL are set, authenticated routes answer `503` with `Retry-After: 30` until the agent has registered and knows its own ID
3. `auth.Middleware` validates the HMAC-SHA256 signature and the claims: issuer `eightly-server`, type `agent`, `iat` and `exp` no more than 30 days apart, not expired, and `sub` equal to this agent's ID
4. Request is routed to the appropriate handler:
   - `/ping`, `/healthz`, `/readyz`, `/version` — no auth required
   - `/browse/*` — file browser confined to the volume directory; hidden paths and non-regular files refused
   - `/websocket/attach`, `/websocket/exec` — WebSocket upgrade with Docker ID validation
   - `/host` — host info summary (commands time out after 15 s; result cached 30 s)
   - `/*` — Docker API pass-through via reverse proxy, behind a filter that refuses requests that would reach the host
5. Response headers include `Eightly-Agent` (version), `Eightly-Agent-API-Version`, `Strict-Transport-Security`, `X-Content-Type-Options` and `X-Frame-Options`

## Trust Boundaries

- **Agent ↔ Docker**: Unix socket (Linux/macOS) or named pipe (Windows). Trusted — the agent has full Docker access.
- **Agent ↔ EOS Server**: HTTPS with server certificate verification by default (system trust store). Use `--server-ca` to pin a custom CA, or `--insecure-server-tls` to disable verification (development only). Authentication via enrollment token (first registration) or existing agent token (re-registration/heartbeat).
- **Agent ↔ Client**: HTTPS with self-signed or custom TLS cert. Authentication via HMAC-SHA256 Bearer token. A valid token grants root-equivalent access via the Docker socket.

## Platform Abstraction

Build tags handle platform differences:
- `config/defaults_other.go` / `defaults_windows.go` — socket path, data directory, state file (and on Windows the service's secret file)
- `hostinfo/hostinfo_linux.go` / `hostinfo_darwin.go` / `hostinfo_windows.go` — PCI + disk detection
- `cmd/agent/service_windows.go` / `service_other.go` — service management
- `winacl/winacl_windows.go` / `winacl_other.go` — file access lists
- `browse/nofollow_unix.go` / `nofollow_windows.go` — no-follow opens
