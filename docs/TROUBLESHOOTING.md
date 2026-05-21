# Troubleshooting

## Agent won't start

**"EIGHTLY_AGENT_SECRET is required"**
Set the secret via environment variable or flag. Generate one with `openssl rand -hex 32`. For development only, use `--insecure-no-auth`.

**"failed to generate TLS cert"**
The agent can't write to the data directory. Check permissions: the directory should be writable by the eightly-agent user (or root if running as root).

**Port already in use**
Another process is using port 9001. Use `--port` to pick a different port, or find the conflicting process: `lsof -i :9001` (Linux/macOS) or `netstat -ano | findstr 9001` (Windows).

## Registration fails

**"registration failed, will retry via heartbeat"**
The agent can't reach the EOS server. Check:
1. `EIGHTLY_SERVER_URL` is correct and reachable
2. The enrollment token is valid and unused
3. Firewall allows outbound HTTPS to the EOS server
4. If using `--server-ca`, the CA cert matches the server's certificate

**Token expired**
EOS enrollment tokens are single-use and may have an expiry. Generate a new one from the EOS admin panel.

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
The connecting client doesn't trust the agent's self-signed certificate. Use `InsecureSkipVerify` on the client side, or deploy a proper CA-signed certificate.

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
- `EIGHTLY_AGENT_SECRET` not set as a system environment variable
- Docker Desktop not running
- Port 9001 in use

**How to set environment variables for the service**
```powershell
[System.Environment]::SetEnvironmentVariable("EIGHTLY_AGENT_SECRET", "your-secret", "Machine")
```
Restart the service after changing variables.

## Debug logging

Run with `--log-level debug` (or `LOG_LEVEL=debug`) to see detailed output including heartbeat timing, Docker socket communication, and TLS certificate details.

For the Docker container:
```bash
docker run -e LOG_LEVEL=debug ...
```

For the Windows service, set `LOG_LEVEL=debug` as a system environment variable and restart.
