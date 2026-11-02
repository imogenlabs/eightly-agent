# Control Mapping

Every shipped control → the threat it mitigates → the code that implements it. All remediation landed across rounds r1–r5.

| Control | Round | Threat(s) mitigated | Implementing code |
|---------|-------|---------------------|-------------------|
| **Fail-closed auth** (no secret + no opt-out → 503) | r4 | Info disclosure / Elevation (unauth Docker access) | `internal/auth/jwt.go` `Middleware` |
| **HMAC-SHA256 Bearer verify** (constant-time) | r4 | Spoofing (forged token) | `internal/auth/jwt.go` (`hmac.Equal`, issuer + type check, `lifetimeProblem`) |
| **Token bound to this agent; 503 until registered** | — | Spoofing (another agent's token) | `internal/auth/jwt.go` (`sub` check), `internal/server/server.go` `authMiddleware`, `registerWithRetry` |
| **`[SECURITY]` auth event logging** | r4 | Repudiation; detection | `internal/auth/jwt.go` `logSecurityEvent` |
| **Docker request deny-list** (create, exec, update, volume create) | r4 | Elevation / Tampering (host escape) | `internal/proxy/docker.go` `inspectBody`, `checkHostConfig`, `dangerousCaps`, `sensitivePaths`, `allowedSecurityOpt`, `isSafeSysctl`, `checkVolumeOptions` |
| **Swarm and plugin API refusal** | — | Elevation (plugin privileges, Swarm secrets) | `internal/proxy/docker.go` `refusedEndpoint` |
| **Path normalisation before the filter** | — | Tampering (filter bypass via path tricks) | `internal/proxy/docker.go` `dockerPath` |
| **Privileged + host-namespace block** | r4 | Elevation (container escape) | `internal/proxy/docker.go` `isHostNamespace`, `UsernsMode` |
| **Sensitive bind-mount block** (Binds + Mounts) | r4 | Elevation (host FS exposure) | `internal/proxy/docker.go` `isSensitiveHostPath` |
| **Inspection body cap** (over 1 MiB → 413, never truncated; unparsable → 403) | r4 | DoS (memory), Tampering (smuggled config) | `internal/proxy/docker.go` `maxInspectBody`, `denyDangerousCreates` |
| **JSON-encoded denial reason** | r4 | Info disclosure / injection in error echo | `internal/proxy/docker.go` `deny` |
| **CSWSH origin enforcement** (allowlist / same-origin via SNI) | r2 | Spoofing (cross-site WS hijack) | `internal/websocket/websocket.go` `checkOrigin`, `requestHost`, `hostMatches` |
| **Absent-Origin requires token** | r2 | Elevation (browser-shaped unauth WS) | `internal/websocket/websocket.go` `hasToken` |
| **Docker ID validation** | r2 | Tampering (raw-request injection) | `internal/websocket/websocket.go` `validDockerID` |
| **WS DoS bounds** (50-session cap, 64KB frames, 10s write deadline, idle/absolute timeout) | r2 | DoS | `internal/websocket/websocket.go` `maxConcurrentSessions`, `maxFrameBytes`, `writeWait`, `idleTimeout`, `absoluteTimeout`, `bridge` |
| **Bounded Docker dial/handshake** | r2 | DoS (hung backend) | `internal/websocket/websocket.go` `dockerDialTimeout`, `dockerHandshakeTimeout` |
| **Browse confinement** (`..` refused, `os.Root` on the volume) | r3 | Tampering / Elevation (path traversal) | `internal/browse/browse.go` `openPath`, `volumeName` |
| **`O_NOFOLLOW` on get/put** | r3 | Elevation (symlink redirection) | `internal/browse/nofollow_unix.go` `openNoFollow`; `nofollow_windows.go` + `openverified.go` `openVerified` |
| **MkdirAll inside the root** | r3 | Elevation (symlink race on dir create) | `internal/browse/browse.go` put handler (`root.MkdirAll`) |
| **Hidden-path refusal** (any dot segment → 403, every route) | r3 | Info disclosure (hidden secrets) | `internal/browse/browse.go` `hasHiddenSegment` |
| **Regular files only** (pipes, sockets, devices → 400) | — | DoS (hung read), Elevation (device node) | `internal/browse/openverified.go` `checkRegular`, `nofollow_unix.go` `checkFd` |
| **Rename destination re-validation** | r3 | Elevation (rename escape) | `internal/browse/browse.go` rename handler, `validIdentifier` |
| **Upload size cap** (100 MB) | r3 | DoS (volume fill) | `internal/browse/browse.go` `MaxBytesReader` |
| **Mutate audit** (actor/verb/path/result) | r3 | Repudiation | `internal/browse/browse.go` `audit`, `actorFromRequest` |
| **Browse ReadOnly mode** (mutating verbs → 403 + audit) | r3 | Tampering (when enabled) | `internal/browse/browse.go` `ReadOnly`, `rejectReadOnly` |
| **Host info minimization** | r3 | Info disclosure (hardware fingerprint) | `internal/hostinfo/hostinfo.go` `MinimalHostInfo`, `minimize` |
| **Host command timeout + cache** (15 s, 30 s) | — | DoS (hung or repeated host commands) | `internal/hostinfo/hostinfo.go` `commandTimeout`, `cacheTTL`, `runCommand` |
| **TOFU server-cert SPKI pin** | r1 | Spoofing / Tampering (rogue/MITM server) | `internal/register/pin.go` `verifyPinned`, `spkiFingerprint`; `register.go` `BuildServerTLSConfig` |
| **Pin persistence + restart seeding** | r1 | Spoofing (re-capture downgrade) | `internal/register/pin.go` `seedPin`, `setIfEmpty`; `register.go` `StartHeartbeat` |
| **Optional CA chain verify (defense in depth)** | r1 | Tampering (same-CA mis-issue) | `internal/register/register.go` `BuildServerTLSConfig` (RootCAs + VerifyConnection) |
| **`--insecure-server-tls` flag-only escape hatch** | r1 | Info disclosure (silent downgrade) | `internal/config/config.go` (no env override), `register.go` loud warn |
| **https-required server URL** | r1 | Tampering (plaintext orchestration) | `internal/config/config.go` `Validate` |
| **State file perms/owner gate** (0600/0700 + uid) | r1 | Info disclosure (credential leak) | `internal/register/register.go` `checkStatePerms`, `assertPerm`; `fileuid_unix.go` |
| **TLS key perm repair** (tighten to 0600) | r1 | Info disclosure (leaked private key) | `internal/tlsutil/tls.go` `tightenKeyPerm` |
| **Windows access lists** (data folder, state, secret file, TLS key/cert) | — | Info disclosure (local users reading secrets) | `internal/winacl` `Secure`, `ProtectDir`; `cmd/agent/service_windows.go` `protectDataDirs` |
| **Secret file instead of the environment** | — | Info disclosure (secret in env / Machine variable) | `internal/config/secretfile.go` `loadSecretFile`, `ReadSecretFile` |
| **Health check pinned to the agent's own certificate** | — | Spoofing (another listener on the port looks healthy) | `internal/server/healthcheck.go` `pinnedTLSConfig` |
| **Secret-or-explicit-opt-out required; bad boolean env refused** | r1 | Misconfiguration → unauth | `internal/config/config.go` `Validate`, `envOverrideBool` |
| **TLS 1.2+ with restricted cipher suites** | r1 | Tampering (weak transport) | `internal/server/server.go` `srv.TLSConfig` |
| **Security response headers** (HSTS/nosniff/DENY) | r1 | Tampering (clickjack/sniff) | `internal/server/server.go` `headerMiddleware` |
| **Listener DoS bounds** (header timeout, 1 MiB max header) | r1 | DoS (slowloris) | `internal/server/server.go` `ReadHeaderTimeout`, `MaxHeaderBytes` |
| **Go 1.26.10 pinned toolchain** | r5 | Tampering (known CVEs) | `go.mod` (`toolchain go1.26.10`); govulncheck 0 findings |
| **Digest-pinned base images + SHA-pinned Actions** | r5 | Tampering (supply chain) | `Dockerfile`, `.github/workflows/*` |
| **Fork pull requests on GitHub-hosted runners only** | — | Tampering (untrusted code on signing runners) | `.github/workflows/ci.yml`, `pentest.yml`, `codeql.yml` (`runs-on` expressions) |

Every threat enumerated in `04-stride-per-element.md` and every **[BLOCKED]** leaf
in `05-attack-trees.md` maps to a row above. **[RESIDUAL]** leaves are tracked in
`07-residual-risk.md`.
