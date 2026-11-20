# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.1.0] - 2026-11-02

### Breaking changes

Read [Upgrading to 2.1.0](docs/UPGRADING.md#upgrading-to-210) before you upgrade. These used to work and are now refused:

- **Host folders in containers.** A container may bind a host folder only under `--allowed-bind-roots` / `EIGHTLY_ALLOWED_BIND_ROOTS`, which is empty by default: containers get named volumes only. Set it to the folders your apps keep their data in, for example `/opt/appdata`, or creating and recreating those apps from the Eight.ly OS dashboard is refused with `403`.
- **Consoles and file uploads into containers that reach the host.** Exec, attach, the WebSocket console and file uploads (`PUT /containers/{id}/archive`) are refused with `403` for the agent's own container and for any container that is privileged, shares a host namespace, holds the Docker socket, binds a host folder outside the bind roots, has a capability beyond Docker's defaults or a host device. Containers like that keep running and can still be started, stopped and restarted.
- **Docker endpoints not on the allow-list.** The Docker proxy forwards only a listed set of methods and paths and refuses everything else with `403`. Among the refused: image builds, `commit`, container export and archive download, image load, save and push, registry login, and all of Swarm, services, tasks, nodes, secrets, configs and plugins, reads included.
- **The file browser reads Docker's volumes folder.** Mount `/var/lib/docker/volumes` into the agent's container at the same path, or set `--volumes-path`. Without it the browser answers `503`.
- **Tokens must carry `"type":"agent"`, `iat` and `exp`**, at most 30 days apart. Tokens from Eight.ly OS already do; a token you sign yourself must too.
- **Linux packages and the Windows service keep their secrets in files.** `.deb` / `.rpm` installs read `/etc/eightly-agent/agent.env`; the Windows service reads its secret and enrollment token from files and must be reinstalled.
- **Bind roots in a container install must be mounted into the agent.** When the agent runs in a container, mount each `--allowed-bind-roots` folder into it at the same path, read-only (`-v /opt/appdata:/opt/appdata:ro`), so it can check binds under it for symlinks. A root that is not mounted still works for containers without a restart policy, but a restart policy other than `no` is refused on any container that mounts a path under it.

### Security
- The Docker proxy is an allow-list. Only the listed method and path pairs reach Docker; everything else is refused with `403`. Container create, container update, exec create, volume create and network create bodies are read and refused when they ask for privileged mode; a host or another container's PID, IPC, UTS, user, cgroup or network namespace; a capability beyond Docker's default set; a host folder outside the bind roots; `VolumesFrom`; a non-local volume driver; host devices, device requests or device cgroup rules; a runtime other than `runc`; runtime annotations; masked or read-only path overrides; a security option outside the default profiles, `no-new-privileges` and SELinux user, role and level labels; a sysctl that is not namespaced; `CgroupParent`; `OomKillDisable`; or a tmpfs over `/`, `/proc`, `/sys` or `/etc`. A new network must use the `bridge`, `macvlan` or `ipvlan` driver, and joining a network whose driver is `host` is refused. Paths are cleaned and every `/vX.Y` prefix removed before the check, so the proxy checks exactly what Docker will route.
- `--allowed-bind-roots` / `EIGHTLY_ALLOWED_BIND_ROOTS` names the host folders containers may bind. Empty, the default, allows none. A system folder, a folder inside one or a parent of one (`/`, `/etc`, `/usr`, `/var`, `/opt`, `/var/lib/docker` and others) stops the agent at startup. When the agent runs on the host it also refuses a bind that runs through a symlink below its root, at create and again at start.
- Named volumes are held to the same rule. A volume created with local bind options (`o=bind`, `device=/path`) must point under a bind root, a host block device is refused, NFS, CIFS and tmpfs volumes are allowed, and options for any other volume driver are refused. Every named volume a new container mounts is looked up and checked the same way.
- Exec, attach, exec start, file upload and the WebSocket console check the container they reach and refuse the agent's own container and any container that reaches the host. The request goes on under the full container ID that was checked, so renaming a container in between cannot swap the target. `top` is refused on the agent's own container and on any container holding the Docker socket.
- Container inspect hides environment values: every `EIGHTLY_*` value, every value of the agent's own container and of any container holding the Docker socket, and the values of `--secret` and `--enroll-token` in a container's command line.
- Image pulls from a URL or a tarball (`fromSrc`) are refused; registry pulls work as before.
- A Docker request body the proxy must read is refused with `413` when it is over 1 MiB, instead of being cut short and forwarded. A body the proxy cannot read is refused with `403`.
- On Windows the agent sets a protected access list (SYSTEM, Administrators and its own account) on its data folder, state file, secret file, enrollment token file, TLS key and certificate, takes ownership of any of them another account owns, and refuses to load the state, secret or key while any other account has access or ownership.
- The file browser refuses any path with a segment starting with `.` (`.ssh`, `.env`, `.git`) with `403` on every route, not only downloads, and refuses pipes, sockets and devices with `400`.
- The container health check accepts only the agent's own certificate, byte for byte, and ignores its dates, so another listener on the port is unhealthy and a certificate past its first year is not.
- A saved registration with no agent ID or token is ignored, and a registration reply without them is treated as a failure.
- A Docker request body that names the same key twice, at any depth and in any letter case, is refused. Docker and the proxy could read such a body differently.
- A container the daemon does not know is answered `404` by the agent and never forwarded by name, and every checked route (start, stop, restart, kill, pause, update, rename, delete, top, exec, attach, archive, the WebSocket console) goes on under the inspected full ID.
- The agent's own container cannot be stopped, killed, paused, removed, restarted, updated or renamed through it, and a container holding the Docker socket cannot be renamed. Nothing can be tagged into the agent's own image repositories.
- The agent's own volumes and bind sources, and on a host install its state, key, certificate and secret files, cannot be mounted by another container. The file browser applies the same volume check and refuses a path that runs through a symlink.
- `GET /containers/json` hides the command line of the agent and of socket holders, and the values of secret flags in every other container.
- `/images/create` is a registry pull only: `fromImage` is required and no body is accepted.
- Windows drive, UNC and colon bind sources are refused, and a volume source must be a Docker volume name. Start and restart re-check volumes bound under a root for symlinks, as they do binds.
- On Windows a folder holding the agent's files is refused when another account may add or delete files in it, and only the agent's configured files mark a folder as its own, so it no longer re-locks folders that merely hold a `.json` file.

### Changed
- An agent that registers with Eight.ly OS now answers `503 Service Unavailable` with `Retry-After: 30` on every route that needs a token until it knows its own ID. Before, an agent waiting to register accepted a valid token for any agent. The health probes (`/ping`, `/healthz`, `/readyz`, `/version`) keep answering, and an agent with a saved registration is not affected.
- An agent that cannot register at startup keeps trying in the background, after 5 seconds and then twice as long each time up to 5 minutes, instead of waiting for a restart.
- Tokens must now carry `"type":"agent"`, an issue time (`iat`) and an expiry (`exp`) no more than 30 days after it. A token without an expiry used to be accepted forever. A token issued more than 5 minutes in the future is refused.
- `EIGHTLY_METRICS_ENABLED`, `EIGHTLY_BROWSE_READ_ONLY` and `EIGHTLY_INSECURE_NO_AUTH` accept only `true`/`false`, `1`/`0`, `yes`/`no` or `on`/`off`. Any other value stops the agent at startup with an error naming the variable; before, a typo was silently ignored.
- The state file defaults to `eightly-agent.json` (`state.json` on Windows) in the data directory, so it follows `--data` / `DATA_PATH`. It was fixed at `/data`, which the packaged service could not write, so the agent spent its enrollment token on a registration it could not save. The agent now checks that the state file is writable before it uses the token.
- The `.deb` and `.rpm` packages read settings from `/etc/eightly-agent/agent.env`, created root-owned with mode `0600` and never overwritten on upgrade. Move a secret kept in a `systemctl edit` drop-in there: drop-ins are readable by every user and `systemctl show` prints them.
- The Windows service reads the shared secret from `C:\ProgramData\eightly-agent\secret` and the enrollment token from `C:\ProgramData\eightly-agent\enroll-token` (`eightly-agent.exe install --secret-file <path> --enroll-token-file <path>` picks others), instead of the `EIGHTLY_AGENT_SECRET` and `EIGHTLY_ENROLL_TOKEN` Machine variables, which every local user could read. Reinstall the service; it refuses to start while a variable is set alongside its file.
- The file browser reads volumes from `--volumes-path` / `EIGHTLY_VOLUMES_PATH`, by default `/var/lib/docker/volumes`, instead of `<data>/volumes`. The data directory is the agent's own state folder, so real volumes answered `404`. `docker-compose.yml` now binds `/var/lib/docker/volumes` at the same path; an install that bound it at `/data/volumes` as the old instructions said keeps working. Where the browser cannot work it says why: `501` on Docker Desktop (volumes live inside its VM), `503` naming the folder when it does not exist, and `403` when the agent may not read it.
- Docker instructions and `docker-compose.yml` no longer mount the socket `:ro`: that marks the socket file, not the Docker API, so it never protected anything. Compose takes the socket's group from `DOCKER_GID` (default 999); with `docker run`, pass `--group-add "$(stat -c %g /var/run/docker.sock)"`.
- Releases build, package and sign exactly the commit the version tag points to, and the `latest` image tag moves only when the release is the newest version.

### Added
- `--allowed-bind-roots` / `EIGHTLY_ALLOWED_BIND_ROOTS`, described under Security.
- `--volumes-path` / `EIGHTLY_VOLUMES_PATH`: where the file browser finds Docker's named volumes.
- `--enroll-token-file` / `EIGHTLY_ENROLL_TOKEN_FILE` read the enrollment token from a file on any platform, closed to other users on Windows as the secret file is. A missing or empty file means no token, so the file can be deleted once the agent has registered. Setting it together with `--enroll-token` or `EIGHTLY_ENROLL_TOKEN` is refused.
- `--secret-file` / `EIGHTLY_AGENT_SECRET_FILE` read the shared secret from a file on any platform. Setting it together with `--secret` or `EIGHTLY_AGENT_SECRET` is refused.
- `eightly-agent help` prints the usage and exits 0, as `-h` does.
- Documentation site at https://imogenlabs.github.io/eightly-agent/, published through GitHub Pages.

### Fixed
- On Windows the agent could not reach Docker at all: every Docker client in it dialled a Unix socket. It now uses the named pipe, `//./pipe/docker_engine` by default; `--socket` takes either spelling, `//./pipe/name` or `\\.\pipe\name`.
- An agent whose enrollment was taken by the server but whose answer was lost retried the spent single-use token forever. A refusal (401, 403 or 410) now stops the retries; the agent keeps running, answers 503 on authenticated routes and logs that a new install command is needed. Network errors, rate limiting and server errors are still retried.
- A state file that was there but could not be read or parsed (bad JSON, refused permissions) was logged and the agent enrolled again, replacing its identity and dropping its server pin. The agent now refuses to start, says why, and leaves the file alone. A missing file still means enroll.
- A saved registration for one server was used when `--server-url` named another. The agent now refuses to start unless an enrollment token is supplied, in which case it copies the old state to `<state path>.bak` and enrolls with the new server without applying the old server's pin. URLs are compared without case, trailing slash or default port.
- Token lifetimes were converted to a `time.Duration` before the 30-day check, so a huge `exp` overflowed and passed. Lifetimes are now compared in whole seconds.
- An unknown first argument, or a stray word after the flags, stops the agent with the usage and exit status 2. Before, the agent started and dropped every flag after the word, so `eightly-agent strat --port 9002` ran on 9001.
- Token renewal and re-registration report the agent's port. They sent none, so Eight.ly OS reset an agent listening on another port to 9001 at its first renewal and could no longer reach it.
- A WebSocket console client that stopped reading could hold its session open past the idle and 8-hour limits. Every write now has a 10-second deadline, and the limits are enforced separately from the writes.
- `/host` could hang on a stuck `system_profiler`, `diskutil` or PowerShell command. Each one now stops after 15 seconds, and the summary is cached for 30 seconds so polling does not start new commands each time.
- WebSocket console output that arrived in the same packet as Docker's handshake reply was dropped, so a shell could open with no prompt.
- Behind TLS with SNI, a browser origin with the agent's port (`https://agent.example:9001`) was refused as cross-origin.
- A host install that kept its `--secret-file` in the data folder could not restart: the secret was counted as another program's file and the saved registration was refused.

## [2.0.6] - 2026-11-02

### Security
- Built with Go 1.26.10, which fixes a vulnerability in the Go standard library.

## [2.0.5] - 2026-10-12

### Fixed
- Release packaging; the agent itself is unchanged from 2.0.4.

## [2.0.4] - 2026-10-12

### Changed
- Built with Go 1.26.9.

## [2.0.3] - 2026-10-08

### Fixed
- With `--state-path` pointed into a populated folder (a home directory), the first restart narrowed that whole folder to 0700. The agent now tightens only a folder holding nothing but its own files; otherwise it logs what else is there and keeps refusing the saved state.
- Agents lost their connection a month after enrolling. The bearer token lives 30 days, and the only renewal was the re-register after three failed heartbeats, by which time the token had lapsed and the server refused it. The agent now renews from half-life on, while the token is still valid. Once a token has lapsed, it says once that the agent must be re-enrolled and stops sending the dead token to register. A lapsed saved token plus a configured enrollment token now enrolls again instead of reloading the dead state.
- A restarted agent kept losing its registration: its own guard refused the 0755 directory Docker gives a new volume, so it ignored the saved state and tried to enroll again with a spent single-use token (`register failed: status 403`). On start the agent now tightens a state directory it owns to 0700 and logs it; when it cannot, it says why and keeps refusing. The 0600 file check is unchanged.
- The image left a zombie process every 10 seconds once the agent's listener stopped answering: 115,420 on one host. The busybox `wget` health check forked an `ssl_client` helper, and each timed-out check orphaned it onto PID 1, the agent, which never reaps. The image now runs under `tini -s` (it still reaps, and stays quiet, under `docker run --init`), and the health check is a new `eightly-agent healthcheck` subcommand that does the HTTPS GET in-process and trusts only the agent's own certificate from its data directory, so a listener presenting any other certificate, or a certificate it cannot read, is unhealthy. A CI job builds the image and proves both.
- The file browser works through a handle confined to the volume's directory, so a symlink or `..` cannot lead it outside the volume.

## [2.0.2] - 2026-08-16

### Fixed
- The container image builds again on Alpine 3.23, where GID 999 was already taken.
- The `latest` image tag follows the newest release instead of `main`.

### Changed
- Images are published as `ghcr.io/imogenlabs/eightly-agent`.

## [2.0.1] - 2026-08-14

### Security
- Hardening from a full security review: the agent refuses to serve without a secret unless `--insecure-no-auth` is set; the Docker proxy refuses privileged containers, host namespaces, dangerous capabilities and sensitive bind mounts; the EOS server's certificate is pinned on first use; a token is only accepted by the agent it names; WebSocket origins are checked; the file browser no longer follows symlinks out of a volume and can run read-only (`--browse-read-only`); `/host` returns counts and totals only.
- Built with Go 1.26.6.

## [1.0.0] - 2026-05-21

### Changed (BREAKING)
- Server TLS verification now uses the system trust store by default. Users connecting
  to EOS servers with self-signed certificates must now pass `--insecure-server-tls`
  explicitly, or provide a CA with `--server-ca`.

### Added
- Prometheus `/metrics` endpoint behind `--metrics` flag with heartbeat, registration,
  WebSocket, and Docker socket reachability gauges/counters (closes [#10](https://github.com/imogenlabs/eightly-agent/issues/10))
- `BuildServerTLSConfig` helper centralizes outbound TLS configuration (system trust
  store default, optional `--server-ca` override, `--insecure-server-tls` escape hatch)
- `--insecure-server-tls` flag to skip server certificate verification
- `/healthz` liveness probe (always returns `200 {"status":"ok"}`)
- `/readyz` readiness probe (returns `200` if Docker socket is reachable, `503` otherwise)
- Configurable TLS SANs via `--tls-san` flag (multiple values supported)
- Token expiry (`exp` claim) enforcement in HMAC auth middleware
- 134 tests across 10 packages

### Security
- Outbound connections to EOS servers now verify certificates by default instead of
  skipping verification

## [1.0.0-rc1] - 2026-05-21

### Added
- Health check endpoints: `/healthz` (liveness) and `/readyz` (readiness with Docker socket check)
- `BuildServerTLSConfig` for centralized outbound TLS with system trust store default
- `--insecure-server-tls` flag for opt-in certificate skip
- Configurable `--tls-san` for auto-generated TLS certificates
- Token expiry enforcement in auth middleware
- Cosign keyless signing for release binaries and container images
- SLSA build provenance attestations (via actions/attest-build-provenance)
- SBOM generation (SPDX format)
- `.deb` and `.rpm` packaging via nfpm (amd64 + arm64)
- Homebrew tap (`smashingtags/homebrew-tap`)
- Scoop bucket for Windows (`smashingtags/scoop-eightly`)
- GitHub wiki with 10 documentation pages
- SECURITY.md with threat model and coordinated disclosure policy
- CONTRIBUTING.md and CODE_OF_CONDUCT.md

### Known Limitations
- CI runs on self-hosted Linux runners. Windows and macOS builds are cross-compiled but not executed in CI. Community testing on those platforms is encouraged.
- At the time of this release, build provenance and SBOM attestations required
  a public repository. The repository became public on November 19, 2026.

## [1.0.0-beta1] - 2026-05-21

### Added
- Cross-platform support: Linux (amd64/arm64/arm/v7), macOS (Intel/Apple Silicon), Windows (amd64)
- Docker API proxy over authenticated HTTPS with HMAC-SHA256 tokens
- WebSocket container console (attach + exec) with CRLF injection protection
- File browser for Docker volumes with path traversal hardening
- Host info reporting — Linux (sysfs + NVMe fallback), macOS (system_profiler/diskutil), Windows (PowerShell)
- Auto-registration with Eight.ly OS + 30-second heartbeats with exponential backoff
- Auto-TLS certificate generation with configurable SANs (--tls-san)
- Windows service support (install/uninstall/start/stop/status via SCM)
- Platform-specific defaults (unix socket vs named pipe, /data vs ProgramData)
- Token expiry enforcement (exp claim)
- Fail-closed authentication — refuses to start without secret unless --insecure-no-auth
- Configurable server CA for outbound TLS (--server-ca)
- Re-registration with existing token authentication
- Same-origin WebSocket enforcement when authentication is disabled
- systemd service file with privilege dropping (non-root user, NoNewPrivileges, ProtectSystem)
- Dockerfile runs as non-root eightly-agent user
- Docker Compose file with .env.example
- Homebrew tap (smashingtags/homebrew-tap)
- Scoop bucket for Windows (smashingtags/scoop-eightly)
- .deb and .rpm packaging via nfpm
- Cosign-signed release binaries and container images
- SLSA build provenance attestations
- SBOM generation (SPDX)
- 134 tests across 10 packages
- GitHub wiki with 10 documentation pages
- SECURITY.md with threat model and disclosure policy
- CONTRIBUTING.md and CODE_OF_CONDUCT.md
- Apache 2.0 license

### Security
- Path traversal protection with filepath.Rel containment + symlink evaluation
- Volume ID validation (alphanumeric identifiers only)
- Container/exec ID validation against CRLF injection
- Token expiry enforcement
- Upload size limit (100MB via MaxBytesReader)
- TLS directory permissions (0700)
- Non-root container execution
- systemd sandboxing (ProtectSystem=strict, NoNewPrivileges)

[Unreleased]: https://github.com/imogenlabs/eightly-agent/compare/v2.1.0...HEAD
[2.1.0]: https://github.com/imogenlabs/eightly-agent/compare/v2.0.6...v2.1.0
[2.0.6]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.6
[2.0.5]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.5
[2.0.4]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.4
[2.0.3]: https://github.com/imogenlabs/eightly-agent/compare/v2.0.2...v2.0.3
[2.0.2]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.2
[2.0.1]: https://github.com/imogenlabs/eightly-agent/releases/tag/v2.0.1
[1.0.0]: https://github.com/imogenlabs/eightly-agent/releases/tag/v1.0.0
[1.0.0-rc1]: https://github.com/imogenlabs/eightly-agent/releases/tag/v1.0.0-rc1
[1.0.0-beta1]: https://github.com/imogenlabs/eightly-agent/releases/tag/v1.0.0-beta1
