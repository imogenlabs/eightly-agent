---
description: "The agent's trust model, bind roots, self-protection, and network exposure."
---

# Security Model

Eight.ly Agent mediates access to the Docker daemon. Understanding the trust model is critical before deploying it.

**Who's trusted:** Anyone with a valid HMAC-signed Bearer token for this agent. A token must name this agent's ID, come from Eight.ly OS (issuer `eightly-server`, type `agent`) and carry an issue time and an expiry at most 30 days later; anything else gets 401. A valid token reaches the Docker API on the host through the agent's proxy. It forwards only an allow-list of Docker endpoints and refuses the ways out of a container it knows (privileged mode, host namespaces, extra capabilities, devices, host folders outside `--allowed-bind-roots`, and exec or attach into a container that already reaches the host). Treat the token and the shared secret as root-level credentials all the same.

**What the secret protects:** The shared secret (`EIGHTLY_AGENT_SECRET`) gates all Docker operations, file browser access, host info queries, and WebSocket console sessions. Without it, the agent rejects every request.

**What happens if it leaks:** An attacker with the secret can sign tokens for any agent that uses it and drive the Docker API on every one of those hosts. The proxy stands in the way of the known container escapes, but **rotate the secret immediately** if you suspect compromise.

**Bind roots and symlinks.** With `--allowed-bind-roots` set, a container may mount host paths under those roots, and a container that can write under a root could plant a symlink there for a later mount to follow. The agent refuses a mount through a symlink at create and again at start and restart. On the host it can always look. In a container (the Docker image) it can look only at roots mounted into it at the same path, so container installs mount each root at the same path, read-only (`-v /opt/appdata:/opt/appdata:ro`). For a root that is set but not mounted, the agent cannot check anything, and refuses a restart policy other than `no` on containers that mount under it. The remaining risk is the same in both: a symlink planted after the agent's check at create or start, or before the daemon restarts a container on its own after a reboot or crash, which never passes the agent, is still followed. Pick roots no container can write to the top of.

**What the agent keeps to itself:** its own container cannot be stopped, killed, paused, removed, restarted, updated or renamed through it; its volumes (the state and key in `/data`) cannot be mounted by another container or opened in the file browser; on the host its state, key, certificate and secret files cannot be reached by a bind; and no image can be tagged into its own image repository.

**Network exposure:**

- Bind the agent to a private interface or VPN -- never expose port 9001 to the public internet.
- The agent pins the EOS server's TLS certificate on first use (TOFU). The leaf SPKI fingerprint observed during the first successful registration is persisted in the agent state file and enforced on every later heartbeat and re-registration — a self-signed appliance server is trusted without a CA, but a swapped cert trips the pin. Pass `--server-ca` to *additionally* verify the server chain against a custom CA (the leaf pin still applies). `--insecure-server-tls` disables both verification and pinning — never use it in production.
- Place the agent behind a firewall that restricts access to your EOS server's IP.
- The agent's bearer token lasts 30 days, and the agent renews it on its own from day 15. If an agent was off long enough for its token to lapse, Eight.ly OS marks it **Re-enroll needed**: generate an install command under **Environments → Add Agent** and run it on the host. The agent uses the new enrollment token in place of the lapsed saved one.
- The state file holds the agent's bearer token, so the agent refuses to load it unless the file is 0600 and its directory 0700, both owned by the agent's uid. A new Docker volume directory starts at 0755; on start the agent tightens a directory it owns to 0700 and logs it. If it cannot (another owner, a read-only mount) it says so and keeps refusing, and the fix is `chown` the directory to the agent's uid (1000 in the image) and `chmod 700` it. The tightened directory is the state file's own, which by default is `/data`, the same directory as `--data`. The agent only does this when the directory holds nothing but its own files (the state file, and its TLS key pair when the directory is also `--data`). In a shared directory, such as a home directory, it changes nothing, logs which other files are there, and keeps refusing the saved state, so point `--state-path` at a private directory instead. A bind mount Docker creates for you (`-v $(pwd)/data:/data`) is owned by root, so the agent can neither tighten it nor save its state there; use a named volume or a directory owned by uid 1000.

For vulnerability reporting, see [SECURITY.md](https://github.com/imogenlabs/eightly-agent/blob/main/SECURITY.md).
