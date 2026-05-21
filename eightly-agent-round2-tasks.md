# Eight.ly Agent — Round 2 Task List

This is the follow-up review after the first 40-issue pass. The big-ticket security items are in good shape. What remains is a mix of one new regression, several subtly-incomplete fixes, and the launch-readiness items (SECURITY.md, signed releases, dependabot) that matter disproportionately for a trust-focused open-source launch.

Items are ordered by priority. Launch-blockers are flagged.

---

## 🚨 Launch blockers (must be done before flipping the repo public)

### B1. Create SECURITY.md

A vulnerability disclosure policy is table stakes for any project that proxies a Docker socket. Without it, security researchers either dump findings publicly or give up. Either is bad.

**Required contents:**
- Supported versions table (e.g. `v1.x: supported`)
- Private disclosure channel — preferred is GitHub Security Advisories (`https://github.com/smashingtags/eightly-agent/security/advisories/new`). Optionally a security email with a PGP key.
- Expected response time (e.g. "acknowledgement within 72 hours, fix or status update within 14 days")
- Scope statement: what's in scope (agent + protocol) vs out of scope (the EOS server, third-party Docker daemon bugs)
- Safe harbor language: a brief statement that good-faith research won't be pursued legally
- Credit policy: do you credit reporters in release notes? In a CVE filing? State it.

**Location:** `SECURITY.md` at repo root. GitHub auto-detects it and surfaces it in the Security tab.

### B2. Add `.github/dependabot.yml`

The Dockerfile currently has a comment saying "Dependabot monitors for updates via .github/dependabot.yml" — but that file doesn't appear to exist. If it doesn't, that comment is a lie and the floating base image tags are a real supply-chain risk.

**Required contents:**

```yaml
version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 10

  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"

  - package-ecosystem: "docker"
    directory: "/"
    schedule:
      interval: "weekly"
```

Verify it actually runs once committed — Dependabot's status page should show three ecosystems being scanned.

### B3. Sign release binaries and container images

For a project that gets root-equivalent on hosts, "trust the binary" is a real ask. Cosign signing + SBOM + provenance attestation is the modern minimum.

**Required additions to `.github/workflows/release.yml`:**

```yaml
permissions:
  contents: write
  id-token: write   # cosign keyless signing
  attestations: write

# ... in the release job:
- name: Install cosign
  uses: sigstore/cosign-installer@v3

- name: Sign release binaries
  env:
    COSIGN_EXPERIMENTAL: "1"
  run: |
    for f in release/eightly-agent-*; do
      [ -f "$f" ] && cosign sign-blob --yes "$f" \
        --output-signature "$f.sig" \
        --output-certificate "$f.crt"
    done

- name: Generate SBOM
  uses: anchore/sbom-action@v0
  with:
    path: .
    format: spdx-json
    output-file: release/sbom.spdx.json

- name: Generate build provenance
  uses: actions/attest-build-provenance@v1
  with:
    subject-path: 'release/eightly-agent-*'
```

**Required additions to `.github/workflows/docker-publish.yml`:**

```yaml
- name: Install cosign
  uses: sigstore/cosign-installer@v3

- name: Sign published images
  env:
    COSIGN_EXPERIMENTAL: "1"
  run: |
    for tag in $(echo "${{ steps.meta.outputs.tags }}" | tr ',' '\n'); do
      cosign sign --yes "$tag@${{ steps.build.outputs.digest }}"
    done

- name: Attest image build provenance
  uses: actions/attest-build-provenance@v1
  with:
    subject-name: ghcr.io/smashingtags/eightly-agent
    subject-digest: ${{ steps.build.outputs.digest }}
    push-to-registry: true
```

Document in the README how to verify a release:

```bash
cosign verify-blob \
  --certificate eightly-agent-linux-amd64.crt \
  --signature eightly-agent-linux-amd64.sig \
  --certificate-identity-regexp '^https://github.com/smashingtags/eightly-agent' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  eightly-agent-linux-amd64
```

---

## 🐛 New bug introduced during round 1

### N1. `DetectInfo` is now called on every startup, even without registration

`internal/server/server.go` calls `register.DetectInfo` unconditionally to compute cert SANs. The previous behavior only called it during registration. This means:

- Every agent startup now does a UDP dial to `8.8.8.8:53` to detect local IP — even when `--server-url` isn't set
- Every startup does an HTTP call to the Docker socket for the version string just to compute cert SANs (which don't need it)

This is exactly the privacy/firewall surprise that issue #8 was trying to mitigate. Also, when registration *is* needed, `DetectInfo` runs twice (once for `Register`, once for SANs).

**Fix:** Either cache the result, or extract a lightweight `detectCertSANs(cfg)` that only does `os.Hostname()` + `cfg.AdvertiseIP` + `cfg.TLSSANs` parsing — no UDP probe, no Docker call.

Suggested shape:

```go
func detectCertSANs(cfg *config.Config) []string {
    var sans []string
    if hostname, err := os.Hostname(); err == nil && hostname != "" {
        sans = append(sans, hostname)
    }
    if cfg.AdvertiseIP != "" {
        sans = append(sans, cfg.AdvertiseIP)
    }
    for _, san := range strings.Split(cfg.TLSSANs, ",") {
        if s := strings.TrimSpace(san); s != "" {
            sans = append(sans, s)
        }
    }
    return sans
}
```

Then in `Run`, do `agentInfo` *only* if registration is needed and reuse it; otherwise use `detectCertSANs` for the cert.

**Test:** Add a test that constructs a config with no `ServerURL` and confirms cert generation doesn't trigger network egress. (Can be a separate `hasRegistration` predicate that the test asserts on, since you can't easily intercept a UDP dial in a unit test.)

---

## ⚠️ Subtly incomplete fixes from round 1

### S1. WebSocket `CheckOrigin` + `--insecure-no-auth` combination is exploitable

The comment in `websocket.go` correctly explains that permissive CheckOrigin is safe *when authentication is enabled* because browsers can't set the `Authorization` header on a WS handshake. But `--insecure-no-auth` lets users explicitly disable authentication, and in that mode a malicious webpage opened by a victim *can* drive the agent's WebSocket endpoints from the victim's browser.

**Fix:** Make the upgrader's CheckOrigin aware of auth state. Two acceptable approaches:

**Option A (recommended):** Tighten CheckOrigin when no-auth mode is active.

```go
func NewUpgrader(authEnabled bool) websocket.Upgrader {
    return websocket.Upgrader{
        CheckOrigin: func(r *http.Request) bool {
            if authEnabled {
                return true  // bearer token gate is sufficient
            }
            // No auth: only allow same-origin
            origin := r.Header.Get("Origin")
            return origin == "" || origin == "https://"+r.Host
        },
    }
}
```

Thread `cfg.Secret != ""` (or a derived `authEnabled` bool) through to `AttachHandler` / `ExecHandler`.

**Option B:** Refuse to start with both `--insecure-no-auth` and a non-loopback listen address. Loud fatal:

```
log.Fatal().Msg("--insecure-no-auth requires --addr=127.0.0.1; refusing to expose unauthenticated Docker socket on a non-loopback interface")
```

This is more restrictive but arguably the right call — `--insecure-no-auth` should be for local dev only.

Either way, add a test (or at least a negative integration test) covering the combination.

### S2. Re-registration doesn't authenticate with the existing token

`StartHeartbeat`'s recovery path calls `Register(state.ServerURL, "", info, tlsCfg)` — empty enroll token, no bearer auth from the existing `state.Token`. If your EOS server allows re-registration without proving prior identity, an attacker who can spoof source IP or hostname could overwrite an agent's identity server-side.

**Fix:** Pass the existing token on re-registration. Modify `Register` to accept an optional existing token, or add a parallel `ReRegister(state *State, info AgentInfo, tlsCfg *tls.Config)` that sends `Authorization: Bearer state.Token` and includes the existing `agent_id` in the payload:

```go
func ReRegister(state *State, info AgentInfo, tlsCfg *tls.Config) (*State, error) {
    // POST to /api/agents/register with:
    //   Authorization: Bearer state.Token
    //   body: {...AgentInfo, agent_id: state.AgentID}
    // Server should verify token matches agent_id before issuing a new token.
}
```

This requires a matching change on the EOS server side. Coordinate.

**Test:** Mock HTTP server that asserts the re-registration request carries the existing bearer token.

### S3. Boolean env overrides are one-way

In `config.go`:

```go
if os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "true" || os.Getenv("EIGHTLY_INSECURE_NO_AUTH") == "1" {
    c.InsecureNoAuth = true
}
```

This can only *set* the bool to true. If a user bakes `EIGHTLY_INSECURE_NO_AUTH=true` into a systemd unit and then wants to disable it via a flag, they can't override it back to false.

**Fix:** Make the env override symmetric, matching the documented "env vars take precedence" behavior:

```go
func envOverrideBool(target *bool, key string) {
    v := os.Getenv(key)
    if v == "" {
        return
    }
    switch strings.ToLower(v) {
    case "true", "1", "yes", "on":
        *target = true
    case "false", "0", "no", "off":
        *target = false
    }
}
```

Apply to both `InsecureNoAuth` and `InsecureServerTLS`. Add a test.

### S4. NVMe drives report empty vendor on Linux

`hostinfo_linux.go` reads `/sys/block/<name>/device/vendor`, which doesn't exist for NVMe drives (they use `/sys/block/<name>/device/model` and `/sys/class/nvme/nvmeN/model`).

**Fix:** Try `vendor` first, fall back to `model`:

```go
vendor := readSysfs(filepath.Join(base, "device", "vendor"))
if vendor == "" {
    vendor = readSysfs(filepath.Join(base, "device", "model"))
}
```

**Test:** Hard to test without faking sysfs; at minimum add a comment explaining the NVMe vs SATA distinction.

### S5. `config.Parse` is still untestable

`Parse` calls package-global `flag.Parse()`, so unit tests can't drive it with synthetic args. The current `config_test.go` only tests `envOverride` and constant values.

**Fix:** Refactor `Parse` to take an explicit `*flag.FlagSet` and `[]string`:

```go
func ParseArgs(fs *flag.FlagSet, args []string, version string) (*Config, error) {
    c := &Config{Version: version}
    // ... StringVar / BoolVar against fs ...
    if err := fs.Parse(args); err != nil {
        return nil, err
    }
    // ... env overrides ...
    return c, nil
}

// Keep Parse as a thin wrapper for main.go
func Parse(version string) *Config {
    cfg, err := ParseArgs(flag.CommandLine, os.Args[1:], version)
    if err != nil {
        // flag.CommandLine handles its own exit on parse errors
        os.Exit(2)
    }
    return cfg
}
```

Then write tests that exercise flag parsing, env precedence, and the `Secret == "" && !InsecureNoAuth` invariant directly.

### S6. `main.go` fail-closed check has no test

The `log.Fatal` on empty secret without `--insecure-no-auth` lives in `main.go`, which is hard to unit test. A regression that re-removes it could ship.

**Fix:** Extract the precondition into a method on `Config`:

```go
// Validate returns an error if the configuration is unsafe to start with.
func (c *Config) Validate() error {
    if c.Secret == "" && !c.InsecureNoAuth {
        return errors.New("EIGHTLY_AGENT_SECRET is required; use --insecure-no-auth to explicitly disable authentication")
    }
    return nil
}
```

In `main.go`:

```go
if err := cfg.Validate(); err != nil {
    log.Fatal().Err(err).Msg("invalid configuration")
}
```

Add tests:

- `TestValidate_EmptySecretRejected` — bare config returns error
- `TestValidate_InsecureNoAuthAccepted` — config with `InsecureNoAuth=true` validates
- `TestValidate_WithSecretAccepted` — config with non-empty Secret validates

### S7. `gosec` not in CI

Round 1 asked for `govulncheck` *and* `gosec`. Only `govulncheck` landed.

**Fix:** Add to `.github/workflows/ci.yml` lint job:

```yaml
- name: gosec
  uses: securego/gosec@master
  with:
    args: ./...
```

Be prepared to triage findings — gosec is noisy, but the high-confidence ones are worth fixing. Use `# nosec` comments sparingly with justifications.

### S8. No code coverage reporting

Round 1 asked for coverage. Still missing.

**Fix:** In CI test job:

```yaml
- name: Run tests with coverage
  run: go test ./... -count=1 -race -timeout 60s -coverprofile=coverage.out -covermode=atomic

- name: Upload coverage to Codecov
  uses: codecov/codecov-action@v4
  with:
    files: ./coverage.out
    fail_ci_if_error: false
```

Add the Codecov badge to the README. Aim for >80% on `auth`, `browse`, `websocket`, `register`, `tlsutil`.

### S9. `linux/arm/v7` not in build matrix

Round 1 suggested adding 32-bit ARM for Raspberry Pi support. Not done.

**Fix:** In both `ci.yml` and `release.yml` build matrices, add:

```yaml
- goos: linux
  goarch: arm
  goarm: "7"
  suffix: ""
```

And in the build step:

```yaml
- name: Build binary
  env:
    GOARM: ${{ matrix.goarm }}
  run: |
    CGO_ENABLED=0 GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} \
      go build -ldflags="-s -w -X main.version=${VERSION}" \
      -o eightly-agent-${{ matrix.goos }}-${{ matrix.goarch }}${{ matrix.suffix }} ./cmd/agent/
```

For Docker, add `linux/arm/v7` to the `platforms:` line in `docker-publish.yml`.

### S10. Dockerfile base images not digest-pinned

The comment "Dependabot monitors for updates" is only true once B2 is in place. Even then, weekly tag float is meaningfully different from a fixed digest.

**Fix:** Once Dependabot is configured to update Docker image digests, switch to digest-pinned:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.23-alpine@sha256:<digest> AS builder
# ...
FROM alpine:3.21@sha256:<digest>
```

Find the current digests with `docker pull golang:1.23-alpine && docker inspect ...`. Dependabot will then send PRs updating both the tag *and* the digest.

---

## 📋 Round 1 items to confirm

These were marked as fixed but I didn't verify directly in round 2. Please confirm in the next pass:

### C1. `release.yml` is the file I didn't re-fetch

Confirm that beyond the cosign/SBOM/provenance additions from B3, the file is otherwise unchanged from what I reviewed in round 1.

### C2. `docker-publish.yml` likewise

Confirm cosign signing of the published images was added per B3.

### C3. README has a threat model / security section

Round 1 issue #32 asked for explicit documentation of:

- Who's trusted (anyone with a valid token = root-equivalent on this host)
- What `EIGHTLY_AGENT_SECRET` protects (token forgery)
- What happens if the secret leaks (full Docker socket compromise, rotate immediately)
- Network exposure expectations (private network or behind VPN strongly recommended)

If this isn't in the README, add a "Security Model" section.

### C4. CONTRIBUTING.md and CODE_OF_CONDUCT.md

Helpful for a public launch. Don't have to be elaborate. CONTRIBUTING.md should explain:

- How to run tests locally (`make test`)
- The expected commit message format
- That security issues go to SECURITY.md's channel, not public issues
- Whether large feature PRs need an issue discussion first

CODE_OF_CONDUCT.md can be Contributor Covenant boilerplate.

### C5. License commitment statement

In the README or a `LICENSE_COMMITMENT.md`, an explicit statement:

> This repository will remain Apache License 2.0 in perpetuity. Future versions of the EOS protocol may be implemented in closed-source server components, but the agent itself will not change license.

This kind of statement is worth a surprising amount for trust.

---

## 🧪 Test additions to write

To raise confidence in the fixes:

1. **`config.Validate`** test cases per S6
2. **Boolean env override symmetry** test per S3 (set env to "false", confirm bool flips to false)
3. **`detectCertSANs` no-network test** per N1 (or at least a predicate test that registration is skipped without `ServerURL`)
4. **WebSocket origin enforcement** test per S1 (when auth disabled, reject cross-origin upgrade)
5. **Re-registration auth header** test per S2 (mock server asserts Bearer header presence)
6. **`detectLocalIP` UDP failure path** — set an unreachable target via test helper, confirm graceful empty return

---

## Priority order for round 2

Do these in order:

1. **B1** — SECURITY.md (15 min)
2. **B2** — dependabot.yml (10 min)
3. **B3** — Cosign + SBOM + provenance in release/docker workflows (1–2 hours)
4. **N1** — DetectInfo regression (30 min)
5. **S6** — config.Validate + tests (30 min)
6. **S1** — WebSocket origin check tightening (45 min)
7. **S2** — Re-registration auth (server-side coordination required; allow 1–2 hours plus EOS-side work)
8. **S3** — Symmetric boolean env overrides (15 min)
9. **S7, S8, S9** — gosec + coverage + arm/v7 (30 min total)
10. **S4, S5, S10** — NVMe fallback, config.Parse refactor, digest pinning (1 hour)
11. **C1–C5** — Docs and confirmations (30–45 min)

Estimated total: half a day to a day of focused work, depending on how much EOS-server coordination S2 requires.

---

## What to send back

When this round is complete, paste back:

- A summary of which items were done
- Any items skipped or modified, with reasoning
- A link to the SECURITY.md, dependabot.yml, and updated release.yml so I can verify directly
- Output of `go test ./... -count=1 -race -cover` showing coverage per package
- Output of `govulncheck ./...` and `gosec ./...` (clean or with documented exceptions)

Then we do round 3, which I expect to be mostly verification and a final sign-off.
