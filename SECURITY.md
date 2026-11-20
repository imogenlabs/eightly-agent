# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Eight.ly Agent, please report it responsibly.

**Preferred:** [GitHub private vulnerability reporting](https://github.com/imogenlabs/eightly-agent/security/advisories/new)

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
| 2.1.x   | Yes       |
| < 2.1   | No        |

## Security Model

Eight.ly Agent provides authenticated, TLS-encrypted access to a Docker daemon. Understanding the trust model is essential:

### What the agent protects

- **Docker socket access** — the agent mediates all Docker API calls through HMAC-SHA256 authenticated HTTPS. Without a valid token, no Docker operations are possible. A token must carry the server's issuer (`eightly-server`), type `agent`, an issue time and an expiry no more than 30 days later, and must be issued for this agent's registered ID. An agent configured with a server URL answers `503` (`Retry-After: 30`) on every authenticated route until it has registered and knows that ID.
- **Requests that would reach the host** — even with a valid token, the Docker proxy forwards only an allow-list of Docker methods and paths and refuses everything else (`403`): image builds, `commit`, container export and archive download, image load, save and push, and all of Swarm, services, tasks, nodes, secrets, configs and plugins among them. On the allowed endpoints it reads container create, container update, exec create, volume create and network create bodies and refuses privileged mode; a host or another container's PID, IPC, UTS, user, cgroup or network namespace; a capability beyond Docker's default set; a host folder outside `--allowed-bind-roots` (empty by default, so containers get named volumes only); `VolumesFrom`; host devices, device requests and device cgroup rules; another runtime; runtime annotations; masked or read-only path overrides; a security option outside the default profiles, `no-new-privileges` and SELinux user, role and level labels; a sysctl that is not namespaced; a cgroup parent; `OomKillDisable`; and a tmpfs over `/`, `/proc`, `/sys` or `/etc`. Named volumes made with bind options are held to the bind roots. Exec, attach, file upload and the WebSocket console refuse the agent's own container and any container that already reaches the host, and go on under the full container ID that was checked. Container inspect hides `EIGHTLY_*` values, every environment value of the agent and of containers holding the socket, and the agent's secret flags. A body it must inspect that is over 1 MiB is refused (`413`), never truncated; a body it cannot parse is refused. The path is cleaned and every API version prefix stripped before the check, so the proxy checks exactly what Docker will route. See [API Reference](docs/wiki/API-Reference.md#docker-api-proxy) for the full list.
- **File operations** — the file browser is restricted to Docker volume directories and validates all paths against traversal attacks. Hidden paths (any segment starting with `.`) are refused, and so are pipes, sockets and device nodes.
- **Host information** — PCI device and disk information is only available to authenticated callers.

### What a valid token grants

A valid HMAC-signed token grants control of every container on the host through the allow-listed Docker API: create, start, stop, remove, exec, logs, images, volumes and networks. The proxy is built to stop that control reaching the host itself, but it cannot know every way a future Docker feature might, and a bind root a container can write to weakens it: the agent checks bind paths for symlinks at create and start (in a container, for roots mounted into it at the same path, read-only), but a link planted after that check, or before the daemon restarts a container on its own after a reboot or crash, is still followed, on the host and in a container alike. Treat your `EIGHTLY_AGENT_SECRET` with the same care as a root password.

### What the agent trusts

- **The enrollment token issuer.** The one-time `--enroll-token` is assumed to come from a legitimate EOS administrator. It is exchanged for a persistent agent token during registration.
- **The EOS server's identity via `--server-ca`.** When a CA cert is provided, the agent pins its TLS root of trust to that CA. All heartbeat and registration traffic validates the server's certificate chain against it.

### What the agent does NOT trust

- **HTTP Origin headers.** The WebSocket upgrade checks Origin whether or not auth is enabled. With `--allowed-origins` set, only those origins pass; without it, the Origin must match the host the request was sent to. A request with no Origin header (curl, SDKs, the EOS server) is accepted only when it carries a Bearer token, which a browser cannot add. The Origin check is not authentication; the Bearer token is.
- **Client-supplied paths in `/browse/*`.** A `..` segment, a NUL byte and paths that are not local are refused, and every operation runs through an `os.Root` opened on the volume's `_data` directory, which refuses a symlink or `..` that leads out of the volume at the moment of the operation.

### Privilege required

Access to the Docker socket is **root-equivalent**. Any process that can issue Docker API calls directly can create privileged containers, mount the host filesystem, and escalate to full root. The agent holds the socket, so it must be treated as a root-level service even though its proxy refuses those calls.

### Network exposure

The agent binds to `0.0.0.0:9001` by default, meaning it listens on all interfaces. In production, you should:
- Restrict the listen address to a private interface or use a VPN/firewall. For example, `--addr 192.0.2.5` uses a documentation-only address; replace it with your host's private address.
- Never expose port 9001 to the public internet.

### Attacker scenarios

| Scenario | Impact |
|----------|--------|
| **Stolen HMAC token or shared secret** | Control of every container on every host that shares the secret: run, stop and remove containers, read their logs, exec into them, read and change volume files through the file browser, and write under any bind root. The proxy refuses the known routes to the host itself. **Rotate the secret immediately** if compromised. |
| **Same-LAN attacker without a token** | Cannot do anything. All endpoints except `/ping`, `/healthz`, `/readyz` and `/version` require a valid HMAC-signed Bearer token. TLS prevents passive eavesdropping. |
| **`--insecure-no-auth` enabled** | Any host that can reach port 9001 controls every container through the allow-listed Docker API. This flag exists only for development and must never be used in production. |

### Threat model

| Threat | Mitigation |
|--------|------------|
| Token theft | Every token must carry `iat` and `exp`, at most 30 days apart, and is refused once expired. A token is bound to one agent's ID. Rotate the shared secret if a token or the secret is compromised. |
| Network interception | Trust on first use: the agent pins the server's certificate public key at first registration and refuses a different key; `--server-ca` adds chain verification. `--insecure-server-tls` disables both checks (development only). |
| Path traversal | `..` segments are refused and every file operation is confined by an `os.Root` on the volume directory. Volume IDs are restricted to safe identifiers. |
| CRLF injection | WebSocket container and exec IDs are validated against `^[a-zA-Z0-9]{12,64}$`, and an `{id}` in a proxied path must be a Docker name, before use in Docker socket requests. |
| Container escape through the API | The proxy allow-list and its body and target checks above. Keep `--allowed-bind-roots` empty, or limited to folders that hold app data and nothing the host trusts. |
| Unauthenticated access | The agent refuses to start without a secret (`EIGHTLY_AGENT_SECRET`, or a file named by `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE`) unless `--insecure-no-auth` is explicitly set. It also refuses to start when both a secret and a secret file are set, or when a boolean variable such as `EIGHTLY_INSECURE_NO_AUTH` holds something other than true or false. |

### Best practices

1. **Use the secret from Eight.ly OS** — **Environments → Add Agent** generates an install command carrying the server URL, shared secret and a one-time enrollment token; do not generate a secret by hand
2. **Keep the secret out of the environment where you can** — use `--secret-file` (the Windows service does this by default) or, for the Linux packages, the root-only `/etc/eightly-agent/agent.env`
3. **Bind only what you must** — leave `--allowed-bind-roots` empty, or name app data folders such as `/opt/appdata`, never a folder the host reads its configuration from
4. **Run as non-root** — use the `eightly-agent` user with Docker group access
5. **Restrict network access** — bind the agent to a private interface or use a VPN
6. **Pin TLS certificates** — use `--server-ca` in production instead of `--insecure-server-tls`
7. **Coordinate secret rotation** — the agent reads one secret at startup and needs a restart after it changes; see the Eight.ly OS documentation for server-side rotation
