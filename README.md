# Eight.ly Agent

Lightweight Docker management agent for Eight.ly OS. Runs on remote Docker hosts and provides secure API access for container management, console access, file operations, and system monitoring.

## What It Does

- **Docker API proxy** — forwards authenticated requests to the local Docker socket over HTTPS
- **WebSocket console** — interactive container attach and exec via WebSocket
- **File browser** — list, upload, download, delete, and rename files in Docker volumes
- **Host info** — reports PCI devices, physical disks, and system metadata
- **Registration** — auto-registers with an Eight.ly OS server and sends 30-second heartbeats
- **Auto-TLS** — generates self-signed certificates if none are provided

## Quick Start

```bash
# Build
make build

# Run (dev mode, no auth)
./build/eightly-agent --port 9001

# Run with auth + registration
./build/eightly-agent \
  --secret "your-shared-secret" \
  --server-url "https://your-eos-server:7001" \
  --enroll-token "your-enrollment-token"

# Docker
docker run -d \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:7001" \
  ghcr.io/smashingtags/eightly-agent:latest
```

## Configuration

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--port` | `AGENT_PORT` | `9001` | Listen port |
| `--secret` | `EIGHTLY_AGENT_SECRET` | — | HMAC shared secret for token validation |
| `--server-url` | `EIGHTLY_SERVER_URL` | — | EOS server URL for registration |
| `--enroll-token` | `EIGHTLY_ENROLL_TOKEN` | — | One-time enrollment token |
| `--state-path` | `EIGHTLY_STATE_PATH` | `/data/eightly-agent.json` | Registration state file |
| `--data` | `DATA_PATH` | `/data` | Data directory (TLS certs, state) |
| `--socket` | `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket path |
| `--log-level` | `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

## API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/ping` | No | Health check (returns 200) |
| GET | `/host` | Yes | PCI devices and physical disk info |
| GET | `/browse/ls` | Yes | List directory contents in a volume |
| GET | `/browse/get` | Yes | Download a file from a volume |
| POST | `/browse/put` | Yes | Upload a file to a volume |
| DELETE | `/browse/delete` | Yes | Delete a file or directory |
| PUT | `/browse/rename` | Yes | Rename a file or directory |
| GET | `/websocket/attach` | Yes | WebSocket container console (attach) |
| GET | `/websocket/exec` | Yes | WebSocket container console (exec) |
| * | `/*` | Yes | Docker API pass-through to local socket |

## Architecture

```
Client (EOS Server)
    |
    | HTTPS + Bearer Token
    v
Eight.ly Agent (:9001)
    |
    | Unix Socket
    v
Docker Daemon (/var/run/docker.sock)
```

~975 lines of Go. Zero external dependencies beyond gorilla/websocket and zerolog. No Portainer code.

## License

Proprietary — Eight.ly, Inc.
