# Troubleshooting

## Agent won't start

**"EIGHTLY_AGENT_SECRET is required"**
Set the secret via environment variable, flag or secret file (`--secret-file` / `EIGHTLY_AGENT_SECRET_FILE`). Generate one with `openssl rand -hex 32`. For development only, use `--insecure-no-auth`.

**"both a secret ... and a secret file ... are set"**
Keep only the secret file. On Windows remove the old Machine variable: `[Environment]::SetEnvironmentVariable('EIGHTLY_AGENT_SECRET', $null, 'Machine')`.

**"... is not a boolean; use true or false"**
`EIGHTLY_METRICS_ENABLED`, `EIGHTLY_BROWSE_READ_ONLY` and `EIGHTLY_INSECURE_NO_AUTH` take `true`/`false`, `1`/`0`, `yes`/`no` or `on`/`off`. Fix or unset the variable named in the error.

**"failed to generate TLS cert"**
The agent can't write to the data directory. Check permissions: the directory should be writable by the eightly-agent user (or root if running as root).

**"cannot use the saved registration at ..."**
The state file (`EIGHTLY_STATE_PATH`) exists but cannot be read or parsed. The agent will not start rather than enroll over the identity and server pin it holds, and it leaves the file as it is. If the error names permissions or an owner, fix those. If the file is damaged, move it aside and run a new install command from **Environments → Add Agent**.

**"the saved registration at ... is for server A, but the configured server URL is B"**
The agent is registered with another server. To keep it there, set `EIGHTLY_SERVER_URL` back to the old URL. To move it, run an install command generated on the new server: its enrollment token tells the agent to enroll there, and the old state is copied to `<state path>.bak` first (replacing any older backup). Differences in case, a trailing slash or a default port do not count as another server.

**"--allowed-bind-roots: ... is, holds or is inside the system directory ..."**
A bind root names a system folder, a folder inside one or a parent of one (`/`, `/opt`, `/var`, `/etc` and others). Name the app data folder itself, such as `/opt/appdata`. Each root must be absolute, with no trailing slash.

**"both an enrollment token ... and a token file ... are set"**
Keep only the file. On Windows remove the old Machine variable: `[Environment]::SetEnvironmentVariable('EIGHTLY_ENROLL_TOKEN', $null, 'Machine')`.

**Usage printed and exit status 2**
An unknown word on the command line: a misspelt subcommand, or an argument after the flags. Check the service's command line; `eightly-agent help` lists the flags.

**Port already in use**
Another process is using port 9001. Use `--port` to pick a different port, or find the conflicting process: `lsof -i :9001` (Linux/macOS) or `netstat -ano | findstr 9001` (Windows).

## Registration fails

**"registration failed" and every authenticated request answers 503**
The agent can't reach the EOS server, or the server failed. The agent keeps retrying with a growing pause (up to 5 minutes), and authenticated routes answer 503 until it registers. Check:
1. `EIGHTLY_SERVER_URL` is correct and reachable
2. Firewall allows outbound HTTPS to the EOS server
3. If using `--server-ca`, the CA cert matches the server's certificate

**"the server refused the enrollment token"**
The server answered 401, 403 or 410: the enrollment token is unknown, expired or already used, or the agent it was made for has been deleted. Tokens are single-use, so this also happens when the server accepted the token but its answer never reached the agent. Retrying cannot help, so the agent stops trying, keeps running, and answers 503 on authenticated routes. Generate a new install command under **Environments → Add Agent** and run it on the host.

**"not registering: the registration state cannot be saved"**
The agent will not spend its single-use enrollment token while it cannot save the result. Make the state directory writable by the agent's user, or set `EIGHTLY_STATE_PATH` to a writable file.

**"agent token expired, so this agent must be re-enrolled"**
The agent was off for longer than its 30-day token lasts. Generate a new install command under **Environments → Add Agent** and run it on the host.

## Docker socket errors

**"docker connect failed"**
The Docker socket isn't accessible. Check:
- Linux: `/var/run/docker.sock` exists and the agent user is in the `docker` group
- macOS: Docker Desktop is running
- Windows: Docker Desktop is running and the named pipe `//./pipe/docker_engine` is available

**Permission denied on Docker socket**
The agent process doesn't have permission to access the socket. Either run as root (not recommended) or add the agent user to the `docker` group: `sudo usermod -aG docker eightly-agent`

## Docker requests refused (403)

Every refusal returns `{"message":"forbidden: <reason>"}` and logs `[SECURITY] proxy.denied` (`websocket.denied` for the console) with the reason.

**"host bind mount rejected: ... (no --allowed-bind-roots are set; use a named volume)"** or **"... is outside --allowed-bind-roots"**
The container binds a host folder the agent does not allow. Add the folder or a parent of it to `EIGHTLY_ALLOWED_BIND_ROOTS` and restart the agent, or use a named volume.

**"the agent's own container is off limits"**, **"container ... holds the Docker socket"**, **"container <id>: ..."**
Exec, attach, the console or a file upload aimed at a container that itself reaches the host. It keeps running and can be started and stopped, but nothing can be run inside it through the agent.

**"endpoint not allowed: <METHOD> <path>"**
The Docker endpoint is not on the allow-list (see the API reference). Builds, commits, exports, image load and push, and Swarm are not available through the agent.

## File browser

**501** — Docker Desktop keeps volumes inside its VM; the browser is not available unless `--volumes-path` names a folder that holds them.

**503 naming a folder** — the volumes folder is not there. Mount `/var/lib/docker/volumes` into the agent's container at the same path, or set `--volumes-path`.

**403 "volume is not readable by the agent"** — the agent's user cannot read the volume. Docker's own volumes folder is root-only, so an agent running on the host as its own user (the `.deb`/`.rpm` service) gets this for every volume.

## TLS errors

**Certificate SAN mismatch**
The EOS server is connecting by an IP or hostname not in the agent's certificate SANs. Use `--tls-san` to add the correct hostname/IP, or use `--advertise-ip` to set the IP that appears in the cert.

**"x509: certificate signed by unknown authority"**
The connecting client (typically the EOS server, or curl) doesn't trust the agent's self-signed certificate. Options:
- Provide a proper CA-signed certificate to the agent via `--tls-san` with the correct hostnames
- Configure the EOS server to trust the agent's self-signed cert via `--server-ca`
- For curl debugging only: use `--insecure` (curl flag, never in production)

For outbound connections to EOS, the agent pins the server's certificate on first registration and refuses a different one afterwards, so a self-signed EOS server needs no extra flag. `--server-ca` adds chain verification against your CA. `--insecure-server-tls` turns verification and pinning off and is for development only.

## WebSocket issues

**Connection drops immediately**
Check that the container/exec ID is valid (12-64 letters and digits). A `403` before the upgrade means the target container was refused (see "Docker requests refused" above). Check Docker logs for the container — it may have exited.

**"invalid container id" / "invalid exec id"**
The ID doesn't match the expected format: 12-64 letters and digits. Docker container IDs are 12-64 character hex strings. Make sure you're passing the correct ID.

## Windows service

**"service already exists"**
Run `eightly-agent.exe uninstall` first, then `eightly-agent.exe install`.

**Service starts but immediately stops**
Check Event Viewer > Windows Logs > Application for EightlyAgent errors. Common causes:
- The secret file (`C:\ProgramData\eightly-agent\secret` by default) is missing or empty
- `EIGHTLY_AGENT_SECRET` is still set as a Machine variable next to the secret file
- Another account has access to the secret file, state file or TLS key (check with `icacls`)
- Docker Desktop not running
- Port 9001 in use

The service's own log is not written to Event Viewer. To read it, stop the service and run `eightly-agent.exe --secret-file C:\ProgramData\eightly-agent\secret --log-level debug` in an elevated PowerShell.

**How to set the secret for the service**
```powershell
Set-Content -NoNewline -Path C:\ProgramData\eightly-agent\secret -Value 'your-secret'
```
Do not use a Machine environment variable for the secret: every local user can read those. Restart the service (`stop`, then `start`) after changing it.

## Debug logging

Run with `--log-level debug` (or `LOG_LEVEL=debug`) to see detailed output including heartbeat timing, Docker socket communication, and TLS certificate details.

For the Docker container:
```bash
docker run -e LOG_LEVEL=debug ...
```

For the Windows service, run the agent by hand as shown above; the service discards its log output.

## Platform testing limitations

Linux CI runs on GitHub-hosted runners. The test suite also runs on a GitHub-hosted Windows runner; macOS builds are verified by cross-compilation only. Community testing on Windows and macOS is encouraged. If you encounter platform-specific issues, please file a bug report with your platform details.
