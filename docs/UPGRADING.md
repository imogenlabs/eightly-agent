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

## Version history

### 2.0.6 → next release

Nothing to do for a Docker install that uses the image's own settings. Check these before upgrading:

- **Authenticated routes answer 503 until the agent has registered.** When a secret and a server URL are set, every route that needs a token returns `503` with `Retry-After: 30` until the agent knows its own ID. An agent that already has a saved registration is unaffected. One that cannot register keeps retrying in the background (5 seconds, doubling to 5 minutes).
- **Tokens must carry `type`, `iat` and `exp`.** Tokens from Eight.ly OS already do. A token you sign yourself (scripts, monitoring) must have `"iss":"eightly-server"`, `"type":"agent"`, an `iat`, an `exp` at most 30 days later, and this agent's ID in `sub`. See [Authentication](wiki/Authentication.md).
- **A bad boolean environment variable stops startup.** `EIGHTLY_METRICS_ENABLED`, `EIGHTLY_BROWSE_READ_ONLY` and `EIGHTLY_INSECURE_NO_AUTH` must be `true`/`false`, `1`/`0`, `yes`/`no` or `on`/`off`. A typo used to be ignored.
- **`.deb` / `.rpm`: settings move to `/etc/eightly-agent/agent.env`.** The upgrade creates the file (root, mode `0600`) if it is missing and never overwrites it. The unit also sets `EIGHTLY_STATE_PATH=/var/lib/eightly-agent/eightly-agent.json`. If you kept the secret in a `systemctl edit` drop-in, move it into `agent.env`, delete the drop-in, and `sudo systemctl daemon-reload && sudo systemctl restart eightly-agent`.
- **Windows service: the secret lives in a file.** Run `eightly-agent.exe uninstall`, then `eightly-agent.exe install`, write the secret to `C:\ProgramData\eightly-agent\secret`, and remove the old Machine variable with `[Environment]::SetEnvironmentVariable('EIGHTLY_AGENT_SECRET', $null, 'Machine')`. The service refuses to start while both are set. On first start the agent also locks the data folder, state file and TLS key to SYSTEM, Administrators and its own account, and refuses them if another account keeps access.
- **State file default follows the data directory.** Without `EIGHTLY_STATE_PATH` the state file is `eightly-agent.json` (`state.json` on Windows) in `--data`. With the default data directory nothing changes.
- **The Docker proxy refuses more requests.** Besides privileged mode, host namespaces, dangerous capabilities and sensitive host mounts, it now refuses: the Swarm API; changes through the plugin, service, config, secret and node APIs; exec and container update bodies that ask for host access; volume options that bind host paths; unconfined `SecurityOpt`; `MaskedPaths`/`ReadonlyPaths` overrides; host devices and device cgroup rules; non-namespaced sysctls; `CgroupParent`; and tmpfs over `/`, `/proc`, `/sys` or `/etc`. An inspected body over 1 MiB gets `413` instead of being cut short, and a body the agent cannot read gets `403`. The reason is in the response and in a `[SECURITY] proxy.denied` log line.
- **The file browser refuses hidden paths and special files.** Any path with a segment starting with `.` gets `403` on every route, and pipes, sockets and devices get `400`.
- **Docker: no `:ro` on the socket, and the socket's group.** Mount `/var/run/docker.sock` without `:ro` (it never protected the Docker API) and give the agent the socket's group with `--group-add "$(stat -c %g /var/run/docker.sock)"`, or `DOCKER_GID` in `.env` for compose.

### Breaking change policy

- Minor versions (2.x) will not break the API, config flags, or env vars
- If a breaking change is unavoidable, it will be in a major version bump with migration docs
- Deprecations will be announced at least one minor version before removal
