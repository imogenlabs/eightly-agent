# Security Gauntlet Verdict (eightly-agent)

> **Note, 2026-11-02 (release 2.1.0):** this is a historical record of the June 2026 audit and its findings are left as written. Since then the Docker proxy has become an allow-list of methods and paths, host bind mounts are limited to `--allowed-bind-roots` (none by default), exec, attach, file upload and the WebSocket console check the container they reach, and container inspect redacts the agent's secrets. Where this record says "deny-list", read the current design in [SECURITY.md](../../SECURITY.md) and the [threat model](../threat-model/06-control-mapping.md).

**Status at the audit date:** PASS — remediation independently re-proven by sabotage tests.
**Audit:** Eight.ly remote Docker agent security gauntlet, phases 0–11
**Verdict date:** 2026-06-19
**Auditor:** internal security gauntlet (audit → remediate → validate-by-breaking → adversarial loop)

---

## 1. Scope

| Field | Value |
|-------|-------|
| Repository | `imogenlabs/eightly-agent` |
| Component | Eight.ly remote Docker host agent (clean-room v2) — Go single binary |
| Reviewed at | `c3f810a43dd1679c4dd4ca4b7b3eb3d0638bc3a5` |
| Remediation history | Five remediation commits covering TLS/registration, WebSocket, auth/proxy, browsing/host information and supply chain, combined at the reviewed commit |
| Go toolchain | `go 1.26.4` (pinned in `go.mod` and every workflow) |
| Owned target | **local only** — a branch deployed on our own lab hosts; no third-party system was touched |
| Authorization | Owner-operated infrastructure (Eight.ly OS dev/prod VMs + agent test host); source review of the repository as it stood in June 2026 |

**Surface in scope.** The agent exposes a small HTTP/WebSocket surface over TLS: bearer-token auth middleware (`internal/auth`), a Docker socket reverse proxy (`internal/proxy`), a per-volume file browser (`internal/browse`), server enrollment + heartbeat with TOFU cert pinning (`internal/register`, `internal/tlsutil`), a container-attach WebSocket bridge (`internal/websocket`), host-info reporting (`internal/hostinfo`), `/metrics`, and config (`internal/config`). The agent runs adjacent to the Docker daemon, so any auth/proxy weakness is effectively host-root exposure — the threat model treats every finding at that blast radius.

---

## 2. Severity summary

| Severity | Count |
|----------|-------|
| Critical | 0 |
| **High** | **4** |
| **Medium** | **8** |
| **Low / Info** | **8** |
| **Total** | **20** |

All 20 findings are dispositioned **Fixed** (F1–F9 carry the control-bearing fixes; the remaining Low/Info items were folded into the same five remediation commits — see §3). No finding is left Open or Accepted-as-risk. Residual (design-level, not a defect) risks are catalogued in §5.

---

## 3. Per-finding disposition

The four High findings are the ones that, unfixed, hand an attacker host-root or a same-site credential-replay path. Each fix below ships with a **sabotage-proven** regression test: the test was confirmed to go **red** when the fix is reverted, so CI re-opening the vuln fails the PR (gated by `.github/workflows/pentest.yml`, this change).

| ID | Sev | Finding | Control (the fix) | Where / proof | Commit |
|----|-----|---------|-------------------|---------------|--------|
| **F1** | High | **Auth fail-open.** `Middleware` did `if secret == "" { return next }` — an empty/misconfigured secret silently served the Docker socket (host root) unauthenticated. | **Fail-closed.** Empty secret + no explicit `insecureNoAuth` opt-in now returns `503` for every request and never calls the next handler. No-auth is opt-in only. | `internal/auth/jwt.go`; sabotage test `TestEmptySecretWithoutOptInFailsClosed` (+ `…RejectsEvenWithToken`) in `jwt_test.go` | `93fda74` |
| **F2** | High | **CSWSH on the attach WebSocket.** `CheckOrigin` returned `true` unconditionally when auth was enabled, so a logged-in operator's browser could be driven cross-site into a container shell. Same-origin check also trusted the spoofable `Host` header. | **Origin enforced even with auth + empty allowlist;** SNI (TLS `ServerName`) is authoritative over `Host`; non-web schemes rejected; no-Origin only passes with a bearer token; allowlist honored. | `internal/websocket/websocket.go`; `TestNewUpgrader_AuthEnabled_EmptyAllowlist_RejectsCrossOrigin`, `TestCheckOrigin_PrefersTLSServerNameOverHost`, `TestCheckOrigin_RejectsNonWebScheme` | `fc74539` |
| **F3** | High | **Docker-proxy privilege escalation.** The proxy forwarded `container create`/`exec` verbatim — a caller could request `Privileged`, host PID/IPC/network, `CAP_SYS_ADMIN`, or bind-mount `/`, `/etc`, `/proc`, `docker.sock` and break out to the host. | **Deny-list on create/exec bodies** (`denyDangerousCreates`): rejects `Privileged`, host namespaces, dangerous caps (incl. `CAP_`/`ALL`), and sensitive bind/mount sources with `403`; prefix-match is exact (`/etcfoo` is allowed), read traffic untouched. **Extended:** also rejects confinement-disabling `SecurityOpt` (`seccomp/apparmor=unconfined`, `label:disable`), host `Devices`, non-namespaced `Sysctls`, `CgroupParent`, and tmpfs over sensitive paths. | `internal/proxy/docker.go`; `TestProxy_PrivilegedCreate_Rejected`, `TestProxy_DangerousFields_Rejected`, `TestProxy_ExecPrivileged_Rejected`, `TestProxy_SafeBind_PassesThrough`, `TestProxy_PathPrefixNotOverMatched`, `TestProxy_SecurityOpt_Rejected`, `TestProxy_Devices_Rejected`, `TestProxy_Sysctls_Rejected`, `TestProxy_CgroupParent_Rejected`, `TestProxy_Tmpfs_Rejected` | `93fda74` (+ deny-list extension) |
| **F4** | High | **File-browser symlink escape (read + write).** A symlink planted inside a volume let `get`/`put`/`delete`/`rename` resolve outside the volume root — arbitrary host read and arbitrary write/overwrite. | **Confinement + `O_NOFOLLOW`.** Every path is re-validated with `containedPath` (incl. after `MkdirAll`, which can follow a symlink); reads/writes use `syscallNoFollow` so a final-component symlink cannot redirect I/O. | `internal/browse/browse.go`, `nofollow_unix.go`/`_windows.go`; `TestSymlinkEscapeRejected`, `TestSymlinkEscapeWriteRejected` (covers put-overwrite, put-new, delete, rename-dest escape) | `c3f810a` |
| **F5** | Med | **No TLS server pinning** between agent and server (MITM/impersonation after enrollment). | **TOFU SPKI pinning.** First register captures `sha256/<base64-SPKI>`; subsequent heartbeats reject any cert with a different SPKI (`pin mismatch`); pin survives restart via persisted state; `--insecure` is an explicit escape hatch. | `internal/register/register.go`, `pin.go`, `internal/tlsutil/tls.go`; `TestPinnedConfig_RejectsDifferentCert`, `TestSeedPin_EnforcesPersistedPinAcrossRestart`, `TestRegister_CapturesPin_OnFirstConnect` | `bd57a41` |
| **F6** | Med | **WebSocket DoS / unbounded frames** on the attach bridge. | **64 KB read limit** (parity with the OS terminal) — an oversized frame tears the session down. | `internal/websocket/websocket.go`; `TestBridge_ReadLimitClosesOnOversizedFrame` (asserts `maxFrameBytes == 65536`) | `fc74539` |
| **F7** | Med | **State-file secret exposure.** State (token, pin) could be read from a world-/group-readable file or directory. | **Permission gate.** `LoadState` refuses a state file looser than `0600` or a parent dir looser than `0700`. | `internal/register/register.go` + `fileuid_*.go`; `TestLoadState_RejectsBroadFilePerms`, `…RejectsBroadDirPerms`, `…AcceptsTightPerms` | `bd57a41` |
| **F8** | Med | **Token replay / no expiry enforcement.** Tokens without `exp` lived forever; expired tokens weren't rejected. | **Expiry enforced.** Expired `exp` → `401`; absent `exp` still accepted for backward compat (documented residual, §5); wrong issuer → `401`. | `internal/auth/jwt.go`; `TestExpiredToken`, `TestWrongIssuer`, `TestTokenWithoutExpStillWorks` | `93fda74` |
| **F9** | Med | **Browser-volume mutation without audit / no read-only mode + unbounded upload.** | **Mutation auditing + read-only mode + upload cap.** Mutating ops emit audit records; read-only mode returns `403` on `put`/`delete`/`rename` while reads pass; oversize uploads rejected; dotfiles refused. | `internal/browse/browse.go`; `TestReadOnlyRejectsMutations`, `TestPutRejectsOversizeUpload`, `TestGetRefusesDotfile`, `TestLsSkipsDotfiles` | `c3f810a` |
| EA — Med (×3) | Med | Docker-ID injection on attach (`id` could carry CRLF/path separators); host-info over-disclosure; config defaults hardening. | `validDockerID` allowlist (12/64-char hex only); `hostinfo` minimized to non-sensitive fields; config fail-safe defaults. | `internal/websocket` (`TestValidDockerID`), `internal/hostinfo`, `internal/config` (`config_test.go`) | `fc74539`, `c3f810a`, `bd57a41` |
| EA — Low/Info (×8) | Low/Info | Supply-chain + hardening hygiene: Go bumped to `1.26.4` (0 reachable govulncheck), digest-pinned Docker base images, SHA-pinned Actions, `docker.sock:ro` deploy guidance, `[SECURITY]`-tagged audit log lines, error-verbosity reduction, malformed-JSON passthrough (don't 500 on the daemon's behalf), default-port-equivalence in origin checks. | Applied across the r1–r5 commits; enforced going forward by `ci.yml` (gosec/govulncheck/staticcheck/golangci-lint), `scorecard.yml`, `codeql.yml`, and this `pentest.yml`. | `8fc4a2b` + r1–r4 |

> F1–F9 are the nine control-bearing findings (4 High + 5 Med). The three remaining Med items and the eight Low/Info items were remediated in the same five commits and are listed above for completeness so the count reconciles to **4 High / 8 Med / 8 Low-Info = 20**.

---

## 4. What was validated, and how

Three independent evidence classes back this verdict. Where a finding is only static or only sabotage-tested, that is stated honestly in §6.

**Sabotage-test (red-on-vuln) — the strongest class.** For every High and the pin/DoS/perms/expiry Mediums, the regression test was confirmed to fail when the fix is reverted, then pass with it in place. These are the assertions named in §3 and now gated by `pentest.yml`. Local run on this branch (`go 1.26.4`):

```
ok  internal/auth      (fail-closed auth, expiry, issuer)
ok  internal/proxy     (deny-list create/exec, safe-bind passthrough)
ok  internal/browse    (symlink read+write escape, read-only, oversize, dotfiles)
ok  internal/register  (TOFU pin capture + mismatch + restart, state perms)
ok  internal/websocket (CSWSH/origin/SNI, 64 KB read limit, docker-ID allowlist)
```

(`internal/websocket`'s `TestBridge_ReadLimitClosesOnOversizedFrame` binds a unix socket under `$TMPDIR`; on macOS the default `/var/folders` path exceeds the `sun_path` limit and the test skips/errors with `bind: invalid argument` — it passes on the Linux self-hosted CI runners and with a short `TMPDIR`. This is a test-harness artifact, not a control gap.)

**Static / SAST.** `gosec` (severity ≥ medium), `govulncheck` (**0 reachable** vulns on the `1.26.4` toolchain), `staticcheck`, `golangci-lint`, GitHub CodeQL, and OpenSSF Scorecard run in CI. These backed the supply-chain and injection-class Low/Info findings.

**Live (owned target).** Remediation was exercised against the agent deployed on our own lab host alongside Eight.ly OS — enrollment + pin capture, heartbeat, the auth middleware, and the Docker proxy were observed end-to-end. No third-party system was in scope (owner's standing rule: dynamic testing only against infrastructure we own).

**Continuous enforcement (this change).** `.github/workflows/pentest.yml` adds a focused gate on PRs to `main`/`dev`:
- `security-regression` — `go test -race` across the five hardened packages (the sabotage suite).
- `govulncheck-gate` — fails on **any reachable** vulnerability.
- `gosec-gate` — `gosec -severity medium` (G301/G302 file-mode rules excluded with justification; all injection/path rules active).

Least-privilege `permissions: contents: read`; every action SHA-pinned to the same digests as the existing workflows.

---

## 5. Residual risks (by design, not defects)

These are accepted design properties of an appliance agent that sits next to the Docker socket. They are tracked in **`docs/threat-model/07-residual-risk.md`** (threat-model deliverable, phase 9); summarized here:

1. **Tokens without `exp` are accepted** for backward compatibility (F8). Operators who want hard expiry must issue `exp`-bearing tokens; rotation is operator-driven (no server-pushed revocation). Server-pushed rotation/revocation is tracked as open, server-side work — not an in-agent defect.
2. **TOFU, not a CA.** The first handshake is trusted on first use. A MITM present *during initial enrollment* can pin its own cert. Mitigation: pre-seed the pin or enroll over a trusted path; `--server-ca` allows pinning to a CA instead.
3. **Docker-proxy deny-list, not allow-list (extended in a follow-up).** The deny-list now blocks the known break-out vectors — privileged, host namespaces, dangerous caps, sensitive binds, **plus confinement-disabling `SecurityOpt`, host `Devices`, non-namespaced `Sysctls`, `CgroupParent`, and tmpfs over sensitive paths**. The remaining residual is *structural, not accepted-by-design*: a deny-list can never be exhaustive, so a future Docker API field conferring equivalent privilege would pass until added. Migration to an upstream **socket-proxy / request allow-list** is the tracked follow-up. The proxy is defense-in-depth; the agent should still run non-root under systemd hardening and be network-isolated (note: `docker.sock:ro` is near-cosmetic — the flag marks the socket file, not the API, so it is **not** a real control).
4. **File-browser read-only is wired.** `--browse-read-only` / `EIGHTLY_BROWSE_READ_ONLY` (default off) now flows from config through `server.go` into `browse.Handler`; deployments that don't need writes fail upload/delete/rename closed with `403`. Production defaults mutable for the file-manager UX — the residual is the operator's choice to leave writes on.
5. **Admin == host owner.** Attach gives an authenticated admin a container shell by design (appliance model, like Unraid/TrueNAS/Proxmox). There is no in-agent privilege separation; isolation is at the deployment boundary.
6. **`--insecure` escape hatches** (skip TLS verify / no-auth) exist for bootstrapping and are explicitly opt-in; they are footguns if shipped enabled.
7. **gosec G301/G302** (browse volume dir `0755` / file `0644`) are intentional for Docker-volume compatibility and carry inline `#nosec` justifications; note the annotations don't fully suppress under gosec v2.22.4 (rule-ID/mode mismatch), so the gate excludes those two rules explicitly rather than relying on the annotation. Tightening perms or fixing the annotations is a follow-up hygiene item, not a security gap.

---

## 6. What was NOT tested (honesty box)

- **No deep fuzz corpus.** Path/origin/JSON parsing was covered by hand-written boundary cases and sabotage tests, not a long-running `go test -fuzz` campaign with a persisted corpus. The deny-list and `containedPath` logic would benefit from continuous fuzzing.
- **No live multi-host enrollment-race test.** The TOFU pin capture and the `503` fail-closed path were unit/integration tested, but a concurrent multi-agent enrollment stampede (token issuance + pin capture under contention) was not exercised against a live server.
- **No live CSWSH browser exploit.** The CSWSH fix is proven by the origin/SNI unit assertions; an end-to-end attacker-page-drives-a-real-browser PoC was not run (the unit proof is authoritative for the `CheckOrigin` contract, but the full browser path is unexercised).
- **No load/DoS soak.** The 64 KB frame cap is asserted functionally; sustained connection-flood / slow-loris behavior under real concurrency was not load-tested.
- **Docker daemon version drift.** Validation used a single daemon API version; the deny-list's field coverage across the full range of supported API versions was not swept.
- **Windows paths** are compiled (`nofollow_windows.go`, `fileuid_windows.go`) but the agent is Linux-first; the Windows symlink/perm behavior was not validated on a Windows host.

---

## 7. Rollback path

All remediation is additive and self-contained per package; CI gates are new files only.

- **Revert the CI gate (this change):** `git rm .github/workflows/pentest.yml docs/audit/security-gauntlet-verdict.md` — the existing `ci.yml`/`scorecard.yml`/`codeql.yml` are untouched and continue to run.
- **Revert a single control:** each fix is one commit (`bd57a41`, `fc74539`, `93fda74`, `c3f810a`, `8fc4a2b`) — `git revert <sha>` backs out that control's source + its tests together. Reverting re-opens the corresponding finding **and turns the sabotage test red**, which is the intended tripwire.
- **Full rollback:** reset the integration branch to its pre-gauntlet base (`6a684f4`, the last commit before `gauntlet/r5`).
- **Deployed binary rollback:** redeploy the prior agent build; the agent is a single static binary, so rollback is a binary swap + service restart (no migrations, no schema).

---

## 8. Gauntlet phase map (0–11)

| Phase | Activity |
|-------|----------|
| 0 | Scope + owned-target confirmation (local lab only) |
| 1–2 | Source review + threat model of the agent surface |
| 3 | Finding enumeration → 4 High / 8 Med / 8 Low-Info (F1–F9 control-bearing) |
| 4–8 | Remediation waves r1–r5 (TLS/register, websocket, auth/proxy, browse/hostinfo, supply-chain) |
| 9 | Threat-model + residual-risk write-up (`docs/threat-model/07-residual-risk.md`) |
| 10 | **Continuous CI gate** — `pentest.yml` (sabotage suite + govulncheck + gosec) ← this deliverable |
| 11 | **Verdict** — this document; independent re-proof that every "fixed" claim fails closed when reverted |

**Bottom line:** every High and the security-bearing Mediums are re-proven by tests that go red the moment the fix is undone, govulncheck is clean on `1.26.4`, and the gate now blocks regressions on every PR. The agent is cleared to go public, with the §5 residual risks and §6 untested areas documented for the operator.
