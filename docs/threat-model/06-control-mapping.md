# Control Mapping

Every shipped control → the threat it mitigates → the code that implements it. All remediation landed across rounds r1–r5.

| Control | Round | Threat(s) mitigated | Implementing code |
|---------|-------|---------------------|-------------------|
| **Fail-closed auth** (no secret + no opt-out → 503) | r4 | Info disclosure / Elevation (unauth Docker access) | `internal/auth/jwt.go` `Middleware` |
| **HMAC-SHA256 Bearer verify** (constant-time) | r4 | Spoofing (forged token) | `internal/auth/jwt.go` (`hmac.Equal`, issuer + type check, `lifetimeProblem`) |
| **Token bound to this agent; 503 until registered** | — | Spoofing (another agent's token) | `internal/auth/jwt.go` (`sub` check), `internal/server/server.go` `authMiddleware`, `registerWithRetry` |
| **`[SECURITY]` auth event logging** | r4 | Repudiation; detection | `internal/auth/jwt.go` `logSecurityEvent` |
| **Docker endpoint allow-list** (method + path; everything else 403) | — | Elevation (build, commit, export, image load/push, Swarm, plugins, any new endpoint) | `internal/proxy/routes.go` `routes`, `matchRoute`; `filter.go` `ServeHTTP` |
| **Path normalisation before the filter** | — | Tampering (filter bypass via path tricks) | `internal/proxy/docker.go` `dockerPath`; the decoded path is what is forwarded (`NewDockerProxy` `Rewrite`) |
| **Host-config checks** on create, update and exec bodies (privileged, private namespaces only, default caps only, VolumesFrom, devices, SecurityOpt allow-list, masked paths, annotations, runtime, sysctls, CgroupParent, OomKillDisable, tmpfs) | r4 | Elevation (container escape) | `internal/proxy/policy.go` `checkHostConfig`, `privateNamespace`, `checkNetwork`, `allowedCaps`, `allowedSecurityOpt`, `isSafeSysctl`, `isSensitiveTmpfsTarget` |
| **Bind roots** (host paths only under `--allowed-bind-roots`, default none; system folders refused as roots) | — | Elevation (host FS exposure) | `internal/proxy/policy.go` `checkBind`, `checkMount`, `checkBindSource`, `rootFor`; `internal/config/config.go` `BindRoots`, `systemPaths` |
| **Symlink check below a bind root** (at create and at start, when the agent runs on the host) | — | Elevation (symlink planted under a root) | `internal/proxy/filter.go` `checkSymlinks`, `checkStartMounts`, `seesHostFS` |
| **Volume option and named-volume checks** | — | Elevation (host path through a local volume) | `internal/proxy/policy.go` `checkVolumeOptions`, `checkNamedVolume` |
| **Network driver checks** (create: bridge/macvlan/ipvlan; connect: no host-driver network) | — | Elevation (host network namespace) | `internal/proxy/filter.go` `checkBody`; `policy.go` `checkNetwork` |
| **Target-container checks** for exec, attach, exec start, archive upload and the WebSocket console; checked full ID forwarded | — | Elevation (getting inside a container that reaches the host) | `internal/proxy/filter.go` `checkTargetContainer`, `checkExecStart`, `checkPrivateRead`, `isSelf`, `selfContainerIDs`; `target.go` `TargetChecker`; `internal/websocket/websocket.go` `Targets`, `refuse` |
| **Inspect redaction** (`EIGHTLY_*`, all env of the agent and socket holders, secret flags) | — | Info disclosure (agent secret through inspect) | `internal/proxy/redact.go` `redactInspect`, `redactContainerJSON`, `redactArgs` |
| **Image import refusal** (`fromSrc`) | — | Elevation / SSRF (daemon fetches a caller's URL or tarball) | `internal/proxy/filter.go` `ServeHTTP` (`checkImagePull`) |
| **Inspection body cap** (over 1 MiB → 413, never truncated; unparsable → 403) | r4 | DoS (memory), Tampering (smuggled config) | `internal/proxy/filter.go` `readBody`, `maxInspectBody`, `checkBody` |
| **JSON-encoded denial reason** | r4 | Info disclosure / injection in error echo | `internal/proxy/docker.go` `deny`; `internal/websocket/websocket.go` `refuse` |
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
| **Secret and enrollment token files instead of the environment** | — | Info disclosure (secret or token in env / Machine variable) | `internal/config/secretfile.go` `loadSecretFile`, `ReadSecretFile`, `loadEnrollTokenFile` |
| **Refused enrollment not retried; unusable or foreign state refused** | — | Spoofing / availability (identity replaced, token replayed) | `internal/register/register.go`, `internal/server/server.go` (`BackupState` for a deliberate move to another server) |
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
