#!/usr/bin/env bash
#
# coverage-check.sh -- the coverage ratchet: whole-tree and per-package floors.
#
# Every package under cmd/ and internal/ has a floor, set about two points
# under what CI measured when it was last raised, so ordinary churn does not
# fail the build and a real loss does. Raise a floor when coverage climbs.
# Never lower one to get a build green; if code really was removed, say so in
# the commit that lowers it.
#
# Usage:
#   scripts/coverage-check.sh [coverprofile]   gate a profile CI already wrote,
#                                              or run the suite to make one
#   scripts/coverage-check.sh --self-test      fire all three outcomes
#
# Three outcomes, three exit statuses:
#
#   0  every floor measured and held             a fact about the code
#   1  a measured number is BELOW ITS FLOOR,     a fact about the code
#      or a package has no floor at all
#   2  CANNOT MEASURE: no number was produced    a fact about the build
#
# 2 is kept apart from 1 on purpose. A suite that does not build writes no
# profile, and a renamed package is simply absent from one; reading either as
# 0% would report a coverage regression and send whoever reads it to write
# tests for code that does not compile. CI fails on 2 as well, with different
# words.
#
# Floors are measured on Linux, as CI runs. hostinfo, winacl, browse and
# register have platform files, so a local run on macOS or Windows reads
# differently and is advisory only.
set -uo pipefail

EXIT_OK=0
EXIT_BELOW_FLOOR=1
EXIT_CANNOT_MEASURE=2

# The go command, as a seam for the self-test. Nothing in CI sets it.
GO_BIN="${COVERAGE_CHECK_GO:-go}"

# Statement coverage of the whole tree. Floors below were set from Linux CI
# (Test job, 2026-11-02): overall 84.1, cmd/agent 93.5, auth 94.1, browse 85.8,
# config 94.3, hostinfo 89.8, metrics 100, proxy 96.9, register 77.2,
# server 81.0, tlsutil 68.3, websocket 75.2, winacl 75.7. Raised after the
# WebSocket target checks and proxy daemon tests (Linux CI, same day): overall
# 86.2, proxy 93.1, websocket 81.5. Raised after the fourth policy round
# (proxy 94.1 measured on macOS; the package has no platform files).
OVERALL_FLOOR=84.0

# package (relative to the module root) : floor %
FLOORS=(
  "cmd/agent:91.0"
  "internal/auth:92.0"
  "internal/browse:83.0"
  "internal/config:92.0"
  "internal/hostinfo:87.0"
  "internal/metrics:98.0"
  "internal/proxy:92.0"
  "internal/register:75.0"
  "internal/server:79.0"
  "internal/tlsutil:66.0"
  "internal/websocket:79.0"
  "internal/winacl:73.0"
  "internal/dockerdial:98.0"
)

# self_test fires the gate at every outcome against profiles it writes itself,
# with a stub go command. It is defined before the dispatch below because bash
# resolves functions when they are called.
#
# Every assertion pins an EXACT status: `-ne 0` cannot tell a floor breach from
# a tree that was never measured, which is the distinction this script exists
# to keep.
self_test() {
  local tmp cases=0 module
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  module="$(awk '$1 == "module" { print $2; exit }' "$ROOT/go.mod")"

  # A stand-in for go. `go test ... -coverprofile=X` copies $STUB_PROFILE to X,
  # or fails without writing anything when STUB_FAIL=1.
  mkdir -p "$tmp/bin"
  cat >"$tmp/bin/go" <<'STUB'
#!/usr/bin/env bash
[ "$1" = "test" ] || exit 0
for a in "$@"; do
  case "$a" in -coverprofile=*|--coverprofile=*) prof="${a#*=}" ;; esac
done
[ "${STUB_FAIL:-0}" = "1" ] && { echo "stub: build failed" >&2; exit 1; }
[ -n "${prof:-}" ] && cp "$STUB_PROFILE" "$prof"
exit 0
STUB
  chmod +x "$tmp/bin/go"

  # profile FILE PCT [PKG PCT WEIGHT]... -- one 100-statement block pair per
  # floored package at PCT, then any overrides (a package listed again is
  # replaced, and WEIGHT scales its statement count).
  profile() {
    local out=$1 pct=$2
    shift 2
    {
      echo "mode: atomic"
      local entry pkg i skip
      for entry in "${FLOORS[@]}"; do
        pkg="${entry%%:*}"
        skip=0
        for ((i = 1; i <= $#; i += 3)); do
          [ "${!i}" = "$pkg" ] && skip=1
        done
        [ "$skip" = 1 ] && continue
        echo "$module/$pkg/f.go:1.1,2.2 $pct 1"
        echo "$module/$pkg/f.go:3.1,4.2 $((100 - pct)) 0"
      done
      while [ $# -ge 3 ]; do
        echo "$module/$1/f.go:1.1,2.2 $(($2 * $3)) 1"
        echo "$module/$1/f.go:3.1,4.2 $(((100 - $2) * $3)) 0"
        shift 3
      done
    } >"$out"
  }

  local SELF_OUT=""
  expect_exit() {
    local want=$1 label=$2 got
    shift 2
    SELF_OUT=$(COVERAGE_CHECK_GO="$tmp/bin/go" GITHUB_STEP_SUMMARY="" "$SCRIPT" "$@" 2>&1)
    got=$?
    cases=$((cases + 1))
    if [ "$got" != "$want" ]; then
      echo "self-test: $label should exit $want, got $got" >&2
      echo "$SELF_OUT" >&2
      return 1
    fi
  }
  said() {
    case "$SELF_OUT" in *"$1"*) return 0 ;; esac
    echo "self-test: expected the output to say '$1'" >&2
    echo "$SELF_OUT" >&2
    return 1
  }
  said_no() {
    case "$SELF_OUT" in *"$1"*)
      echo "self-test: the output must NOT say '$1'" >&2
      echo "$SELF_OUT" >&2
      return 1
      ;;
    esac
  }

  # The lowest-floored package, for building a tree that is below the overall
  # floor while every package holds its own.
  local lowest_pkg="" lowest=101 entry f
  for entry in "${FLOORS[@]}"; do
    f="${entry##*:}"
    f="${f%%.*}"
    if [ "$f" -lt "$lowest" ]; then lowest=$f lowest_pkg="${entry%%:*}"; fi
  done
  if [ $((lowest + 1)) -ge "${OVERALL_FLOOR%%.*}" ]; then
    echo "self-test: cannot build a tree below the overall floor ($OVERALL_FLOOR) whose packages all hold; the lowest package floor is $lowest" >&2
    return 1
  fi
  local first_pkg="${FLOORS[0]%%:*}"

  # 1. Everything measured, everything held.
  profile "$tmp/clean.out" 99
  expect_exit 0 "a clean measured profile" "$tmp/clean.out" || return 1
  said "PASS" || return 1

  # 2. The tree below its floor while every package holds its own.
  profile "$tmp/tree-low.out" 99 "$lowest_pkg" $((lowest + 1)) 1000
  expect_exit 1 "the tree below its floor" "$tmp/tree-low.out" || return 1
  said "OVERALL" || return 1
  said "BELOW FLOOR" || return 1
  said_no "CANNOT MEASURE" || return 1

  # 3. One package below its floor, by name.
  profile "$tmp/pkg-low.out" 99 "$first_pkg" 1 1
  expect_exit 1 "a package below its floor" "$tmp/pkg-low.out" || return 1
  said "$first_pkg" || return 1
  said "BELOW FLOOR" || return 1
  said_no "CANNOT MEASURE" || return 1

  # 4. A package nobody gave a floor is a finding: it would otherwise lose
  #    coverage unguarded forever.
  profile "$tmp/unfloored.out" 99 "internal/brandnew" 99 1
  expect_exit 1 "a package with no floor" "$tmp/unfloored.out" || return 1
  said "internal/brandnew" || return 1
  said "NO FLOOR" || return 1

  # 5. The suite wrote no profile: the build is broken, not the coverage.
  STUB_FAIL=1 expect_exit 2 "a suite that wrote no profile" || return 1
  said "CANNOT MEASURE" || return 1
  said_no "BELOW FLOOR" || return 1

  # 6. A profile with no coverage rows in it.
  echo "mode: atomic" >"$tmp/empty.out"
  expect_exit 2 "a profile with no rows" "$tmp/empty.out" || return 1
  said "CANNOT MEASURE" || return 1
  said_no "BELOW FLOOR" || return 1

  # 7. A floored package absent from the profile: renamed, deleted, or not
  #    building. Not a lost-coverage finding.
  grep -v "/$first_pkg/" "$tmp/clean.out" >"$tmp/missing.out"
  expect_exit 2 "a floored package that measured nothing" "$tmp/missing.out" || return 1
  said "NO MEASUREMENT" || return 1
  said "CANNOT MEASURE" || return 1
  said_no "BELOW FLOOR" || return 1

  # 8. With COVERAGE_CHECK_GO unset the script runs go from PATH and gates the
  #    profile it made. A test that only sets the seam proves the branch, not
  #    the wiring.
  local out rc
  out=$(PATH="$tmp/bin:$PATH" STUB_PROFILE="$tmp/clean.out" GITHUB_STEP_SUMMARY="" \
    env -u COVERAGE_CHECK_GO "$SCRIPT" 2>&1)
  rc=$?
  cases=$((cases + 1))
  if [ "$rc" -ne 0 ]; then
    echo "self-test: with COVERAGE_CHECK_GO unset, go from PATH should make a profile that passes; got exit $rc" >&2
    echo "$out" >&2
    return 1
  fi

  # A self-test that examined nothing is not a pass.
  local expected=8
  if [ "$cases" -ne "$expected" ]; then
    echo "self-test: $cases cases ran, expected $expected" >&2
    return 1
  fi
  echo "coverage-check: self-test passed ($cases cases: 0 held, 1 below floor or unfloored, 2 cannot measure)"
}

SCRIPT="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [[ "${1:-}" == "--self-test" ]]; then
  self_test
  exit $?
fi

cd "$ROOT" || exit "$EXIT_CANNOT_MEASURE"
PLATFORM="$(uname -s)"

cannot_measure() {
  echo
  echo "CANNOT MEASURE: $1"
  echo
  echo "  No coverage number was produced, so this says nothing about coverage."
  echo "  It is not a floor breach and not a pass. $2"
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    {
      echo "### Coverage gate: CANNOT MEASURE"
      echo
      echo "$1"
      echo
      echo "No number was produced, so this is **not** a coverage regression. $2"
    } >>"$GITHUB_STEP_SUMMARY"
  fi
  exit "$EXIT_CANNOT_MEASURE"
}

module="$(awk '$1 == "module" { print $2; exit }' go.mod)"
[[ -n "$module" ]] || cannot_measure "go.mod names no module." "Run this from the repository."

profile="${1:-}"
if [[ -z "$profile" ]]; then
  profile="$(mktemp)"
  trap 'rm -f "$profile"' EXIT
  echo "==> running the suite for a coverprofile"
  test_rc=0
  "$GO_BIN" test -count=1 -covermode=atomic -coverprofile="$profile" ./... >/dev/null 2>&1 || test_rc=$?
  if [[ ! -s "$profile" ]]; then
    cannot_measure "\`$GO_BIN test\` exited $test_rc and wrote no coverprofile." \
      "Run \`go test ./...\` and fix what it says; the tree almost certainly does not build."
  fi
elif [[ ! -s "$profile" ]]; then
  cannot_measure "the coverprofile $profile is missing or empty." \
    "Check the step that was meant to write it."
fi

# One line per package, "<package> <covered> <statements>", then
# "TOTAL <covered> <statements>". A block is counted once however many test
# binaries reported it, and is covered if any of them ran it; that is how
# `go tool cover` totals a profile too.
measured="$(awk -v mod="$module/" '
  NR == 1 && /^mode:/ { next }
  NF == 3 {
    key = $1
    file = $1; sub(/:[^:]*$/, "", file)
    pkg = file; sub(/\/[^\/]*$/, "", pkg)
    if (index(pkg, mod) == 1) pkg = substr(pkg, length(mod) + 1)
    if (!(key in stmts)) { stmts[key] = $2; pkgof[key] = pkg }
    if ($3 > 0) hit[key] = 1
  }
  END {
    for (k in stmts) {
      p = pkgof[k]; tot[p] += stmts[k]; T += stmts[k]
      if (k in hit) { cov[p] += stmts[k]; C += stmts[k] }
    }
    for (p in tot) printf "%s %d %d\n", p, cov[p], tot[p]
    if (T > 0) printf "TOTAL %d %d\n", C, T
  }' "$profile")"

pct() { awk -v c="$1" -v t="$2" 'BEGIN { printf "%.1f", 100 * c / t }'; }
ge() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a + 0 >= b + 0) }'; }

read -r _ total_cov total_stmts <<<"$(grep '^TOTAL ' <<<"$measured")"
if [[ -z "${total_stmts:-}" || "$total_stmts" -eq 0 ]]; then
  cannot_measure "the coverprofile $profile has no coverage rows, so the tree's coverage is unknown." \
    "A suite that failed to build writes a profile with nothing in it; check the test step."
fi

fail=0
unmeasured=0

summary() { echo "$1"; [[ -n "${GITHUB_STEP_SUMMARY:-}" ]] && echo "$1" >>"$GITHUB_STEP_SUMMARY"; return 0; }
row() { # scope actual floor result
  printf '%-22s %7s %7s   %s\n' "$1" "$2" "$3" "$4"
  [[ -n "${GITHUB_STEP_SUMMARY:-}" ]] && echo "| $1 | $2 | $3 | $4 |" >>"$GITHUB_STEP_SUMMARY"
  return 0
}

check() { # scope actual floor
  local res="ok"
  if [[ -z "$2" ]]; then
    res="NO MEASUREMENT"
    unmeasured=$((unmeasured + 1))
  elif ! ge "$2" "$3"; then
    res="BELOW FLOOR"
    fail=1
  fi
  row "$1" "${2:--}" "$3" "$res"
}

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "### Coverage gate (measured on ${PLATFORM})"
    echo
    echo "| scope | actual % | floor % | result |"
    echo "|---|---|---|---|"
  } >>"$GITHUB_STEP_SUMMARY"
fi

echo "================ coverage gate [measured on ${PLATFORM}] ================"
if [[ "$PLATFORM" != "Linux" ]]; then
  echo "NOTE: floors are measured on Linux CI. Packages with platform files read"
  echo "      differently here, so this run is advisory."
fi
row "scope" "actual%" "floor%" "result"

check "OVERALL" "$(pct "$total_cov" "$total_stmts")" "$OVERALL_FLOOR"

floored=" "
for entry in "${FLOORS[@]}"; do
  pkg="${entry%%:*}"
  floored="$floored$pkg "
  actual=""
  if read -r _ c t <<<"$(awk -v p="$pkg" '$1 == p' <<<"$measured")" && [[ -n "${t:-}" && "$t" -gt 0 ]]; then
    actual="$(pct "$c" "$t")"
  fi
  check "$pkg" "$actual" "${entry##*:}"
  c="" t=""
done

# A package with no floor can lose all its tests and nothing here would notice.
while read -r pkg c t; do
  [[ "$pkg" == "TOTAL" || "$floored" == *" $pkg "* ]] && continue
  row "$pkg" "$(pct "$c" "$t")" "-" "NO FLOOR"
  fail=1
done <<<"$measured"

echo "======================================================================="

if [[ "$fail" -ne 0 ]]; then
  summary ""
  summary "**FAIL: coverage is below a floor, or a package has no floor.** Add tests, or add the new package to FLOORS in scripts/coverage-check.sh at about two points under its measured number."
  [[ "$PLATFORM" != "Linux" ]] && summary "(Measured on ${PLATFORM}; CI on Linux is the authority.)"
  [[ "$unmeasured" -ne 0 ]] && summary "($unmeasured floored package(s) produced no measurement; that is a separate problem, listed as NO MEASUREMENT above.)"
  exit "$EXIT_BELOW_FLOOR"
fi

if [[ "$unmeasured" -ne 0 ]]; then
  cannot_measure "$unmeasured floored package(s) are not in the coverprofile (listed NO MEASUREMENT above)." \
    "They were usually renamed, deleted, or do not build. Fix the path in FLOORS in scripts/coverage-check.sh, or fix the build."
fi

summary ""
summary "**PASS: every coverage floor held (measured on ${PLATFORM}).**"
exit "$EXIT_OK"
