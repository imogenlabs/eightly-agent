---
description: "Build workflows, release rules, and install methods for Eight.ly Agent."
---

# Development

## CI/CD

| Workflow | Trigger | What it does |
|:---------|:--------|:-------------|
| **CI** | Push/PR to `main`, `dev` | Lint (vet, staticcheck, govulncheck, gosec, golangci-lint, all pinned) → Test (race, per-package coverage floors in `scripts/coverage-check.sh`), Test on Windows (GitHub-hosted: named pipe, access lists and owners) → image reaps orphans → end-to-end test against a real Docker daemon → Build matrix (linux/amd64, linux/arm64, linux/arm, darwin/amd64, darwin/arm64, windows/amd64) |
| **Pentest Gate** | Push/PR to `main`, `dev` | Security regression tests for auth, proxy, browse, register and websocket; govulncheck and gosec gates |
| **CodeQL** | Push/PR to `main`, weekly | Go analysis; any finding fails the job |
| **Docs** | Push/PR touching the docs | `mkdocs build --strict`; on `main` publishes the [docs site](https://imogenlabs.github.io/eightly-agent/) to GitHub Pages |
| **Docker Publish** | Push to `main`, tags | Multi-arch build → GHCR + Docker Hub, signed with cosign; `latest`, `MAJOR.MINOR` and `MAJOR` move only for the newest stable release in their line |
| **Release** | Tags (`v*`), or a manual run from the tag | Builds the exact commit the tag points to: 6 platform binaries + `.deb`/`.rpm` packages (amd64, arm64) → GitHub Release with SHA256 checksums, cosign signatures and an SBOM |
| **OpenSSF Scorecard** | Push to `main`, weekly (public repo only) | Supply-chain checks |

Every job runs on GitHub-hosted runners.

**Release tags.** `release.yml` and `docker-publish.yml` check a tag with the same script, `scripts/release-tag.sh`, and its self-test runs in CI and before every release:

- The tag must be `vMAJOR.MINOR.PATCH`, optionally with a `-prerelease` suffix (`v2.2.0-alpha.1`), and point at a commit on `main`. Anything else is refused before anything is built, signed or pushed.
- A tag with a suffix is published as a prerelease, and its image gets only its exact version tag.
- The image aliases `latest`, `MAJOR.MINOR` and `MAJOR` move only for the highest stable tag on `main` in their line, so publishing an older release again never moves one backwards.
- A manual release must be run from the tag itself (`gh workflow run release.yml --ref v2.1.0 -f version=v2.1.0`, or pick the tag under "Use workflow from"). A run from a branch is refused, because its signatures would name the branch and fail the [release verification](Verifying-Releases.md).

### Install Methods

| Method | Platform | Command / Source |
|:-------|:---------|:-----------------|
| Docker (GHCR) | Linux | `docker pull ghcr.io/imogenlabs/eightly-agent:latest` |
| Docker (Hub) | Linux | `docker pull smashingtags/eightly-agent:latest` (a mirror on Docker Hub) |
| Homebrew | macOS, Linux | `brew install imogenlabs/tap/eightly-agent` |
| Scoop | Windows | `scoop bucket add eightly https://github.com/imogenlabs/scoop-eightly && scoop install eightly-agent` |
| .deb package | Debian, Ubuntu | Download from [Releases](https://github.com/imogenlabs/eightly-agent/releases) (amd64, arm64); installs a hardened systemd unit |
| .rpm package | RHEL, Fedora | Download from [Releases](https://github.com/imogenlabs/eightly-agent/releases) (amd64, arm64); installs a hardened systemd unit |
| Windows service | Windows | Download `.exe` from [Releases](https://github.com/imogenlabs/eightly-agent/releases), then `eightly-agent.exe install && eightly-agent.exe start` |
| Binary | Linux, macOS, Windows | Download from [Releases](https://github.com/imogenlabs/eightly-agent/releases) (6 platform builds) |

Full instructions for each are in the [installation guide](Installation.md).
