# Eight.ly Agent — Pre-Public Code Review

Overall this is a tidy, well-scoped little daemon. ~1,550 lines, three deps, clean package layout, decent test coverage in the security-sensitive packages (`auth`, `config`), proper cross-platform build tags, and a thorough CI/release pipeline. It's close to public-ready, but there are several issues — some genuinely serious — that I'd fix before flipping the visibility switch.

## Critical / security issues to fix first

**1. Path traversal in `internal/browse/browse.go` is incomplete.**
`resolvePath` rejects only literal `..` substrings in `volumeID` and `userPath`:

```go
if strings.Contains(volumeID, "..") || strings.Contains(userPath, "..") { ... }
full := filepath.Join(dataPath, "volumes", volumeID, "_data", userPath)
```

This is bypassable in several ways. Absolute paths (`userPath = "/etc/passwd"`) make `filepath.Join` produce `/data/volumes/<id>/_data/etc/passwd`, which is fine — but `volumeID = "/etc"` will yield `/etc/_data/<path>`, fully outside `dataPath`. Symlinks inside a volume also let a caller read or write anywhere the agent process can. URL-encoded traversal (`%2e%2e`) is handled by Go's URL parsing here, but it's brittle to rely on substring checks.

Fix: after the join, compute `filepath.Clean` then verify with `filepath.Rel(base, full)` that the result doesn't start with `..` and isn't absolute outside base. Additionally, `filepath.EvalSymlinks` the result (or open with `O_NOFOLLOW` on Linux) and re-check containment. Reject `volumeID` values that contain any path separator or are not a simple identifier (regex like `^[A-Za-z0-9_.-]+$`). Also: the `rename` handler joins `body.NewName` after the traversal check on `Path` but only checks `NewName` afterwards — restructure so all final paths are validated against the base.

**2. Auth middleware skips authentication entirely when `Secret == ""`.**
In `internal/auth/jwt.go`:

```go
if secret == "" { return next }
```

And `config.Parse` has no required-field validation, so starting the binary without `EIGHTLY_AGENT_SECRET` exposes an unauthenticated Docker socket on TCP. That is, by far, the worst-case outcome of this whole repo — anyone reaching port 9001 gets root-equivalent on the host via the Docker daemon. The README implies the secret is required but nothing enforces it. The `.env.example` similarly only suggests setting it.

Fix: fail closed. In `config.Parse`, refuse to start if `Secret` is empty unless an explicit `--insecure-no-auth` flag is given (and log a loud warning when it is). At minimum, log a `FATAL` rather than silently disabling auth.

**3. The "JWT" is not a JWT and has no replay or expiry protection.**
The token format is `base64(claims_json).base64(hmac_sha256(claims_json))`. There is:

- No `exp` claim or verification — `Iat` exists but is never checked
- No `nbf`, `jti`, or audience binding
- No algorithm header, so no protection against future changes
- Claims are not bound to the request (no nonce, no path/method binding)

A leaked token is valid forever. The constant-time compare on the MAC is good (`hmac.Equal`), but missing expiration is a meaningful gap given this token grants full Docker control. Either move to real JWTs (e.g., `github.com/golang-jwt/jwt/v5`) with mandatory `exp`/`nbf`/`aud` checks, or add an explicit `exp` field and enforce it. Document the token lifetime guarantees in the README.

Smaller but related: the middleware does `strings.TrimPrefix(..., "Bearer ")` and then checks `token == r.Header.Get("Authorization")` to detect "no prefix" — that's clever but fragile (lowercase `bearer ` would also bypass). Use `strings.HasPrefix` with case-insensitivity or `strings.CutPrefix`.

**4. WebSocket `CheckOrigin: return true` disables origin checking.**
In `internal/websocket/websocket.go`:

```go
var upgrader = websocket.Upgrader{ CheckOrigin: func(r *http.Request) bool { return true } }
```

Combined with the previous issues, this means a browser visiting an attacker page can attempt cross-origin WebSocket connections. Authentication is still required, but only via the `Authorization` header which a browser can't set on a WS handshake — so this is probably exploit-safe today by accident. Still, set an allow-list of origins (e.g., the registered EOS server URL) or document the threat model explicitly.

**5. `InsecureSkipVerify: true` for outbound registration and heartbeat.**
`internal/register/register.go` uses `&tls.Config{InsecureSkipVerify: true}` for both `Register` and `sendHeartbeat`. The agent will happily talk to any MITM presenting any cert on its way to the EOS server, leaking the enrollment token, agent token, and OS/host inventory. Since you generate a self-signed cert on the agent side, presumably the server also might — but you should at minimum support pinning the server's CA via a flag (`--server-ca` / `EIGHTLY_SERVER_CA`) and only fall back to `InsecureSkipVerify` if explicitly opted into (`--insecure-server-tls`). Today this is silent insecure behavior.

**6. WebSocket attach/exec forwards arbitrary `id` to Docker via a hand-rolled HTTP request.**
```go
raw := fmt.Sprintf("POST /containers/%s/attach?...", containerID)
```

`containerID` is taken straight from `r.URL.Query().Get("id")` with no validation, then formatted into a raw HTTP request line. A value containing `\r\n` would let a caller inject additional HTTP headers/body into the Docker socket connection — classic CRLF/request-smuggling against the Docker daemon. Auth is required to reach this endpoint, but auth'd misuse is still in scope. Validate `id` against `^[a-zA-Z0-9]{12,64}$` (Docker container/exec IDs) before formatting.

Same applies to `execID` in `ExecHandler`.

**7. `os.MkdirAll(dir, 0755)` for the TLS key directory.**
`tlsutil.EnsureCert` creates the data directory at `0755` and then writes the cert at default mode (`0644` via `os.Create`) and the key at `0600`. The cert mode is fine, but the directory should be `0700` (matching what `register.Save` does for the state file dir). On a multi-user host this allows other users to enumerate the cert file.

Also: there's no rotation. A 365-day self-signed cert and no mechanism to renew is a sharp edge — at minimum, log a warning when the cert is within 30 days of expiry. Consider treating the cert as invalid and regenerating when `NotAfter` is in the past.

**8. `detectLocalIP` uses a UDP "dial" to 8.8.8.8.**
This works but is a privacy/firewall surprise on hosts that block egress or run on offline networks. It also reports the IP of whichever interface happens to route to 8.8.8.8, which on multi-homed hosts is often not the right answer. Consider making this configurable (`--advertise-ip`) and falling back to the current trick. Document it in the README — surprising behavior for an "offline-friendly" agent.

## Correctness / robustness issues

**9. `config.Parse` calls `flag.Parse()` and silently accepts unknown flags incorrectly.**
The version-flag handling is awkward:

```go
cfg := config.Parse(version)
if cfg.Version == version {
    for _, arg := range os.Args[1:] {
        if arg == "--version" || arg == "-version" { ... }
    }
}
```

`flag.Parse()` will already have errored on `--version` if it's not registered. Just add a real `--version` flag to the FlagSet and short-circuit before flag parsing in main, the same way you handle `install`/`start`/etc. The current code path is dead in practice.

**10. README and code disagree on flag names.**
README says `--data` and `--state-path`, but config.go defines `--data-path`. README says `--socket`, code defines `--socket-path`. Either rename the flags to match the docs (more user-friendly) or fix the docs. This kind of drift will bite users on day one.

**11. `register.StartHeartbeat` re-registration loses the enrollment token.**
After 3 heartbeat failures, the code calls `Register(state.ServerURL, "", info)` with no enroll token. If the server requires an enroll token for first-time registration but accepts re-registration without one (because the AgentID/Token is presented elsewhere), this is fine — but the call here passes neither the old token nor a new enroll token, so it relies entirely on server-side trust of the hostname/IP. Worth a comment explaining the protocol assumption, and ideally pass the existing `state.Token` as an `Authorization: Bearer` so the server can authenticate the re-registration.

Also: `DetectInfo("", "", version)` is called during re-registration with an empty socket path, so `detectDockerVersion("")` will fail silently and you'll re-register reporting an empty Docker version. Pass the real `cfg.SocketPath` through (the cfg isn't available at this scope — you'd need to capture it in the closure or store it on `State`).

**12. `register.Register` decodes the response body twice.**
You read with `bufio.NewReader(resp.Body)` and pass to `json.NewDecoder`. The `json.NewDecoder` already buffers. Just `json.NewDecoder(resp.Body).Decode(&result)`.

**13. The `defer ws.Close()`/`defer docker.Close()` ordering in `websocket.go` can leak goroutines.**
In `bridge`, only one direction signals `done`; the other goroutine (ws → docker) blocks on `ws.ReadMessage()` forever if the docker side closes first, because closing `docker` doesn't unblock `ws.ReadMessage`. After `<-done` returns, you exit, the deferred `ws.Close()` and `docker.Close()` fire, and the second goroutine eventually unblocks — so it's not a permanent leak, but it's racy: the second goroutine might still be writing to a closing connection. Add a `done2` channel and `select` on both, or use `errgroup.Group` with a shared context. Also handle the close more cleanly — currently you silently swallow read/write errors and never send a normal close frame to the peer.

**14. `browse.put` reads the entire upload into a 32 MiB buffer at once.**
```go
buf := make([]byte, 32*1024*1024)
io.CopyBuffer(dst, file, buf)
```

That's an unbounded per-request allocation (one buffer per concurrent upload). On a server with limited memory, ten concurrent uploads = 320 MB of buffers permanently allocated until GC. Use the default `io.Copy` (32 KiB) — disk I/O is rarely the bottleneck, and the kernel handles batching. Also: there's no upload size limit at all; consider `http.MaxBytesReader` or `r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)`.

**15. `browse.put` writes to `os.Create(full)` and truncates without checking if `full` is a directory or if there's an existing file the caller shouldn't be allowed to overwrite.**
Combined with the traversal issue above, this is a write-anywhere primitive. Even with traversal fixed, you may want a "no overwrite" mode or at least to refuse if `full` is a directory (currently `os.Create` returns an error in that case, so this is okay — but worth an explicit test).

**16. `browse.rename` does `filepath.Dir(full)` *before* validating `NewName`.**
The check `strings.Contains(body.NewName, "..") || strings.Contains(body.NewName, "/")` is correct, but it happens after `dest` is computed. The check still works because `os.Rename` uses the validated `dest` only when the check passes, but reorder it for readability and to prevent future bugs.

**17. `hostinfo_darwin.go`: `strings.Fields(line)[0]` will panic on an empty line.**
After the prefix check it should be safe, but defensive: use `parts := strings.Fields(line); if len(parts) == 0 { continue }`. Same general comment: the darwin/windows implementations shell out to `system_profiler`, `diskutil`, and `powershell` — if those binaries aren't on `PATH` (unlikely but possible in a stripped-down container or chroot), the agent silently returns empty results. Log at debug level when these commands fail.

**18. `hostinfo_linux.go` size math overflows for >8 EB disks.**
Not a real concern today, but `sectors * 512` can overflow `int64` on… extremely hypothetical hardware. More importantly: `readSysfs(.../device/vendor)` doesn't exist for NVMe drives (which use `model` instead). NVMe disks will report empty vendors. Worth a fallback to `model`.

**19. Server timeouts: `WriteTimeout: 30 * time.Minute`.**
That's deliberate (to support long WebSocket sessions / large uploads), but it also defangs the `WriteTimeout` for normal HTTP. Better pattern: keep a short default and use `http.ResponseController.SetWriteDeadline` per request for endpoints that need to extend it. Also, no `IdleTimeout` is set — set one to avoid idle keep-alive connections sitting forever.

**20. `register.LoadState` returns `(nil, err)` when the file is missing — wait, it returns `(nil, nil)`. Good.**
But `server.Run` does `state, _ := register.LoadState(...)` which discards real errors (corrupt JSON, permission denied). At least log the error.

**21. `headerMiddleware` writes headers but the underlying `http.ResponseWriter` may already have been written to by the proxy / WS upgrade.**
For the reverse proxy this is fine because `httputil.ReverseProxy` calls `WriteHeader` itself. But for the WebSocket upgrade handlers, those custom headers will be lost because `Upgrader.Upgrade` hijacks the connection. Not a correctness bug, just a wart — document or accept.

**22. `Subject: pkix.Name{CommonName: "eightly-agent"}` and `DNSNames: []string{"localhost"}`.**
The cert is only valid for `localhost` and `127.0.0.1`. If the EOS server connects by IP or hostname of the agent (which the README implies — "point EOS at it"), the server will need `InsecureSkipVerify` on its side too. Add the detected IP and hostname to `IPAddresses` and `DNSNames` at cert generation time, and offer a way to pass extra SANs via flag (`--tls-san`).

**23. `config_test.go` does not test `Parse` itself, only `envOverride`.**
Because `flag.Parse()` reads global `os.Args`, it's awkward to unit-test, but you can refactor `Parse` to accept a `*flag.FlagSet` and `[]string` and test it directly. Worthwhile given how central config is.

**24. No tests for `browse`, `proxy`, `websocket`, `tlsutil`, or `hostinfo` despite the README implying coverage.**
`config_test.go`, `auth/jwt_test.go`, and presumably `server_test.go`, `register_test.go`, `browse_test.go`, `proxy/docker_test.go`, `hostinfo_test.go` are listed in the tree — I'd recommend specifically verifying the path-traversal fixes have negative tests (`../`, absolute paths, symlinks, URL-encoded variants).

## Operational / packaging issues

**25. systemd unit doesn't drop privileges or sandbox.**
`deploy/eightly-agent.service` runs as root with no `User=`, `NoNewPrivileges=`, `ProtectSystem=`, `ProtectHome=`, `PrivateTmp=`, `CapabilityBoundingSet=`, etc. The agent does need access to the Docker socket and `/sys` for hostinfo, but a dedicated `eightly-agent` user in the `docker` group would be much safer than root. Add:

```
User=eightly-agent
SupplementaryGroups=docker
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/eightly-agent
```

The `nfpm.yml` postinstall script (referenced but not in this review) would need to create that user. Currently the file is referenced (`postinstall: ./deploy/postinstall.sh`) but I didn't see it in the directory listing — make sure it exists, or the package build will fail.

**26. Dockerfile runs as root.**
No `USER` directive. Add a non-root user with `docker` group access (gid passed via build arg or detected at runtime). Also pin the base images by digest (`alpine:3.21@sha256:...`) and add `--no-cache` is already there, good. Consider `gcr.io/distroless/static` instead of alpine for a smaller, more locked-down image — you have CGO disabled so it should just work.

**27. Dockerfile `golang:1.23-alpine` and `alpine:3.21` aren't pinned.**
Tags float. Pin to specific digests for reproducible builds, especially for a published image.

**28. Healthcheck uses `--no-check-certificate`.**
Functionally necessary since the cert is self-signed, but combined with the auth-skip on `/ping` it means anyone in the container's network namespace can probe `/ping`. That's acceptable, just confirm `/ping` returns *nothing* sensitive (it currently returns 200 with no body — good).

**29. CI workflow is solid but missing a few things.**
- No `govulncheck` step. Add `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...` to the lint job.
- No code coverage report uploaded.
- No `gosec` scan, which would have caught some of the issues above.
- The build matrix doesn't include `linux/arm/v7` (Raspberry Pi 32-bit), which is common for Docker hosts. Worth adding if you want to support that.
- Dependabot config (`.github/dependabot.yml`) isn't visible in the tree. Add one for `gomod`, `github-actions`, and `docker`.

**30. Release workflow signs nothing.**
For a public release of an agent binary that gets root on Docker hosts, you really want:
- `cosign` signing of release binaries
- `cosign` signing of container images (you already have `id-token: write` permission set up — finish the job)
- An SBOM (`syft` or `cyclonedx-gomod`)
- Provenance attestation (`actions/attest-build-provenance`)

These will matter a lot for downstream package managers (Homebrew, distro maintainers) and security-conscious users.

**31. `.dockerignore` is referenced but I didn't read it.**
Make sure it excludes `.env`, `*.pem`, `build/`, `release/`, `.git/` to avoid leaking secrets into image layers.

**32. README inconsistencies and gaps.**
- Mentions Homebrew tap (`smashingtags/tap`) and Scoop bucket — make sure those exist and are versioned, or the README will mislead users on day one.
- Doesn't document the security model (who's trusted, what `EIGHTLY_AGENT_SECRET` actually protects, what happens if it leaks, threat model).
- Doesn't mention the agent grants root-equivalent access via the Docker socket to any holder of a valid token.
- No CONTRIBUTING.md, no SECURITY.md (you really want one for vulnerability disclosure), no CODE_OF_CONDUCT.md.

**33. License headers.**
The repo is Apache-2.0 but the source files don't have SPDX headers (`// SPDX-License-Identifier: Apache-2.0`). Adding them is cheap and helps downstream consumers.

## Style / minor polish

**34.** `internal/proxy/docker.go`: the `dockerListEndpoints` map iteration order is non-deterministic, but since you `return` on the first match and the keys are distinct, it doesn't matter. Just noting it's not idiomatic — a simple `switch path { case "/containers": return "/containers/json"; ... }` is clearer and faster.

**35.** Many handlers ignore `json.NewEncoder(w).Encode(v)`'s return value. Failures are rare but worth at least logging at debug.

**36.** `auth.unauthorized` ignores `w.Write` errors. Fine.

**37.** `cmd/agent/service_windows.go` `installService` doesn't set `Restart` policies or pass through environment. Windows services configured via `mgr.Config` should set `DelayedAutoStart: true` (or document why not), and you'll want to support passing args/env to the service. Right now `eightly-agent.exe install` registers a service that runs with no `EIGHTLY_AGENT_SECRET`, which combined with issue #2 means a Windows install starts with auth disabled. That's a footgun.

**38.** `service_windows.go` `stopService` doesn't wait for the service to stop — returns immediately after sending the signal. Fine, but the message "stop signal sent" is honest about that.

**39.** `register.go` `AgentInfo` is duplicated by `registerPayload`. Just use `AgentInfo` with an extra `EnrollToken` field, or embed it.

**40.** Magic numbers throughout (heartbeat interval, retry threshold, timeouts) — pulling these into named constants at the top of `register.go` would help.

## Suggested fix priority order

If you can only do a handful before publishing:

1. **Refuse to start without `EIGHTLY_AGENT_SECRET`** (issue #2) — this is the one that turns a typo into a remote-root.
2. **Harden path traversal in `browse`** (issue #1) — proper containment check with `filepath.Rel` + symlink eval.
3. **Validate container/exec IDs before formatting into raw HTTP** (issue #6).
4. **Add token expiry / `exp` claim enforcement** (issue #3).
5. **Replace `InsecureSkipVerify` outbound with a configurable CA** (issue #5), or at least make it a loud opt-in.
6. **Run `eightly-agent` as a non-root user** in the Docker image and systemd unit (issues #25, #26).
7. **Add `SECURITY.md` and document the threat model in the README.**
8. **Add `govulncheck` and `gosec` to CI.**

Everything else is polish, test coverage, and supply-chain hygiene that you can iterate on after going public.

The bones of this project are good — clear separation of concerns, sensible cross-platform abstractions via build tags, real tests on the auth path, working CI, and a tight dependency footprint. The issues above are mostly the natural set of things you find when shifting from "internal tool" to "public, downloadable, root-equivalent agent." Fix the top half-dozen and I think this is in good shape to go public.
