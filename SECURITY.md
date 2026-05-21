# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Eight.ly Agent, please report it responsibly.

**Preferred:** [GitHub Security Advisories](https://github.com/smashingtags/eightly-agent/security/advisories/new) (private, trackable, integrated with GitHub)

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

### Threat model

| Threat | Mitigation |
|--------|------------|
| Token theft | Tokens support expiry (`exp` claim). Rotate the shared secret if a token is compromised. |
| Network interception | All communication is over TLS. For production, configure a proper CA (`--server-ca`) instead of self-signed certs. |
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
