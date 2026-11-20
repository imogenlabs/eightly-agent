# Trust Boundaries

The agent sits between three parties: the **EOS server** (which orchestrates it),
the **operator/UI traffic** that reaches its HTTP API, and the **host's Docker
daemon + filesystem** that it brokers. Each crossing is a trust boundary with its
own protocol, authentication, and gating controls.

## Boundary map

| # | Boundary | Direction | Protocol | Authentication | Gating controls |
|---|----------|-----------|----------|----------------|-----------------|
| TB-1 | **Internet/LAN → Agent HTTP API** | inbound | HTTPS (TLS 1.2+, restricted cipher suites) | HMAC-SHA256 Bearer token (`internal/auth`): issuer, type, `iat`/`exp` at most 30 days apart, `sub` = this agent | Fail-closed middleware; 503 until the agent knows its registered ID; security headers (HSTS/nosniff/DENY); `ReadHeaderTimeout`, `MaxHeaderBytes 1 MiB` |
| TB-2 | **API caller → Docker socket** | inbound→host | HTTP-over-unix (named pipe on Windows) reverse proxy (`internal/proxy`) | Inherited from TB-1 (auth before proxy) | **Allow-list** of method + path (`routes.go`); everything else 403. Container create, update, exec create, volume create and network create bodies checked: privileged, non-private namespaces, capabilities beyond Docker's default set, host paths outside `--allowed-bind-roots` (default none), `VolumesFrom`, devices, SecurityOpt outside an allow-list, masked/read-only path overrides, annotations, other runtimes, non-namespaced sysctls, CgroupParent, OomKillDisable, sensitive tmpfs, named volumes with host-path options, network drivers. Exec, attach, exec start and archive upload check the target container and forward its full ID. Inspect responses redacted. Bodies over 1 MiB refused (413), unparsable bodies refused (403); path cleaned and every version prefix stripped |
| TB-3 | **Browser/UI → WebSocket exec/attach** | inbound | WSS upgrade → raw Docker hijack (`internal/websocket`) | Inherited from TB-1 + **CSWSH origin check** | The proxy's target-container check (`proxy.TargetChecker`) before the upgrade, attach under the checked full ID; Origin allowlist / strict same-origin (orthogonal to auth); frame-size cap, 10 s write deadline, idle/absolute timeouts, 50-session cap, bounded Docker dial/handshake |
| TB-4 | **API caller → Volume files** | inbound→host FS | HTTP (`internal/browse`) | Inherited from TB-1 | `os.Root` confinement to the volume directory; `..` refused; hidden paths refused (403); `O_NOFOLLOW`; regular files only; identifier validation; optional ReadOnly; mutate audit |
| TB-5 | **API caller → Host hardware** | inbound→host | HTTP (`internal/hostinfo`) | Inherited from TB-1 | Response minimization (counts/totals only, no fingerprints); 15 s command timeout; 30 s cache |
| TB-6 | **Agent → EOS server** | outbound | HTTPS register/heartbeat (`internal/register`) | Enroll token (first), then server bearer token | **TOFU SPKI pin** enforced every dial; optional CA chain verify; https-required (`Validate`) |
| TB-7 | **Agent → Docker daemon (internal)** | local | HTTP-over-unix, or a named pipe on Windows (`internal/dockerdial`) | none (local socket) | Bounded dial timeouts; the socket file is the trust root |
| TB-8 | **CI/Supply chain → Agent binary** | build-time | Go modules, pinned base images | module checksums, image digests | Go 1.26.11 pinned, digest-pinned bases, govulncheck (0 findings), SHA-pinned Actions; pull requests from forks run only on GitHub-hosted runners, never on the self-hosted signing runners |
| TB-9 | **Operator → Config** | startup | CLI flags + env | OS process trust | `InsecureServerTLS` is flag-only; `Validate()` requires secret-or-explicit-opt-out and https server URL, refuses a secret and a secret file (or an enrollment token and a token file) together, refuses a boolean variable that is not true/false (or 1/0, yes/no, on/off), and refuses a bind root that is, holds or is inside a system directory; an unknown command-line word exits 2 |

## Trust-level summary

```
  UNTRUSTED                  SEMI-TRUSTED                 TRUSTED (host root)
 ┌──────────┐  TB-1 auth   ┌────────────┐  TB-2/3/4/5  ┌──────────────────┐
 │ Internet │ ───────────▶ │   Agent    │ ───────────▶ │ Docker socket A-01│
 │ / LAN /  │   TB-3 CSWSH │  HTTP API  │  allow-list  │ Volume FS    A-08 │
 │ Browser  │              │ (this box) │   confine    │ Host HW      A-09 │
 └──────────┘              └─────┬──────┘              └──────────────────┘
                                 │ TB-6 TOFU pin
                                 ▼
                          ┌────────────┐
                          │ EOS server │  (orchestrator, semi-trusted —
                          └────────────┘   holds A-05 token, pinned via A-06)
```

## Design notes on the boundaries

- **Auth gates *who*, the allow-list gates *what*.** TB-1 establishes identity;
  TB-2 assumes a caller may still be malicious (or compromised): it forwards only
  listed endpoints, refuses host-escape payloads on them, and refuses getting
  inside a container that already reaches the host. TB-3 applies the same target
  check. Defense in depth — neither alone is trusted.
- **Origin is orthogonal to auth (TB-3).** The CSWSH check runs in every branch;
  enabling auth never disables it. A browser with stolen credentials still cannot
  drive the exec socket cross-origin.
- **TB-6 trust is first-use (TOFU), not PKI.** Self-signed appliance servers can't
  be vouched for by a public CA, so the first-seen leaf SPKI becomes the anchor and
  is enforced on every later dial — persisted across restarts so re-capture can't
  silently downgrade trust.
- **The host boundary (TB-2/4/5/7) is the one that cannot be undone.** Everything
  upstream is layered specifically because a slip here is root.
