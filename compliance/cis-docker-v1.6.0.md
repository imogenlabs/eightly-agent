# CIS Docker Benchmark v1.6.0 — Eight.ly Agent Posture

**Artifact:** a single static Go binary shipped two ways — (a) a hardened
container image (`Dockerfile`, `docker-compose.yml`) and (b) a hardened systemd
service (`deploy/eightly-agent.service`, installed by the `.deb`/`.rpm`).

**Scope:** the container image + runtime configuration this repo ships, mapped to
the CIS items that a single-binary agent can actually own.

**Out of scope (operator responsibility):** host daemon flags (Section 2), host
filesystem (Section 1), registry config (Section 3). The agent's whole job is to
mediate the Docker socket, so the daemon itself is the deploying operator's to
harden.

A note on the socket mount: unlike a typical app, the agent's *purpose* is to
broker the Docker API, so it does mount `docker.sock`, read-write. A `:ro` mount
would mark only the socket file read-only, not the Docker API: anything that can
open the socket can still create and start containers, so it is not a control.
The controls are the HMAC token check and an in-process Docker proxy
(`internal/proxy`) that forwards only an allow-list of Docker methods and paths
(`routes.go`) and refuses requests that would reach the host: privileged mode,
non-private namespaces, capabilities beyond Docker's default set, host folders
outside `--allowed-bind-roots` (none by default), `VolumesFrom`, host devices,
confinement-disabling security options, host sysctls, host-path volume options,
and exec, attach or file upload into a container that already reaches the host.
Swarm, plugins, builds and image push are not on the list.

## Section 4 — Container Images & Build

### 4.1 Ensure a user for the container has been created — **Met**
`Dockerfile`: `adduser -S -u 1000 -G "$DOCKER_GRP" eightly-agent` (the group with
GID 999) then `USER eightly-agent`. The container runs as uid 1000, not root; a
host whose socket has another group adds it with `--group-add` (compose:
`group_add: ["${DOCKER_GID:-999}"]`). systemd path runs as `User=eightly-agent`.
Evidence: `evidence/CIS-4.1-user.txt`

### 4.2 Ensure containers use only trusted base images — **Met**
`golang:1.26.11-alpine` (builder) and `alpine:3.23` (runtime), both official images
**pinned by `@sha256:` digest**, not floating tags. Dependabot's `docker`
ecosystem watches them (`.github/dependabot.yml`).
Evidence: `evidence/CIS-4.2-base-images.txt`

### 4.3 Ensure unnecessary packages are not installed — **Met**
Runtime stage installs only `ca-certificates`, `tzdata` and `tini` (PID 1, reaps orphaned processes). `git` lives in the
builder stage only and never reaches the final image (multi-stage build).
The binary is `CGO_ENABLED=0` static — no libc runtime dependency.

### 4.4 Ensure images are scanned for vulnerabilities — **Met**
CI runs `govulncheck` and `gosec` on every PR (`.github/workflows/ci.yml`),
CodeQL on push/PR/weekly (`codeql.yml`), and OpenSSF Scorecard weekly
(`scorecard.yml`). Dependabot opens dependency PRs weekly for the `gomod`,
`github-actions`, and `docker` ecosystems.
Evidence: `evidence/CIS-4.4-scanners.txt`

### 4.5 Ensure content trust / image signing is enabled — **Partial**
Docker Content Trust (Notary) is **not** used. Instead, `docker-publish.yml`
**cosign-signs** every pushed image by digest (keyless, Sigstore OIDC) and emits
SLSA build-provenance attestations. cosign provides equivalent — arguably
stronger — cryptographic attestation. Verify with:
`cosign verify --certificate-identity-regexp imogenlabs --certificate-oidc-issuer https://token.actions.githubusercontent.com ghcr.io/imogenlabs/eightly-agent`

### 4.6 Ensure HEALTHCHECK instructions have been added — **Met**
`Dockerfile` defines `HEALTHCHECK` as `/app/eightly-agent healthcheck`: an
in-process HTTPS GET of `/healthz` that accepts only the agent's own certificate
from its data directory and starts no child process. `docker-compose.yml` does
not override it.

### 4.7 Ensure update instructions are not used alone — **Met**
The single `apk add --no-cache` in the runtime stage uses `--no-cache`; there is
no standalone `apk update`/`upgrade` layer that could pin a stale package set.

### 4.9 Ensure COPY is used instead of ADD — **Met**
Zero `ADD` instructions. The Dockerfile uses `COPY` only.

### 4.10 Ensure secrets are not stored in Dockerfiles — **Met**
No secrets in the Dockerfile. `EIGHTLY_AGENT_SECRET` is injected at runtime via
env (`docker-compose.yml`), a file named by `--secret-file` /
`EIGHTLY_AGENT_SECRET_FILE`, or the root-only `/etc/eightly-agent/agent.env` that
the systemd unit loads with `EnvironmentFile=`; never baked into a layer or
build arg. gitleaks/secret-scanning is part of the OpenSSF Scorecard checks.

### 4.11 Ensure only verified packages are installed — **Met**
Go modules are pinned in `go.mod`/`go.sum` (checksum-verified). CI gates on
`go mod tidy` cleanliness (`ci.yml` lint job) so the lockfile cannot drift.

## Section 5 — Container Runtime

The runtime evidence below is expressed for the `docker-compose.yml` reference
deployment. Operators using a bare `docker run` should mirror these flags.

### 5.4 Ensure privileged containers are not used — **Met**
No `privileged: true` anywhere. The agent runs as a normal non-root container
and additionally **refuses to create** privileged containers downstream
(`internal/proxy/policy.go` `checkHostConfig`), and refuses exec or attach into
an existing privileged container (`filter.go` `checkTargetContainer`).

### 5.5 Ensure sensitive host directories are not mounted — **Partial / by design**
Two host paths are mounted: `/var/run/docker.sock`, read-write, because
brokering that socket is the agent's reason to exist; and
`/var/lib/docker/volumes`, read-write, so the file browser can serve and change
volume files (leave it out to turn the browser off). The agent's own data lives
in the named `agent-data` volume. The exposure is mitigated by (a) HMAC-auth
gating *who* reaches it, (b) the proxy's allow-list and checks gating *what*
they can do through the socket (`SECURITY.md` → "What the agent protects"), and
(c) the file browser's confinement to each volume's `_data` folder.
Evidence: `evidence/CIS-5.5-mounts.txt`

### 5.7 Ensure privileged ports are not mapped — **Met**
Published port is `9001` (≥ 1024). No port below 1024 is mapped.

### 5.9 Ensure host network namespace is not shared — **Met**
No `network_mode: host`. The container uses Docker's default bridge networking.

### 5.12 Ensure the container's root filesystem is mounted read-only — **NA (container) / Met (systemd)**
The compose reference does not set `read_only: true` (the binary writes its
self-signed TLS material and state under `/data`, a named volume). Operators
**can** add `read_only: true` with a writable mount for `/data`. On the systemd
path this is enforced: `ProtectSystem=strict` makes the entire filesystem
read-only except the single `ReadWritePaths=/var/lib/eightly-agent`.
Recommended compose hardening is documented in `posture.md` (accepted residual).

### 5.14 / 5.25 Ensure no-new-privileges is set — **NA (container) / Met (systemd)**
The systemd unit sets `NoNewPrivileges=true`. The compose reference does not yet
set `security_opt: [no-new-privileges:true]` — recommended operator hardening,
tracked as an accepted residual in `posture.md`. The binary itself never
`exec`s a setuid helper, so it cannot escalate regardless.

### 5.19 Ensure the default seccomp profile is not disabled — **Met**
No `seccomp:unconfined` and no `--security-opt seccomp=...` override. Docker's
default seccomp profile applies. The systemd unit additionally pins
`SystemCallFilter=@system-service` + `SystemCallArchitectures=native`.

### 5.29 Ensure Docker socket is not mounted with write permissions — **Partial / by design**
`docker-compose.yml` mounts the socket read-write on purpose. The agent's job is
to make Docker calls, and `:ro` would not stop them anyway: it marks the socket
file, not the API. (See 5.5 for why it is mounted at all.) Compensating
controls: every call from outside reaches the socket only through the agent's
token check and the proxy's allow-list and checks, and the agent process itself
runs as uid 1000.

## Container vs. systemd hardening crosswalk

| Control | Container (`docker-compose.yml`) | systemd (`deploy/eightly-agent.service`) |
|---------|----------------------------------|------------------------------------------|
| Non-root | `USER eightly-agent` (uid 1000) | `User=eightly-agent` |
| No new privileges | operator-recommended | `NoNewPrivileges=true` |
| Read-only root | operator-recommended (`/data` rw) | `ProtectSystem=strict` + `ReadWritePaths=/var/lib/eightly-agent` |
| Drop capabilities | Docker default cap set | `CapabilityBoundingSet=` + `AmbientCapabilities=` (empty = none) |
| Restrict namespaces | bridge net only | `RestrictNamespaces=true` |
| Kernel protection | host/daemon | `ProtectKernelModules/Tunables/Logs=true`, `ProtectControlGroups=true` |
| Private tmp | container fs | `PrivateTmp=true`, `UMask=0077` |
| Syscall filter | Docker default seccomp | `SystemCallFilter=@system-service` |
| Healthcheck | `HEALTHCHECK` (`eightly-agent healthcheck`) | systemd `Restart=always` |

Evidence: `evidence/SYSTEMD-hardening.txt`

## Status distribution (Sections 4–5)

| Status | Count |
|--------|-------|
| Met | 13 |
| Partial / by-design | 5 |
| NA (operator-recommended on container path, Met on systemd path) | 2 |
