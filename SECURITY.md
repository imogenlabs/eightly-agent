# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Eight.ly Agent, please report it responsibly.

**Preferred:** [GitHub Security Advisories](https://github.com/imogenlabs/eightly-agent/security/advisories/new) (private, trackable, integrated with GitHub — note this repo is private today, so filing an advisory requires collaborator access; use email otherwise)

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

- **Docker socket access** — the agent mediates all Docker API calls through HMAC-SHA256 authenticated HTTPS. Without a valid token, no Docker operations are possible. A token must carry the server's issuer (`eightly-server`), type `agent`, an issue time and an expiry no more than 30 days later, and must be issued for this agent's registered ID. An agent configured with a server URL answers `503` (`Retry-After: 30`) on every authenticated route until it has registered and knows that ID.
- **Requests that would reach the host** — even with a valid token, the Docker proxy refuses (`403`) container create, exec create and container update requests that ask for privileged mode, a host PID, IPC, network, user, UTS or cgroup namespace, a dangerous capability, a bind mount of a sensitive host path, host devices or device cgroup rules, a security option outside a short allow-list, masked or read-only path overrides, a sysctl that is not namespaced, a cgroup parent, or a tmpfs over `/`, `/proc`, `/sys` or `/etc`. It also refuses volume driver options that bind a host path or device, the whole Swarm API, and any write to the plugin, service, config, secret and node APIs. A body it must inspect that is over 1 MiB is refused (`413`), never truncated; a body it cannot parse is refused. The path is cleaned and every API version prefix stripped before the check, so the filter sees exactly what Docker will route. This is a deny-list: it blocks the known escape routes, not every possible one.
- **File operations** — the file browser is restricted to Docker volume directories and validates all paths against traversal attacks. Hidden paths (any segment starting with `.`) are refused, and so are pipes, sockets and device nodes.
- **Host information** — PCI device and disk information is only available to authenticated callers.

### What a valid token grants

A valid HMAC-signed token grants **full Docker API access** on the host where the agent runs. This is equivalent to root access on that host. Treat your `EIGHTLY_AGENT_SECRET` with the same care as a root password.

### What the agent trusts

- **The enrollment token issuer.** The one-time `--enroll-token` is assumed to come from a legitimate EOS administrator. It is exchanged for a persistent agent token during registration.
- **The EOS server's identity via `--server-ca`.** When a CA cert is provided, the agent pins its TLS root of trust to that CA. All heartbeat and registration traffic validates the server's certificate chain against it.

### What the agent does NOT trust

- **HTTP Origin headers.** The WebSocket upgrade checks Origin whether or not auth is enabled. With `--allowed-origins` set, only those origins pass; without it, the Origin must match the host the request was sent to. A request with no Origin header (curl, SDKs, the EOS server) is accepted only when it carries a Bearer token, which a browser cannot add. The Origin check is not authentication; the Bearer token is.
- **Client-supplied paths in `/browse/*`.** A `..` segment, a NUL byte and paths that are not local are refused, and every operation runs through an `os.Root` opened on the volume's `_data` directory, which refuses a symlink or `..` that leads out of the volume at the moment of the operation.

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
| **Same-LAN attacker without a token** | Cannot do anything. All endpoints except `/ping`, `/healthz`, `/readyz` and `/version` require a valid HMAC-signed Bearer token. TLS prevents passive eavesdropping. |
| **`--insecure-no-auth` enabled** | Any host that can reach port 9001 has full Docker API access. This flag exists only for development and must never be used in production. |

### Threat model

| Threat | Mitigation |
|--------|------------|
| Token theft | Every token must carry `iat` and `exp`, at most 30 days apart, and is refused once expired. A token is bound to one agent's ID. Rotate the shared secret if a token or the secret is compromised. |
| Network interception | All communication is over TLS. The default is fail-closed: the agent verifies the server certificate against the system trust store. Use `--server-ca` to pin a custom CA, or `--insecure-server-tls` to disable verification (development only). |
| Path traversal | `..` segments are refused and every file operation is confined by an `os.Root` on the volume directory. Volume IDs are restricted to safe identifiers. |
| CRLF injection | Container and exec IDs are validated against `^[a-zA-Z0-9]{12,64}$` before use in Docker socket requests. |
| Unauthenticated access | The agent refuses to start without a secret (`EIGHTLY_AGENT_SECRET`, or a file named by `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE`) unless `--insecure-no-auth` is explicitly set. It also refuses to start when both a secret and a secret file are set, or when a boolean variable such as `EIGHTLY_INSECURE_NO_AUTH` holds something other than true or false. |

### Best practices

1. **Always set a strong secret** — generate with `openssl rand -hex 32`
2. **Keep the secret out of the environment where you can** — use `--secret-file` (the Windows service does this by default) or, for the Linux packages, the root-only `/etc/eightly-agent/agent.env`
3. **Run as non-root** — use the `eightly-agent` user with Docker group access
4. **Restrict network access** — bind the agent to a private interface or use a VPN
5. **Pin TLS certificates** — use `--server-ca` in production instead of `--insecure-server-tls`
6. **Rotate secrets regularly** — change `EIGHTLY_AGENT_SECRET` on a schedule
