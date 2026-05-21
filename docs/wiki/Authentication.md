# Authentication

Eight.ly Agent uses HMAC-SHA256 signed tokens for request authentication. The EOS server and the agent share a secret key. EOS signs tokens with that key, and the agent validates them on every request.

## Overview

- Algorithm: HMAC-SHA256
- Token location: `Authorization: Bearer <token>` header
- Shared secret: set via `--secret` flag or `EIGHTLY_AGENT_SECRET` env var
- When the secret is empty, authentication is completely disabled (dev mode)

## Token Format

Tokens are a two-part string separated by a dot:

```
<base64url(payload)>.<base64url(signature)>
```

This is structurally similar to a JWT but uses a simplified format with only two segments (no separate header).

### Payload

The payload is a JSON object, base64url-encoded (no padding):

```json
{
  "sub": "admin",
  "iss": "eightly-server",
  "type": "agent-api",
  "iat": 1716249600
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sub` | string | No | Subject -- typically the username or API client identifier |
| `iss` | string | Yes | Issuer -- **must** be `"eightly-server"` or the token is rejected |
| `type` | string | No | Token type identifier |
| `iat` | int64 | No | Issued-at timestamp (Unix epoch seconds) |
| `exp` | int64 | No | Expiration timestamp (Unix epoch seconds). Enforced when present. |

The `iss` field is the only required claim beyond signature verification. If `iss` is not exactly `"eightly-server"`, the request is rejected with 401.

Token expiry (`exp` claim) is **enforced when present**. If the `exp` field is set and the current time is past that timestamp, the token is rejected with 401. If `exp` is absent, the token does not expire based on time. Rotation is handled by changing the shared secret.

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

func GenerateToken(secret string) string {
    claims := map[string]any{
        "sub":  "admin",
        "iss":  "eightly-server",
        "type": "agent-api",
        "iat":  time.Now().Unix(),
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
PAYLOAD=$(echo -n '{"sub":"admin","iss":"eightly-server","type":"agent-api","iat":1716249600}' | base64 | tr '+/' '-_' | tr -d '=')
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$SECRET" -binary | base64 | tr '+/' '-_' | tr -d '=')
TOKEN="${PAYLOAD}.${SIG}"

curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/host
```

## Validation Flow

When a request arrives, the auth middleware executes these steps in order:

1. **Extract token**: Read the `Authorization` header, strip the `Bearer ` prefix. If the header is missing or has no `Bearer ` prefix, return 401.

2. **Split on dot**: The token must contain exactly one dot, producing two parts: `[payload, signature]`. If the split fails, return 401.

3. **Verify signature**: Compute `HMAC-SHA256(secret, payload_part)` and compare against the base64url-decoded signature using constant-time comparison (`hmac.Equal`). If the signature does not match, return 401.

4. **Decode payload**: Base64url-decode the payload part and unmarshal the JSON into a Claims struct. If decoding or JSON parsing fails, return 401.

5. **Check issuer**: Verify that `claims.Iss == "eightly-server"`. If not, return 401.

6. **Check expiry**: If the `exp` claim is present and the current time exceeds it, return 401.

7. **Pass through**: If all checks pass, the request proceeds to the handler.

## Middleware Chain

The authentication middleware is applied as a wrapper around protected handlers in `main.go`:

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

The `/healthz`, `/readyz`, `/version`, and `/ping` endpoints are registered directly on the mux without the auth wrapper. Every other route passes through `auth.Middleware(secret)(handler)`.

## Failure Responses

All authentication failures return the same response to avoid leaking information about which step failed:

```
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{"message":"unauthorized"}
```

## Dev Mode (No Auth)

The agent is **fail-closed by default**: it refuses to start without `EIGHTLY_AGENT_SECRET` set. To run without authentication (dev/testing only), you must explicitly pass `--insecure-no-auth` or set `EIGHTLY_INSECURE_NO_AUTH=true`. When running in this mode, the middleware is bypassed entirely -- no token validation occurs and all endpoints are open.

This is only appropriate for local development and testing. In production, always set a strong shared secret.

## Setting Up Shared Secrets

The same secret must be configured on both sides:

1. **On the agent**: Set `EIGHTLY_AGENT_SECRET` or `--secret` to the shared secret value.
2. **On EOS**: Configure the agent's secret in the remote environment settings. EOS will use this secret to sign tokens when making API calls to the agent.

Choose a strong random secret. Example generation:

```bash
openssl rand -hex 32
```

This produces a 64-character hex string (256 bits of entropy).

## Secret Rotation

To rotate the shared secret:

1. Generate a new secret
2. Update the EOS remote environment configuration with the new secret
3. Restart the agent with the new `EIGHTLY_AGENT_SECRET`

There is no grace period or dual-secret support. Both sides must switch at the same time, which means a brief window where requests may fail. For zero-downtime rotation in a fleet, roll agents one at a time and update EOS between each.

## See Also

- [[Configuration]] -- Where to set the secret flag and env var
- [[API-Reference]] -- Which endpoints require auth
- [[Registration-and-Heartbeat]] -- How the registration token differs from the API token
- [[Troubleshooting]] -- Debugging 401 errors
