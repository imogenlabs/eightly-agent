# Asset Inventory

What the eightly-agent protects, where it lives, and how much it matters. The
agent runs on **every managed Docker host** and proxies the Docker socket, so
its single most consequential property is that a compromise of the agent is a
compromise of the host at **root**.

## Crown jewels (a compromise here = host root)

| ID | Asset | Sensitivity | Location | Owner | Notes |
|----|-------|-------------|----------|-------|-------|
| A-01 | **Docker socket** (`/var/run/docker.sock`) | Critical | Host unix socket, proxied by `internal/proxy` | Host OS | Docker API == host root. The agent's reason to exist and its biggest liability. Never exposed raw; only the method + path allow-list passes, and host-reaching bodies and targets are refused. |
| A-02 | **HMAC shared secret** (`cfg.Secret`) | Critical | Process memory; supplied via `--secret` / `EIGHTLY_AGENT_SECRET`, or read from a file named by `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE` (on Windows the service's default, with a protected access list) | Operator | Verifies every Bearer token. Disclosure lets an attacker forge tokens and reach A-01. |
| A-03 | **Registration state file** (`eightly-agent.json`; `state.json` on Windows) | High | `cfg.StatePath` (default: in the data directory), mode 0600 / dir 0700; a protected access list on Windows | Agent uid | Holds the server bearer `Token` and the TOFU `ServerPin`. Read is refused if perms/ownership (or, on Windows, the access list) are loose (`checkStatePerms`, `secureState`). |
| A-04 | **Agent TLS private key** (`key.pem`) | High | `cfg.DataPath`, mode 0600 (`tightenKeyPerm` repairs drift); a protected access list on Windows (`winacl.Secure`), refused if another account keeps access | Agent uid | Self-signed server identity for inbound HTTPS. Leak enables agent impersonation / MITM of the EOS↔agent channel. |
| A-05 | **Server bearer token** (`State.Token`) | High | Inside A-03 | EOS server | Lives 30 days; the agent renews it by re-registering from half-life on. A lapsed token cannot be renewed; the agent must be enrolled again. |
| A-06 | **TOFU server-cert pin** (`State.ServerPin`) | High | Inside A-03 | Agent | SPKI SHA-256 of the EOS server leaf, captured first-connect. Trust anchor for outbound calls when no CA is supplied. |

## Live data the agent brokers

| ID | Asset | Sensitivity | Location | Owner | Notes |
|----|-------|-------------|----------|-------|-------|
| A-07 | **Container exec/attach streams** | High | WebSocket bridge (`internal/websocket`) ↔ Docker | Operator | Interactive root-in-container shells. Bridged byte-for-byte; CSWSH + DoS bounds are the only gate after auth. |
| A-08 | **Docker volume file contents** | High | `<volumes-path>/<id>/_data` (`cfg.VolumesRoot()`, default `/var/lib/docker/volumes`) via `internal/browse` | Operator | Read/list/upload/delete/rename inside a volume root. Symlink-escape and path-traversal confinement enforced; hidden paths and non-regular files refused. |
| A-09 | **Host hardware inventory** | Medium | `internal/hostinfo` (sysfs on Linux; `system_profiler`/`diskutil` on macOS; PowerShell on Windows) | Host OS | PCI + disk enumeration. Minimized on the wire to counts/totals (`MinimalHostInfo`) — full vendor/model strings are dropped. Each command times out after 15 s; the result is cached 30 s. |
| A-10 | **Agent metrics** | Low | `internal/metrics` → `/metrics` (auth-gated, opt-in) | Operator | Prometheus counters. Reveals operational posture (heartbeat failures, WS clients) if exposed. |

## Supporting / infrastructure

| ID | Asset | Sensitivity | Location | Owner | Notes |
|----|-------|-------------|----------|-------|-------|
| A-11 | **Enrollment token** (`cfg.EnrollToken`) | Medium | Process memory; env / flag, or a file named by `--enroll-token-file` / `EIGHTLY_ENROLL_TOKEN_FILE` (on Windows the service's default, with a protected access list); sent once at register | Operator | Single-use first-contact secret to the EOS server registration endpoint. A refused token (401/403/410) is not retried. |
| A-12 | **Security/audit logs** | Medium | zerolog stdout, `[SECURITY]` + `browse.mutate` lines | Operator | Greppable detection signal for auth failures, proxy denials, browse mutations. No on-box integrity chain (residual risk). |
| A-13 | **Agent binary + supply chain** | High | Built from `cmd/agent`, Go 1.26.11, digest-pinned base images | CI / maintainers | A tampered binary is total compromise. Pinned toolchain + bases + 0 govulncheck findings are the controls. |
| A-14 | **Config / flags** | Medium | CLI flags + env (`internal/config`) | Operator | Carries A-02, A-11, the insecure escape hatches and `--allowed-bind-roots`, which decides which host folders containers may bind. `InsecureServerTLS` is flag-only (never env) by design. |

## Sensitivity legend

- **Critical** — disclosure or tampering yields host root or full agent impersonation.
- **High** — significant confidentiality/integrity loss; a strong step toward Critical.
- **Medium** — operational exposure, info disclosure, or a precursor to a larger attack.
- **Low** — limited blast radius on its own.
