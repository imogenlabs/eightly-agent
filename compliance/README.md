# Eight.ly Agent — Compliance Binder

A proportional control binder for a single static Go binary shipped as a
hardened non-root container **and** a hardened systemd service. It maps the
remediated agent to three frameworks and points each claim at concrete repo
evidence (file + line), not prose.

| File | Framework |
|------|-----------|
| [`posture.md`](posture.md) | Summary, status distribution, accepted residuals, patch cadence |
| [`cis-docker-v1.6.0.md`](cis-docker-v1.6.0.md) | CIS Docker Benchmark v1.6.0 (Sections 4–5) + container/systemd crosswalk |
| [`owasp-asvs-v4.0.3-L2.md`](owasp-asvs-v4.0.3-L2.md) | OWASP ASVS v4.0.3 Level 2 (auth, token, access control, TLS, validation, logging) |
| [`nist-csf-2.0.md`](nist-csf-2.0.md) | NIST CSF 2.0 (Govern / Identify / Protect / Detect / Respond / Recover) |
| [`sbom.cdx.json`](sbom.cdx.json) | CycloneDX 1.6 SBOM of the module dependency graph |
| [`sbom-howto.md`](sbom-howto.md) | How to regenerate the SBOM |
| [`collect-evidence.sh`](collect-evidence.sh) | Generates point-in-time evidence into `evidence/` |

## Generating evidence

```bash
bash compliance/collect-evidence.sh
```

Writes timestamped, commit-stamped snapshots to `compliance/evidence/`. The
snapshots are git-ignored (point-in-time, host-specific) — regenerate on demand,
in CI, and on every release tag. The script degrades gracefully: absent tools
(docker, govulncheck, gosec, systemd-analyze) are noted, not fatal.

## Disclosure

Vulnerability reporting policy: [`SECURITY.md`](../SECURITY.md). Machine-readable
contact: [`.well-known/security.txt`](../.well-known/security.txt) (RFC 9116).
