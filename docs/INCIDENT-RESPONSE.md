# Incident Response — eightly-agent

eightly-agent runs on a remote Docker host and proxies the **Docker socket (host
root)** to the Eight.ly server. A compromise of the agent — or of its bearer
token, its server-cert pin, or the host it runs on — is a host-takeover-class
event. This runbook is the operator playbook: **detect → triage → contain →
eradicate → recover → post-mortem**, with the concrete commands for this agent.

Assumptions: systemd deployment (`eightly-agent.service`, runs as the
`eightly-agent` user, binary at `/usr/bin/eightly-agent`). Containerized
deployments (`ghcr.io/smashingtags/eightly-agent`, container name
`eightly-agent`) are noted inline where commands differ.

Detection contract: [`pentest/expected-detections.yaml`](../pentest/expected-detections.yaml).

---

## 1. Detect

The agent emits structured detection events to its process log (zerolog →
journald). Pull the security signal first:

```sh
# All [SECURITY] events (auth.unauthorized, auth.misconfigured, proxy.denied):
journalctl -u eightly-agent --since "1 hour ago" | grep '\[SECURITY\]'

# Browse mutate audit (path-traversal/symlink/read-only rejections + ok/error),
# with actor attribution and source IP — NOT [SECURITY]-prefixed:
journalctl -u eightly-agent --since "1 hour ago" | grep browse.mutate

# Containerized deployment:
docker logs --since 1h eightly-agent 2>&1 | grep -E '\[SECURITY\]|browse.mutate'
```

What each signal means:

| Signal | Meaning | Source |
|--------|---------|--------|
| `[SECURITY] auth.unauthorized` | Rejected token (missing/forged/expired/wrong issuer). A **burst from one IP** = token brute force. | `internal/auth/jwt.go` |
| `[SECURITY] auth.misconfigured` | Agent started with no secret and no explicit opt-out — **failing closed (503)**. Operational misconfig, not an attack, but every request is being refused. | `internal/auth/jwt.go` |
| `[SECURITY] proxy.denied` | A container/exec create requesting privileged / host namespace / dangerous cap / sensitive bind was **blocked before the Docker socket**. The `reason` names the vector. | `internal/proxy/docker.go` |
| `browse.mutate result=rejected` | Path-traversal or symlink-escape write rejected. | `internal/browse/browse.go` |
| `browse.mutate result=rejected_readonly` | Mutating verb refused on a read-only agent. | `internal/browse/browse.go` |
| `server cert pin mismatch` (register/heartbeat error) | TLS MITM — the server presented a cert whose key does not match the pinned SPKI. The agent **refused to connect**. | `internal/register/pin.go` |
| WS upgrade 403 / "too many sessions" 503 | CSWSH origin reject / WebSocket DoS cap. | `internal/websocket/websocket.go` |

Triage prompts:

```sh
# Top source IPs behind auth failures (token brute hunt):
journalctl -u eightly-agent --since "6 hours ago" \
  | grep 'auth.unauthorized' | grep -oE 'ip=[^ ]+' | sort | uniq -c | sort -rn

# Every blocked host-escape attempt and who/what tried it:
journalctl -u eightly-agent --since "6 hours ago" | grep 'proxy.denied'

# Browse abuse by actor (the actor field is the verified token's sub claim):
journalctl -u eightly-agent --since "6 hours ago" | grep browse.mutate
```

---

## 2. Triage

Classify by what the signal implies about agent integrity. Severity drives how
fast you reach for Section 3.

- **Critical — token or pin compromise / successful escape.** A `proxy.denied`
  is the control *working*; a successful privileged create that you can confirm
  reached the daemon (a privileged container is running that you did not deploy)
  is the control *failing* — treat as host takeover. A cert-pin mismatch that
  you did **not** cause by a planned cert rotation means someone is on-path.
  Leaked bearer token (state file readable by another user, token in a process
  list, token in a backup) = attacker can drive the Docker socket as you.
- **High — credential brute / sustained unauth pressure.** A high-rate burst of
  `auth.unauthorized` from one source. The agent fails closed, but the source is
  hostile and should be cut off at the network.
- **Medium — browse abuse.** Repeated `browse.mutate result=rejected` from one
  actor: someone with a valid token is probing for a traversal/symlink escape.
  The control holds, but rotate that actor's token and review what it can reach.
- **Low / operational — fail-closed misconfig.** `auth.misconfigured` means the
  agent is rejecting *everything* with 503 because it has no secret and no
  opt-out. Not an attack; fix the deployment (Section 6) so it serves again.

Preserve evidence before you change anything:

```sh
mkdir -p ./ir-$(date +%s) && cd ./ir-$(date +%s)
journalctl -u eightly-agent --since "24 hours ago" > agent-journal.log
sudo cp -a /usr/bin/eightly-agent ./eightly-agent.binary       # for hashing
sudo cp -a "$EIGHTLY_STATE_PATH" ./state.json 2>/dev/null      # token + pin
sha256sum eightly-agent.binary state.json > evidence.sha256
```

> The agent **state file** holds the long-lived bearer token and the server-cert
> pin (`server_pin`). Its path is `EIGHTLY_STATE_PATH` (default
> `/data/eightly-agent.json`; commonly under `/var/lib/eightly-agent/` in the
> systemd deployment). It is 0600 and owned by the `eightly-agent` user by
> contract — the agent refuses to load it otherwise.

---

## 3. Contain

Goal: stop the agent from acting on the host, and cut the attacker's path,
without destroying evidence.

```sh
# Stop the agent immediately (closes the Docker-socket proxy + all WS bridges):
sudo systemctl stop eightly-agent
#   containerized:  docker stop eightly-agent

# If you must keep it running for forensics, isolate it at the network instead
# of killing it — drop the agent's listener (default :9001) from everyone but
# the Eight.ly server's IP:
sudo iptables -I INPUT -p tcp --dport 9001 -j DROP
sudo iptables -I INPUT -p tcp --dport 9001 -s <eightly-server-ip> -j ACCEPT
```

Isolate the host if you suspect the Docker socket was actually driven by an
attacker (a privileged container you did not create, an unexpected bind mount):

```sh
# Pull the whole host off the network except your admin path, then enumerate:
docker ps -a                       # look for containers you did not deploy
docker inspect <id> | grep -Ei 'Privileged|Binds|PidMode|NetworkMode|CapAdd'
```

---

## 4. Eradicate

### 4a. Revoke a compromised agent token

The agent's bearer token lives in the state file. The token is minted and
trusted by the **Eight.ly server** (issuer `eightly-server`), so revocation has
two halves — invalidate it server-side, then re-enroll the agent.

```sh
# 1. On the Eight.ly server: revoke/de-register this agent so the old token is
#    no longer accepted (server-side agent registry; see the server runbook).
#    Until this is done, the leaked token still works against any agent that
#    shares the same secret.

# 2. On the agent host: delete the captured state so the old token can't be
#    reloaded, then re-enroll with a fresh single-use enrollment token.
sudo systemctl stop eightly-agent
sudo rm -f "$EIGHTLY_STATE_PATH"          # default /data/eightly-agent.json
# Re-enroll: supply a fresh EIGHTLY_ENROLL_TOKEN (1-hour, single-use) and start.
sudo EIGHTLY_ENROLL_TOKEN="<new-enroll-token>" systemctl start eightly-agent
#   The agent registers, gets a new token + re-captures the server pin, and
#   writes a fresh 0600 state file.
```

### 4b. Rotate the HMAC secret (`EIGHTLY_AGENT_SECRET`)

The secret is the symmetric key that verifies every bearer token. If it leaked,
**every** token signed with it is forged-able — rotate it on the server and on
every agent that shares it, together.

```sh
# Generate a new secret (server + agent must match):
openssl rand -hex 32

# On the agent host — update the unit's environment (or the EnvironmentFile /
# the .env for the container) and restart:
sudo systemctl edit eightly-agent     # set  Environment=EIGHTLY_AGENT_SECRET=<new>
sudo systemctl restart eightly-agent
#   containerized:  edit .env / -e EIGHTLY_AGENT_SECRET=<new>, then recreate.

# Set the SAME new secret on the Eight.ly server so it signs tokens the agent
# will accept. A mismatch surfaces immediately as [SECURITY] auth.unauthorized
# reason="invalid signature".
```

> If you ever see `auth.misconfigured` after a rotation, the agent came up with
> an empty secret — the env var didn't reach it. Fix the unit/env and restart;
> the agent is failing closed (503) by design, not broken.

### 4c. Re-pin a rotated (or attacker-swapped) server cert

The agent TOFU-pins the server's leaf SPKI in `server_pin` and enforces it on
every dial. A **legitimate** server cert rotation that changes the key will trip
the pin (`server cert pin mismatch`) exactly like an attacker would — that is by
design. To accept a *known-good* new cert, clear the pin so the next connect
re-captures it:

```sh
sudo systemctl stop eightly-agent

# Clear ONLY the pin, preserving the token, so the next dial re-TOFUs the cert.
# (Requires jq; the agent reads server_pin omitempty, so dropping it = re-capture.)
sudo -u eightly-agent jq 'del(.server_pin)' "$EIGHTLY_STATE_PATH" \
  > /tmp/state.new && sudo install -o eightly-agent -g eightly-agent -m 0600 \
  /tmp/state.new "$EIGHTLY_STATE_PATH" && sudo rm -f /tmp/state.new

sudo systemctl start eightly-agent
#   The first dial captures the NEW leaf SPKI and pins it; every later dial
#   enforces it. Confirm the new server_pin value matches the cert you rotated to.
```

> Only clear the pin when **you** rotated the cert and have verified the new
> fingerprint out-of-band. A pin mismatch you did not cause is an active MITM —
> do NOT clear the pin; investigate the network path first.

### 4d. Roll back the agent binary

If the agent binary itself is suspect (tampered, or a bad build shipped a
regression), roll back to the last known-good binary. The deploy keeps a backup
alongside the live binary by convention (`.bak-<reason>`); use whatever your
deploy left.

```sh
sudo systemctl stop eightly-agent
sha256sum /usr/bin/eightly-agent                      # record the suspect hash
sudo cp -a /usr/bin/eightly-agent.bak /usr/bin/eightly-agent   # last known good
sudo chmod 0755 /usr/bin/eightly-agent
sudo systemctl start eightly-agent
sudo systemctl status eightly-agent --no-pager
#   containerized:  docker pull <known-good digest>; recreate the eightly-agent
#   container pinned to that digest (never :latest during an incident).
```

---

## 5. Recover

Bring the agent back to a verified-clean state and confirm controls are live.

```sh
# 1. Confirm the agent is up, registered, and pinned to the right server:
sudo systemctl status eightly-agent --no-pager
journalctl -u eightly-agent --since "5 min ago" | grep -iE 'registered|pin'

# 2. Confirm auth is enforced (fail-closed): an unauthenticated probe must 401,
#    NOT 200, and must log auth.unauthorized.
curl -ks -o /dev/null -w '%{http_code}\n' "$EA_TARGET/containers"   # expect 401
journalctl -u eightly-agent --since "2 min ago" | grep auth.unauthorized

# 3. Confirm the host-escape deny filter is live: a privileged create must 403
#    and log proxy.denied. (Run from the server side with a valid token, against
#    your OWNED agent only.)
journalctl -u eightly-agent --since "2 min ago" | grep proxy.denied

# 4. Re-run the detection harness end-to-end and grade against
#    pentest/expected-detections.yaml — every expected event must fire.
bash pentest/harness/run.sh
```

Do not declare recovery until: the token is rotated/re-enrolled, the secret is
rotated if it was exposed, the server pin matches the intended server, the
binary hash matches a known-good build, and the harness grades green.

---

## 6. Post-mortem

After the incident is closed:

1. **Timeline.** Reconstruct from `journalctl -u eightly-agent` — first
   `[SECURITY]` / `browse.mutate` event, the source IP/actor, what was attempted,
   what (if anything) succeeded. Cross-reference the Eight.ly server's audit log
   for the same window (the server attributes the agent's token to a user).
2. **Blast radius.** Did any `proxy.denied`-class create *succeed*? Enumerate
   running containers and bind mounts (`docker ps -a`, `docker inspect`) to
   confirm nothing escaped. Was the token used against *other* agents sharing the
   secret? Rotate everywhere it was shared.
3. **Root cause.** Leaked secret/token (how — backup, process list, loose state
   perms)? Misconfig (`auth.misconfigured` should never appear in steady state)?
   A control gap the harness didn't cover?
4. **Guardrail.** Every new attack vector becomes a TTP in
   `pentest/expected-detections.yaml` and a case in the harness *before* the next
   release. A detection that should have existed but didn't is the real finding.
5. **Document.** Root cause, timeline, containment + eradication steps taken, the
   rollback command used, and the verification that controls are live again.
