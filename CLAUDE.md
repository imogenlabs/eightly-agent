# CLAUDE.md — Eight.ly Agent

## What this is

Lightweight Docker management agent for Eight.ly OS. Runs on remote Docker hosts and provides secure API access for container management, console access, file operations, and system monitoring.

- Repo: `smashingtags/eightly-agent` (private)
- Owner: Michael (smashingtags)
- Language: Go 1.23
- Binary: ~975 lines, zero external deps beyond gorilla/websocket and zerolog

## How it works

EOS server connects to agents running on remote Docker hosts. The agent:
1. Proxies authenticated requests to the local Docker socket over HTTPS
2. Provides WebSocket console (attach + exec)
3. Provides file browser for Docker volumes
4. Reports host info (PCI devices, disks, system metadata)
5. Auto-registers with EOS and sends 30-second heartbeats
6. Generates self-signed TLS certs if none provided

## Build

```bash
make build                    # local binary → build/eightly-agent
make test                     # go test ./...
make docker VERSION=2.1.0     # Docker image
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
cmd/agent/main.go         — entry point, HTTP mux, TLS, graceful shutdown
internal/auth/jwt.go      — HMAC shared-secret token validation middleware
internal/browse/browse.go — file browser for Docker volumes (ls, get, put, delete, rename)
internal/config/config.go — CLI flags + env var parsing
internal/hostinfo/         — PCI device and physical disk detection
internal/proxy/docker.go  — reverse proxy to Docker socket
internal/register/         — EOS registration + 30s heartbeat loop
internal/tlsutil/tls.go   — self-signed cert generation
internal/websocket/        — WebSocket attach + exec handlers
```

## CI/CD

| Workflow | Trigger | What it does |
|----------|---------|-------------|
| `ci.yml` | push/PR to main/dev | Lint (vet + staticcheck), test (race), build matrix (amd64 + arm64) |
| `docker-publish.yml` | push to main, tags | Multi-arch Docker build, push to GHCR + Docker Hub |
| `release.yml` | tags (v*) | Build binaries, create GitHub Release with checksums |

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

- Linux only. No Windows code.
- Go files max 500 lines.
- Don't push to remote without asking Michael.
- No Co-Authored-By on commits.

## Related repos

| Repo | What |
|------|------|
| smashingtags/eightly-os | Eight.ly OS — the main product that connects to agents |
| smashingtags/eightly-backup-server | EBS — backup server |
| smashingtags/eightly-site | eight.ly marketing site |
