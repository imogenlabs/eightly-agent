# HANDOFF — Eight.ly Agent

**Date:** 2026-05-21
**Last commit:** `30071af` (test: expand /metrics coverage)
**Tag:** `v1.0.0` on commit `6008b08`
**Release:** https://github.com/smashingtags/eightly-agent/releases/tag/v1.0.0 (32 assets, all signed)
**Repo visibility:** PRIVATE — waiting for coordinated public announcement

## What happened this session

Started from a bare Go binary (~975 LOC) and shipped a production-grade, security-reviewed open source project.

### Major work completed:
1. **Containerized for distribution** — Dockerfile, GHCR, Docker Hub, docker-compose.yml
2. **CI/CD pipeline** — 7 GitHub Actions workflows (CI, release, docker-publish, CodeQL, Scorecard, Dependabot)
3. **Cross-platform** — Linux (amd64/arm64/arm/v7), macOS (Intel/Apple Silicon), Windows (amd64)
4. **Windows service support** — SCM install/uninstall/start/stop/status
5. **Package managers** — Homebrew tap (`smashingtags/homebrew-tap`), Scoop bucket (`smashingtags/scoop-eightly`), .deb, .rpm via nfpm
6. **Security hardening** — 6 rounds of review with Opus 4.7, all 40+ issues addressed:
   - Path traversal hardened (filepath.Rel + symlink eval)
   - Fail-closed auth (refuses to start without secret)
   - Token expiry enforcement
   - TLS fail-closed (system trust store default, explicit --insecure-server-tls opt-in)
   - CRLF injection validation on container/exec IDs
   - WebSocket origin enforcement when auth disabled
   - Re-registration authenticates with existing token
   - systemd deep hardening (15 directives)
   - Dockerfile non-root user
7. **Supply chain** — Cosign-signed binaries + container images, SLSA provenance, SBOM, all Actions SHA-pinned
8. **Prometheus /metrics** — 5 app metrics behind --metrics flag, auth-gated
9. **129 tests** across 9 packages, race detector clean, 60%+ coverage
10. **Linters** — go vet, staticcheck, govulncheck, gosec, golangci-lint (errcheck, bodyclose, noctx), all gating CI
11. **License** — Apache 2.0 (was Proprietary)
12. **Documentation** — README with badges/architecture/verification docs, SECURITY.md with threat model, CONTRIBUTING.md, CODE_OF_CONDUCT.md, CHANGELOG.md, docs/ARCHITECTURE.md, docs/TROUBLESHOOTING.md, docs/UPGRADING.md, 10-page GitHub wiki
13. **Self-hosted runners** — 4 LXCs on Proxmox 183 (106-109) registered to eightly-agent repo
14. **wiki.eight.ly** — Ecosystem tab, EBS/Agent/Mobile promoted, brand theme, agent page rewritten

## What's NOT done yet

### Before going public:
- **Announcement plan** — Michael wants a coordinated launch (blog, Discord, Reddit, social)
- **Homebrew/Scoop SHA update** — hashes in homebrew-tap and scoop-eightly are for v1.0.0-beta1 binaries, need updating to v1.0.0
- **After-public verification** — attestations require public repo; run `gh attestation verify` after flip

### Known limitations:
- Windows/macOS CI tests only via cross-compilation (self-hosted runners are Linux-only)
- GitHub Actions billing still out of minutes for paid runners
- The eightly-agent self-hosted runners are NOT available for the eightly repo (wiki deploys) — need to re-register runner to eightly or set up a second one

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

All have Go 1.23+ and Docker installed. The eightly repo's wiki deploys need a runner re-registered (was on LXC 106 before it got moved to eightly-agent).

### Build machine:
iMac M1 at 192.168.1.208 — Go installed, cosign installed. Use for cross-compilation and signature verification.

## Key files
- `CHANGELOG.md` — v1.0.0 content in [Unreleased], moves to [1.0.0] at tag time (already tagged)
- `.golangci.yml` — errcheck + bodyclose + noctx + govet + staticcheck + ineffassign + unused
- `nfpm.yml` — .deb/.rpm packaging config (uses envsubst for variable resolution)
- `deploy/eightly-agent.service` — hardened systemd unit
- `internal/metrics/metrics.go` — 5 Prometheus metrics

## Repos created this session
- `smashingtags/homebrew-tap` (public) — Homebrew formulae
- `smashingtags/scoop-eightly` (public) — Scoop bucket for Windows
