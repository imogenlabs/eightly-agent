# STRIDE Per Element

STRIDE analysis for each DFD element. "Controls" cite the gauntlet round (r1–r5)
and the implementing package. Residual is the rating *after* the shipped control.

## Agent HTTP API surface (TB-1, `internal/server` + `internal/auth`)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **S**poofing | Attacker forges a Bearer token to impersonate the server | Low | Critical | r4 HMAC-SHA256 verify, `hmac.Equal` constant-time compare, issuer + expiry check | Low |
| **T**ampering | MITM alters API traffic | Low | High | r1 TLS 1.2+ with restricted GCM/ChaCha cipher suites, HSTS | Low |
| **R**epudiation | Caller denies a privileged action | Medium | Medium | r4 `[SECURITY]` auth logs, r3 `browse.mutate` audit with actor `sub` | Medium (no on-box log integrity chain) |
| **I**nfo disclosure | Misconfigured agent serves Docker unauthenticated | Low | Critical | r4 **fail-closed**: no secret + no opt-out → 503, not open | Low |
| **D**enial of service | Slowloris / oversized headers exhaust the listener | Medium | Medium | r1 `ReadHeaderTimeout` 10s, `MaxHeaderBytes` 1 MiB, `IdleTimeout` | Low |
| **E**levation | Unauth caller reaches a host-reaching handler | Low | Critical | r4 auth wraps every host handler; fail-closed default | Low |

## Docker proxy (TB-2, `internal/proxy`)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **T**ampering | Crafted create body mounts host root / sets privileged | Medium | Critical | r4 deny-list: `Privileged`, host PID/IPC/Net/Userns, dangerous caps (ALL, SYS_ADMIN, SYS_PTRACE…), sensitive binds (`/`, `/etc`, `/proc`, docker.sock) | Low |
| **E**levation | Container escape to host root via Docker API | Medium | Critical | r4 deny-list (above) + auth (TB-1) | **Medium** — deny-list is non-exhaustive on `Devices`, `SecurityOpt`, `Sysctls` (residual risk) |
| **D**enial of service | Huge create body buffered for inspection | Low | Medium | r4 `maxInspectBody` 1 MiB `LimitReader` cap | Low |
| **I**nfo disclosure | Denial reason echoes attacker-supplied path/cap | Low | Low | r4 reason JSON-encoded (not string-concat) before write | Low |

## WebSocket exec/attach (TB-3, `internal/websocket`)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **S**poofing | Cross-site WebSocket hijack drives a root container shell | Medium | Critical | r2 CSWSH origin check (allowlist or strict same-origin via TLS SNI), runs in every branch | Low |
| **T**ampering | Malformed container/exec ID injects into the raw Docker request | Low | High | r2 `validDockerID` regex `^[a-zA-Z0-9]{12,64}$` before building the raw request | Low |
| **R**epudiation | User denies opening an exec session | Medium | Medium | r4 auth logs upstream; ws upgrade has no per-session audit record | Medium |
| **D**enial of service | Many idle/large sessions exhaust agent or Docker | Medium | High | r2 50-session cap, 64KB frame limit, 15min idle + 8h absolute timeout, bounded Docker dial(5s)/handshake(10s), ping/pong liveness | Low |
| **E**levation | Browser without token opens exec via absent Origin | Low | Critical | r2 absent Origin allowed **only** with a valid token (`hasToken`) | Low |

## Browse / volume files (TB-4, `internal/browse`)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **T**ampering | `../` path traversal writes outside the volume | Medium | High | r3 lexical `filepath.Rel` check + symlink-resolved containment (`containedPath`) | Low |
| **E**levation | Symlink planted at final component redirects write to host path | Medium | High | r3 `O_NOFOLLOW` on get/put + re-validate after `MkdirAll` + deepest-existing-ancestor resolution | Low |
| **I**nfo disclosure | Listing/downloading hidden dotfiles (e.g. `.env`) | Medium | Medium | r3 dotfiles skipped in `ls` and refused on `get` | Low |
| **R**epudiation | Operator denies deleting a file | Medium | Medium | r3 `browse.mutate` audit (actor/verb/path/result/remote) on put/delete/rename | Low |
| **D**enial of service | Huge upload fills the volume | Low | Medium | r3 `MaxBytesReader` 100 MB cap on put | Low |
| **E**levation | Rename target escapes volume via crafted `newName` | Low | High | r3 `validIdentifier` + reject `..` / `/` + destination re-validated through `containedPath` | Low |

## Host info (TB-5, `internal/hostinfo`)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **I**nfo disclosure | Full PCI/disk fingerprint aids host targeting | Medium | Low | r3 `MinimalHostInfo` — counts + total GB only, vendor/model/address dropped | Low |

## Agent → EOS server (TB-6, `internal/register` + `pin.go`)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **S**poofing | Rogue server impersonates EOS to harvest heartbeats / push state | Low | High | r1 TOFU SPKI pin captured first-connect, enforced every dial, seeded from persisted state across restarts | Low |
| **T**ampering | MITM under the same CA swaps the leaf cert | Low | High | r1 pin is leaf-SPKI, so even a same-CA mis-issued cert trips it (defense in depth over chain verify) | Low |
| **I**nfo disclosure | `--insecure-server-tls` disables verification + pinning | Low | High | r1 escape hatch is flag-only (never env), loud `Warn` log, https required by `Validate()` otherwise | Low (operator opt-in) |
| **S**poofing | Stolen long-lived server token replayed | Low | High | r1 token stored 0600, perms/owner enforced on load | **Medium** — no agent-side token rotation (server-side residual) |

## State + key files at rest (A-03/04/05/06)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **I**nfo disclosure | Another user reads the token/pin or TLS key | Low | Critical | r1 `checkStatePerms` (0600 file / 0700 dir + uid match, fail-closed), r1 `tightenKeyPerm` repairs key mode | Low |
| **T**ampering | Attacker rewrites the pin to accept a rogue server | Low | High | r1 ownership + perm gate on load; pin only set-if-empty in memory | Low |

## Supply chain (TB-8, `cmd/agent` + CI)

| Threat | Description | Likelihood | Impact | Controls | Residual |
|--------|-------------|-----------|--------|----------|----------|
| **T**ampering | Vulnerable dependency or poisoned base image | Low | Critical | r5 Go 1.26.4 pinned, digest-pinned base images, govulncheck 0 findings, SHA-pinned GitHub Actions | Low |
