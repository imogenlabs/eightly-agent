---
description: "Install Eight.ly Agent with Docker Compose, Docker, Homebrew, Scoop, .deb or .rpm packages, a binary, or from source."
---

# Installation

Multiple ways to install Eight.ly Agent: Docker Compose (recommended), Docker container, Homebrew (macOS/Linux), Scoop (Windows), pre-built binary, Windows service, or build from source. The newest release is on the [Releases](https://github.com/imogenlabs/eightly-agent/releases) page.

## Prerequisites

- A host running Linux, macOS, or Windows with Docker installed
- Network connectivity between the agent host and your Eight.ly OS server (for registration)
- A shared secret or enrollment token from your EOS instance

## Homebrew (macOS / Linux)

```bash
brew install imogenlabs/tap/eightly-agent
```

After installation, configure and run:

```bash
eightly-agent \
  --secret "your-shared-secret" \
  --server-url "https://your-eos-server:8080" \
  --enroll-token "your-enrollment-token"
```

Or start as a background service via `brew services start imogenlabs/tap/eightly-agent`.

## Scoop (Windows)

```powershell
scoop bucket add eightly https://github.com/imogenlabs/scoop-eightly
scoop install eightly-agent
```

After installation, see the [Windows Service](#windows-service) section below for running as a service, or run directly from a terminal:

```powershell
eightly-agent.exe `
  --secret "your-shared-secret" `
  --server-url "https://your-eos-server:8080" `
  --enroll-token "your-enrollment-token"
```

> **Note:** On Windows, the default Docker socket path is `//./pipe/docker_engine` (Docker Desktop named pipe) and the default data path is `C:\ProgramData\eightly-agent`.

## Docker Compose (Recommended)

The easiest way to deploy. A `docker-compose.yml` and `.env.example` are included in the repo:

```bash
cp .env.example .env
# Edit .env: your shared secret, EOS server URL and enrollment token
docker compose up -d
```

The compose file for reference:

```yaml
services:
  eightly-agent:
    image: ghcr.io/imogenlabs/eightly-agent:latest
    container_name: eightly-agent
    restart: unless-stopped
    init: true  # reaps orphaned processes; the image's own health check needs no override
    ports:
      - "9001:9001"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - agent-data:/data
      - /var/lib/docker/volumes:/var/lib/docker/volumes  # for the file browser
    group_add:
      - "${DOCKER_GID:-999}"
    environment:
      - EIGHTLY_AGENT_SECRET=${EIGHTLY_AGENT_SECRET}
      - EIGHTLY_SERVER_URL=${EIGHTLY_SERVER_URL:-}
      - EIGHTLY_ENROLL_TOKEN=${EIGHTLY_ENROLL_TOKEN:-}
      - LOG_LEVEL=${LOG_LEVEL:-info}

volumes:
  agent-data:
```

The agent runs as uid 1000 and reaches Docker through the group that owns the socket. The image assumes GID 999. If your host's socket has another group, put it in `.env`:

```bash
echo "DOCKER_GID=$(stat -c %g /var/run/docker.sock)" >> .env
```

See [Configuration](Configuration.md) for all available environment variables to put in `.env`.

## Docker

The agent is published to GitHub Container Registry as a multi-arch image (amd64 and arm64). Eight.ly OS writes this command for you, with a one-time enrollment token, under **Environments → Add host**.

```bash
docker run -d --init \
  --name eightly-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v eightly-agent-data:/data \
  -v /var/lib/docker/volumes:/var/lib/docker/volumes \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:8080" \
  -e EIGHTLY_ENROLL_TOKEN="your-enrollment-token" \
  ghcr.io/imogenlabs/eightly-agent:latest
```

To choose when you upgrade, use a release version in place of `latest`, without the `v` (`ghcr.io/imogenlabs/eightly-agent:X.Y.Z`), or a minor line (`X.Y`). The same image is mirrored on Docker Hub as `smashingtags/eightly-agent`.

### Volume Mounts

| Mount | Required | Purpose |
|-------|----------|---------|
| `/var/run/docker.sock` | Yes | Docker daemon socket -- the agent proxies API requests to this socket |
| `/data` | Recommended | Persistent storage for TLS certificates (`cert.pem`, `key.pem`) and registration state (`eightly-agent.json`). Without this volume, the agent regenerates certs and re-registers on every restart. Use a named volume: a host directory Docker creates for you is owned by root, and the agent (uid 1000) cannot write its state there. |

!!! warning "A read-only socket mount protects nothing"
    Mounting the socket with `:ro` marks the socket file read-only, not the Docker API. Anything that can open the socket can still create, start and delete containers, so `:ro` is not a security control. The control is the agent itself: it accepts only signed tokens and refuses Docker requests that would break out of a container (privileged mode, host namespaces, sensitive host mounts and similar). Treat the agent's secret like a root password.

## Binary Download

Pre-built binaries for linux/amd64, linux/arm64, linux/arm (v7), darwin/amd64, darwin/arm64, and windows/amd64 are available on [GitHub Releases](https://github.com/imogenlabs/eightly-agent/releases). On Debian, Ubuntu, RHEL and Fedora, prefer the `.deb` or `.rpm` package from the same page: it installs the binary, a service user and a hardened systemd unit.

```bash
# Download the latest release (amd64 example)
curl -Lo eightly-agent https://github.com/imogenlabs/eightly-agent/releases/latest/download/eightly-agent-linux-amd64
chmod +x eightly-agent

# Run
EIGHTLY_AGENT_SECRET="your-shared-secret" \
EIGHTLY_SERVER_URL="https://your-eos-server:8080" \
EIGHTLY_ENROLL_TOKEN="your-enrollment-token" \
./eightly-agent --data "$HOME/.eightly-agent"
```

The data directory holds the agent's TLS key and saved bearer token, so the agent keeps it private (0700) and refuses one shared with other files.

### Systemd Service

The `.deb` and `.rpm` packages install this unit for you. For a manual install, use the unit from the repo, which runs the agent as its own user with systemd hardening:

```bash
sudo install -m 0755 eightly-agent /usr/bin/eightly-agent
sudo useradd --system --no-create-home --shell /usr/sbin/nologin eightly-agent
sudo usermod -aG docker eightly-agent
sudo install -d -m 0700 -o eightly-agent -g eightly-agent /var/lib/eightly-agent
sudo install -d -m 0700 /etc/eightly-agent
sudo install -m 0600 /dev/null /etc/eightly-agent/agent.env
sudo cp deploy/eightly-agent.service /etc/systemd/system/
sudo systemctl daemon-reload
```

The unit reads its settings from `/etc/eightly-agent/agent.env` (`EnvironmentFile=`), which the packages create root-owned with mode `0600`. Put the secret there, not on the command line, where any user can read it in the process list, and not in a `systemctl edit` drop-in, which every user can read and `systemctl show` prints:

```bash
sudoedit /etc/eightly-agent/agent.env
```

```ini
EIGHTLY_AGENT_SECRET=your-shared-secret
EIGHTLY_SERVER_URL=https://your-eos-server:8080
EIGHTLY_ENROLL_TOKEN=your-enrollment-token
```

```bash
sudo systemctl enable --now eightly-agent
```

Values in `agent.env` override the unit's own `Environment=` lines. A package upgrade never overwrites an existing `agent.env`; it only resets its owner and mode. If an older install keeps the secret in a drop-in, move it into `agent.env` and delete the drop-in.

### Windows Service

The agent binary has built-in Windows service support via the Service Control Manager (SCM). You must run these commands from an **elevated (Administrator) command prompt or PowerShell**.

**Install and start the service:**

```powershell
eightly-agent.exe install
Set-Content -NoNewline -Path C:\ProgramData\eightly-agent\secret -Value 'your-shared-secret'
Set-Content -NoNewline -Path C:\ProgramData\eightly-agent\enroll-token -Value 'your-enrollment-token'
[System.Environment]::SetEnvironmentVariable("EIGHTLY_SERVER_URL", "https://your-eos-server:8080", "Machine")
eightly-agent.exe start
```

`install` creates `C:\ProgramData\eightly-agent`, replaces its access list with SYSTEM and Administrators only (nothing inherited from ProgramData, where every local user can read), and registers the service with `--secret-file C:\ProgramData\eightly-agent\secret --enroll-token-file C:\ProgramData\eightly-agent\enroll-token`. Because the folder is locked down first, the files you write into it afterwards are closed to other users too, and the agent checks each one again when it reads it. To keep them elsewhere, pass `eightly-agent.exe install --secret-file D:\path\secret --enroll-token-file D:\path\enroll-token`.

**Do not put the secret in a Machine environment variable.** Every local user can read those. The service refuses to start while `EIGHTLY_AGENT_SECRET` is set alongside the secret file. When upgrading an install that used one, run `eightly-agent.exe uninstall`, then `install`, write the file, and remove the variable:

```powershell
[System.Environment]::SetEnvironmentVariable("EIGHTLY_AGENT_SECRET", $null, "Machine")
```

The service takes no other settings on its command line, so the server URL is read from the `EIGHTLY_SERVER_URL` Machine variable, as above. It is not a secret.

The enrollment token is single-use. Delete its file once the agent has registered; a missing or empty token file is not an error, and the agent starts from its saved registration. **Do not put the token in a Machine variable** either: every local user can read it until the agent spends it, and the service refuses to start while `EIGHTLY_ENROLL_TOKEN` is set alongside the token file. An install that used one should remove it:

```powershell
Remove-Item C:\ProgramData\eightly-agent\enroll-token   # after the agent has registered
[System.Environment]::SetEnvironmentVariable("EIGHTLY_ENROLL_TOKEN", $null, "Machine")
```

**Other service commands:**

```powershell
eightly-agent.exe status     # Show the service state
eightly-agent.exe stop       # Stop the service
eightly-agent.exe uninstall  # Remove the service
```

The service runs as `EightlyAgent` (LocalSystem) in the Windows Services panel. Default paths on Windows:
- Docker socket: `//./pipe/docker_engine`
- Data directory: `C:\ProgramData\eightly-agent`
- State file: `C:\ProgramData\eightly-agent\state.json`
- Secret file: `C:\ProgramData\eightly-agent\secret`

**File access on Windows.** Windows ignores unix file modes, so the agent sets an explicit, protected access list (SYSTEM and Administrators full control, plus the agent's own account when it is run by hand) on the data folder, state file, secret file, TLS key and certificate whenever it creates or reuses them. It refuses to load the state, secret or key if any other account still has access after that. A folder that also holds other things (a volume, a home directory) keeps its access list; the agent's files in it are protected one by one. Check with `icacls C:\ProgramData\eightly-agent\*`.

## Build from Source

Requires Go 1.26.4 or later; the module pins toolchain `go1.26.10`, which Go downloads for you when your installed version is older.

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
| `make test` | Run tests with the race detector |
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

The `-k` flag is needed because the agent uses a self-signed TLS certificate by default. See [TLS](TLS.md) for details on using custom certificates.

## Next Steps

- [Configuration](Configuration.md) -- Tune flags and environment variables
- [Authentication](Authentication.md) -- Set up the shared secret between EOS and the agent
- [Registration and Heartbeat](Registration-and-Heartbeat.md) -- Understand how the agent connects to EOS
