# Installation

Multiple ways to install Eight.ly Agent: Docker Compose (recommended), Docker container, Homebrew (macOS/Linux), Scoop (Windows), pre-built binary, Windows service, or build from source. The current release is **v1.0.0**.

## Prerequisites

- A host running Linux, macOS, or Windows with Docker installed
- Network connectivity between the agent host and your Eight.ly OS server (for registration)
- A shared secret or enrollment token from your EOS instance

## Homebrew (macOS / Linux)

```bash
brew install smashingtags/tap/eightly-agent
```

After installation, configure and run:

```bash
eightly-agent \
  --secret "your-shared-secret" \
  --server-url "https://your-eos-server:7001" \
  --enroll-token "your-enrollment-token"
```

Or start as a background service via `brew services start smashingtags/tap/eightly-agent`.

## Scoop (Windows)

```powershell
scoop bucket add eightly https://github.com/imogenlabs/scoop-bucket
scoop install eightly-agent
```

After installation, see the [Windows Service](#windows-service) section below for running as a service, or run directly from a terminal:

```powershell
eightly-agent.exe `
  --secret "your-shared-secret" `
  --server-url "https://your-eos-server:7001" `
  --enroll-token "your-enrollment-token"
```

> **Note:** On Windows, the default Docker socket path is `//./pipe/docker_engine` (Docker Desktop named pipe) and the default data path is `C:\ProgramData\eightly-agent`.

## Docker Compose (Recommended)

The easiest way to deploy. A `docker-compose.yml` and `.env.example` are included in the repo:

```bash
cp .env.example .env
# Edit .env with your shared secret and EOS server URL
docker compose up -d
```

The compose file contents for reference:

```yaml
services:
  eightly-agent:
    image: ghcr.io/imogenlabs/eightly-agent:v1.0.0
    container_name: eightly-agent
    restart: unless-stopped
    ports:
      - "9001:9001"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - agent-data:/data
    env_file: .env
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "https://localhost:9001/healthz", "--no-check-certificate"]
      interval: 10s
      timeout: 5s
      retries: 3

volumes:
  agent-data:
```

See [[Configuration]] for all available environment variables to put in `.env`.

## Docker

The agent is published to both GitHub Container Registry and Docker Hub as a multi-arch image (amd64 and arm64).

### GHCR

```bash
docker run -d \
  --name eightly-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v eightly-agent-data:/data \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:7001" \
  -e EIGHTLY_ENROLL_TOKEN="your-enrollment-token" \
  ghcr.io/imogenlabs/eightly-agent:v1.0.0
```

### Docker Hub

```bash
docker run -d \
  --name eightly-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v eightly-agent-data:/data \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:7001" \
  -e EIGHTLY_ENROLL_TOKEN="your-enrollment-token" \
  smashingtags/eightly-agent:v1.0.0
```

### Volume Mounts

| Mount | Required | Purpose |
|-------|----------|---------|
| `/var/run/docker.sock` | Yes | Docker daemon socket -- the agent proxies API requests to this socket |
| `/data` | Recommended | Persistent storage for TLS certificates (`cert.pem`, `key.pem`) and registration state (`eightly-agent.json`). Without this volume, the agent regenerates certs and re-registers on every restart. |

## Binary Download

Pre-built binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, and windows/amd64 are available on [GitHub Releases](https://github.com/imogenlabs/eightly-agent/releases).

```bash
# Download the latest release (amd64 example)
curl -Lo eightly-agent https://github.com/imogenlabs/eightly-agent/releases/latest/download/eightly-agent-linux-amd64
chmod +x eightly-agent

# Create data directory
sudo mkdir -p /data

# Run
./eightly-agent \
  --secret "your-shared-secret" \
  --server-url "https://your-eos-server:7001" \
  --enroll-token "your-enrollment-token" \
  --data-path /data
```

### Systemd Service

To run the agent as a system service:

```ini
[Unit]
Description=Eight.ly Agent
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/eightly-agent \
  --secret "your-shared-secret" \
  --server-url "https://your-eos-server:7001" \
  --data-path /var/lib/eightly-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo cp eightly-agent /usr/local/bin/
sudo cp eightly-agent.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now eightly-agent
```

### Windows Service

The agent binary has built-in Windows service support via the Service Control Manager (SCM). You must run these commands from an **elevated (Administrator) command prompt or PowerShell**.

**Install and start the service:**

```powershell
eightly-agent.exe install
eightly-agent.exe start
```

**Set environment variables before installing** so the service picks them up:

```powershell
[System.Environment]::SetEnvironmentVariable("EIGHTLY_AGENT_SECRET", "your-shared-secret", "Machine")
[System.Environment]::SetEnvironmentVariable("EIGHTLY_SERVER_URL", "https://your-eos-server:7001", "Machine")
[System.Environment]::SetEnvironmentVariable("EIGHTLY_ENROLL_TOKEN", "your-enrollment-token", "Machine")
eightly-agent.exe install
eightly-agent.exe start
```

**Other service commands:**

```powershell
eightly-agent.exe stop       # Stop the service
eightly-agent.exe restart    # Restart the service
eightly-agent.exe uninstall  # Remove the service
```

The service runs as `EightlyAgent` in the Windows Services panel. Default paths on Windows:
- Docker socket: `//./pipe/docker_engine`
- Data directory: `C:\ProgramData\eightly-agent`
- State file: `C:\ProgramData\eightly-agent\state.json`

## Build from Source

Requires Go 1.23 or later.

```bash
git clone https://github.com/imogenlabs/eightly-agent.git
cd eightly-agent

# Build for the current platform
make build

# Binary is at build/eightly-agent
./build/eightly-agent --help
```

### Build targets

| Make target | Description |
|-------------|-------------|
| `make build` | Build for the current platform to `build/eightly-agent` |
| `make test` | Run tests with race detector |
| `make lint` | Run `go vet` |
| `make docker-local` | Build a local Docker image |
| `make docker` | Build and push multi-arch images to GHCR and Docker Hub |
| `make clean` | Remove build artifacts |

### Cross-compile manually

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -ldflags "-s -w -X main.version=1.0.0" \
  -o build/eightly-agent-arm64 \
  ./cmd/agent
```

The binary is fully static (`CGO_ENABLED=0`) and has no runtime dependencies.

## Verifying the Installation

After starting the agent, verify it is running:

```bash
# Liveness check (no auth required)
curl -k https://localhost:9001/healthz
# Expected: 200 OK

# Readiness check (verifies Docker is reachable, no auth required)
curl -k https://localhost:9001/readyz
# Expected: 200 OK (or 503 if Docker is unreachable)

# Legacy health check
curl -k https://localhost:9001/ping
# Expected: 200 OK with empty body

# Check the response headers
curl -kI https://localhost:9001/ping
# Should include:
#   Eightly-Agent: 1.0.0
#   Eightly-Agent-API-Version: 2
```

The `-k` flag is needed because the agent uses a self-signed TLS certificate by default. See [[TLS]] for details on using custom certificates.

## Next Steps

- [[Configuration]] -- Tune flags and environment variables
- [[Authentication]] -- Set up the shared secret between EOS and the agent
- [[Registration-and-Heartbeat]] -- Understand how the agent connects to EOS
