# Control Mapping

Every shipped control → the threat it mitigates → the code that implements it. All remediation landed across rounds r1–r5.

| Control | Round | Threat(s) mitigated | Implementing code |
|---------|-------|---------------------|-------------------|
| **Fail-closed auth** (no secret + no opt-out → 503) | r4 | Info disclosure / Elevation (unauth Docker access) | `internal/auth/jwt.go` `Middleware` |
| **HMAC-SHA256 Bearer verify** (constant-time) | r4 | Spoofing (forged token) | `internal/auth/jwt.go` (`hmac.Equal`, issuer + `Exp` check) |
| **`[SECURITY]` auth event logging** | r4 | Repudiation; detection | `internal/auth/jwt.go` `logSecurityEvent` |
| **Docker create/exec deny-list** | r4 | Elevation / Tampering (host escape) | `internal/proxy/docker.go` `inspectCreateBody`, `dangerousCaps`, `sensitivePaths` |
| **Privileged + host-namespace block** | r4 | Elevation (container escape) | `internal/proxy/docker.go` `isHostNamespace`, `UsernsMode` |
| **Sensitive bind-mount block** (Binds + Mounts) | r4 | Elevation (host FS exposure) | `internal/proxy/docker.go` `isSensitiveHostPath`, `cleanPath` |
| **Inspection body cap** (1 MiB LimitReader) | r4 | DoS (memory) | `internal/proxy/docker.go` `maxInspectBody` |
| **JSON-encoded denial reason** | r4 | Info disclosure / injection in error echo | `internal/proxy/docker.go` `deny` |
| **CSWSH origin enforcement** (allowlist / same-origin via SNI) | r2 | Spoofing (cross-site WS hijack) | `internal/websocket/websocket.go` `checkOrigin`, `requestHost`, `hostMatches` |
| **Absent-Origin requires token** | r2 | Elevation (browser-shaped unauth WS) | `internal/websocket/websocket.go` `hasToken` |
| **Docker ID validation** | r2 | Tampering (raw-request injection) | `internal/websocket/websocket.go` `validDockerID` |
| **WS DoS bounds** (50-session cap, 64KB frames, idle/absolute timeout) | r2 | DoS | `internal/websocket/websocket.go` `maxConcurrentSessions`, `maxFrameBytes`, `idleTimeout`, `absoluteTimeout`, `bridge` |
| **Bounded Docker dial/handshake** | r2 | DoS (hung backend) | `internal/websocket/websocket.go` `dockerDialTimeout`, `dockerHandshakeTimeout` |
| **Browse symlink-resolved containment** | r3 | Tampering / Elevation (path traversal) | `internal/browse/browse.go` `containedPath`, `resolveExisting` |
| **`O_NOFOLLOW` on get/put** | r3 | Elevation (symlink redirection) | `internal/browse/browse.go` (`syscallNoFollow`), `nofollow_unix.go` |
| **Re-validate after MkdirAll** | r3 | Elevation (symlink race on dir create) | `internal/browse/browse.go` put handler |
| **Dotfile suppression** (ls + get) | r3 | Info disclosure (hidden secrets) | `internal/browse/browse.go` ls/get handlers |
| **Rename destination re-validation** | r3 | Elevation (rename escape) | `internal/browse/browse.go` rename handler, `validIdentifier` |
| **Upload size cap** (100 MB) | r3 | DoS (volume fill) | `internal/browse/browse.go` `MaxBytesReader` |
| **Mutate audit** (actor/verb/path/result) | r3 | Repudiation | `internal/browse/browse.go` `audit`, `actorFromRequest` |
| **Browse ReadOnly mode** (mutating verbs → 403 + audit) | r3 | Tampering (when enabled) | `internal/browse/browse.go` `ReadOnly`, `rejectReadOnly` |
| **Host info minimization** | r3 | Info disclosure (hardware fingerprint) | `internal/hostinfo/hostinfo.go` `MinimalHostInfo`, `minimize` |
| **TOFU server-cert SPKI pin** | r1 | Spoofing / Tampering (rogue/MITM server) | `internal/register/pin.go` `verifyPinned`, `spkiFingerprint`; `register.go` `BuildServerTLSConfig` |
| **Pin persistence + restart seeding** | r1 | Spoofing (re-capture downgrade) | `internal/register/pin.go` `seedPin`, `setIfEmpty`; `register.go` `StartHeartbeat` |
| **Optional CA chain verify (defense in depth)** | r1 | Tampering (same-CA mis-issue) | `internal/register/register.go` `BuildServerTLSConfig` (RootCAs + VerifyConnection) |
| **`--insecure-server-tls` flag-only escape hatch** | r1 | Info disclosure (silent downgrade) | `internal/config/config.go` (no env override), `register.go` loud warn |
| **https-required server URL** | r1 | Tampering (plaintext orchestration) | `internal/config/config.go` `Validate` |
| **State file perms/owner gate** (0600/0700 + uid) | r1 | Info disclosure (credential leak) | `internal/register/register.go` `checkStatePerms`, `assertPerm`; `fileuid_unix.go` |
| **TLS key perm repair** (tighten to 0600) | r1 | Info disclosure (leaked private key) | `internal/tlsutil/tls.go` `tightenKeyPerm` |
| **Secret-or-explicit-opt-out required** | r1 | Misconfiguration → unauth | `internal/config/config.go` `Validate` |
| **TLS 1.2+ with restricted cipher suites** | r1 | Tampering (weak transport) | `internal/server/server.go` `srv.TLSConfig` |
| **Security response headers** (HSTS/nosniff/DENY) | r1 | Tampering (clickjack/sniff) | `internal/server/server.go` `headerMiddleware` |
| **Listener DoS bounds** (header timeout, 1 MiB max header) | r1 | DoS (slowloris) | `internal/server/server.go` `ReadHeaderTimeout`, `MaxHeaderBytes` |
| **Go 1.26.10 pinned toolchain** | r5 | Tampering (known CVEs) | `go.mod` (`toolchain go1.26.10`); govulncheck 0 findings |
| **Digest-pinned base images + SHA-pinned Actions** | r5 | Tampering (supply chain) | `Dockerfile`, `.github/workflows/*` |

Every threat enumerated in `04-stride-per-element.md` and every **[BLOCKED]** leaf
in `05-attack-trees.md` maps to a row above. **[RESIDUAL]** leaves are tracked in
`07-residual-risk.md`.
