# HANDOFF — Eight.ly Agent

**Date:** 2026-05-21
**Last commit:** `60b3917` (fix: reconcile LOC and test counts)
**Tag:** `v1.0.0` on commit `6008b08`
**Release:** https://github.com/smashingtags/eightly-agent/releases/tag/v1.0.0 (32 assets, all signed)
**Repo visibility:** PRIVATE — waiting for coordinated public announcement
**Codebase:** 2,050 source LOC, 134 tests across 10 packages

## What happened this session

Started from a bare Go binary (~975 LOC) with zero CI, zero tests, proprietary license. Shipped a production-grade, security-reviewed open source project with v1.0.0 release.

### Major work completed:
1. **Containerized for distribution** — Dockerfile (non-root), GHCR, Docker Hub, docker-compose.yml + .env.example
2. **CI/CD pipeline** — 7 GitHub Actions workflows (CI, Release, Docker Publish, CodeQL, Scorecard, Dependabot, Dependency Graph), all Actions SHA-pinned, all tools version-pinned
3. **Cross-platform** — Linux (amd64/arm64/arm/v7), macOS (Intel/Apple Silicon), Windows (amd64)
4. **Windows service support** — SCM install/uninstall/start/stop/status, platform-specific defaults
5. **Package managers** — Homebrew tap (`smashingtags/homebrew-tap`), Scoop bucket (`smashingtags/scoop-eightly`), .deb, .rpm via nfpm
6. **Security hardening** — 6 rounds of review with Opus 4.7, all issues addressed:
   - Path traversal hardened (filepath.Rel + symlink eval + volumeID regex)
   - Fail-closed auth (refuses to start without secret)
   - Token expiry enforcement (exp claim)
   - TLS fail-closed (system trust store default, explicit --insecure-server-tls opt-in)
   - CRLF injection validation on container/exec IDs
   - WebSocket origin enforcement when auth disabled
   - Configurable --allowed-origins for WebSocket
   - Re-registration authenticates with existing token
   - systemd deep hardening (15+ directives including ProtectProc, ProcSubset)
   - Dockerfile non-root user
   - TLS 1.2 minimum, AEAD cipher suites, HSTS
   - X-Content-Type-Options, X-Frame-Options headers
   - Upload size limit (100MB MaxBytesReader)
7. **Supply chain** — Cosign-signed binaries + containers (including .deb files), SLSA provenance, SBOM, all Actions SHA-pinned, all tools version-pinned
8. **Prometheus /metrics** — 5 app metrics behind --metrics flag, auth-gated
9. **134 tests** across 10 packages, race detector clean, 60%+ coverage, fuzz test for ParseArgs
10. **Linters** — go vet, staticcheck, govulncheck, gosec, golangci-lint (errcheck, bodyclose, noctx), all gating CI, every nosec annotation individually justified
11. **License** — Apache 2.0 (was Proprietary)
12. **Documentation** — README with Mermaid architecture diagram, badges, per-platform download links, verification docs. SECURITY.md with threat model. CONTRIBUTING.md, CODE_OF_CONDUCT.md, CHANGELOG.md. docs/ARCHITECTURE.md, TROUBLESHOOTING.md, UPGRADING.md. 10-page GitHub wiki with screenshots. Issue templates, PR template, CODEOWNERS, .editorconfig.
13. **Self-hosted runners** — 4 LXCs on Proxmox 183 (106-109) registered to eightly-agent repo
14. **All sites updated** — mjashley.com, imogenlabs.ai, eight.ly, wiki.eight.ly, GitHub wiki

## What's NOT done yet

### Before going public:
- **Announcement plan** — Michael wants a coordinated launch (blog, Discord, Reddit, social)
- **Homebrew/Scoop SHA update** — hashes in homebrew-tap and scoop-eightly need updating to v1.0.0 binaries
- **After-public verification** — attestations require public repo; run `gh attestation verify` after flip
- **Re-register a runner for eightly repo** — wiki.eight.ly deploys need a runner (currently all 4 are on eightly-agent)

### Reviewer notes for v1.0.1 (P3, non-blocking):
- CHANGELOG beta1 section lists features that landed in rc1 (historical inaccuracy)
- Consolidate G104 nosec annotations into gosec allow-list
- release-please or release-drafter for automated changelogs
- After-public: re-run release workflow to get attestations, verify Scorecard score

## Infrastructure

### Self-hosted runners on Proxmox 183:
| LXC | Name | IP | Registered to |
|-----|------|----|---------------|
| 106 | gitrunner1 | 192.168.1.222 | smashingtags/eightly-agent |
| 107 | gitrunner2 | DHCP | smashingtags/eightly-agent |
| 108 | gitrunner3 | DHCP | smashingtags/eightly-agent |
| 109 | gitrunner4 | DHCP | smashingtags/eightly-agent |

All have Go 1.23+ and Docker installed.

### Build machine:
iMac M1 at 192.168.1.208 — Go 1.26.3, cosign installed. Use for cross-compilation and signature verification. Do NOT build on Mac Mini (low disk).

## Key files
- `CHANGELOG.md` — v1.0.0 content in [Unreleased] (tag on 6008b08 predates latest commits)
- `.golangci.yml` — errcheck + bodyclose + noctx + govet + staticcheck + ineffassign + unused
- `nfpm.yml` — .deb/.rpm packaging config (uses envsubst for variable resolution)
- `deploy/eightly-agent.service` — hardened systemd unit (15+ directives)
- `internal/metrics/metrics.go` — 5 Prometheus metrics
- `SECURITY.md` — threat model, disclosure policy, safe harbor
- `docs/wiki/` — 10 wiki pages, synced to GitHub wiki

## Repos created this session
- `smashingtags/homebrew-tap` (public) — Homebrew formulae
- `smashingtags/scoop-eightly` (public) — Scoop bucket for Windows
