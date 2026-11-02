---
description: "Every Eight.ly Agent flag and environment variable, with defaults and what each one controls."
---

# Configuration

Eight.ly Agent is configured entirely through command-line flags and environment variables. There are no config files. Almost every flag has an equivalent environment variable (`--addr`, `--insecure-server-tls` and `--version` are flag-only), and **environment variables take precedence** over flags when both are set.

## Reference Table

| Flag | Env Var | Default | Category | Description |
|------|---------|---------|----------|-------------|
| `--addr` | -- | `0.0.0.0` | Network | Listen address |
| `--port` | `AGENT_PORT` | `9001` | Network | Listen port |
| `--socket` | `DOCKER_SOCKET` | `/var/run/docker.sock` | Network | Path to the Docker daemon socket (named pipe on Windows) |
| `--secret` | `EIGHTLY_AGENT_SECRET` | (empty) | Authentication | HMAC shared secret for token validation. **Required** (here or in a secret file) unless `--insecure-no-auth` is set. |
| `--secret-file` | `EIGHTLY_AGENT_SECRET_FILE` | (empty) | Authentication | File holding the shared secret, instead of `--secret`. Setting both is refused. |
| `--server-url` | `EIGHTLY_SERVER_URL` | (empty) | Registration | EOS server URL for registration and heartbeat. Must be `https` unless `--insecure-server-tls`. When empty, registration is skipped. |
| `--enroll-token` | `EIGHTLY_ENROLL_TOKEN` | (empty) | Registration | One-time enrollment token for first-time registration |
| `--state-path` | `EIGHTLY_STATE_PATH` | `eightly-agent.json` in the data directory | Storage | JSON file that stores registration state |
| `--data` | `DATA_PATH` | `/data` | Storage | Data directory. TLS certificates are stored here, and the file browser resolves volume paths under it. |
| `--metrics` | `EIGHTLY_METRICS_ENABLED` | `false` | Monitoring | Enable the Prometheus `/metrics` endpoint |
| `--allowed-origins` | `EIGHTLY_ALLOWED_ORIGINS` | (empty) | Security | Comma-separated WebSocket origin allowlist (empty = same-origin check) |
| `--browse-read-only` | `EIGHTLY_BROWSE_READ_ONLY` | `false` | Security | Serve the file browser read-only (refuse upload, delete and rename) |
| `--advertise-ip` | `EIGHTLY_ADVERTISE_IP` | (auto-detected) | Network | Override the detected local IP sent during registration |
| `--tls-san` | `EIGHTLY_TLS_SANS` | (empty) | TLS | Extra SANs (DNS names or IPs) to include in the auto-generated TLS certificate |
| `--server-ca` | `EIGHTLY_SERVER_CA` | (empty) | TLS | CA certificate file used to verify the EOS server chain, in addition to the pinned certificate |
| `--insecure-server-tls` | -- | `false` | TLS | Skip TLS verification and pinning for outbound connections to EOS. Flag-only. Never in production. |
| `--insecure-no-auth` | `EIGHTLY_INSECURE_NO_AUTH` | `false` | Authentication | Start without a shared secret. Dev only -- never use in production. |
| `--log-level` | `LOG_LEVEL` | `info` | Logging | Log verbosity: `trace`, `debug`, `info`, `warn`, `error` |
| `--version` | -- | -- | -- | Print the version and exit |

### Boolean environment variables

`EIGHTLY_METRICS_ENABLED`, `EIGHTLY_BROWSE_READ_ONLY` and `EIGHTLY_INSECURE_NO_AUTH` accept `true`, `1`, `yes`, `on` or `false`, `0`, `no`, `off` (any case). Any other value stops the agent at startup with an error naming the variable, for example `EIGHTLY_METRICS_ENABLED="ture" is not a boolean; use true or false`. An empty or unset variable leaves the flag value alone.

## Platform-Specific Defaults

The defaults are chosen at build time. Flags and environment variables override them on any platform.

### Linux / macOS

| Setting | Default |
|---------|---------|
| Docker socket | `/var/run/docker.sock` |
| Data path | `/data` |
| State path | `eightly-agent.json` in the data path (`/data/eightly-agent.json`) |

The `.deb` and `.rpm` packages set `DATA_PATH=/var/lib/eightly-agent` and `EIGHTLY_STATE_PATH=/var/lib/eightly-agent/eightly-agent.json` in the systemd unit.

### Windows

| Setting | Default |
|---------|---------|
| Docker socket | `//./pipe/docker_engine` |
| Data path | `C:\ProgramData\eightly-agent` |
| State path | `state.json` in the data path (`C:\ProgramData\eightly-agent\state.json`) |
| Secret file (set by `eightly-agent.exe install`) | `C:\ProgramData\eightly-agent\secret` |

These defaults are defined in `internal/config/defaults_other.go` and `internal/config/defaults_windows.go`. When `--state-path` is not set, the state file goes in whatever directory `--data` names, so moving the data directory moves the state file with it.

## Categories

### Network

**`--addr`** (no env var override)

The IP address the agent binds to. Default `0.0.0.0` listens on all interfaces. Set to `127.0.0.1` to restrict to localhost only (useful for testing or when behind a reverse proxy).

**`--port` / `AGENT_PORT`**

TCP port for the HTTPS listener. Default `9001`. The agent always serves over TLS; there is no plaintext HTTP mode.

**`--socket` / `DOCKER_SOCKET`**

Path to the Docker daemon socket (a named pipe on Windows). The agent proxies Docker API requests and WebSocket console sessions through this socket. Change this if your Docker daemon is configured to use a non-standard socket path.

### Authentication

**`--secret` / `EIGHTLY_AGENT_SECRET`**

The HMAC-SHA256 shared secret used to validate Bearer tokens on incoming requests. This must match the secret configured in your EOS instance. See [Authentication](Authentication.md) for the full token format and validation flow.

The agent **refuses to start** without this value unless `--insecure-no-auth` is explicitly set (fail-closed default). This prevents accidental deployment without authentication.

**`--secret-file` / `EIGHTLY_AGENT_SECRET_FILE`**

A file holding the shared secret, read at startup instead of `--secret`. Surrounding whitespace, such as an editor's trailing newline, is ignored, and an empty file is refused. This keeps the secret out of the process environment and the command line. Setting a secret file and `--secret` or `EIGHTLY_AGENT_SECRET` together is refused, so a leftover variable cannot keep a copy of the secret around.

On Windows the agent closes the file to everyone but SYSTEM, Administrators and its own account, and refuses it if that does not hold. `eightly-agent.exe install` points the service at `C:\ProgramData\eightly-agent\secret`. On Linux and macOS keep the file mode `0600` or `0400` and owned by the agent's user; the agent does not refuse a wider mode there, because container secrets are often mounted `0444`.

**`--insecure-no-auth` / `EIGHTLY_INSECURE_NO_AUTH`**

When set, allows the agent to start without a shared secret. Authentication is bypassed entirely -- every request is accepted without a token. **Dev/testing only. Never use in production.**

### Registration

**`--server-url` / `EIGHTLY_SERVER_URL`**

The URL of the EOS server this agent should register with. Example: `https://your-eos-server:8080` or `https://os.eight.ly`. When set, the agent will attempt to register on startup and begin sending heartbeats every 30 seconds. When empty, registration and heartbeat are both disabled. See [Registration and Heartbeat](Registration-and-Heartbeat.md) for details.

**`--enroll-token` / `EIGHTLY_ENROLL_TOKEN`**

A one-time token provided by the EOS admin to authorize the first registration. This is sent in the `Authorization` header of the registration POST and in the request body. After successful registration, this token is no longer needed -- the agent receives a persistent token from EOS and stores it in the state file.

### Storage

**`--state-path` / `EIGHTLY_STATE_PATH`**

Path to the JSON file where the agent stores its registration state (agent ID, server-assigned token, server URL, registration timestamp). Unset, it is `eightly-agent.json` (`state.json` on Windows) inside the data directory. This file is written atomically (write to `.tmp`, then rename). The agent checks that it can write here before it spends a single-use enrollment token. See [Registration and Heartbeat](Registration-and-Heartbeat.md) for the file format.

**`--data` / `DATA_PATH`**

Root directory for agent data. Two things live here:

1. TLS certificates: `<data>/cert.pem` and `<data>/key.pem` (auto-generated if missing). See [TLS](TLS.md).
2. Volume file browser root: the file browser resolves paths as `<data>/volumes/<volumeID>/_data/<path>`. See [File Browser](File-Browser.md).

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

Comma-separated list of allowed origins for WebSocket upgrade requests. When set, a present `Origin` header must match an entry exactly (scheme, host and port). When empty (default), a present `Origin` must match the host the request was sent to. In both cases a request with no `Origin` is accepted only when it carries a valid token. See [WebSocket Console](WebSocket-Console.md).

Example: `--allowed-origins "https://os.eight.ly,https://os-dev.eight.ly"`

### TLS (Outbound)

**`--insecure-server-tls`** (flag only, no environment variable)

Outbound TLS to EOS is **fail-closed by default**. `--server-url` must be `https`, and the agent pins the server's certificate on first use and enforces that pin on every later heartbeat and re-registration. Pass `--server-ca` to also verify the server chain against your own CA. `--insecure-server-tls` turns off both verification and pinning and allows a non-`https` URL. It has no environment variable on purpose, so an inherited or leaked variable can never switch it on. Never use it in production. See [TLS](TLS.md).

**`--server-ca` / `EIGHTLY_SERVER_CA`**

Path to a CA certificate file used to verify the EOS server's certificate chain. The first-use pin still applies on top of it.

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
  --data ./data \
  --state-path ./data/agent.json
```

Or with Docker:

```bash
docker run -d --init \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v agent-data:/data \
  -p 9001:9001 \
  -e LOG_LEVEL=debug \
  -e EIGHTLY_INSECURE_NO_AUTH=true \
  ghcr.io/imogenlabs/eightly-agent:latest
```

Authentication is disabled (`--insecure-no-auth`), registration is skipped (no server URL). All endpoints are open. Useful for testing the Docker proxy and file browser locally.

### Single Host

```bash
docker run -d --init \
  --name eightly-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v agent-data:/data \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="a-strong-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:8080" \
  -e EIGHTLY_ENROLL_TOKEN="token-from-eos-admin" \
  ghcr.io/imogenlabs/eightly-agent:latest
```

Standard deployment for a single remote Docker host managed by EOS.

### Multi-Host Fleet

Deploy the same image to each host with the same shared secret but unique enrollment tokens:

```bash
# Host A (192.168.1.50)
docker run -d --init --name eightly-agent --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v agent-data:/data -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="fleet-secret" \
  -e EIGHTLY_SERVER_URL="https://eos.local:8080" \
  -e EIGHTLY_ENROLL_TOKEN="token-host-a" \
  ghcr.io/imogenlabs/eightly-agent:latest

# Host B (192.168.1.51)
docker run -d --init --name eightly-agent --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v agent-data:/data -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="fleet-secret" \
  -e EIGHTLY_SERVER_URL="https://eos.local:8080" \
  -e EIGHTLY_ENROLL_TOKEN="token-host-b" \
  ghcr.io/imogenlabs/eightly-agent:latest
```

All agents use the same shared secret so EOS can sign tokens for any of them; each agent accepts only tokens issued for its own ID. Each gets its own enrollment token for initial registration. After enrollment, the token can be removed from the environment.

### Custom TLS Certificates

```bash
docker run -d --init --name eightly-agent --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v /etc/eightly-agent/certs/cert.pem:/data/cert.pem:ro \
  -v /etc/eightly-agent/certs/key.pem:/data/key.pem:ro \
  -v agent-data:/data \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-secret" \
  ghcr.io/imogenlabs/eightly-agent:latest
```

The agent checks for existing `cert.pem` and `key.pem` in the data directory before generating self-signed certs. A read-only key must already be mode `0600` and owned by uid 1000 on the host. See [TLS](TLS.md) for details.

## See Also

- [Installation](Installation.md) -- Setup instructions
- [Authentication](Authentication.md) -- Token format and validation
- [Registration and Heartbeat](Registration-and-Heartbeat.md) -- How registration uses `--server-url` and `--enroll-token`
- [TLS](TLS.md) -- Certificate generation and custom certs
- [Troubleshooting](Troubleshooting.md) -- Debug logging and common config issues
