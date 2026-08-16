#!/usr/bin/env bash
# Generate point-in-time compliance evidence for Eight.ly Agent into
# compliance/evidence/. Safe to run anywhere: every probe degrades to a
# "tool not available" note instead of failing the run.
#
# Run on demand, in CI, and on every release tag.
set -uo pipefail

# Resolve repo root from this script's location so it works from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

OUT="compliance/evidence"
mkdir -p "$OUT"
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
HEAD="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
IMAGE="${EVIDENCE_IMAGE:-ghcr.io/imogenlabs/eightly-agent:latest}"
CONTAINER="${EVIDENCE_CONTAINER:-eightly-agent}"
UNIT="deploy/eightly-agent.service"

hdr() { printf '# %s\n# timestamp=%s git=%s\n\n' "$1" "$TS" "$HEAD"; }
have() { command -v "$1" >/dev/null 2>&1; }

echo "Collecting Eight.ly Agent evidence at $TS (git $HEAD)"

# --- Toolchain: single-sourced Go version (ASVS V14.1.1, CSF ID.AM-08) ---
{
  hdr "Go toolchain pin"
  echo "## go.mod declared toolchain:"
  grep -E '^go ' go.mod || echo "(go directive not found)"
  echo
  echo "## workflow pins (should all match go.mod):"
  grep -rhoE "go-version: '[0-9.]+'" .github/workflows/ | sort -u || echo "(no workflow pins found)"
  echo
  echo "## locally installed go:"
  if have go; then go version; else echo "go not installed on this host"; fi
} > "$OUT/toolchain-go-version.txt"

# --- Dependency vulnerabilities (CIS 4.4, ASVS V14.2.1, CSF ID.RA-01) ---
{
  hdr "govulncheck"
  if have govulncheck; then
    govulncheck ./... 2>&1 || echo "(govulncheck exited non-zero — review findings above)"
  elif have go; then
    echo "govulncheck not on PATH; attempting 'go run golang.org/x/vuln/cmd/govulncheck@latest'..."
    GOFLAGS=-mod=mod go run golang.org/x/vuln/cmd/govulncheck@latest ./... 2>&1 \
      || echo "(govulncheck run failed — likely offline; record and retry online)"
  else
    echo "go/govulncheck not available on this host — skipped"
  fi
} > "$OUT/govulncheck.txt"

# --- Static security analysis (CIS 4.4, ASVS V5.x, CSF PR.PS-06) ---
{
  hdr "gosec"
  if have gosec; then
    gosec -quiet ./... 2>&1 || echo "(gosec exited non-zero — review findings above)"
  else
    echo "gosec not installed on this host — CI runs it on every PR (.github/workflows/ci.yml)"
  fi
} > "$OUT/gosec.txt"

# --- Container user is non-root (CIS 4.1) ---
{
  hdr "CIS 4.1 — container runs as non-root"
  echo "## Dockerfile directives:"
  grep -E 'adduser|USER ' Dockerfile || echo "(no USER directive found)"
  echo
  if have docker; then
    echo "## live container effective user (id):"
    if docker ps -q --filter "name=$CONTAINER" | grep -q .; then
      docker exec "$CONTAINER" id 2>/dev/null || echo "(could not exec into $CONTAINER)"
    else
      echo "$CONTAINER not running; inspecting image config instead:"
      docker image inspect "$IMAGE" --format '{{.Config.User}}' 2>/dev/null \
        || echo "(image $IMAGE not present locally)"
    fi
  else
    echo "docker not available — relying on Dockerfile USER directive above"
  fi
} > "$OUT/CIS-4.1-user.txt"

# --- Base images are digest-pinned (CIS 4.2) ---
{
  hdr "CIS 4.2 — base images pinned by digest"
  grep -E '^FROM ' Dockerfile || echo "(no FROM lines found)"
} > "$OUT/CIS-4.2-base-images.txt"

# --- Scanner wiring (CIS 4.4) ---
{
  hdr "CIS 4.4 — scanners wired in CI"
  echo "## workflows present:"
  ls -1 .github/workflows/ 2>/dev/null || echo "(no workflows dir)"
  echo
  echo "## scanner invocations in ci.yml:"
  grep -nE 'govulncheck|gosec|staticcheck|golangci-lint' .github/workflows/ci.yml 2>/dev/null || echo "(none found)"
} > "$OUT/CIS-4.4-scanners.txt"

# --- Socket mount is read-only (CIS 5.5 / 5.29 / 5.31) ---
{
  hdr "CIS 5.5/5.29/5.31 — docker.sock mounted read-only"
  echo "## docker-compose.yml socket mount:"
  grep -nE 'docker.sock' docker-compose.yml || echo "(no socket mount declared)"
  echo
  if have docker && docker ps -q --filter "name=$CONTAINER" | grep -q .; then
    echo "## live mount mode (RW=false expected):"
    docker inspect "$CONTAINER" \
      --format '{{range .Mounts}}{{.Source}}->{{.Destination}} RW={{.RW}}{{"\n"}}{{end}}' 2>/dev/null \
      || echo "(inspect failed)"
  else
    echo "(container not running — relying on compose declaration above)"
  fi
} > "$OUT/CIS-5.5-mounts.txt"

# --- systemd hardening (CIS 5.x crosswalk, CSF PR.IR-01) ---
{
  hdr "systemd unit hardening directives"
  echo "## declared in $UNIT:"
  grep -E 'NoNewPrivileges|ProtectSystem|ProtectHome|PrivateTmp|CapabilityBoundingSet|AmbientCapabilities|RestrictNamespaces|RestrictAddressFamilies|RestrictRealtime|LockPersonality|ProtectControlGroups|ProtectKernel|SystemCallFilter|SystemCallArchitectures|ProtectProc|ProcSubset|ReadWritePaths|UMask|User=' "$UNIT" 2>/dev/null \
    || echo "(unit file not found at $UNIT)"
  echo
  if have systemd-analyze; then
    echo "## systemd-analyze security (if unit is installed):"
    systemd-analyze security eightly-agent 2>&1 | head -60 || echo "(unit not installed on this host)"
  else
    echo "systemd-analyze not available — directives above are the source of truth"
  fi
} > "$OUT/SYSTEMD-hardening.txt"

# --- TLS posture (ASVS V9.1.2) ---
{
  hdr "ASVS V9.1.2 — TLS minimum version + cipher policy"
  echo "## listener config (internal/server/server.go):"
  grep -nE 'MinVersion|TLS_ECDHE|ListenAndServeTLS' internal/server/server.go 2>/dev/null \
    || echo "(server.go not found)"
} > "$OUT/ASVS-V9.1.2-tls.txt"

# --- Auth fail-closed (ASVS V4.1.5) ---
{
  hdr "ASVS V4.1.5 — auth fails closed"
  echo "## config validation (internal/config/config.go):"
  grep -nE 'EIGHTLY_AGENT_SECRET is required|insecure-no-auth' internal/config/config.go 2>/dev/null \
    || echo "(config.go not found)"
  echo
  echo "## middleware fail-closed contract (internal/auth/jwt.go):"
  grep -nE 'fail closed|FAIL CLOSED|serviceUnavailable|misconfigured' internal/auth/jwt.go 2>/dev/null \
    || echo "(jwt.go not found)"
} > "$OUT/ASVS-V4.1.5-fail-closed.txt"

# --- Supply chain: signing + SBOM + pinned actions (CSF GV.SC-01) ---
{
  hdr "CSF GV.SC-01 — supply-chain controls"
  echo "## cosign / provenance / SBOM steps:"
  grep -rnE 'cosign|attest-build-provenance|sbom-action|attest-sbom' .github/workflows/ 2>/dev/null \
    || echo "(none found)"
  echo
  echo "## CycloneDX SBOM present in repo?"
  if [ -f compliance/sbom.cdx.json ]; then
    echo "yes: compliance/sbom.cdx.json"
    if have jq; then
      jq -r '"components: " + (.components | length | tostring)' compliance/sbom.cdx.json 2>/dev/null || true
    fi
  else
    echo "no committed SBOM — see compliance/sbom-howto.md for the generation command"
  fi
} > "$OUT/supply-chain.txt"

echo "Evidence written to $OUT/ (git $HEAD)"
