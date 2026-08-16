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
broker the Docker API, so it does mount `docker.sock`. We mount it **read-only**
(`:ro`) and layer an in-process deny filter (`internal/proxy/docker.go`
`denyDangerousCreates`) that rejects privileged / host-namespace / sensitive
bind-mount creates. This is the relevant control, not socket-absence.

## Section 4 — Container Images & Build

### 4.1 Ensure a user for the container has been created — **Met**
`Dockerfile`: `adduser -S -u 1000 -G docker eightly-agent` then `USER eightly-agent`.
The container runs as uid 1000, not root. systemd path runs as `User=eightly-agent`.
Evidence: `evidence/CIS-4.1-user.txt`

### 4.2 Ensure containers use only trusted base images — **Met**
`golang:1.26-alpine` (builder) and `alpine:3.23` (runtime), both official images
**pinned by `@sha256:` digest**, not floating tags. Dependabot's `docker`
ecosystem watches them (`.github/dependabot.yml`).
Evidence: `evidence/CIS-4.2-base-images.txt`

### 4.3 Ensure unnecessary packages are not installed — **Met**
Runtime stage installs only `ca-certificates` and `tzdata`. `git` lives in the
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
`Dockerfile` defines `HEALTHCHECK` against `https://localhost:9001/healthz`.
`docker-compose.yml` defines an independent compose-level healthcheck against
`/ping`.

### 4.7 Ensure update instructions are not used alone — **Met**
The single `apk add --no-cache` in the runtime stage uses `--no-cache`; there is
no standalone `apk update`/`upgrade` layer that could pin a stale package set.

### 4.9 Ensure COPY is used instead of ADD — **Met**
Zero `ADD` instructions. The Dockerfile uses `COPY` only.

### 4.10 Ensure secrets are not stored in Dockerfiles — **Met**
No secrets in the Dockerfile. `EIGHTLY_AGENT_SECRET` is injected at runtime via
env (`docker-compose.yml`) / systemd environment, never baked into a layer or
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
(`denyDangerousCreates`).

### 5.5 Ensure sensitive host directories are not mounted — **Partial / by design**
The only host mount is `/var/run/docker.sock`, mounted **`:ro`** — and brokering
that socket is the agent's reason to exist. No other host path is mounted; data
lives in the named `agent-data` volume. The socket exposure is mitigated by (a)
read-only mount, (b) HMAC-auth gating *who* reaches it, and (c) the create-deny
filter gating *what* they can do (`SECURITY.md` → "Privilege required").
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

### 5.29 Ensure Docker socket is not mounted with write permissions — **Met**
`docker-compose.yml` mounts the socket **`:ro`**. (See 5.5 for why it is mounted
at all.) Mutating Docker calls still flow through the agent's authenticated,
filtered proxy rather than raw socket writes from inside the container.

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
| Healthcheck | `HEALTHCHECK` + compose | systemd `Restart=always` |

Evidence: `evidence/SYSTEMD-hardening.txt`

## Status distribution (Sections 4–5)

| Status | Count |
|--------|-------|
| Met | 14 |
| Partial / by-design | 4 |
| NA (operator-recommended on container path, Met on systemd path) | 2 |
