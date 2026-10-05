# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- A restarted agent kept losing its registration: its own guard refused the 0755 directory Docker gives a new volume, so it ignored the saved state and tried to enroll again with a spent single-use token (`register failed: status 403`). On start the agent now tightens a state directory it owns to 0700 and logs it; when it cannot, it says why and keeps refusing. The 0600 file check is unchanged (EOS-2534).
- The image left a zombie process every 10 seconds once the agent's listener stopped answering: 115,420 on one host. The busybox `wget` health check forked an `ssl_client` helper, and each timed-out check orphaned it onto PID 1, the agent, which never reaps. The image now runs under `tini`, and the health check is a new `eightly-agent healthcheck` subcommand that does the HTTPS GET in-process. A CI job builds the image and proves both (EOS-2533).
- The image left a zombie process every 10 seconds once the agent's listener stopped answering: 115,420 on one host. The busybox `wget` health check forked an `ssl_client` helper, and each timed-out check orphaned it onto PID 1, the agent, which never reaps. The image now runs under `tini -s` (it still reaps, and stays quiet, under `docker run --init`), and the health check is a new `eightly-agent healthcheck` subcommand that does the HTTPS GET in-process. A CI job builds the image and proves both (EOS-2533).
- The image left a zombie process every 10 seconds once the agent's listener stopped answering: 115,420 on one host. The busybox `wget` health check forked an `ssl_client` helper, and each timed-out check orphaned it onto PID 1, the agent, which never reaps. The image now runs under `tini -s` (it still reaps, and stays quiet, under `docker run --init`), and the health check is a new `eightly-agent healthcheck` subcommand that does the HTTPS GET in-process and trusts only the agent's own certificate from its data directory, so a listener presenting any other certificate, or a certificate it cannot read, is unhealthy. A CI job builds the image and proves both (EOS-2533).

## [1.0.0] - 2026-05-21

### Changed (BREAKING)
- Server TLS verification now uses the system trust store by default. Users connecting
  to EOS servers with self-signed certificates must now pass `--insecure-server-tls`
  explicitly, or provide a CA with `--server-ca`.

### Added
- Prometheus `/metrics` endpoint behind `--metrics` flag with heartbeat, registration,
  WebSocket, and Docker socket reachability gauges/counters (closes [#10](https://github.com/smashingtags/eightly-agent/issues/10))
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

[1.0.0-rc1]: https://github.com/smashingtags/eightly-agent/releases/tag/v1.0.0-rc1
[1.0.0-beta1]: https://github.com/smashingtags/eightly-agent/releases/tag/v1.0.0-beta1
