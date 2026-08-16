# Configuration

Eight.ly Agent is configured entirely through command-line flags and environment variables. There are no config files. Every flag has an equivalent environment variable, and **environment variables take precedence** over flags when both are set.

## Reference Table

| Flag | Env Var | Default | Category | Description |
|------|---------|---------|----------|-------------|
| `--addr` | -- | `0.0.0.0` | Network | Listen address |
| `--port` | `AGENT_PORT` | `9001` | Network | Listen port |
| `--socket-path` | `DOCKER_SOCKET` | `/var/run/docker.sock` | Network | Path to the Docker daemon Unix socket |
| `--secret` | `EIGHTLY_AGENT_SECRET` | (empty) | Authentication | HMAC shared secret for token validation. **Required** unless `--insecure-no-auth` is set. |
| `--server-url` | `EIGHTLY_SERVER_URL` | (empty) | Registration | EOS server URL for registration and heartbeat. When empty, registration is skipped. |
| `--enroll-token` | `EIGHTLY_ENROLL_TOKEN` | (empty) | Registration | One-time enrollment token for first-time registration |
| `--state-path` | `EIGHTLY_STATE_PATH` | `/data/eightly-agent.json` | Storage | Path to the JSON file that stores registration state |
| `--data-path` | `DATA_PATH` | `/data` | Storage | Root data directory. TLS certificates are stored here, and the file browser resolves volume paths relative to this directory. |
| `--metrics` | `EIGHTLY_METRICS_ENABLED` | `false` | Monitoring | Enable Prometheus `/metrics` endpoint |
| `--allowed-origins` | `EIGHTLY_ALLOWED_ORIGINS` | (empty) | Security | Comma-separated WebSocket origin allowlist |
| `--advertise-ip` | `EIGHTLY_ADVERTISE_IP` | (auto-detected) | Network | Override the detected local IP sent during registration |
| `--tls-san` | `EIGHTLY_TLS_SANS` | (empty) | TLS | Extra SANs (DNS names or IPs) to include in auto-generated TLS certificates |
| `--insecure-server-tls` | `EIGHTLY_INSECURE_SERVER_TLS` | `false` | TLS | Skip TLS verification for outbound connections to EOS. **Required** for self-signed EOS servers. Fail-closed by default. |
| `--insecure-no-auth` | `EIGHTLY_INSECURE_NO_AUTH` | `false` | Authentication | Start without a shared secret. Dev only -- never use in production. |
| `--log-level` | `LOG_LEVEL` | `info` | Logging | Log verbosity: `trace`, `debug`, `info`, `warn`, `error` |

## Platform-Specific Defaults

The agent detects the host OS at build time and applies appropriate defaults. Flags and environment variables override these defaults on any platform.

### Linux / macOS

| Setting | Default |
|---------|---------|
| Docker socket | `/var/run/docker.sock` |
| Data path | `/data` |
| State path | `/data/eightly-agent.json` |

### Windows

| Setting | Default |
|---------|---------|
| Docker socket | `//./pipe/docker_engine` |
| Data path | `C:\ProgramData\eightly-agent` |
| State path | `C:\ProgramData\eightly-agent\state.json` |

These defaults are defined in `internal/config/defaults_linux.go`, `internal/config/defaults_darwin.go`, and `internal/config/defaults_windows.go` using Go build tags. You never need to set `--socket-path`, `--data-path`, or `--state-path` on any platform unless you want non-standard paths.

## Categories

### Network

**`--addr`** (no env var override)

The IP address the agent binds to. Default `0.0.0.0` listens on all interfaces. Set to `127.0.0.1` to restrict to localhost only (useful for testing or when behind a reverse proxy).

**`--port` / `AGENT_PORT`**

TCP port for the HTTPS listener. Default `9001`. The agent always serves over TLS; there is no plaintext HTTP mode.

**`--socket-path` / `DOCKER_SOCKET`**

Path to the Docker daemon Unix socket. The agent proxies Docker API requests and WebSocket console sessions through this socket. Change this if your Docker daemon is configured to use a non-standard socket path.

### Authentication

**`--secret` / `EIGHTLY_AGENT_SECRET`**

The HMAC-SHA256 shared secret used to validate Bearer tokens on incoming requests. This must match the secret configured in your EOS instance. See [[Authentication]] for the full token format and validation flow.

The agent **refuses to start** without this value unless `--insecure-no-auth` is explicitly set (fail-closed default). This prevents accidental deployment without authentication.

**`--insecure-no-auth` / `EIGHTLY_INSECURE_NO_AUTH`**

When set, allows the agent to start without a shared secret. Authentication is bypassed entirely -- every request is accepted without a token. **Dev/testing only. Never use in production.**

### Registration

**`--server-url` / `EIGHTLY_SERVER_URL`**

The URL of the EOS server this agent should register with. Example: `https://your-eos-server:7001` or `https://os.eight.ly`. When set, the agent will attempt to register on startup and begin sending heartbeats every 30 seconds. When empty, registration and heartbeat are both disabled. See [[Registration-and-Heartbeat]] for details.

**`--enroll-token` / `EIGHTLY_ENROLL_TOKEN`**

A one-time token provided by the EOS admin to authorize the first registration. This is sent in the `Authorization` header of the registration POST and in the request body. After successful registration, this token is no longer needed -- the agent receives a persistent token from EOS and stores it in the state file.

### Storage

**`--state-path` / `EIGHTLY_STATE_PATH`**

Path to the JSON file where the agent stores its registration state (agent ID, server-assigned token, server URL, registration timestamp). This file is written atomically (write to `.tmp`, then rename). See [[Registration-and-Heartbeat]] for the file format.

**`--data-path` / `DATA_PATH`**

Root directory for agent data. Two things live here:

1. TLS certificates: `<data-path>/cert.pem` and `<data-path>/key.pem` (auto-generated if missing). See [[TLS]].
2. Volume file browser root: the file browser resolves paths as `<data-path>/volumes/<volumeID>/_data/<path>`. See [[File-Browser]].

### Logging

**`--log-level` / `LOG_LEVEL`**

Controls log verbosity using zerolog levels:

| Level | What it logs |
|-------|-------------|
| `trace` | Everything including internal state details |
| `debug` | Heartbeat confirmations, request details, startup config. Enables human-readable console output. |
| `info` | Startup, registration, shutdown events (default) |
| `warn` | Heartbeat failures, registration retries |
| `error` | Fatal-adjacent errors (state save failures, re-registration failures) |

At `debug` or `trace` level, the logger switches from JSON to a human-readable console format with timestamps.

### Monitoring

**`--metrics` / `EIGHTLY_METRICS_ENABLED`**

When set to `true`, exposes a Prometheus-compatible `/metrics` endpoint. This endpoint requires authentication (Bearer token). Metrics include heartbeat counters, registration attempts, and standard Go runtime metrics. Disabled by default.

### Security

**`--allowed-origins` / `EIGHTLY_ALLOWED_ORIGINS`**

Comma-separated list of allowed origins for WebSocket upgrade requests. When set, only connections with a matching `Origin` header are accepted. When empty (default), all origins are allowed.

Example: `--allowed-origins "https://os.eight.ly,https://os-dev.eight.ly"`

### TLS (Outbound)

**`--insecure-server-tls` / `EIGHTLY_INSECURE_SERVER_TLS`**

Outbound TLS to EOS is **fail-closed by default** -- the agent verifies the server certificate against the system trust store. If your EOS server uses a self-signed or internal CA certificate, you must either provide the CA with `--server-ca` or explicitly opt in to skipping verification with `--insecure-server-tls`. This flag is required for self-signed EOS servers.

**`--tls-san` / `EIGHTLY_TLS_SANS`**

Extra Subject Alternative Names to include in auto-generated TLS certificates. Comma-separated list of DNS names or IP addresses. Useful when the agent is accessed by hostname or LAN IP in addition to localhost.

Example: `--tls-san "docker-host-01.local,192.168.1.50"`

**`--advertise-ip` / `EIGHTLY_ADVERTISE_IP`**

Override the auto-detected local IP that the agent sends to EOS during registration. Useful when the default-route IP detection picks the wrong interface (e.g., VPN or Docker bridge).

## Example Configurations

### Development (no auth, no registration)

```bash
./eightly-agent \
  --insecure-no-auth \
  --log-level debug \
  --data-path ./data \
  --state-path ./data/agent.json
```

Or with Docker:

```bash
docker run -d \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v $(pwd)/data:/data \
  -p 9001:9001 \
  -e LOG_LEVEL=debug \
  -e EIGHTLY_INSECURE_NO_AUTH=true \
  ghcr.io/imogenlabs/eightly-agent:v1.0.0
```

Authentication is disabled (`--insecure-no-auth`), registration is skipped (no server URL). All endpoints are open. Useful for testing the Docker proxy and file browser locally.

### Single Host

```bash
docker run -d \
  --name eightly-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v agent-data:/data \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="a-strong-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:7001" \
  -e EIGHTLY_ENROLL_TOKEN="token-from-eos-admin" \
  ghcr.io/imogenlabs/eightly-agent:v1.0.0
```

Standard deployment for a single remote Docker host managed by EOS.

### Multi-Host Fleet

Deploy the same image to each host with the same shared secret but unique enrollment tokens:

```bash
# Host A (192.168.1.50)
docker run -d --name eightly-agent --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock -v agent-data:/data -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="fleet-secret" \
  -e EIGHTLY_SERVER_URL="https://eos.local:7001" \
  -e EIGHTLY_ENROLL_TOKEN="token-host-a" \
  ghcr.io/imogenlabs/eightly-agent:v1.0.0

# Host B (192.168.1.51)
docker run -d --name eightly-agent --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock -v agent-data:/data -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="fleet-secret" \
  -e EIGHTLY_SERVER_URL="https://eos.local:7001" \
  -e EIGHTLY_ENROLL_TOKEN="token-host-b" \
  ghcr.io/imogenlabs/eightly-agent:v1.0.0
```

All agents use the same shared secret so EOS can authenticate to any of them. Each gets its own enrollment token for initial registration. After enrollment, the token can be removed from the environment.

### Custom TLS Certificates

```bash
docker run -d --name eightly-agent --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /etc/eightly-agent/certs/cert.pem:/data/cert.pem:ro \
  -v /etc/eightly-agent/certs/key.pem:/data/key.pem:ro \
  -v agent-data:/data \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-secret" \
  ghcr.io/imogenlabs/eightly-agent:v1.0.0
```

The agent checks for existing `cert.pem` and `key.pem` in the data directory before generating self-signed certs. See [[TLS]] for details.

## See Also

- [[Installation]] -- Setup instructions
- [[Authentication]] -- Token format and validation
- [[Registration-and-Heartbeat]] -- How registration uses `--server-url` and `--enroll-token`
- [[TLS]] -- Certificate generation and custom certs
- [[Troubleshooting]] -- Debug logging and common config issues
