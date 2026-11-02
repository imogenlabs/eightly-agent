#!/usr/bin/env bash
# Resolve a release tag for release.yml and docker-publish.yml, so the binary
# release and the images accept exactly the same tags.
#
#   scripts/release-tag.sh resolve vX.Y.Z[-pre]   print key=value lines for $GITHUB_OUTPUT
#   scripts/release-tag.sh --self-test            prove the rules on a scratch repo
#
# A tag is accepted only if it is vMAJOR.MINOR.PATCH with an optional
# -prerelease suffix (semver, no leading zeros, no +build), exists, and points
# at a commit on origin/main. Anything else exits 1 with the reason.
#
# Image aliases follow the highest stable (no suffix) tag on main:
#   latest       the highest stable tag of all
#   MAJOR.MINOR  the highest stable patch in that minor line
#   MAJOR        the highest stable tag in that major line
# so republishing an older release never moves an alias backwards. A
# prerelease gets none of them. Tags not on main are not counted, so a stray
# tag off main cannot hold an alias back.
set -euo pipefail

SEMVER='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
STABLE='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
MAIN_REF="${RELEASE_MAIN_REF:-refs/remotes/origin/main}"

die() {
  echo "$*" >&2
  exit 1
}

# Stable tags whose commit is on main.
stable_on_main() {
  local t c
  git tag -l 'v*' | { grep -E "$STABLE" || true; } | while IFS= read -r t; do
    c=$(git rev-parse -q --verify "refs/tags/$t^{commit}") || continue
    if git merge-base --is-ancestor "$c" "$MAIN_REF"; then
      echo "$t"
    fi
  done
}

# Highest of the stable tags on stdin whose name starts with $1.
highest() {
  awk -v p="$1" 'index($0, p) == 1' | sort -V | tail -n 1
}

resolve() {
  local tag="$1" sha major minor pre stable
  if ! [[ "$tag" =~ $SEMVER ]]; then
    die "Not a release tag: '$tag' (expected vX.Y.Z or vX.Y.Z-prerelease)"
  fi
  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  pre="${BASH_REMATCH[4]}"
  if ! sha=$(git rev-parse -q --verify "refs/tags/$tag^{commit}"); then
    die "Tag $tag does not exist (or does not point at a commit). Push the tag first."
  fi
  if ! git rev-parse -q --verify "$MAIN_REF" >/dev/null; then
    die "$MAIN_REF is missing from the checkout"
  fi
  if ! git merge-base --is-ancestor "$sha" "$MAIN_REF"; then
    die "Tag $tag points at $sha, which is not on main. Releases ship from main only."
  fi

  local prerelease=false latest=false minor_alias=false major_alias=false
  if [ -n "$pre" ]; then
    prerelease=true
  else
    stable=$(stable_on_main)
    # Prefixes end in a dot, so v2.1. never matches v2.10.x (or v12.1.x).
    [ "$tag" = "$(printf '%s\n' "$stable" | highest v)" ] && latest=true
    [ "$tag" = "$(printf '%s\n' "$stable" | highest "v$major.$minor.")" ] && minor_alias=true
    [ "$tag" = "$(printf '%s\n' "$stable" | highest "v$major.")" ] && major_alias=true
  fi

  echo "tag=$tag"
  echo "version=${tag#v}"
  echo "sha=$sha"
  echo "prerelease=$prerelease"
  echo "latest=$latest"
  echo "minor_alias=$minor_alias"
  echo "minor_tag=$major.$minor"
  echo "major_alias=$major_alias"
  echo "major_tag=$major"
}

self_test() {
  local dir fails=0 self
  self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
  dir=$(mktemp -d)
  # Malformed names are built, not written out, so nothing mistakes them for a
  # version this repo uses.
  local leading_zero short
  leading_zero=$(printf 'v%s.%s.%s' 02 1 0)
  short=$(printf 'v%s.%s' 2 1)
  (
    cd "$dir"
    git init -q -b main
    git config user.email t@example.invalid
    git config user.name t
    git config commit.gpgSign false
    git config tag.gpgSign false
    c() { git commit -q --allow-empty -m "$1"; }
    tg() { git tag "$@"; }
    c c1; tg v2.0.0; tg "$leading_zero"; tg "$short"
    c c2; tg v2.1.0
    git checkout -q -b side; c off; tg v99.0.0; tg -a v98.0.0 -m off; git checkout -q main
    c c3; tg v2.1.1; tg v12.1.5; tg v2.10.0
    c c4; tg v2.2.0-alpha.1; tg v1.9.9
    c c5; tg -a v2.1.10 -m annotated
    git update-ref refs/remotes/origin/main main
  ) >/dev/null

  # tag | prerelease latest minor major, or refused
  local cases=(
    "v12.1.5|false true true true"
    "v2.10.0|false false true true"
    "v2.1.10|false false true false"
    "v2.1.1|false false false false"
    "v2.1.0|false false false false"
    "v2.0.0|false false true false"
    "v1.9.9|false false true true"
    "v2.2.0-alpha.1|true false false false"
    "v99.0.0|refused"
    "v98.0.0|refused"
    "$leading_zero|refused"
    "$short|refused"
    "v3.0.0|refused"
    "2.1.0|refused"
    "v2.1.0+build|refused"
  )
  local entry tag want out got
  for entry in "${cases[@]}"; do
    tag="${entry%%|*}"
    want="${entry#*|}"
    if out=$(cd "$dir" && RELEASE_MAIN_REF=refs/remotes/origin/main "$self" resolve "$tag" 2>/dev/null); then
      got=$(printf '%s\n' "$out" | awk -F= '$1=="prerelease"{p=$2} $1=="latest"{l=$2} $1=="minor_alias"{mi=$2} $1=="major_alias"{ma=$2} END{print p, l, mi, ma}')
    else
      got=refused
    fi
    if [ "$got" = "$want" ]; then
      echo "ok   $tag: $got"
    else
      echo "FAIL $tag: got '$got', want '$want'"
      fails=$((fails + 1))
    fi
  done
  # The sha is the tagged commit, also for an annotated tag.
  out=$(cd "$dir" && "$self" resolve v2.1.10)
  if [ "$(printf '%s\n' "$out" | sed -n 's/^sha=//p')" = "$(git -C "$dir" rev-parse 'v2.1.10^{commit}')" ]; then
    echo "ok   v2.1.10: sha is the tagged commit"
  else
    echo "FAIL v2.1.10: sha is not the tagged commit"
    fails=$((fails + 1))
  fi
  rm -rf "$dir"
  if [ "$fails" -ne 0 ]; then
    echo "release-tag self-test: $fails failed" >&2
    return 1
  fi
  echo "release-tag self-test: all passed"
}

case "${1:-}" in
  resolve)
    [ $# -eq 2 ] || die "usage: $0 resolve TAG"
    resolve "$2"
    ;;
  --self-test)
    self_test
    ;;
  *)
    die "usage: $0 resolve TAG | --self-test"
    ;;
esac
