# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- An agent that registers with Eight.ly OS now answers `503 Service Unavailable` with `Retry-After: 30` on every route that needs a token until it knows its own ID. Before, an agent waiting to register accepted a valid token for any agent. The health probes (`/ping`, `/healthz`, `/readyz`, `/version`) keep answering, and an agent with a saved registration is not affected.
- An agent that cannot register at startup keeps trying in the background, after 5 seconds and then twice as long each time up to 5 minutes, instead of waiting for a restart.
- Tokens must now carry `"type":"agent"`, an issue time (`iat`) and an expiry (`exp`) no more than 30 days after it. A token without an expiry used to be accepted forever. Tokens from Eight.ly OS already meet this; a token you sign yourself must too.
- `EIGHTLY_METRICS_ENABLED`, `EIGHTLY_BROWSE_READ_ONLY` and `EIGHTLY_INSECURE_NO_AUTH` accept only `true`/`false`, `1`/`0`, `yes`/`no` or `on`/`off`. Any other value stops the agent at startup with an error naming the variable; before, a typo was silently ignored.
- The state file defaults to `eightly-agent.json` (`state.json` on Windows) in the data directory, so it follows `--data` / `DATA_PATH`. It was fixed at `/data`, which the packaged service could not write, so the agent spent its enrollment token on a registration it could not save. The agent now checks that the state file is writable before it uses the token.
- The `.deb` and `.rpm` packages read settings from `/etc/eightly-agent/agent.env`, created root-owned with mode `0600` and never overwritten on upgrade. Move a secret kept in a `systemctl edit` drop-in there: drop-ins are readable by every user and `systemctl show` prints them.
- The Windows service reads the shared secret from a file, `C:\ProgramData\eightly-agent\secret` by default (`eightly-agent.exe install --secret-file <path>` picks another). Reinstall the service, write the file, and remove the old `EIGHTLY_AGENT_SECRET` Machine variable, which every local user could read; the agent refuses to start while both are set.
- Docker instructions and `docker-compose.yml` no longer mount the socket `:ro`: that marks the socket file, not the Docker API, so it never protected anything. Compose takes the socket's group from `DOCKER_GID` (default 999); with `docker run`, pass `--group-add "$(stat -c %g /var/run/docker.sock)"`.

### Added
- `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE` read the shared secret from a file on any platform. Setting it together with `--secret` or `EIGHTLY_AGENT_SECRET` is refused.
- Documentation site at https://imogenlabs.github.io/eightly-agent/, published once the repository is public.

### Security
- The Docker proxy refuses more ways out of a container. On top of privileged mode, host PID/IPC/network namespaces, dangerous capabilities and sensitive bind mounts, it now refuses: host user, UTS and cgroup namespaces; the Swarm API; changes through the plugin, service, config, secret and node APIs; exec and container update bodies that ask for any of these; volume driver options that bind a host path or device; `SecurityOpt` values outside the default profiles and `no-new-privileges`; `MaskedPaths` and `ReadonlyPaths` overrides; host devices and device cgroup rules; non-namespaced sysctls; `CgroupParent`; and tmpfs over `/`, `/proc`, `/sys` or `/etc`. Paths are cleaned and every `/vX.Y` prefix removed before the check, so the filter sees what Docker will route.
- A Docker request body the proxy must inspect is refused with `413` when it is over 1 MiB. Before, only the first 1 MiB was read and that cut-short body was forwarded. A body the proxy cannot parse is refused with `403`.
- On Windows the agent sets a protected access list (SYSTEM, Administrators and its own account) on its data folder, state file, secret file, TLS key and certificate, and refuses to load the state, secret or key while any other account has access.
- The file browser refuses any path with a segment starting with `.` (`.ssh`, `.env`, `.git`) with `403` on every route, not only downloads, and refuses pipes, sockets and devices with `400`.
- The container health check accepts only the agent's own certificate, byte for byte, and ignores its dates, so another listener on the port is unhealthy and a certificate past its first year is not.
- A saved registration with no agent ID or token is ignored, and a registration reply without them is treated as a failure.
- Pull requests from forks run only on GitHub-hosted runners.

### Fixed
- A WebSocket console client that stopped reading could hold its session open past the idle and 8-hour limits. Every write now has a 10-second deadline, and the limits are enforced separately from the writes.
- `/host` could hang on a stuck `system_profiler`, `diskutil` or PowerShell command. Each one now stops after 15 seconds, and the summary is cached for 30 seconds so polling does not start new commands each time.

## [2.0.6] - 2026-11-02

### Security
- Built with Go 1.26.10, which fixes a vulnerability in the Go standard library.

## [2.0.5] - 2026-10-12

### Fixed
- Release packaging; the agent itself is unchanged from 2.0.4.

## [2.0.4] - 2026-10-12

### Changed
- Built with Go 1.26.9.

## [2.0.3] - 2026-10-08

### Fixed
- With `--state-path` pointed into a populated folder (a home directory), the first restart narrowed that whole folder to 0700. The agent now tightens only a folder holding nothing but its own files; otherwise it logs what else is there and keeps refusing the saved state.
- Agents lost their connection a month after enrolling. The bearer token lives 30 days, and the only renewal was the re-register after three failed heartbeats, by which time the token had lapsed and the server refused it. The agent now renews from half-life on, while the token is still valid. Once a token has lapsed, it says once that the agent must be re-enrolled and stops sending the dead token to register. A lapsed saved token plus a configured enrollment token now enrolls again instead of reloading the dead state.
- A restarted agent kept losing its registration: its own guard refused the 0755 directory Docker gives a new volume, so it ignored the saved state and tried to enroll again with a spent single-use token (`register failed: status 403`). On start the agent now tightens a state directory it owns to 0700 and logs it; when it cannot, it says why and keeps refusing. The 0600 file check is unchanged.
- The image left a zombie process every 10 seconds once the agent's listener stopped answering: 115,420 on one host. The busybox `wget` health check forked an `ssl_client` helper, and each timed-out check orphaned it onto PID 1, the agent, which never reaps. The image now runs under `tini -s` (it still reaps, and stays quiet, under `docker run --init`), and the health check is a new `eightly-agent healthcheck` subcommand that does the HTTPS GET in-process and trusts only the agent's own certificate from its data directory, so a listener presenting any other certificate, or a certificate it cannot read, is unhealthy. A CI job builds the image and proves both.
- The file browser works through a handle confined to the volume's directory, so a symlink or `..` cannot lead it outside the volume.

## [2.0.2] - 2026-08-16

### Fixed
- The container image builds again on Alpine 3.23, where GID 999 was already taken.
- The `latest` image tag follows the newest release instead of `main`.

### Changed
- Images are published as `ghcr.io/imogenlabs/eightly-agent`.

## [2.0.1] - 2026-08-14

### Security
- Hardening from a full security review: the agent refuses to serve without a secret unless `--insecure-no-auth` is set; the Docker proxy refuses privileged containers, host namespaces, dangerous capabilities and sensitive bind mounts; the EOS server's certificate is pinned on first use; a token is only accepted by the agent it names; WebSocket origins are checked; the file browser no longer follows symlinks out of a volume and can run read-only (`--browse-read-only`); `/host` returns counts and totals only.
- Built with Go 1.26.6.

## [1.0.0] - 2026-05-21

### Changed (BREAKING)
- Server TLS verification now uses the system trust store by default. Users connecting
  to EOS servers with self-signed certificates must now pass `--insecure-server-tls`
  explicitly, or provide a CA with `--server-ca`.

### Added
- Prometheus `/metrics` endpoint behind `--metrics` flag with heartbeat, registration,
  WebSocket, and Docker socket reachability gauges/counters (closes [#10](https://github.com/imogenlabs/eightly-agent/issues/10))
- `BuildServerTLSConfig` helper centralizes outbound TLS configuration (system trust
  store default, optional `--server-ca` override, `--insecure-server-tls` escape hatch)
- `--insecure-server-tls` flag to skip server certificate verification
- `/healthz` liveness probe (always returns `200 {"status":"ok"}`)
- `/readyz` readiness probe (returns `200` if Docker socket is reachable, `503` otherwise)
- Configurable TLS SANs via `--tls-san` flag (multiple values supported)
- Token expiry (`exp` claim) enforcement in HMAC auth middleware
- 134 tests across 10 packages

### Security
- Outbound connections to EOS servers now verify certificates by default instead of
  skipping verification

## [1.0.0-rc1] - 2026-05-21

### Added
- Health check endpoints: `/healthz` (liveness) and `/readyz` (readiness with Docker socket check)
- `BuildServerTLSConfig` for centralized outbound TLS with system trust store default
- `--insecure-server-tls` flag for opt-in certificate skip
- Configurable `--tls-san` for auto-generated TLS certificates
- Token expiry enforcement in auth middleware
- Cosign keyless signing for release binaries and container images
- SLSA build provenance attestations (via actions/attest-build-provenance)
- SBOM generation (SPDX format)
- `.deb` and `.rpm` packaging via nfpm (amd64 + arm64)
- Homebrew tap (`smashingtags/homebrew-tap`)
- Scoop bucket for Windows (`smashingtags/scoop-eightly`)
- GitHub wiki with 10 documentation pages
- SECURITY.md with threat model and coordinated disclosure policy
- CONTRIBUTING.md and CODE_OF_CONDUCT.md

### Known Limitations
- CI runs on self-hosted Linux runners. Windows and macOS builds are cross-compiled but not executed in CI. Community testing on those platforms is encouraged.
- Build provenance and SBOM attestations are present in the release workflow but
  inactive while the repository is private. They activate automatically when the
  repository is made public.

## [1.0.0-beta1] - 2026-05-21

### Added
- Cross-platform support: Linux (amd64/arm64/arm/v7), macOS (Intel/Apple Silicon), Windows (amd64)
- Docker API proxy over authenticated HTTPS with HMAC-SHA256 tokens
- WebSocket container console (attach + exec) with CRLF injection protection
- File browser for Docker volumes with path traversal hardening
- Host info reporting — Linux (sysfs + NVMe fallback), macOS (system_profiler/diskutil), Windows (PowerShell)
- Auto-registration with Eight.ly OS + 30-second heartbeats with exponential backoff
- Auto-TLS certificate generation with configurable SANs (--tls-san)
- Windows service support (install/uninstall/start/stop/status via SCM)
- Platform-specific defaults (unix socket vs named pipe, /data vs ProgramData)
- Token expiry enforcement (exp claim)
- Fail-closed authentication — refuses to start without secret unless --insecure-no-auth
- Configurable server CA for outbound TLS (--server-ca)
- Re-registration with existing token authentication
- Same-origin WebSocket enforcement when authentication is disabled
- systemd service file with privilege dropping (non-root user, NoNewPrivileges, ProtectSystem)
- Dockerfile runs as non-root eightly-agent user
- Docker Compose file with .env.example
- Homebrew tap (smashingtags/homebrew-tap)
- Scoop bucket for Windows (smashingtags/scoop-eightly)
- .deb and .rpm packaging via nfpm
- Cosign-signed release binaries and container images
- SLSA build provenance attestations
- SBOM generation (SPDX)
- 134 tests across 10 packages
- GitHub wiki with 10 documentation pages
- SECURITY.md with threat model and disclosure policy
- CONTRIBUTING.md and CODE_OF_CONDUCT.md
- Apache 2.0 license

### Security
- Path traversal protection with filepath.Rel containment + symlink evaluation
- Volume ID validation (alphanumeric identifiers only)
- Container/exec ID validation against CRLF injection
- Token expiry enforcement
- Upload size limit (100MB via MaxBytesReader)
- TLS directory permissions (0700)
- Non-root container execution
- systemd sandboxing (ProtectSystem=strict, NoNewPrivileges)

[Unreleased]: https://github.com/imogenlabs/eightly-agent/compare/v2.0.6...HEAD
[2.0.6]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.6
[2.0.5]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.5
[2.0.4]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.4
[2.0.3]: https://github.com/imogenlabs/eightly-agent/compare/v2.0.2...v2.0.3
[2.0.2]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.2
[2.0.1]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.1
[1.0.0]: https://github.com/imogenlabs/eightly-agent/releases/tag/v1.0.0
[1.0.0-rc1]: https://github.com/imogenlabs/eightly-agent/releases/tag/v1.0.0-rc1
[1.0.0-beta1]: https://github.com/imogenlabs/eightly-agent/releases/tag/v1.0.0-beta1
