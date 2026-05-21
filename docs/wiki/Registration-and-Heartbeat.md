# Registration and Heartbeat

Eight.ly Agent can register itself with an Eight.ly OS server and maintain connectivity through periodic heartbeats. This enables EOS to discover remote Docker hosts automatically and monitor their availability.

## Overview

The registration system has three phases:

1. **Initial registration** -- The agent announces itself to EOS and receives a persistent identity.
2. **State persistence** -- The agent saves its identity to disk so it survives restarts.
3. **Heartbeat loop** -- The agent sends periodic signals to EOS to confirm it is alive.

Registration is optional. If `--server-url` (or `EIGHTLY_SERVER_URL`) is not set, the agent runs standalone with no EOS integration.

## Initial Registration

On startup, the agent checks for existing state at the configured `--state-path` (default `/data/eightly-agent.json`). If no state file exists and a `--server-url` is configured, the agent initiates registration.

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

The agent stores `agent_id` and `token` for future heartbeats.

### Registration Failure

If registration fails (network error, bad enrollment token, EOS unreachable), the agent logs a warning and continues running. It does not exit. The heartbeat loop will attempt re-registration later if connectivity is restored.

```
WRN registration failed, will retry via heartbeat
```

## State File

After successful registration, the agent writes a JSON state file:

```json
{
  "agent_id": "agt_a1b2c3d4",
  "token": "persistent-auth-token-from-eos",
  "server_url": "https://your-eos-server:7001",
  "registered_at": "2026-05-20T15:30:00Z"
}
```

**File location**: Configured via `--state-path` / `EIGHTLY_STATE_PATH` (default `/data/eightly-agent.json`).

**Atomic writes**: The file is written to a temporary path (`<state-path>.tmp`) and then renamed to the final path. This prevents corruption if the agent is killed mid-write.

**Permissions**: The state file is written with mode `0600`. The parent directory is created with mode `0700` if it does not exist.

On subsequent startups, the agent loads this file and skips registration. It goes directly to the heartbeat loop.

## Heartbeat Loop

Once registered (or loaded from state), the agent starts a background goroutine that sends heartbeats to EOS.

### Normal Operation

- **Interval**: 30 seconds
- **Method**: `PUT <server-url>/api/agents/<agent-id>/heartbeat`
- **Auth**: `Authorization: Bearer <token>` (the persistent token from registration, not the enrollment token)
- **Body**: Empty
- **Expected response**: Any 2xx or 3xx status code

On success, the interval remains at 30 seconds and the failure counter resets to zero.

### Failure Handling

When a heartbeat fails (network error or 4xx/5xx response):

1. The failure counter increments.
2. The interval doubles (exponential backoff): 30s -> 60s -> 120s -> 240s -> 300s (capped at 5 minutes).
3. The failure is logged at `warn` level.

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

Outbound TLS is **fail-closed by default**. Registration and heartbeat requests verify the EOS server's TLS certificate against the system trust store. If verification fails, the connection is refused. For EOS servers with self-signed or internal CA certificates, use `--server-ca <path>` to pin the CA, or `--insecure-server-tls` as an explicit opt-in for dev environments only. See [[TLS]] for full details.

## Timeline

```
Startup
  |
  +-- Load state file
  |     |
  |     +-- Found? --> Start heartbeat loop
  |     +-- Not found + server-url set? --> Register
  |           |
  |           +-- Success --> Save state --> Start heartbeat loop
  |           +-- Failure --> Log warning, start server anyway
  |
  +-- Start HTTPS server
  |
  ... running ...
  |
  +-- Every 30s: heartbeat PUT
  |     |
  |     +-- OK --> reset interval
  |     +-- Fail --> backoff, after 3 fails --> re-register
  |
  +-- SIGINT/SIGTERM
  |     |
  |     +-- Stop heartbeat
  |     +-- Shutdown server (10s grace)
  |     +-- Exit
```

## See Also

- [[Configuration]] -- `--server-url`, `--enroll-token`, `--state-path` flags
- [[Authentication]] -- How API tokens differ from enrollment/heartbeat tokens
- [[TLS]] -- Outbound TLS configuration for registration and heartbeat
- [[Troubleshooting]] -- Debugging registration and heartbeat failures
