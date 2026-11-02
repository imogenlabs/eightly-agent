---
description: "How Eight.ly Agent enrolls with Eight.ly OS, pins its certificate, and sends a heartbeat every 30 seconds."
---

# Registration and Heartbeat

Eight.ly Agent can register itself with an Eight.ly OS server and maintain connectivity through periodic heartbeats. This enables EOS to discover remote Docker hosts automatically and monitor their availability.

## Overview

The registration system has three phases:

1. **Initial registration** -- The agent announces itself to EOS and receives a persistent identity.
2. **State persistence** -- The agent saves its identity to disk so it survives restarts.
3. **Heartbeat loop** -- The agent sends periodic signals to EOS to confirm it is alive.

Registration is optional. If `--server-url` (or `EIGHTLY_SERVER_URL`) is not set, the agent runs standalone with no EOS integration.

## Initial Registration

On startup, the agent checks for existing state at the configured `--state-path` (default: `eightly-agent.json` in the data directory, so `/data/eightly-agent.json` in the image). If no state file exists and a `--server-url` is configured, the agent initiates registration.

Before it sends the enrollment token, the agent checks that it can write the state file. The token is single-use, so a registration that could not be saved would be lost on the next restart along with the token. When the file cannot be written, the agent logs `not registering: the registration state cannot be saved` and does not register.

### What the Agent Sends

The agent auto-detects information about the host and sends a POST request to EOS:

```
POST <server-url>/api/agents/register
Content-Type: application/json
Authorization: Bearer <enroll-token>   (if enroll-token is set)
```

```json
{
  "hostname": "docker-host-01",
  "ip": "192.168.1.50",
  "port": "9001",
  "docker_version": "27.4.1",
  "os": "linux",
  "arch": "amd64",
  "agent_version": "2.0.0",
  "enroll_token": "the-enrollment-token"
}
```

**Auto-detection details:**

| Field | How it is detected |
|-------|--------------------|
| `hostname` | `os.Hostname()` |
| `ip` | Opens a UDP connection to `8.8.8.8:53` and reads the local address. This finds the default-route IP without sending any traffic. |
| `port` | From the agent's `--port` configuration |
| `docker_version` | Queries `GET http://docker/version` via the Unix socket |
| `os` | `runtime.GOOS` |
| `arch` | `runtime.GOARCH` |
| `agent_version` | Build-time version string |

### Enrollment Token

The `--enroll-token` is a one-time authorization token created by the EOS admin. It is sent both in the `Authorization: Bearer` header and in the request body. After a successful registration, the enrollment token is not needed again -- the agent receives a persistent token from EOS.

If no enrollment token is provided, the registration request is sent without an `Authorization` header. Whether EOS accepts unauthenticated registration depends on its configuration.

### What EOS Returns

On success (200 or 201):

```json
{
  "success": true,
  "data": {
    "agent_id": "agt_a1b2c3d4",
    "token": "persistent-auth-token-from-eos"
  }
}
```

The agent stores `agent_id` and `token` for future heartbeats. A response that reports success without both is treated as a failure.

### Registration Failure

If registration fails (network error, rate limiting, a server error, EOS unreachable), the agent logs a warning and keeps running. It does not exit. It retries in the background, first after 5 seconds and then twice as long each time, up to every 5 minutes, until it registers or is stopped.

```
WRN registration failed
WRN agent is not registered; authenticated routes answer 503 until it is
```

Until it has registered, every authenticated route answers `503` with `Retry-After: 30`, because the agent cannot yet check that a token was issued for it (see [Authentication](Authentication.md#before-the-agent-knows-its-id)). The probes (`/ping`, `/healthz`, `/readyz`, `/version`) keep answering. When the server refuses the enrollment token (401, 403 or 410: unknown, expired, already used, or its agent deleted), the agent stops retrying, since a single-use token cannot become valid again. It keeps running, answers `503` on authenticated routes, and logs `the server refused the enrollment token; ... generate a new install command on the Agents page and run it on this host`.

## State File

After successful registration, the agent writes a JSON state file:

```json
{
  "agent_id": "agt_a1b2c3d4",
  "token": "persistent-auth-token-from-eos",
  "server_url": "https://your-eos-server:8080",
  "registered_at": "2026-05-20T15:30:00Z",
  "server_pin": "sha256/<base64 fingerprint>"
}
```

`server_pin` is the fingerprint of the EOS server certificate's public key, captured on the first registration and enforced on every later connection (see [TLS](TLS.md#outbound-tls-registration-and-heartbeat)).

**File location**: Configured via `--state-path` / `EIGHTLY_STATE_PATH` (default: `eightly-agent.json` in the data directory; `state.json` on Windows).

**Atomic writes**: The file is written to a temporary path (`<state-path>.tmp`) and then renamed to the final path. This prevents corruption if the agent is killed mid-write.

**Permissions**: The state file is written with mode `0600`. The parent directory is created with mode `0700` if it does not exist. The agent refuses to load a state file that is not `0600` in a `0700` directory, both owned by its own user; it tightens a directory it owns that holds nothing but its own files. On Windows it sets a protected access list instead and refuses the file if another account keeps access.

On subsequent startups, the agent loads this file and skips registration. It goes directly to the heartbeat loop. A saved state with no agent ID or token is ignored and the agent enrolls again. A state file that exists but cannot be read or parsed stops the agent at startup, and the file is left as it is, rather than enrolling over the identity and server pin it holds. A saved registration for a server other than `--server-url` (compared without case, trailing slash or default port) also stops the agent, unless an enrollment token is configured: then the agent copies the old state to `<state-path>.bak` and enrolls with the new server without the old server's pin. A saved token that has expired is replaced by a fresh enrollment when an enrollment token is configured; otherwise the agent logs that it must be re-enrolled.

## Heartbeat Loop

Once registered (or loaded from state), the agent starts a background goroutine that sends heartbeats to EOS.

### Normal Operation

- **Interval**: 30 seconds
- **Method**: `PUT <server-url>/api/agents/<agent-id>/heartbeat`
- **Auth**: `Authorization: Bearer <token>` (the persistent token from registration, not the enrollment token)
- **Body**: Empty
- **Expected response**: Any status below 400

On success, the interval remains at 30 seconds and the failure counter resets to zero.

### Failure Handling

When a heartbeat fails (network error or 4xx/5xx response):

1. The failure counter increments.
2. The interval doubles (exponential backoff): 30s -> 60s -> 120s -> 240s -> 300s (capped at 5 minutes).
3. The failure is logged at `warn` level.

### Token Renewal

The bearer token from EOS lives 30 days. From half its lifetime on (day 15), each successful heartbeat is followed by a re-registration that trades the still-valid token for a new one and saves it. A failed renewal is retried on the next beat. If the agent was off long enough for the token to lapse, the server refuses it; the agent then logs `agent token expired, so this agent must be re-enrolled` once, slows to the 5-minute interval, and needs a new install command from EOS.

### Re-Registration

After **3 consecutive heartbeat failures**, the agent attempts to re-register with EOS:

1. Calls `DetectInfo()` again to get current host information.
2. Sends a new `POST /api/agents/register` request via the `ReRegister` function. The existing Bearer token from state is included in the `Authorization` header (not the enrollment token).
3. If successful, replaces the in-memory state and saves the new state file.
4. Resets the failure counter and interval back to 30 seconds.
5. If re-registration also fails, the heartbeat loop continues with backoff. It will try re-registration again after the next 3 consecutive failures.

### Graceful Shutdown

The heartbeat goroutine listens on the application's context. When the agent receives SIGINT or SIGTERM:

1. The context is cancelled.
2. The heartbeat loop logs "heartbeat stopped" and exits.
3. The HTTP server shuts down with a 10-second grace period.

## Prometheus Metrics

When the agent is started with `--metrics`, the following counters related to registration and heartbeat are exposed at `/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `eightly_agent_heartbeats_sent_total` | Counter | Total heartbeats sent (successful and failed) |
| `eightly_agent_heartbeats_failed_total` | Counter | Total failed heartbeats |
| `eightly_agent_registration_attempts_total` | Counter | Total registration and re-registration attempts |

These counters are useful for alerting on connectivity issues (e.g., alert when `heartbeats_failed_total` increases over a window).

## TLS for Registration and Heartbeat

Outbound TLS is **fail-closed by default**. The server URL must be `https`. The first successful registration pins the EOS server certificate's public key, and every later registration, renewal and heartbeat must present the same key, so a self-signed EOS server works without a CA. `--server-ca <path>` additionally verifies the chain against your CA. `--insecure-server-tls` turns verification and pinning off, for dev environments only. See [TLS](TLS.md) for full details.

## Timeline

```
Startup
  |
  +-- Load state file
  |     |
  |     +-- Found? --> Start heartbeat loop
  |     +-- Not found + server-url set? --> Check state file is writable --> Register
  |           |
  |           +-- Success --> Save state --> Start heartbeat loop
  |           +-- Failure --> Start server anyway (authenticated routes 503),
  |                           retry every 5s, doubling to 5 min
  |
  +-- Start HTTPS server
  |
  ... running ...
  |
  +-- Every 30s: heartbeat PUT
  |     |
  |     +-- OK --> reset interval; renew the token from day 15
  |     +-- Fail --> backoff, after 3 fails --> re-register
  |
  +-- SIGINT/SIGTERM
  |     |
  |     +-- Stop heartbeat
  |     +-- Shutdown server (10s grace)
  |     +-- Exit
```

## See Also

- [Configuration](Configuration.md) -- `--server-url`, `--enroll-token`, `--state-path` flags
- [Authentication](Authentication.md) -- How API tokens differ from enrollment/heartbeat tokens
- [TLS](TLS.md) -- Outbound TLS configuration for registration and heartbeat
- [Troubleshooting](Troubleshooting.md) -- Debugging registration and heartbeat failures
