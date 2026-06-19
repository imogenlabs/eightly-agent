# Eight.ly Agent — Compliance Posture Summary

**Last updated:** 2026-06-19
**Git HEAD:** run `compliance/collect-evidence.sh` for the exact commit
**Frameworks:** CIS Docker v1.6.0 (Sec 4–5), OWASP ASVS v4.0.3 L2, NIST CSF 2.0
**Artifact:** one static Go binary, shipped as a hardened non-root container
**and** a hardened systemd service.

This binder is deliberately **proportional to a single-binary agent**. It does
not claim the controls of a multi-service web stack (no password store, no
browser sessions, no MFA, no UI) — those requirements are marked N/A in the
chapter files with the reason stated.

## Status distribution

| Framework | Met | Partial | Gap | N/A |
|-----------|-----|---------|-----|-----|
| CIS Docker v1.6.0 (Sec 4–5) | 14 | 4 | 0 | 2 |
| OWASP ASVS v4.0.3 L2 | 33 | 3 | 0 | (chapters listed N/A) |
| NIST CSF 2.0 | 23 | 3 | 2 | — |

## What carries the posture

- **Fail-closed auth.** No secret + no explicit `--insecure-no-auth` → the agent
  refuses to serve (503 / startup error). The Docker socket is never exposed
  unauthenticated by accident. (`internal/auth/jwt.go`, `internal/config/config.go`)
- **Defense-in-depth over the socket.** Auth gates *who*; the
  `denyDangerousCreates` filter gates *what* — privileged / host-namespace /
  sensitive-bind-mount creates are rejected even with a valid token.
  (`internal/proxy/docker.go`)
- **Transport.** TLS-only listener, TLS 1.2 minimum, AEAD-only cipher list;
  outbound EOS traffic verified against the system store or a pinned CA, plus a
  TOFU SPKI pin. (`internal/server/server.go`, `internal/register/pin.go`)
- **Path confinement.** `/browse/*` uses lexical `filepath.Rel` containment +
  post-`EvalSymlinks` re-check + `O_NOFOLLOW`. (`internal/browse/browse.go`)
- **Supply chain.** cosign signing, SLSA provenance, SBOM, digest-pinned base
  images, SHA-pinned Actions, Dependabot, and a CI gate of
  govulncheck/gosec/staticcheck/golangci-lint/CodeQL/Scorecard.
- **Two hardening surfaces.** systemd unit
  (`NoNewPrivileges`, `ProtectSystem=strict`, empty `CapabilityBoundingSet`,
  `RestrictNamespaces`, syscall filter) and a non-root container (uid 1000,
  socket `:ro`, healthcheck).

## Accepted residuals

| ID | Status | Residual | Reason it is accepted | Compensating control |
|----|--------|----------|-----------------------|----------------------|
| CIS 5.5 | Partial | `docker.sock` mounted into the container | Brokering the socket is the agent's core function | `:ro` mount + HMAC auth + create-deny filter |
| CIS 5.12 / 5.14 | NA (container) | compose ref does not set `read_only`/`no-new-privileges` | The binary writes TLS material + state under `/data`; operators can add these flags | systemd path enforces both; binary execs no setuid helper |
| ASVS V4.1.3 | Partial | A valid token grants full Docker API (root-equivalent) | Documented trust model — the agent *is* a root-level service (`SECURITY.md`) | Token `exp`, secret rotation guidance, create-deny filter |
| ASVS V7.4.1 | Partial | Logs not shipped off-box by the agent | Central aggregation is an operator-pipeline concern | Stable `[SECURITY]` JSON prefix for easy ingestion |
| CIS 4.5 | Partial | Docker Content Trust / Notary not used | cosign keyless signing is equivalent/stronger | cosign verify + SLSA provenance |

None of these are code defects; each is a documented design boundary with a
compensating control. Operator-side hardening recommendations (set
`read_only: true` + `security_opt: [no-new-privileges:true]` on the container,
restrict the listen interface, ship logs to a SIEM) belong to the deployment,
not the binary.

## Patch cadence

- **Go toolchain is single-sourced to `1.26.4`** — declared in `go.mod` and
  pinned identically in `ci.yml`, `release.yml`, and `docker-publish.yml`
  (via the digest-pinned `golang:1.26-alpine` builder). Bumping one place and
  the CI `go mod tidy` gate keeps them from drifting.
- **Dependabot** runs **weekly** across three ecosystems — `gomod`,
  `github-actions`, and `docker` (`.github/dependabot.yml`) — so module,
  workflow-action, and base-image updates all arrive as reviewable PRs.
- **`govulncheck` is a PR gate** (`ci.yml`): a known-exploitable CVE in a
  reachable code path fails the build, not just an advisory.
- **Scheduled re-scan:** CodeQL and OpenSSF Scorecard run weekly
  (`cron: '0 6 * * 1'`) independent of commit activity.

## Evidence collection

Run `bash compliance/collect-evidence.sh` to write point-in-time snapshots into
`compliance/evidence/`. The script degrades gracefully: when a tool (docker,
govulncheck, gosec, systemd-analyze) is absent it records a note rather than
failing, so it is safe to run on a developer laptop or in CI.
