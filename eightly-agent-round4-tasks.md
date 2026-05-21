# Eight.ly Agent — Round 4 Task List

**Repo:** smashingtags/eightly-agent
**Baseline commit:** a049040 — "feat: complete round 3 — all P0/P1/P2 items addressed"
**Goal:** Close the remaining gaps from Round 3 verification and run a real release dry-run before tagging v1.0.0.

This list contains everything I flagged during Round 3 verification: one missing item, one overstated item, three polish gaps, and one operational gate. Plus the verification outputs I need from you to sign off.

Priority tags:
- **[P0]** Must do before tagging v1.0.0
- **[P1]** Strongly recommended before tagging v1.0.0
- **[P2]** Polish

---

## R4-1. [P1] Implement `/metrics` Prometheus endpoint (E1 from Round 3)

You said all P0/P1/P2 from Round 3 were done. I could not find this one in `internal/server/server.go` — there are zero occurrences of `/metrics`. Either implement it or explicitly document the deferral.

**Implement (preferred):**
- Add `github.com/prometheus/client_golang/prometheus/promhttp` dependency
- Register an off-by-default flag `--metrics-enabled` (env: `METRICS_ENABLED`), defaulting to `false`
- When enabled, mount `promhttp.Handler()` at `/metrics`
- Decide whether `/metrics` requires the Bearer auth or is unauthenticated on a separate port — recommend **requires auth** by default (it can leak operational signal otherwise)
- Register a small app metric set:
  - `eightly_agent_heartbeats_sent_total` (counter)
  - `eightly_agent_heartbeats_failed_total` (counter, labeled by reason)
  - `eightly_agent_ws_clients_connected` (gauge)
  - `eightly_agent_registration_attempts_total` (counter, labeled by result)
  - `eightly_agent_docker_socket_reachable` (gauge, 0/1)
- The default Go collector (go_*, process_*) comes for free via promhttp
- Add a test that hits `/metrics` with auth and asserts the expected metric names appear
- Document the endpoint and flag in README and `docs/CONFIGURATION.md` (or wherever the flag table lives)

**Or defer (acceptable, but document it):**
- Open a GitHub issue titled "Add Prometheus /metrics endpoint" and label it `v1.1.0`
- Add a line to CHANGELOG.md "Unreleased" section noting it as planned
- Add a TODO in server.go where the metrics handler would go, with a link to the issue
- Mention in README that metrics are planned for v1.1.0

---

## R4-2. [P0] Make C14 (multi-OS CI) actually multi-OS

The current `ci.yml` cross-compiles to 5 platforms but every job runs on `ubuntu-latest`. Your Windows service code (`cmd/agent/service_windows.go`), Windows hostinfo (the PowerShell branch), and `defaults_windows.go` have **never been executed in CI**. Cross-compilation only proves they compile — not that they run.

Add to `ci.yml` (or a new `platform.yml` workflow if you want to keep CI fast):

```yaml
platform-test:
  name: Platform Test (${{ matrix.os }})
  runs-on: ${{ matrix.os }}
  needs: [lint]
  strategy:
    fail-fast: false
    matrix:
      os: [ubuntu-latest, macos-latest, windows-latest]
  steps:
    - uses: actions/checkout@<sha> # v4
    - uses: actions/setup-go@<sha> # v5
      with:
        go-version: '1.23'
    - name: Run tests
      run: go test ./... -count=1 -timeout 120s
    - name: Build agent
      run: go build ./cmd/agent
```

Notes:
- Skip the `-race` flag on Windows if it's flaky there; keep race only on linux+darwin
- macOS runner exercises `hostinfo_darwin.go` and `service_other.go` paths
- Windows runner exercises `hostinfo_windows.go` (PowerShell branch), `service_windows.go`, and `defaults_windows.go`
- This will catch real bugs. Almost guaranteed.

---

## R4-3. [P1] Fix A4 inconsistency in codeql.yml and scorecard.yml

Every other workflow has actions pinned to full SHAs with version comments. These two don't:

```yaml
# Currently in scorecard.yml:
- uses: ossf/scorecard-action@v2 # v2     # ← floating tag, not a SHA

# Currently in codeql.yml:
- uses: github/codeql-action/init@v3 # v3      # ← floating tag
- uses: github/codeql-action/autobuild@v3 # v3 # ← floating tag
- uses: github/codeql-action/analyze@v3 # v3   # ← floating tag
```

Replace each with the actual commit SHA, same pattern as the rest of the repo:
```yaml
- uses: ossf/scorecard-action@<sha> # v2.x.x
- uses: github/codeql-action/init@<sha> # v3.x.x
```

Look up the SHAs on the action's releases page. Dependabot will then bump them.

---

## R4-4. [P1] Attest the SBOM (A6 follow-through)

You added `anchore/sbom-action` and the SBOM ships as a release asset, but it's not attested. Add an `actions/attest-sbom@v1` step in release.yml immediately after the SBOM is generated:

```yaml
- name: Generate SBOM
  uses: anchore/sbom-action@<sha> # v0
  with:
    path: .
    format: spdx-json
    output-file: release/sbom.spdx.json

- name: Attest SBOM
  uses: actions/attest-sbom@<sha> # v1
  with:
    subject-path: 'release/eightly-agent-*'
    sbom-path: release/sbom.spdx.json
```

Once done, the SBOM is verifiable via `gh attestation verify --predicate-type https://spdx.dev/Document` (or similar). Add the verify command to the README's "Verifying Releases" section.

---

## R4-5. [P1] Complete the systemd hardening (E2 follow-through)

`deploy/eightly-agent.service` has `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `ReadWritePaths`. Add the deep hardening:

```ini
# Append to [Service] section:
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true
MemoryDenyWriteExecute=true
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectKernelLogs=true
ProtectProc=invisible
ProcSubset=pid
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
SystemCallArchitectures=native
UMask=0077
```

Notes:
- `CapabilityBoundingSet=` (empty) drops all capabilities — the agent talks to a Unix socket and HTTP/HTTPS, no caps required
- `RestrictAddressFamilies` allows Unix socket + IPv4/IPv6 only
- `MemoryDenyWriteExecute=true` may need to be set to `false` if the Go runtime's JIT-ish behavior trips it — test before shipping
- Verify with `systemd-analyze security eightly-agent` after install; expect a score ≤ 2.0 (lower is better)
- If any of these break agent operation, comment them out with a note explaining why

---

## R4-6. [P1] Tighten the WebSocket origin allowlist (D9 follow-through)

In `internal/websocket/websocket.go`, when `allowedOrigins` is set and the request has no `Origin` header, `CheckOrigin` returns `true`. That's intentional for non-browser clients but it means an explicit allowlist isn't a hard wall — a curl call from anywhere on the network passes.

Pick one:

**Option A — strict mode (recommended for security):**
- Add a flag `--strict-origin` (env: `STRICT_ORIGIN`) defaulting to `false`
- When `strict-origin=true` AND `allowedOrigins` is set, require `Origin` to be present and matched
- Document the trade-off in README + SECURITY.md

**Option B — document the current behavior:**
- Add a paragraph to SECURITY.md explicitly stating: "When `--allowed-origins` is set, requests with no Origin header are still permitted to support non-browser clients. Origin is not an authentication mechanism; rely on the Bearer token for security."

Either is fine. Option A gives users a knob; Option B is honest documentation.

---

## R4-7. [P0] Run a real release dry-run on v1.0.0-rc1

This is the operational gate I cannot verify from the browser. Until a release workflow actually runs end-to-end, we don't know if cosign, attestations, SBOM, and multi-platform builds work in practice.

Steps:
1. Tag `v1.0.0-rc1` and push: `git tag v1.0.0-rc1 && git push origin v1.0.0-rc1`
2. Watch the `release.yml` run to completion in the Actions tab
3. Confirm all 6 binaries, both `.deb`, both `.rpm`, `SHA256SUMS`, `sbom.spdx.json`, and signatures are attached to the GitHub Release
4. From a clean machine (or a fresh Docker container), download one binary + its `.sig` and `.crt` and run:
   ```bash
   cosign verify-blob \
     --certificate eightly-agent-linux-amd64.crt \
     --signature eightly-agent-linux-amd64.sig \
     --certificate-identity-regexp 'https://github.com/smashingtags/eightly-agent' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     eightly-agent-linux-amd64
   ```
5. Verify the GHCR container image:
   ```bash
   cosign verify \
     --certificate-identity-regexp 'https://github.com/smashingtags/eightly-agent' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     ghcr.io/smashingtags/eightly-agent:v1.0.0-rc1
   ```
6. Verify the SLSA attestation:
   ```bash
   gh attestation verify eightly-agent-linux-amd64 --owner smashingtags
   ```
7. (After R4-4) Verify the SBOM attestation
8. Paste the outputs back here for sign-off

If any of the above fail, fix the workflow before tagging `v1.0.0`. This is the single most important step before going public — the whole "trust" pitch depends on a working verification flow.

---

## R4-8. [P0] Verification outputs from the latest commit

I cannot read CI run outputs from the browser. Paste:

- [ ] CI run URL on commit `a049040` (or whatever the head is) — status green
- [ ] Tail of `go test ./... -race -count=1 -timeout 120s` (you said 128 tests across 10 packages — confirm)
- [ ] Last line of `go tool cover -func=coverage.out | tail -1` — total coverage %
- [ ] `go vet ./...` — must be clean
- [ ] `golangci-lint run ./...` — must be clean
- [ ] `govulncheck ./...` — must be clean
- [ ] `gosec ./...` — clean OR every finding has a documented justification in `# nosec` comments or a `.gosec.json` exclude file
- [ ] `staticcheck ./...` — must be clean

If any tool finds anything, fix or justify each finding in the same round.

---

## R4-9. [P2] Add `actions/attest-sbom` verify command to README

(Depends on R4-4.) Add to the "Verifying Releases" section in README:

```bash
# Verify the SBOM
gh attestation verify eightly-agent-linux-amd64 \
  --owner smashingtags \
  --predicate-type https://spdx.dev/Document
```

---

## R4-10. [P2] Document the metrics endpoint deferral OR the metrics flag

(Depends on R4-1 decision.) Either way, make sure the CHANGELOG.md "Unreleased" or v1.0.0 entry mentions the decision so users know.

---

## R4-11. [P2] systemd-analyze security score in the docs

After R4-5, run `systemd-analyze security eightly-agent` on a test host and paste the score into `docs/ARCHITECTURE.md` or a new "Hardening" section. This is a concrete, paste-able trust signal that no other open-source Docker agent will have.

---

## R4-12. [P2] Add a v1.0.0 entry to CHANGELOG.md

Before tagging v1.0.0:
- Move all "Unreleased" entries into a new `## [1.0.0] - 2026-05-XX` section
- Include a one-line summary of the security posture: "Apache 2.0, Sigstore-signed binaries and images, SLSA build provenance, SBOM, OpenSSF Scorecard, CodeQL, gosec, govulncheck, golangci-lint, race-detector clean, 128+ tests"
- Keep an empty "Unreleased" header at the top for future work

---

## R4-13. [P2] Confirm Codecov badge is rendering

If the README has a Codecov badge, verify the project is registered on codecov.io and the badge renders a real number (not "unknown"). If you haven't enabled Codecov, either enable it or drop the badge.

---

## Suggested order

1. R4-2 (multi-OS CI) — will probably surface bugs you'll want to fix in the same round
2. R4-1 (metrics OR defer) — decide and execute
3. R4-3 (SHA-pin remaining actions)
4. R4-4 (SBOM attestation)
5. R4-5 (systemd hardening)
6. R4-6 (origin allowlist tightening or doc)
7. R4-12 (CHANGELOG v1.0.0 entry)
8. R4-7 (tag v1.0.0-rc1 — this is the gate)
9. R4-8 (paste verification outputs)
10. R4-9, R4-10, R4-11, R4-13 (polish)

After R4-7 and R4-8 are green, tag v1.0.0 and flip the repo public.

---

## What I'll do when you come back

Give me the new commit SHA. I will:
- Pull every file you changed and verify each R4 item against the actual code
- Read the v1.0.0-rc1 release page and confirm all assets + signatures are present
- Sign off (or hand you a Round 5 list, but I do not expect to)
