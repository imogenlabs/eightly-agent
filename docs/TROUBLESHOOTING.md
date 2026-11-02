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
The state file (`EIGHTLY_STATE_PATH`) exists but cannot be read or parsed. The agent will not start rather than enroll over the identity and server pin it holds, and it leaves the file as it is. If the error names permissions or an owner, fix those. If the file is damaged, move it aside and run a new install command from the Agents page.

**"the saved registration at ... is for server A, but the configured server URL is B"**
The agent is registered with another server. To keep it there, set `EIGHTLY_SERVER_URL` back to the old URL. To move it, run an install command generated on the new server: its enrollment token tells the agent to enroll there, and the old state is copied to `<state path>.bak` first (replacing any older backup). Differences in case, a trailing slash or a default port do not count as another server.

**Port already in use**
Another process is using port 9001. Use `--port` to pick a different port, or find the conflicting process: `lsof -i :9001` (Linux/macOS) or `netstat -ano | findstr 9001` (Windows).

## Registration fails

**"registration failed" and every authenticated request answers 503**
The agent can't reach the EOS server, or the server failed. The agent keeps retrying with a growing pause (up to 5 minutes), and authenticated routes answer 503 until it registers. Check:
1. `EIGHTLY_SERVER_URL` is correct and reachable
2. Firewall allows outbound HTTPS to the EOS server
3. If using `--server-ca`, the CA cert matches the server's certificate

**"the server refused the enrollment token"**
The server answered 401, 403 or 410: the enrollment token is unknown, expired or already used, or the agent it was made for has been deleted. Tokens are single-use, so this also happens when the server accepted the token but its answer never reached the agent. Retrying cannot help, so the agent stops trying, keeps running, and answers 503 on authenticated routes. Generate a new install command on the Agents page and run it on the host.

**"not registering: the registration state cannot be saved"**
The agent will not spend its single-use enrollment token while it cannot save the result. Make the state directory writable by the agent's user, or set `EIGHTLY_STATE_PATH` to a writable file.

**"agent token expired, so this agent must be re-enrolled"**
The agent was off for longer than its 30-day token lasts. Generate a new install command on the Agents page and run it on the host.

## Docker socket errors

**"docker connect failed"**
The Docker socket isn't accessible. Check:
- Linux: `/var/run/docker.sock` exists and the agent user is in the `docker` group
- macOS: Docker Desktop is running
- Windows: Docker Desktop is running and the named pipe `//./pipe/docker_engine` is available

**Permission denied on Docker socket**
The agent process doesn't have permission to access the socket. Either run as root (not recommended) or add the agent user to the `docker` group: `sudo usermod -aG docker eightly-agent`

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
Check that the container/exec ID is valid (12-64 hex characters). Check Docker logs for the container — it may have exited.

**"invalid container id" / "invalid exec id"**
The ID doesn't match the expected format. Docker container IDs are 12-64 character hex strings. Make sure you're passing the correct ID.

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

CI runs on Linux: self-hosted runners for this repository's own branches, GitHub-hosted runners for pull requests from forks. Windows and macOS builds are verified via cross-compilation but not executed in CI. Community testing on Windows and macOS is encouraged. If you encounter platform-specific issues, please file a bug report with your platform details.
