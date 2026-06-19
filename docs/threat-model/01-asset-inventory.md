# Asset Inventory

What the eightly-agent protects, where it lives, and how much it matters. The
agent runs on **every managed Docker host** and proxies the Docker socket, so
its single most consequential property is that a compromise of the agent is a
compromise of the host at **root**.

## Crown jewels (a compromise here = host root)

| ID | Asset | Sensitivity | Location | Owner | Notes |
|----|-------|-------------|----------|-------|-------|
| A-01 | **Docker socket** (`/var/run/docker.sock`) | Critical | Host unix socket, proxied by `internal/proxy` | Host OS | Docker API == host root. The agent's reason to exist and its biggest liability. Never exposed raw; every call passes the deny-list. |
| A-02 | **HMAC shared secret** (`cfg.Secret`) | Critical | Process memory; supplied via `--secret` / `EIGHTLY_AGENT_SECRET` | Operator | Verifies every Bearer token. Disclosure lets an attacker forge tokens and reach A-01. |
| A-03 | **Registration state file** (`state.json`) | High | `cfg.StatePath` (default per-OS), mode 0600 / dir 0700 | Agent uid | Holds the long-lived server bearer `Token` and the TOFU `ServerPin`. Read is refused if perms/ownership are loose (`checkStatePerms`). |
| A-04 | **Agent TLS private key** (`key.pem`) | High | `cfg.DataPath`, mode 0600 (`tightenKeyPerm` repairs drift) | Agent uid | Self-signed server identity for inbound HTTPS. Leak enables agent impersonation / MITM of the EOS↔agent channel. |
| A-05 | **Server bearer token** (`State.Token`) | High | Inside A-03 | EOS server | Long-lived; the agent has no protocol affordance to rotate it (server-side concern — see residual risk). |
| A-06 | **TOFU server-cert pin** (`State.ServerPin`) | High | Inside A-03 | Agent | SPKI SHA-256 of the EOS server leaf, captured first-connect. Trust anchor for outbound calls when no CA is supplied. |

## Live data the agent brokers

| ID | Asset | Sensitivity | Location | Owner | Notes |
|----|-------|-------------|----------|-------|-------|
| A-07 | **Container exec/attach streams** | High | WebSocket bridge (`internal/websocket`) ↔ Docker | Operator | Interactive root-in-container shells. Bridged byte-for-byte; CSWSH + DoS bounds are the only gate after auth. |
| A-08 | **Docker volume file contents** | High | `cfg.DataPath/volumes/<id>/_data` via `internal/browse` | Operator | Read/list/upload/delete/rename inside a volume root. Symlink-escape and path-traversal confinement enforced. |
| A-09 | **Host hardware inventory** | Medium | `internal/hostinfo` (`/proc`, sysfs) | Host OS | PCI + disk enumeration. Minimized on the wire to counts/totals (`MinimalHostInfo`) — full vendor/model strings are dropped. |
| A-10 | **Agent metrics** | Low | `internal/metrics` → `/metrics` (auth-gated, opt-in) | Operator | Prometheus counters. Reveals operational posture (heartbeat failures, WS clients) if exposed. |

## Supporting / infrastructure

| ID | Asset | Sensitivity | Location | Owner | Notes |
|----|-------|-------------|----------|-------|-------|
| A-11 | **Enrollment token** (`cfg.EnrollToken`) | Medium | Process memory / env, sent once at register | Operator | Single-use first-contact secret to the EOS server registration endpoint. |
| A-12 | **Security/audit logs** | Medium | zerolog stdout, `[SECURITY]` + `browse.mutate` lines | Operator | Greppable detection signal for auth failures, proxy denials, browse mutations. No on-box integrity chain (residual risk). |
| A-13 | **Agent binary + supply chain** | High | Built from `cmd/agent`, Go 1.26.4, digest-pinned base images | CI / smashingtags | A tampered binary is total compromise. Pinned toolchain + bases + 0 govulncheck findings are the controls. |
| A-14 | **Config / flags** | Medium | CLI flags + env (`internal/config`) | Operator | Carries A-02, A-11, and the insecure escape hatches. `InsecureServerTLS` is flag-only (never env) by design. |

## Sensitivity legend

- **Critical** — disclosure or tampering yields host root or full agent impersonation.
- **High** — significant confidentiality/integrity loss; a strong step toward Critical.
- **Medium** — operational exposure, info disclosure, or a precursor to a larger attack.
- **Low** — limited blast radius on its own.
