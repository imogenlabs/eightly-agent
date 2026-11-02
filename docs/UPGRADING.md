# Upgrading

## General upgrade process

> Download the new release from [GitHub Releases](https://github.com/imogenlabs/eightly-agent/releases),
> or use your package manager as shown below.

1. Download the new binary for your platform from [Releases](https://github.com/imogenlabs/eightly-agent/releases)
2. Stop the agent (`systemctl stop eightly-agent` / `eightly-agent.exe stop` / `docker compose down`)
3. Replace the binary
4. Start the agent
5. Verify with `eightly-agent version` or `GET /version`

For Docker: `docker compose pull && docker compose up -d` (image `ghcr.io/imogenlabs/eightly-agent`)
For Homebrew: `brew upgrade eightly-agent`

## Upgrading to 2.1.0

2.1.0 closes the ways a token holder could reach the host through the agent. Some things that used to work are now refused, so do the steps for your install before you upgrade.

### What you must do

1. **Name the host folders your containers bind.** The Docker proxy now refuses a container that binds a host folder unless the folder is under `--allowed-bind-roots` / `EIGHTLY_ALLOWED_BIND_ROOTS`. It is empty by default, which allows named volumes only. Set it to the folders your apps keep their data in, comma-separated:

    ```bash
    # docker run
    -e EIGHTLY_ALLOWED_BIND_ROOTS=/opt/appdata
    # /etc/eightly-agent/agent.env
    EIGHTLY_ALLOWED_BIND_ROOTS=/opt/appdata,/mnt/media
    ```

    `/opt/appdata` is where the Eight.ly OS app catalog puts app data by default. Without it, installing or recreating such an app from the dashboard, opening its console and uploading files into it are refused with `403`. Each root must be an absolute path with no trailing slash. A system folder, a folder inside one or a parent of one (`/`, `/etc`, `/usr`, `/var`, `/run`, `/opt`, `/root`, `/var/lib/docker` and others) stops the agent at startup. `docker-compose.yml` does not pass this variable; add `- EIGHTLY_ALLOWED_BIND_ROOTS=/opt/appdata` under `environment:`. The time zone files `/etc/localtime` and `/etc/timezone` may still be bound read-only without a root.

2. **Docker installs: mount Docker's volumes folder for the file browser.** The browser now reads `/var/lib/docker/volumes` (`--volumes-path` / `EIGHTLY_VOLUMES_PATH`). Add `-v /var/lib/docker/volumes:/var/lib/docker/volumes` to `docker run`; the current `docker-compose.yml` already has it. An install that bound the folder at `/data/volumes`, as older instructions said, keeps working. If Docker keeps its data elsewhere (`data-root` in `daemon.json`), mount `<data-root>/volumes` and set `--volumes-path`.

3. **Linux packages: settings live in `/etc/eightly-agent/agent.env`.** The upgrade creates the file (root, mode `0600`) if it is missing and never overwrites it. The unit also sets `EIGHTLY_STATE_PATH=/var/lib/eightly-agent/eightly-agent.json`. If you kept the secret in a `systemctl edit` drop-in, move it into `agent.env`, delete the drop-in, then `sudo systemctl daemon-reload && sudo systemctl restart eightly-agent`. Put `EIGHTLY_ALLOWED_BIND_ROOTS` there too.

4. **Windows service: reinstall, and move the secret and enrollment token into files.** Run `eightly-agent.exe uninstall`, then `eightly-agent.exe install`. Write the secret to `C:\ProgramData\eightly-agent\secret` and, if the agent has not registered yet, the enrollment token to `C:\ProgramData\eightly-agent\enroll-token`. Remove the old Machine variables:

    ```powershell
    [Environment]::SetEnvironmentVariable('EIGHTLY_AGENT_SECRET', $null, 'Machine')
    [Environment]::SetEnvironmentVariable('EIGHTLY_ENROLL_TOKEN', $null, 'Machine')
    ```

    The service refuses to start while a variable is set alongside its file. On first start the agent locks the data folder, state file, secret and TLS key to SYSTEM, Administrators and its own account, takes ownership of any of them another account owns, and refuses them if another account keeps access. Rotate the fleet secret if a Windows host ran an older version: the secret was readable by every local user there.

5. **Tokens you sign yourself** (scripts, monitoring) must carry `"iss":"eightly-server"`, `"type":"agent"`, an `iat`, an `exp` at most 30 days later, and this agent's ID in `sub`. Tokens from Eight.ly OS already do. See [Authentication](wiki/Authentication.md).

6. **Check your service command line.** An unknown word, such as a misspelt subcommand or a stray argument after the flags, now stops the agent with exit status 2 instead of starting it without the flags that followed.

### What is refused now that used to work

Every refusal names its reason in the response and in a `[SECURITY] proxy.denied` (or `websocket.denied`) log line.

| Request | Now | Reason in the response |
|---------|-----|------------------------|
| Create or recreate a container that binds a host folder outside the bind roots | `403` | `host bind mount rejected: … (no --allowed-bind-roots are set; use a named volume)` or `… is outside --allowed-bind-roots` |
| Exec, attach, the WebSocket console, or file upload (`PUT /containers/{id}/archive`) into the agent's own container, or one that is privileged, shares a host namespace, holds the Docker socket, binds a host folder outside the roots, has a capability beyond Docker's defaults or a host device | `403` | `the agent's own container is off limits`, `container … holds the Docker socket`, `container …: <rule>` |
| Any Docker method and path not on the [allow-list](wiki/API-Reference.md#allowed-docker-endpoints): image builds, `commit`, container export, archive download, image load, save and push, registry login, Swarm, services, tasks, nodes, secrets, configs, plugins | `403` | `endpoint not allowed: <method> <path>` |
| `VolumesFrom`, a non-local volume driver, a runtime other than `runc`, runtime annotations, `OomKillDisable`, `DeviceRequests` (GPUs) | `403` | names the field |
| A volume with local bind options outside the bind roots, a host block device, or options for a non-local driver | `403` | `volume …` / `driver options for volume driver … rejected` |
| A network with a driver other than `bridge`, `macvlan` or `ipvlan` | `403` | `network driver rejected: …` |
| Image import from a URL or tarball (`POST /images/create?fromSrc=…`) | `403` | `image import (fromSrc) rejected` |
| `GET /containers/{id}/top` on the agent's own container or one holding the socket | `403` | `container … holds the Docker socket` |
| `POST /images/create` without `fromImage`, or with any request body | `403` | `image import rejected: …` / `a request body is not accepted here` |
| Stop, kill, pause, restart, update, rename or remove the agent's own container; rename a container holding the socket | `403` | `the agent's own container is off limits` |
| Mount the agent's own volume (`agent-data`) in another container, or open it in the file browser | `403` | `volume … holds the agent's own data` |
| Tag an image into the agent's own repository | `403` | `tagging into the agent's own image repository rejected: …` |
| Agent in a container with a bind root that is not mounted into its container at the same path: a restart policy other than `no` on a container that binds a path under that root. Mount each root into the agent at the same path, read-only (`-v /opt/appdata:/opt/appdata:ro`), and it is allowed and checked for symlinks | `403` | `a restart policy is refused on a container that mounts a path under the bind root … Mount … into the agent's container at the same path, read-only …` |
| A request body that gives a key twice (`{"Privileged":true,"privileged":false}`) | `403` | `request body could not be interpreted: duplicate key …` |
| A Docker call on a container that does not exist (exec, attach, start, stop, …) | `404` from the agent | `No such container: …` |
| A Docker request body the proxy reads that is over 1 MiB | `413` | `request body over the 1 MiB inspection limit` |
| Any authenticated route before an agent with a server URL has registered | `503`, `Retry-After: 30` | `agent is not registered with the server yet` |
| File browser with no volumes folder | `503` (`501` on Docker Desktop) | names the folder |
| File browser on a volume the agent may not read | `403` | `volume is not readable by the agent` |
| File browser path with a segment starting with `.` | `403` | `forbidden` |

Still allowed: containers that already bind host folders keep running, and can be started, stopped, restarted and removed. Container inspect still works, with `EIGHTLY_*` values (and every environment value of the agent and of socket holders) shown as `<redacted>`.

### Startup refusals to expect

- A bad bind root, a bad boolean variable (`EIGHTLY_METRICS_ENABLED`, `EIGHTLY_BROWSE_READ_ONLY`, `EIGHTLY_INSECURE_NO_AUTH` take `true`/`false`, `1`/`0`, `yes`/`no`, `on`/`off`), a secret and a secret file set together, or an enrollment token and a token file set together.
- A state file that exists but cannot be read or parsed. Fix or remove it; the agent leaves it alone.
- A saved registration for a different `--server-url`. Set the old URL back, or supply a new enrollment token: the agent then copies the old state to `<state path>.bak` and enrolls with the new server.
- On Windows, a data, state, secret or token folder that holds other files and lets another account add, delete or rename files in it, or that sits under a folder another account may delete or rename things in (a folder made under `C:\` by a user inherits that), is refused. Keep the agent's files in `C:\ProgramData\eightly-agent` or another folder only SYSTEM and Administrators can change.
- An agent whose state folder holds files that are not its own and is wider than 0700 does not enroll: the next start would refuse the saved registration, so the single-use token is kept.
- An enrollment token the server refuses (401, 403 or 410) no longer retries forever. The agent keeps running, answers `503` on authenticated routes and logs that a new install command is needed.

### Other changes you may notice

- Without `EIGHTLY_STATE_PATH` the state file is `eightly-agent.json` (`state.json` on Windows) in `--data`. With the default data directory nothing changes.
- An agent that cannot register keeps retrying in the background (5 seconds, doubling to 5 minutes).
- Docker: mount `/var/run/docker.sock` without `:ro` (it never protected the Docker API) and give the agent the socket's group with `--group-add "$(stat -c %g /var/run/docker.sock)"`, or `DOCKER_GID` in `.env` for compose.
- The agent tells Eight.ly OS its port when it renews its token, so an agent on a port other than 9001 stays reachable after renewal.

## Breaking change policy

- Minor versions (2.x) will not break the API, config flags, or env vars, except where a security fix requires it. 2.1.0 is such a release; its breaking changes are listed above and first in the changelog
- If a breaking change is unavoidable, it will be in a major version bump with migration docs
- Deprecations will be announced at least one minor version before removal
