---
description: "How Eight.ly Agent checks HMAC-SHA256 signed bearer tokens from Eight.ly OS on every request."
---

# Authentication

Eight.ly Agent uses HMAC-SHA256 signed tokens for request authentication. The EOS server and the agent share a secret key. EOS signs tokens with that key, and the agent validates them on every request.

## Overview

- Algorithm: HMAC-SHA256
- Token location: `Authorization: Bearer <token>` header
- Shared secret: set via `--secret`, `EIGHTLY_AGENT_SECRET`, or a file named by `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE`
- Without a secret the agent refuses to start, unless `--insecure-no-auth` is set (dev only)
- A token opens only the agent it names: its `sub` must be this agent's registered ID

## Token Format

Tokens are a two-part string separated by a dot:

```
<base64url(payload)>.<base64url(signature)>
```

This is structurally similar to a JWT but uses a simplified format with only two segments (no separate header).

### Payload

The payload is a JSON object, base64url-encoded (no padding). Eight.ly OS issues tokens like this one:

```json
{
  "sub": "<agent ID>",
  "iss": "eightly-server",
  "type": "agent",
  "iat": 1716249600,
  "exp": 1718841600
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sub` | string | Yes, once registered | The agent ID this token is for. An agent that knows its ID refuses a token for any other agent. |
| `iss` | string | Yes | Issuer. Must be exactly `"eightly-server"`. |
| `type` | string | Yes | Token type. Must be exactly `"agent"`. |
| `iat` | int64 | Yes | Issued-at time (Unix epoch seconds). |
| `exp` | int64 | Yes | Expiry (Unix epoch seconds). Must be after `iat` and no more than 30 days after it. |

A token that lacks `iat` or `exp`, expires before it was issued, lives longer than 30 days, has already expired, or carries another issuer or type is refused with 401. A token without an expiry would never lapse, so it is not accepted.

### Signature

The signature is computed as:

```
HMAC-SHA256(shared_secret, base64url(payload))
```

The HMAC is computed over the raw base64url-encoded payload string (not the decoded JSON). The resulting signature is then base64url-encoded (no padding) and appended after the dot.

## Generating a Token

To generate a valid token programmatically (example in Go):

```go
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "time"
)

func GenerateToken(secret, agentID string) string {
    now := time.Now()
    claims := map[string]any{
        "sub":  agentID,
        "iss":  "eightly-server",
        "type": "agent",
        "iat":  now.Unix(),
        "exp":  now.Add(time.Hour).Unix(),
    }
    payload, _ := json.Marshal(claims)
    encoded := base64.RawURLEncoding.EncodeToString(payload)

    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(encoded))
    sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

    return encoded + "." + sig
}
```

Example using shell tools:

```bash
SECRET="your-shared-secret"
AGENT_ID="the-agent-id"   # agent_id in the agent's state file
NOW=$(date +%s)
PAYLOAD=$(printf '{"sub":"%s","iss":"eightly-server","type":"agent","iat":%d,"exp":%d}' "$AGENT_ID" "$NOW" $((NOW + 3600)) | base64 | tr -d '\n' | tr '+/' '-_' | tr -d '=')
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$SECRET" -binary | base64 | tr '+/' '-_' | tr -d '=')
TOKEN="${PAYLOAD}.${SIG}"

curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/host
```

## Validation Flow

When a request arrives, the auth middleware executes these steps in order:

1. **Extract token**: Read the `Authorization` header, strip the `Bearer ` prefix. If the header is missing or has no `Bearer ` prefix, return 401.

2. **Split on dot**: The token is split at its first dot into `[payload, signature]`. If there is no dot, return 401.

3. **Verify signature**: Compute `HMAC-SHA256(secret, payload_part)` and compare against the base64url-decoded signature using constant-time comparison (`hmac.Equal`). If the signature does not match, return 401.

4. **Decode payload**: Base64url-decode the payload part and unmarshal the JSON into a Claims struct. If decoding or JSON parsing fails, return 401.

5. **Check issuer and type**: `iss` must be `"eightly-server"` and `type` must be `"agent"`. If not, return 401.

6. **Check lifetime**: `iat` and `exp` must both be set, `exp` must be after `iat` and at most 30 days after it, and the current time must be before `exp`. If not, return 401.

7. **Check subject**: When the agent knows its registered ID, `sub` must equal it. If not, return 401.

8. **Pass through**: If all checks pass, the request proceeds to the handler.

### Before the agent knows its ID

When a secret and a server URL are both set, every authenticated route answers `503 Service Unavailable` with `Retry-After: 30` until the agent has registered and knows its own ID:

```
HTTP/1.1 503 Service Unavailable
Content-Type: application/json
Retry-After: 30

{"message":"service unavailable: agent is not registered with the server yet"}
```

Without its ID the agent cannot check whose token it was handed, so it serves nothing that needs one. The unauthenticated probes keep answering. In local-only mode (no server URL) the agent never has an ID and checks only the signature, issuer, type and lifetime.

## Middleware Chain

The authentication middleware is applied as a wrapper around protected handlers in `internal/server/server.go`:

```
GET  /healthz                -- NO auth (liveness, always open)
GET  /readyz                 -- NO auth (readiness, checks Docker)
GET  /version                -- NO auth (version info)
GET  /ping                   -- NO auth (legacy health check)
GET  /metrics                -- auth required (Prometheus, behind --metrics flag)
*    /websocket/attach       -- auth required
*    /websocket/exec         -- auth required
*    /browse/*               -- auth required
GET  /host                   -- auth required
*    /* (Docker proxy)       -- auth required
```

The `/healthz`, `/readyz`, `/version`, and `/ping` endpoints are registered directly on the mux without the auth wrapper. Every other route passes through the registration gate described above and then `auth.Middleware`.

## Failure Responses

All token failures return the same response to avoid leaking information about which step failed. The reason is written to the agent's log as a `[SECURITY] auth.unauthorized` line.

```
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{"message":"unauthorized"}
```

## Dev Mode (No Auth)

The agent is **fail-closed by default**: it refuses to start without a shared secret. To run without authentication (dev/testing only), you must explicitly pass `--insecure-no-auth` or set `EIGHTLY_INSECURE_NO_AUTH=true`. When running in this mode, the middleware is bypassed entirely -- no token validation occurs and all endpoints are open.

This is only appropriate for local development and testing. In production, always set a strong shared secret.

## Setting Up Shared Secrets

Eight.ly OS creates the shared secret and includes it in the install command under **Environments → Add Agent**. That command carries the server URL, the shared secret and a one-time enrollment token. You do not generate a secret by hand.

For a package or binary install, use those values with `EIGHTLY_AGENT_SECRET` or `--secret`, or put the secret in a file and use `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE`. The packaged Linux service reads `/etc/eightly-agent/agent.env`; the Windows service reads `C:\ProgramData\eightly-agent\secret`.

## Secret Rotation

The agent reads one shared secret at startup. To change it, update its environment, `agent.env` or secret file, then restart the agent. There is no grace period or dual-secret support: tokens signed with the old secret are refused after the restart.

The server and agents must use the same secret. See the Eight.ly OS documentation for server-side rotation and coordination.

## See Also

- [Configuration](Configuration.md) -- Where to set the secret flag and env var
- [API Reference](API-Reference.md) -- Which endpoints require auth
- [Registration and Heartbeat](Registration-and-Heartbeat.md) -- How the registration token differs from the API token
- [Troubleshooting](Troubleshooting.md) -- Debugging 401 errors
