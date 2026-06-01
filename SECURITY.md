# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Eight.ly Agent, please report it responsibly.

**Preferred:** [GitHub Security Advisories](https://github.com/imogenlabs/eightly-agent/security/advisories/new) (private, trackable, integrated with GitHub)

**Email:** security@imogenlabs.ai

Please include:
- Description of the vulnerability
- Steps to reproduce
- Impact assessment
- Suggested fix (if any)

We will acknowledge receipt within 48 hours and provide an initial assessment within 5 business days.

## Safe Harbor

We will not pursue legal action against researchers who report vulnerabilities in good faith.

## Credit

We credit reporters in release notes unless they prefer anonymity.

## Scope

**In scope:** the agent binary, its protocol, and its configuration.

**Out of scope:** the EOS server, Docker daemon bugs, third-party dependencies.

## Supported Versions

| Version | Supported |
|---------|-----------|
| 1.x     | Yes       |
| < 1.0   | No        |

## Security Model

Eight.ly Agent provides authenticated, TLS-encrypted access to a Docker daemon. Understanding the trust model is essential:

### What the agent protects

- **Docker socket access** — the agent mediates all Docker API calls through HMAC-SHA256 authenticated HTTPS. Without a valid token, no Docker operations are possible.
- **File operations** — the file browser is restricted to Docker volume directories and validates all paths against traversal attacks.
- **Host information** — PCI device and disk information is only available to authenticated callers.

### What a valid token grants

A valid HMAC-signed token grants **full Docker API access** on the host where the agent runs. This is equivalent to root access on that host. Treat your `EIGHTLY_AGENT_SECRET` with the same care as a root password.

### What the agent trusts

- **The enrollment token issuer.** The one-time `--enroll-token` is assumed to come from a legitimate EOS administrator. It is exchanged for a persistent agent token during registration.
- **The EOS server's identity via `--server-ca`.** When a CA cert is provided, the agent pins its TLS root of trust to that CA. All heartbeat and registration traffic validates the server's certificate chain against it.

### What the agent does NOT trust

- **HTTP Origin headers when auth is enabled.** With a valid `EIGHTLY_AGENT_SECRET`, the WebSocket upgrader accepts any Origin because the Bearer token is the access control boundary, not the Origin header. When `--allowed-origins` is set, requests with no Origin header are still permitted to support non-browser clients (curl, SDKs, EOS server). The Origin header is not an authentication mechanism — rely on the Bearer token for security. To require Origin on all requests, set `--allowed-origins` AND ensure all legitimate clients send the Origin header.
- **Client-supplied paths in `/browse/*`.** All paths are resolved against a volume base directory. `filepath.Rel` containment checks and post-symlink-resolution re-checks reject any traversal attempt.

### Privilege required

Access to the Docker socket is **root-equivalent**. Any process that can issue Docker API calls can create privileged containers, mount the host filesystem, and escalate to full root. The agent must be treated as a root-level service.

### Network exposure

The agent binds to `0.0.0.0:9001` by default, meaning it listens on all interfaces. In production, you should:
- Restrict the listen address to a private interface (`--addr 10.0.0.5`) or use a VPN/firewall.
- Never expose port 9001 to the public internet.

### Attacker scenarios

| Scenario | Impact |
|----------|--------|
| **Stolen HMAC token or shared secret** | Full Docker API access on the host = root-equivalent. The attacker can create privileged containers, mount `/`, read/write any file, and pivot to other hosts. **Rotate the secret immediately** if compromised. |
| **Same-LAN attacker without a token** | Cannot do anything. All endpoints except `/ping`, `/healthz`, and `/readyz` require a valid HMAC-signed Bearer token. TLS prevents passive eavesdropping. |
| **`--insecure-no-auth` enabled** | Any host that can reach port 9001 has full Docker API access. This flag exists only for development and must never be used in production. |

### Threat model

| Threat | Mitigation |
|--------|------------|
| Token theft | Tokens support expiry (`exp` claim). Rotate the shared secret if a token is compromised. |
| Network interception | All communication is over TLS. The default is fail-closed: the agent verifies the server certificate against the system trust store. Use `--server-ca` to pin a custom CA, or `--insecure-server-tls` to disable verification (development only). |
| Path traversal | Volume paths are validated with `filepath.Rel` containment checks. Volume IDs are restricted to safe identifiers. |
| CRLF injection | Container and exec IDs are validated against `^[a-zA-Z0-9]{12,64}$` before use in Docker socket requests. |
| Unauthenticated access | The agent refuses to start without `EIGHTLY_AGENT_SECRET` unless `--insecure-no-auth` is explicitly set. |

### Best practices

1. **Always set a strong secret** — generate with `openssl rand -hex 32`
2. **Use token expiry** — configure your EOS server to issue tokens with `exp` claims
3. **Run as non-root** — use the `eightly-agent` user with Docker group access
4. **Restrict network access** — bind the agent to a private interface or use a VPN
5. **Pin TLS certificates** — use `--server-ca` in production instead of `--insecure-server-tls`
6. **Rotate secrets regularly** — change `EIGHTLY_AGENT_SECRET` on a schedule
