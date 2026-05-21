# Troubleshooting

Common issues with Eight.ly Agent and how to resolve them.

## Enable Debug Logging

The first step for any issue is to enable debug logging. This switches output from JSON to human-readable format and includes heartbeat confirmations, startup config details, and request-level information.

**Docker**:
```bash
docker run -d \
  -e LOG_LEVEL=debug \
  ... \
  ghcr.io/smashingtags/eightly-agent:latest
```

Or for a running container:
```bash
docker stop eightly-agent
docker rm eightly-agent
# Re-run with LOG_LEVEL=debug
```

**Binary**:
```bash
./eightly-agent --log-level debug ...
```

**Viewing logs**:
```bash
docker logs -f eightly-agent
```

At `debug` level, you will see heartbeat confirmations (`heartbeat ok`), startup configuration values, and the agent version/port/socket path at startup.

---

## Agent Will Not Register

**Symptoms**: Agent starts but never appears in the EOS dashboard. Log shows:
```
WRN registration failed, will retry via heartbeat
```

**Check these in order**:

1. **Is the server URL correct?**
   ```bash
   curl -k https://your-eos-server:7001/api/health
   ```
   If this times out or returns an error, the agent cannot reach EOS.

2. **Is the enrollment token valid?**
   EOS enrollment tokens are single-use and may expire. Generate a new one from the EOS admin panel and restart the agent with the new token.

3. **Network connectivity**
   The agent needs to reach EOS on the configured port. Check for firewalls, VPN requirements, or DNS resolution issues.
   ```bash
   # From the agent host
   curl -k -v https://your-eos-server:7001/api/agents/register
   ```

4. **State file from a previous registration**
   If a state file exists at the configured `--state-path`, the agent skips registration and goes straight to heartbeat. If the state is stale (different EOS server, old agent ID), delete the state file and restart:
   ```bash
   docker exec eightly-agent rm /data/eightly-agent.json
   docker restart eightly-agent
   ```

---

## Heartbeat Failures

**Symptoms**: Agent is registered but shows as offline in EOS. Logs show:
```
WRN heartbeat failed  error="heartbeat: 401" failures=1
WRN heartbeat failed  error="heartbeat: 401" failures=2
WRN re-registering after repeated heartbeat failures
```

**Causes and fixes**:

- **401 status**: The agent's token was invalidated on the EOS side (e.g., the agent was deleted and re-added in EOS). Delete the state file and restart the agent to re-register.

- **Connection refused / timeout**: EOS is unreachable. The agent will back off (30s -> 60s -> 120s -> 240s -> 300s) and attempt re-registration after 3 consecutive failures. Once EOS is reachable again, the agent recovers automatically.

- **Exponential backoff stuck at 5 minutes**: After sustained failures, the heartbeat interval caps at 300 seconds. If EOS is back online, the agent will eventually reconnect, but it may take up to 5 minutes. Restart the agent for immediate reconnection.

---

## Docker Socket Permission Denied

**Symptoms**: Requests to the Docker proxy return errors. Agent may log:
```
docker connect failed
```

**Fixes**:

1. **Check socket permissions**:
   ```bash
   ls -la /var/run/docker.sock
   # srw-rw---- 1 root docker 0 May 20 10:00 /var/run/docker.sock
   ```

2. **Docker container**: Ensure the socket is mounted correctly:
   ```yaml
   volumes:
     - /var/run/docker.sock:/var/run/docker.sock
   ```

3. **Binary mode**: The user running the agent must be in the `docker` group or be root:
   ```bash
   sudo usermod -aG docker $USER
   ```

4. **Socket path mismatch**: If Docker uses a non-standard socket path (e.g., rootless Docker at `$XDG_RUNTIME_DIR/docker.sock`), configure it:
   ```bash
   -e DOCKER_SOCKET=/run/user/1000/docker.sock
   ```

---

## TLS Errors

### Client gets "certificate signed by unknown authority"

The agent uses a self-signed certificate by default. Clients must skip certificate verification:

```bash
# curl
curl -k https://agent-host:9001/ping

# Go
&http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
```

Or use custom certificates from a trusted CA. See [[TLS]].

### "failed to generate TLS cert"

The agent cannot write to the data directory. Check:

```bash
# Directory exists and is writable
ls -la /data/
# Or for Docker, check the volume mount
docker inspect eightly-agent | grep -A5 Mounts
```

### "failed to load TLS cert"

The cert or key file exists but is malformed. Delete both and restart to regenerate:

```bash
rm /data/cert.pem /data/key.pem
# Restart agent
```

### Certificate expired

Auto-generated certificates are valid for 1 year. Check expiration:

```bash
openssl s_client -connect localhost:9001 </dev/null 2>/dev/null | openssl x509 -enddate -noout
```

To renew, delete both cert files and restart the agent. Note: this will change the certificate fingerprint, requiring re-acceptance in EOS if TOFU is in use. See [[TLS]].

---

## WebSocket Connection Drops

**Symptoms**: Terminal session disconnects unexpectedly.

**Possible causes**:

1. **Container exited**: When the container or exec process exits, Docker closes the stream and the WebSocket terminates. Check if the container is still running:
   ```bash
   curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/containers/<id>/json | jq .State.Running
   ```

2. **Write timeout**: The HTTP server has a 30-minute write timeout. Sessions idle for over 30 minutes may be terminated. Reconnect if this happens.

3. **Network interruption**: Any break in the TCP connection between the client and agent will terminate the WebSocket. There is no automatic reconnection built into the agent.

4. **Reverse proxy timeout**: If a load balancer or reverse proxy sits between the client and agent, ensure it has WebSocket support enabled and a long enough idle timeout (at least 30 minutes).

### WebSocket returns "docker connect failed"

The agent cannot connect to the Docker socket. Same as the Docker socket permission issue above.

### WebSocket returns "docker attach failed" or "docker exec start failed"

Docker rejected the attach or exec request. Common causes:
- The container ID does not exist
- The container is not running (for attach mode)
- The exec ID is invalid or already started (for exec mode)

---

## File Browser 403 / Permission Denied

**Symptoms**: File browser operations fail with 500 errors containing permission messages.

**Fixes**:

1. **Volume mount for file browser**: The agent resolves file paths as `<data-path>/volumes/<volumeID>/_data/<path>`. Ensure Docker volume data is accessible at this path. For Docker deployments:
   ```yaml
   volumes:
     - /var/lib/docker/volumes:/data/volumes:rw
   ```

2. **File ownership**: Docker volumes may be owned by different UIDs inside containers. The agent process needs read/write access. Running the agent as root (default in the Docker image) typically resolves this.

3. **Path traversal rejection**: If you see `{"error":"path traversal rejected"}`, your `volumeID` or `path` parameter contains `..`. Use clean paths without parent directory references.

---

## Agent Not Responding on Expected Port

1. **Check the port configuration**:
   ```bash
   docker logs eightly-agent 2>&1 | grep "listening"
   # Expected: listening (TLS) addr=0.0.0.0:9001
   ```

2. **Port mapping**: In Docker, ensure the port is published:
   ```bash
   docker port eightly-agent
   # 9001/tcp -> 0.0.0.0:9001
   ```

3. **Firewall**: Check that port 9001 (or your custom port) is open:
   ```bash
   sudo ufw status | grep 9001
   sudo iptables -L -n | grep 9001
   ```

4. **Address binding**: The default `--addr 0.0.0.0` listens on all interfaces. If the agent is configured with `--addr 127.0.0.1`, it only accepts local connections.

---

## Version Mismatch

Check the agent version from the response headers:

```bash
curl -kI https://localhost:9001/ping
# Eightly-Agent: 2.0.0
# Eightly-Agent-API-Version: 2
```

If the EOS dashboard expects a different API version, update the agent to match. Pull the latest image:

```bash
docker pull ghcr.io/smashingtags/eightly-agent:latest
docker stop eightly-agent && docker rm eightly-agent
# Re-run with the same configuration
```

---

## Collecting Diagnostic Information

When reporting an issue, include:

1. Agent version (`Eightly-Agent` header or startup log)
2. Logs at debug level (`LOG_LEVEL=debug`)
3. Docker version on the host (`docker version`)
4. How the agent is deployed (Docker or binary, full command/compose)
5. The specific error message or behavior observed
6. Network topology (is there a reverse proxy, VPN, or firewall between EOS and the agent?)

## See Also

- [[Configuration]] -- All flags and environment variables
- [[Authentication]] -- Token format and common auth errors
- [[TLS]] -- Certificate generation and custom cert setup
- [[Registration-and-Heartbeat]] -- Registration flow and heartbeat recovery
- [[WebSocket-Console]] -- WebSocket protocol and error codes
- [[File-Browser]] -- Path resolution and security model
