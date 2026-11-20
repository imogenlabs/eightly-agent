---
description: "Install Eight.ly Agent with Docker Compose, Docker, Homebrew, Scoop, .deb or .rpm packages, a binary, or from source."
---

# Installation

Multiple ways to install Eight.ly Agent: Docker Compose (recommended), Docker container, Homebrew (macOS/Linux), Scoop (Windows), pre-built binary, Windows service, or build from source. The newest release is on the [Releases](https://github.com/imogenlabs/eightly-agent/releases) page.

## Prerequisites

- A host running Linux, macOS, or Windows with Docker installed
- Network connectivity between the agent host and your Eight.ly OS server (for registration)
- An install command from Eight.ly OS (Environments → Add Agent). It carries the server URL, the shared secret and a one-time enrollment token.

## Connect to Eight.ly OS

1. Open **Environments → Add Agent** in Eight.ly OS.
2. Copy the install command; it includes the server URL, shared secret and a one-time enrollment token.
3. Run it on the Docker host you want to manage; see [Docker](#docker) or the install method below for details.
4. Return to **Environments** and wait for the host to show **Online**; see [Registration and Heartbeat](Registration-and-Heartbeat.md).
5. Select the host to manage its containers; if it stays offline, see [Troubleshooting](Troubleshooting.md).

## Homebrew (macOS / Linux)

```bash
brew install imogenlabs/tap/eightly-agent
```

After installation, configure and run:

```bash
eightly-agent \
  --secret "your-shared-secret" \
  --server-url "https://your-eos-server:8080" \
  --enroll-token "your-enrollment-token" \
  --data "$HOME/.eightly-agent"
```

Run with the install command's flags, or use a launchd job on macOS to keep it running.

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
    # A mirror on Docker Hub: smashingtags/eightly-agent.
    container_name: eightly-agent
    restart: unless-stopped
    # A second reaper for images built before tini was added to the image.
    init: true
    ports:
      - "9001:9001"
    volumes:
      # No `:ro`: it marks the socket file, not the Docker API, so a read-only
      # mount still lets anything that can open it create and start containers.
      # The control is the agent's own filter on the requests it forwards.
      - /var/run/docker.sock:/var/run/docker.sock
      - agent-data:/data
      # Docker's named volumes, for the file browser, at the path the agent
      # reads by default (--volumes-path). Leave it out to turn the browser off.
      - /var/lib/docker/volumes:/var/lib/docker/volumes
      # Each folder in EIGHTLY_ALLOWED_BIND_ROOTS, at the same path, read-only,
      # so the agent can check app binds under it. Example for /opt/appdata:
      # - /opt/appdata:/opt/appdata:ro
    # The agent needs the group that owns the socket on this host. The image
    # assumes 999; if yours differs, set DOCKER_GID=$(stat -c %g /var/run/docker.sock)
    # in .env.
    group_add:
      - "${DOCKER_GID:-999}"
    environment:
      - EIGHTLY_AGENT_SECRET=${EIGHTLY_AGENT_SECRET}
      - EIGHTLY_SERVER_URL=${EIGHTLY_SERVER_URL:-}
      - EIGHTLY_ENROLL_TOKEN=${EIGHTLY_ENROLL_TOKEN:-}
      # Host folders apps may bind-mount, comma-separated. Empty allows none.
      - EIGHTLY_ALLOWED_BIND_ROOTS=${EIGHTLY_ALLOWED_BIND_ROOTS:-}
      - LOG_LEVEL=${LOG_LEVEL:-info}
    # No healthcheck override: the image carries its own. An image built before
    # the in-process health check has no `healthcheck` subcommand and would start a second agent
    # on every check if one were named here.

volumes:
  agent-data:
```

The shipped compose file passes `EIGHTLY_ALLOWED_BIND_ROOTS` from `.env`; empty allows named volumes only. If your apps bind host folders, set it in `.env` and uncomment the matching read-only mount. For `/opt/appdata`, set `EIGHTLY_ALLOWED_BIND_ROOTS=/opt/appdata` and uncomment `- /opt/appdata:/opt/appdata:ro`. See [Configuration](Configuration.md#security).

The agent runs as uid 1000 and reaches Docker through the group that owns the socket. The image assumes GID 999. If your host's socket has another group, put it in `.env`:

```bash
echo "DOCKER_GID=$(stat -c %g /var/run/docker.sock)" >> .env
```

See [Configuration](Configuration.md) for all available environment variables to put in `.env`.

## Docker

The agent is published to GitHub Container Registry as a multi-arch image (linux/amd64, linux/arm64 and linux/arm/v7). Eight.ly OS writes this command for you, carrying the server URL, shared secret and a one-time enrollment token, under **Environments → Add Agent**.

```bash
docker run -d --init \
  --name eightly-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --group-add "$(stat -c %g /var/run/docker.sock)" \
  -v eightly-agent-data:/data \
  -v /var/lib/docker/volumes:/var/lib/docker/volumes \
  -v /opt/appdata:/opt/appdata:ro \
  -p 9001:9001 \
  -e EIGHTLY_AGENT_SECRET="your-shared-secret" \
  -e EIGHTLY_SERVER_URL="https://your-eos-server:8080" \
  -e EIGHTLY_ENROLL_TOKEN="your-enrollment-token" \
  -e EIGHTLY_ALLOWED_BIND_ROOTS="/opt/appdata" \
  ghcr.io/imogenlabs/eightly-agent:latest
```

`EIGHTLY_ALLOWED_BIND_ROOTS` names the host folders containers may bind; `/opt/appdata` is where the Eight.ly OS app catalog keeps app data. Without it containers get named volumes only, and an app that binds a host folder is refused. Mount each root into the agent at the same path, read-only, so it can check binds for symlinks; without that, apps with a restart policy are refused.

The agent runs as uid 1000 and reaches Docker through the group that owns the socket. The image assumes GID 999; `--group-add` gives it your host's group, whatever its number.

The socket is mounted read-write on purpose. `:ro` only marks the socket file read-only, not the Docker API: anything that can open the socket can still create and start containers, so it protects nothing. The controls are the agent's signed-token check and its Docker proxy, which forwards only an allow-list of Docker endpoints and refuses privileged containers, host namespaces, extra capabilities, devices and host folders outside the bind roots.

`--init` gives the container a process reaper. Images from this release on carry tini for the same job, and their health check is `eightly-agent healthcheck`. That is an in-process HTTPS GET of the agent's own `/healthz` that exits 0 or 1 and starts no child process. Do not override the health check with that command on an older image: before the subcommand existed, the agent ignored the extra word and would start a second agent.

> `EIGHTLY_SERVER_URL` is your Eight.ly OS dashboard URL — the agent register/heartbeat endpoints live on the same host and port EOS serves the UI from (default `8080`). Eight.ly OS generates a ready-to-paste `docker run` command carrying the server URL, shared secret and a one-time enrollment token under **Environments → Add Agent**.

To choose when you upgrade, use a release version in place of `latest`, without the `v` (`ghcr.io/imogenlabs/eightly-agent:X.Y.Z`), or a minor line (`X.Y`). The same image is available as a mirror on Docker Hub: `smashingtags/eightly-agent`.

### Volume Mounts

| Mount | Required | Purpose |
|-------|----------|---------|
| `/var/run/docker.sock` | Yes | Docker daemon socket -- the agent proxies API requests to this socket |
| `/var/lib/docker/volumes` | For the file browser | Docker's named volumes, mounted at the same path (`--volumes-path`). Without it the file browser answers `503` |
| `/data` | Recommended | Persistent storage for TLS certificates (`cert.pem`, `key.pem`) and registration state (`eightly-agent.json`). Without this volume, the agent regenerates certs and re-registers on every restart. Use a named volume: a host directory Docker creates for you is owned by root, and the agent (uid 1000) cannot write its state there. |

!!! warning "A read-only socket mount protects nothing"
    Mounting the socket with `:ro` marks the socket file read-only, not the Docker API. Anything that can open the socket can still create, start and delete containers, so `:ro` is not a security control. The control is the agent itself: it accepts only signed tokens, forwards only an allow-list of Docker endpoints, and refuses requests that would break out of a container (privileged mode, host namespaces, extra capabilities, devices, host folders outside the bind roots, and exec or attach into a container that already reaches the host). Treat the agent's secret like a root password.

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
EIGHTLY_ALLOWED_BIND_ROOTS=/opt/appdata
```

```bash
sudo systemctl enable --now eightly-agent
```

Values in `agent.env` override the unit's own `Environment=` lines. A package upgrade never overwrites an existing `agent.env`; it only resets its owner and mode. If an older install keeps the secret in a drop-in, move it into `agent.env` and delete the drop-in.

The service keeps its TLS certificate and registration state in `/var/lib/eightly-agent` (`DATA_PATH`; the state file is `eightly-agent.json` there). Unless `EIGHTLY_STATE_PATH` is set, the state file goes in the data directory, and the agent does not use its enrollment token while it cannot save the result.

The service restarts automatically on failure (5-second delay) and starts after `docker.service`.

### Windows Service

On Windows, the agent runs as a native Windows service — no terminal window, starts automatically on boot.

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

The service takes no other settings on its command line, so the server URL is read from the `EIGHTLY_SERVER_URL` Machine variable, as above. It is not a secret. Other settings, such as `EIGHTLY_ALLOWED_BIND_ROOTS`, are Machine variables too. On Windows, bind roots can use an absolute drive path such as `C:\appdata` or `/` form. System directories, drive roots and UNC paths are refused. See [Configuration](Configuration.md#security) for the restrictions.

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

| Subcommand | Description |
|:-----------|:------------|
| `install` | Registers the agent as a Windows service (automatic start) reading its secret from `--secret-file` (default `C:\ProgramData\eightly-agent\secret`) and its enrollment token from `--enroll-token-file` (default `C:\ProgramData\eightly-agent\enroll-token`) |
| `uninstall` | Stops and removes the service |
| `start` | Starts the service |
| `stop` | Sends a stop signal to the running service |
| `status` | Prints current service state (running, stopped, etc.) |

The service runs as `EightlyAgent` (LocalSystem) in the Windows Services panel. Default paths on Windows:
- Docker socket: `//./pipe/docker_engine`
- Data directory: `C:\ProgramData\eightly-agent`
- State file: `C:\ProgramData\eightly-agent\state.json`
- Secret file: `C:\ProgramData\eightly-agent\secret`

**File access on Windows.** Windows ignores unix file modes, so the agent sets an explicit, protected access list (SYSTEM and Administrators full control, plus the agent's own account when it is run by hand) on the data folder, state file, secret file, TLS key and certificate whenever it creates or reuses them. It refuses to load the state, secret or key if any other account still has access after that. A folder that also holds other things (another program's settings, a volume, a home directory) keeps its access list, and the agent's files in it are protected one by one; the agent refuses such a folder if any other account may add, delete or rename files in it, and refuses any folder above its own that lets another account delete or rename what it holds. A folder is only re-permissioned when it holds nothing but the agent's configured files (its state file and the `.tmp` and `.bak` beside it, the TLS pair in the data folder, the secret file and the enrollment token file). Check with `icacls C:\ProgramData\eightly-agent\*`.

Ownership is checked too, because an owner can rewrite the access list at any time. When another account owns the data folder or one of the agent's files (a local user who created `C:\ProgramData\eightly-agent` before the install, for example), the agent takes it back for Administrators; as the service (SYSTEM) or from an elevated prompt it holds the privilege to do so. When it cannot, or when someone else owns a shared folder holding the agent's files, it refuses to start and names the owner. Delete the folder and run `install` again to start clean.

The agent reaches Docker over the named pipe `//./pipe/docker_engine` by default (Docker Desktop and Docker Engine on Windows Server). `--socket` takes either pipe spelling, `//./pipe/name` or `\\.\pipe\name`; any other path is dialed as a Unix socket.

## Build from Source

Build with `make build` from a clone; `go install` is not supported because the module path has no `/v2` suffix for the v2.x tags.

Requires Go 1.26.4 or later; the module pins toolchain `go1.26.11`, which Go downloads for you when your installed version is older.

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
  -ldflags "-s -w -X main.version=X.Y.Z" \
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
#   Eightly-Agent: X.Y.Z
#   Eightly-Agent-API-Version: 2
```

The `-k` flag is needed because the agent uses a self-signed TLS certificate by default. See [TLS](TLS.md) for details on using custom certificates.

## Uninstall

Stop the agent before removing it. If you installed the Windows service, run `eightly-agent.exe stop`, then `eightly-agent.exe uninstall` from an elevated PowerShell before removing the binary or Scoop package.

| Install method | Steps |
|----------------|-------|
| Docker | Run `docker stop eightly-agent`, then `docker rm eightly-agent`. For Compose, run `docker compose down` from the directory holding the compose file. |
| `.deb` | Run `sudo systemctl disable --now eightly-agent`, then `sudo apt remove eightly-agent` and `sudo systemctl daemon-reload`. |
| `.rpm` | Run `sudo systemctl disable --now eightly-agent`, then `sudo dnf remove eightly-agent` and `sudo systemctl daemon-reload`. |
| Homebrew | Stop the running agent or unload your launchd job, then run `brew uninstall eightly-agent`. |
| Scoop | Run `scoop uninstall eightly-agent` after stopping the agent and removing its Windows service, if installed. |
| Plain binary or source build | Stop the agent and delete the binary you installed. If you used the manual systemd setup above, run `sudo systemctl disable --now eightly-agent`, delete `/etc/systemd/system/eightly-agent.service` and `/usr/bin/eightly-agent`, then run `sudo systemctl daemon-reload`. Unload any launchd job you created. |

These steps retain the agent's data and settings. If you no longer need them, delete only its dedicated data directory and settings after stopping it: the Docker volume `eightly-agent-data` (or the Compose `agent-data` volume), `/var/lib/eightly-agent` and `/etc/eightly-agent` for Linux services, `$HOME/.eightly-agent` for the Homebrew example, or `C:\ProgramData\eightly-agent` on Windows. For a custom `--data` or `--state-path`, use the paths you configured.

In Eight.ly OS, open **Environments**, choose the agent's remove action and confirm **Remove**; this removes its dashboard entry, so uninstall it on the host too.

## Next Steps

- [Upgrading](UPGRADING.md) -- Prepare for an upgrade
- [Configuration](Configuration.md) -- Tune flags and environment variables
- [Authentication](Authentication.md) -- Set up the shared secret between EOS and the agent
- [Registration and Heartbeat](Registration-and-Heartbeat.md) -- Understand how the agent connects to EOS
