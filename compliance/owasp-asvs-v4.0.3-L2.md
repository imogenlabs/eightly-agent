# OWASP ASVS v4.0.3 Level 2 — Eight.ly Agent Posture

**Scope:** the agent is a headless HTTPS service — no UI, no user accounts, no
passwords, no browser sessions. It authenticates **machine callers** (the EOS
server / SDKs) with HMAC-SHA256 Bearer tokens and brokers the Docker socket.
The applicable ASVS chapters are therefore V2/V3 (token auth & lifetime), V4
(access control), V5 (input validation), V8/V9 (transport), V7 (logging),
V13 (API), and V14 (configuration). Password-store (V6.2), MFA (V2.7),
HTML/output-encoding (V5.2/V5.3) and cookie chapters are **N/A** — there is no
password, no second factor, and no HTML surface.

Status legend: **Met** / **Partial** / **Gap** / **N/A**.

## V2 — Authentication

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V2.1.1 | Auth credential is high-entropy | **Met** | Shared secret is operator-generated `openssl rand -hex 32` (256-bit); documented in `SECURITY.md` best practices |
| V2.2.1 | No unauthenticated access to protected functions | **Met** | `auth.Middleware` gates every route except `/ping`,`/healthz`,`/readyz` (`internal/server/server.go`) |
| V2.10.1 | Service authentication uses a non-default secret | **Met** | Agent **refuses to start** without `EIGHTLY_AGENT_SECRET` unless `--insecure-no-auth` is explicitly set (`internal/config/config.go` `Validate`) |
| V2.10.4 | Secrets not stored in source/config in clear | **Met** | Secret comes from env only; never committed (`.env.example` is a placeholder) |
| V2.7.* | Multi-factor authentication | **N/A** | Machine-to-machine API; MFA does not apply |

## V3 — Session / Token Management

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V3.2.1 | Tokens are server-generated, not client-chosen | **Met** | EOS server mints the HMAC token; agent only verifies (`internal/auth/jwt.go`) |
| V3.2.3 | Token integrity is cryptographically verified | **Met** | HMAC-SHA256 over the payload, compared with `hmac.Equal` (constant-time) (`jwt.go`) |
| V3.3.1 | Tokens can expire / be invalidated | **Met** | `exp` claim enforced: `time.Now().Unix() > claims.Exp` → 401 (`jwt.go`); EOS issues short-lived tokens |
| V3.5.2 | Signature algorithm is fixed, not attacker-selectable | **Met** | Algorithm is hardcoded HMAC-SHA256 — no `alg` field in the token, so no alg-confusion/`none` attack surface (`jwt.go`) |
| V3.5.3 | Issuer is validated | **Met** | `claims.Iss != "eightly-server"` → 401 (`jwt.go`) |
| V3.1.1 | Token never placed in URL | **Met** | Token is read from the `Authorization: Bearer` header only |

## V4 — Access Control

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V4.1.1 | Access controls enforced on every request | **Met** | All Docker-proxy, `/browse/`, `/host`, `/websocket/*`, `/metrics` routes wrapped in `authed(...)` (`server.go`) |
| V4.1.5 | Access controls fail closed | **Met** | No secret + no explicit opt-out → **503 fail-closed**, never open (`jwt.go` middleware contract; `config.go` Validate) |
| V4.1.3 | Least privilege | **Partial** | A valid token grants full Docker API (root-equivalent) — this is the documented trust model (`SECURITY.md`). The create-deny filter (`denyDangerousCreates`) is the in-process least-privilege boundary, blocking host-escape creates |
| V4.2.1 | No direct object reference bypass | **Met** | Docker/exec IDs validated `^[a-zA-Z0-9]{12,64}$` before reaching the socket (`internal/websocket/websocket.go`); volume IDs validated in `internal/browse/browse.go` |

## V5 — Validation, Sanitization, Encoding

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V5.1.1 | Input validation on untrusted data | **Met** | Container/exec IDs regex-validated; volume IDs restricted to safe identifiers |
| V5.1.3 | All input validated (positive model) | **Met** | `dockerIDRe = ^[a-zA-Z0-9]{12,64}$` allow-list, not a deny-list (`websocket.go`) |
| V5.3.8 | OS / path-traversal command injection prevented | **Met** | CRLF/ID injection blocked by the ID regex; create bodies inspected for host-escape config (`internal/proxy/docker.go`) |
| V12.3.1 / V5.x | Path traversal in file access prevented | **Met** | `/browse/*` resolves paths against a volume base with `filepath.Rel` lexical containment **and** post-`EvalSymlinks` re-check, plus `O_NOFOLLOW` on the final open to defeat symlink-swap (`internal/browse/browse.go`) |

## V7 — Error Handling and Logging

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V7.1.1 | No secrets in logs | **Met** | Structured `zerolog`; auth events log method/path/ip/reason only — never the token or secret (`jwt.go` `logSecurityEvent`) |
| V7.1.3 | Errors don't leak internals to clients | **Met** | Client gets a fixed `{"message":"unauthorized"}` / `{"message":"service unavailable..."}`; detail stays server-side (`jwt.go`) |
| V7.3.1 | Security-relevant events are logged | **Met** | Every auth failure / fail-closed emits a greppable `[SECURITY] event=... reason=...` line (`jwt.go`) |
| V7.4.1 | Logs shipped to a central system | **Partial** | The agent emits structured JSON to stdout/journald with a stable `[SECURITY]` prefix; central aggregation is the operator's pipeline (delegated, documented in `posture.md`) |

## V8 / V9 — Data Protection & Communication

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V9.1.1 | All traffic over TLS | **Met** | Listener is `ListenAndServeTLS` only; there is no plaintext HTTP listener (`server.go`) |
| V9.1.2 | TLS 1.2 or higher | **Met** | `MinVersion: tls.VersionTLS12` with an explicit AEAD-only cipher suite list (ECDHE + GCM/CHACHA20) (`server.go`) |
| V9.2.1 | Outbound connections verify server identity | **Met** | Heartbeat/registration verify the EOS cert against the system store or a pinned `--server-ca`; `--insecure-server-tls` is opt-in dev-only (`internal/register/`) |
| V9.2.2 | Certificate / key pinning where appropriate | **Met** | TOFU SPKI SHA-256 pin (RFC-7469 material) captured on first dial, enforced on every subsequent dial; survives restart via persisted pin (`internal/register/pin.go`) |
| V8.3.4 | Private key material protected at rest | **Met** | Self-signed key written `0600`, dir `0700`; on reuse the key perms are re-tightened to `0600` before serving (`internal/tlsutil/tls.go`) |

## V13 — API and Web Service

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V13.1.1 | Same auth applied to API and any companion surface | **Met** | A single `authed(...)` wrapper covers REST proxy, WebSocket, browse, host, metrics |
| V13.2.3 | CSRF / cross-origin handled for state-changing requests | **Met** | Bearer token (not an ambient cookie) is the access boundary, so classic CSRF does not apply; WebSocket origin allow-list available via `--allowed-origins` (`server.go`, documented trade-off in `SECURITY.md`) |
| V13.2.1 | HTTP methods enforced | **Met** | Go 1.22 method-scoped mux patterns (`GET /ping`, etc.) + the create filter keys on `POST` only (`server.go`, `docker.go`) |
| — | Request resource limits | **Met** | `ReadHeaderTimeout=10s`, `ReadTimeout=30s`, create-body inspection bounded by `io.LimitReader(maxInspectBody)` (`server.go`, `docker.go`) |

## V14 — Configuration

| Req | Requirement | Status | Implementing artifact |
|-----|-------------|--------|-----------------------|
| V14.1.1 | Build is reproducible & uses a defined toolchain | **Met** | Go toolchain single-sourced to `1.26.4` in `go.mod` and pinned in every workflow (`ci.yml`, `release.yml`, `docker-publish.yml`) |
| V14.2.1 | Dependencies kept up to date | **Met** | Dependabot weekly (gomod / github-actions / docker); `govulncheck` gates PRs |
| V14.2.6 | Unintended features disabled by default | **Met** | `--insecure-no-auth` and `--insecure-server-tls` default `false` and must be typed explicitly (`config.go`) |
| V14.4.1 | Security response headers set | **Met** | `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` set globally via `headerMiddleware` (`server.go`) |
| V14.5.2 | Supply-chain integrity (signing/provenance) | **Met** | cosign signing + SLSA provenance + SBOM attestation on release & image publish; base images digest-pinned (`release.yml`, `docker-publish.yml`, `Dockerfile`) |

## Coverage roadmap

Traced this round: V2, V3, V4, V5, V7, V8, V9, V13, V14 (≈37 applicable
requirements).

Explicitly **N/A** for this artifact class (headless machine API, no UI/users):
V2.5–V2.9 (password recovery/MFA), V3.4 (browser cookies), V5.2/V5.3
(HTML/JS output encoding), V6.2 (password storage), V11 (business logic),
V12 file-upload beyond the confined volume browser.

Deferred to the next cadence review (`posture.md`): central log shipping
(V7.4.1) and a written V1 architecture/data-flow diagram.
