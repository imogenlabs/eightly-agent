# CLAUDE.md — Eight.ly Agent

## What this is

Cross-platform Docker management agent for Eight.ly OS. Runs on remote Docker hosts (Linux, macOS, Windows) and provides secure API access for container management, console access, file operations, and system monitoring.

- Repo: `smashingtags/eightly-agent` (private)
- Owner: Michael (smashingtags)
- License: Apache 2.0
- Language: Go 1.23
- Codebase: ~1,550 LOC, 56 tests across 8 packages
- Release: v1.0.0-beta1 (5 platform binaries on GitHub Releases)

## How it works

EOS server connects to agents running on remote Docker hosts. The agent:
1. Proxies authenticated requests to the local Docker socket over HTTPS
2. Provides WebSocket console (attach + exec)
3. Provides file browser for Docker volumes
4. Reports host info (PCI devices, disks, system metadata) — platform-specific: Linux (sysfs), macOS (system_profiler/diskutil), Windows (PowerShell Get-PnpDevice/Get-PhysicalDisk)
5. Auto-registers with EOS and sends 30-second heartbeats
6. Generates self-signed TLS certs if none provided
7. Platform-specific defaults: Linux/macOS use unix socket + /data, Windows uses named pipe + ProgramData
8. Windows service support: install/uninstall/start/stop/status subcommands via SCM

## Build

```bash
make build                    # local binary → build/eightly-agent
make test                     # go test ./...
make docker VERSION=2.1.0     # Docker image
```

## Install

```bash
# Homebrew (macOS/Linux)
brew install smashingtags/tap/eightly-agent

# Scoop (Windows)
scoop bucket add eightly https://github.com/smashingtags/scoop-eightly
scoop install eightly-agent

# .deb / .rpm (built via nfpm in release workflow)
sudo dpkg -i eightly-agent_1.0.0-beta1_amd64.deb
sudo rpm -i eightly-agent-1.0.0-beta1.x86_64.rpm
```

## Docker

```bash
docker run -d \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:7001" \
  ghcr.io/smashingtags/eightly-agent:latest
```

Also on Docker Hub: `smashingtags/eightly-agent`

## Structure

```
cmd/agent/main.go              — entry point, CLI parsing, service subcommands
cmd/agent/service_linux.go     — systemd service helpers (Linux)
cmd/agent/service_darwin.go    — launchd service helpers (macOS)
cmd/agent/service_windows.go   — SCM service install/uninstall/start/stop/status (Windows)
internal/auth/jwt.go           — HMAC shared-secret token validation middleware
internal/browse/browse.go      — file browser for Docker volumes (ls, get, put, delete, rename)
internal/config/config.go      — CLI flags + env var parsing
internal/config/defaults_linux.go   — platform defaults (unix socket, /data)
internal/config/defaults_darwin.go  — platform defaults (unix socket, /data)
internal/config/defaults_windows.go — platform defaults (named pipe, ProgramData)
internal/hostinfo/              — PCI device and physical disk detection (platform-specific)
internal/proxy/docker.go        — reverse proxy to Docker socket
internal/register/              — EOS registration + 30s heartbeat loop
internal/server/server.go       — extracted HTTP server setup and lifecycle
internal/tlsutil/tls.go        — self-signed cert generation
internal/websocket/             — WebSocket attach + exec handlers
deploy/eightly-agent.service    — systemd service file
nfpm.yml                        — .deb/.rpm packaging config
```

## CI/CD

| Workflow | Trigger | What it does |
|----------|---------|-------------|
| `ci.yml` | push/PR to main/dev | Lint (vet + staticcheck), test (race), build matrix (amd64 + arm64) |
| `docker-publish.yml` | push to main, tags | Multi-arch Docker build, push to GHCR + Docker Hub |
| `release.yml` | tags (v*) | Build 5 platform binaries, .deb/.rpm via nfpm, create GitHub Release with checksums |

**Note:** GitHub Actions out of minutes — builds done manually until minutes reset.

**GitHub wiki:** 10 pages of documentation covering install, config, API, platform support, and troubleshooting.

## API

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/ping` | No | Health check |
| GET | `/host` | Yes | PCI devices + disk info |
| GET | `/browse/ls` | Yes | List volume directory |
| GET | `/browse/get` | Yes | Download file from volume |
| POST | `/browse/put` | Yes | Upload file to volume |
| DELETE | `/browse/delete` | Yes | Delete file/directory |
| PUT | `/browse/rename` | Yes | Rename file/directory |
| GET | `/websocket/attach` | Yes | WebSocket container console (attach) |
| GET | `/websocket/exec` | Yes | WebSocket container console (exec) |
| * | `/*` | Yes | Docker API pass-through |

## Rules

- Cross-platform: Linux, macOS, Windows. Use build tags for platform-specific code.
- Go files max 500 lines.
- Don't push to remote without asking Michael.
- No Co-Authored-By on commits.

## Related repos

| Repo | What |
|------|------|
| smashingtags/eightly-os | Eight.ly OS — the main product that connects to agents |
| smashingtags/eightly-backup-server | EBS — backup server |
| smashingtags/eightly-site | eight.ly marketing site |
