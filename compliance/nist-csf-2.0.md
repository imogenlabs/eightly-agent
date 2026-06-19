# NIST CSF 2.0 — Eight.ly Agent Alignment

**Scope:** the code, build pipeline, and runtime hardening shipped in this repo
for a single-binary Docker management agent. Operator-responsibility items
(business-continuity policy, vendor management, staff awareness training,
host-daemon hardening) are explicitly delegated to the deploying operator and
marked **N/A** here.

Status legend: **Met** / **Partial** / **N/A**.

## GV — GOVERN

| Subcategory | Status | Evidence |
|-------------|--------|----------|
| GV.OC-01 Organizational mission understood | **N/A** | Distributed software component; no operating org in scope |
| GV.RM-01 Risk strategy established | **Met** | `SECURITY.md` "Security Model" + "Threat model" state the trust boundaries and risk posture (token = root-equivalent) |
| GV.PO-01 Cybersecurity policy | **Met** | `SECURITY.md` + `.well-known/security.txt` (disclosure contact, policy link, expiry) |
| GV.SC-01 Supply-chain risk management | **Met** | cosign signing + SLSA provenance + SBOM + Dependabot + SHA-pinned Actions + digest-pinned base images (`.github/workflows/`, `Dockerfile`) |

## ID — IDENTIFY

| Subcategory | Status | Evidence |
|-------------|--------|----------|
| ID.AM-02 Software inventory maintained | **Met** | CycloneDX SBOM (`compliance/sbom.cdx.json` or `sbom-howto.md`) + SPDX SBOM attached to releases (`release.yml` anchore/sbom-action) |
| ID.AM-08 Components managed through lifecycle | **Met** | `go.mod` pinned + checksum-verified `go.sum`; CI gates `go mod tidy` cleanliness |
| ID.RA-01 Vulnerabilities identified | **Met** | `govulncheck` + `gosec` + `golangci-lint` + `staticcheck` on every PR; CodeQL + Scorecard weekly (`ci.yml`, `codeql.yml`, `scorecard.yml`) |
| ID.RA-05 Threats prioritized | **Met** | `SECURITY.md` "Attacker scenarios" + "Threat model" tables enumerate threats and mitigations |

## PR — PROTECT

| Subcategory | Status | Evidence |
|-------------|--------|----------|
| PR.AA-01 Identities/credentials managed | **Met** | HMAC shared secret; TOFU cert pin; one-time enrollment token exchanged for a persistent agent token (`internal/register/`) |
| PR.AA-03 Authentication enforced | **Met** | `auth.Middleware` fail-closed on every protected route (`internal/auth/jwt.go`) |
| PR.AA-05 Access permissions, least privilege | **Partial** | Token = full Docker API by design; create-deny filter (`denyDangerousCreates`) is the in-process least-privilege boundary against host escape |
| PR.DS-02 Data in transit protected | **Met** | TLS 1.2+ AEAD-only listener; outbound TLS verified/pinned (`server.go`, `register/pin.go`) |
| PR.DS-01 Data at rest protected | **Met** | TLS private key `0600`/dir `0700`, perms re-tightened on reuse (`internal/tlsutil/tls.go`) |
| PR.PS-01 Configuration management | **Met** | Pinned base images, pinned Go toolchain (1.26.4), SHA-pinned Actions, hardened systemd unit shipped in the package |
| PR.PS-02 Software maintained / patched | **Met** | Dependabot weekly across gomod/actions/docker; `govulncheck` gate (see `posture.md` patch-cadence note) |
| PR.PS-06 Secure SDLC practices | **Met** | Mandatory lint/vet/staticcheck/gosec/govulncheck/test+race/coverage gates before build (`ci.yml`) |
| PR.IR-01 Resources protected from unauthorized use | **Met** | systemd `CapabilityBoundingSet=`, `ProtectSystem=strict`, `RestrictNamespaces`, `NoNewPrivileges`; container non-root + socket `:ro` |

## DE — DETECT

| Subcategory | Status | Evidence |
|-------------|--------|----------|
| DE.CM-01 Network/auth activity monitored | **Met** | Greppable `[SECURITY]` detection lines on every auth failure / fail-closed event (`jwt.go`); Prometheus `/metrics` (heartbeat counters) for operational signals |
| DE.CM-09 Computing surfaces monitored for vulns | **Met** | Scheduled CodeQL + Scorecard; Dependabot alerts |
| DE.AE-02 Detected events analyzed | **Partial** | Structured logs carry the fields for analysis; correlation/SIEM is the operator's pipeline |
| DE.AE-08 Incident declaration criteria | **Met** | `SECURITY.md` defines compromise scenarios ("rotate the secret immediately") and severity by attacker scenario |

## RS — RESPOND

| Subcategory | Status | Evidence |
|-------------|--------|----------|
| RS.MA-01 Incident-management process | **Met** | `SECURITY.md` reporting flow (GitHub Security Advisories + `security@imogenlabs.ai`), 48h ack / 5-day assessment SLA |
| RS.MA-02 Reports triaged | **Met** | `.well-known/security.txt` advertises the disclosure channel; safe-harbor clause in `SECURITY.md` |
| RS.MI-01 Incidents contained | **Met** | Documented containment: rotate `EIGHTLY_AGENT_SECRET`; token `exp` limits blast radius; pin rejects MITM (`SECURITY.md`, `jwt.go`, `pin.go`) |
| RS.CO-02 Stakeholders notified | **Partial** | Release-notes credit + advisory publication; automated downstream notification is manual |

## RC — RECOVER

| Subcategory | Status | Evidence |
|-------------|--------|----------|
| RC.RP-01 Recovery executed | **Met** | Stateless-by-design: the agent regenerates its self-signed cert and re-enrolls on a clean `/data`; `systemd Restart=always` auto-recovers crashes |
| RC.RP-03 Integrity of restored assets verified | **Met** | Released binaries/images are cosign-verifiable + SLSA-attested before redeploy; `SHA256SUMS` published per release (`release.yml`) |
| RC.CO-03 Recovery communicated | **Met** | Versioned releases + `CHANGELOG.md` communicate fixes/restores |

## Status distribution

| Function | Met | Partial | N/A |
|----------|-----|---------|-----|
| GOVERN | 3 | 0 | 1 |
| IDENTIFY | 4 | 0 | 0 |
| PROTECT | 8 | 1 | 0 |
| DETECT | 2 | 1 | 1 |
| RESPOND | 3 | 1 | 0 |
| RECOVER | 3 | 0 | 0 |
