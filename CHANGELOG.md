# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
- 117 tests across 10 packages
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

[1.0.0-beta1]: https://github.com/smashingtags/eightly-agent/releases/tag/v1.0.0-beta1
