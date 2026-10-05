#!/bin/sh
# EOS-2533: prove the agent image reaps orphaned processes and that its own
# health check passes.
#
# On eightly-ms01 the image's health check (busybox wget) timed out every 10s
# and each time left its ssl_client helper orphaned on PID 1, the agent, which
# never reaps: 115,420 zombies. The image now runs under tini and the check is
# in-process. This script runs the built image three ways:
#
#   control  entrypoint forced back to the bare agent, with a health check that
#            always times out and orphans a child. Must show zombies; if it
#            shows none, this script cannot see the failure it exists to catch,
#            and that is a failure too.
#   tini     the image as built, same orphaning check. Must show 0 zombies.
#   own      the image as built with its own HEALTHCHECK. Must reach healthy.
#   init     the image under `docker run --init`, the way Eight.ly OS hands out
#            the install command, same orphaning check. Must show 0 zombies and
#            tini must not warn about not being PID 1. Its control runs tini
#            without -s under --init and must warn, or the warning check is blind.
#
# The orphaning check is synthetic on purpose: the real check no longer starts a
# child, so it could never orphan one. This proves the reaper, not the check.
#
# Exit 0 pass, 1 fail, 2 cannot check (no usable Docker).
#
# Usage: scripts/check-image-reaps.sh IMAGE
set -u

IMAGE=${1:?usage: check-image-reaps.sh IMAGE}
WAIT=${REAP_WAIT_SECONDS:-20}
TAG=$$
ORPHANER="sh -c 'sleep 2 & exec sleep 60'"

# A daemon that never answers hangs `docker info` forever; bound it so that
# reads as "cannot check" instead of a job that never ends.
docker info >/dev/null 2>&1 &
probe=$!
( sleep 30; kill "$probe" 2>/dev/null ) &
watchdog=$!
if ! wait "$probe"; then
	echo "CANNOT CHECK: docker is not available to this runner" >&2
	exit 2
fi
kill "$watchdog" 2>/dev/null

# Some runners answer `docker info` but cannot start any container (nested
# Docker in an LXC: runc fails opening a sysctl before the image runs). That
# says nothing about the image, so it is "cannot check", not a failure of it.
if ! probe_err=$(docker run --rm --entrypoint /bin/true "$IMAGE" 2>&1); then
	echo "CANNOT CHECK: this runner's Docker cannot start a container: $probe_err" >&2
	exit 2
fi

# shellcheck disable=SC2329 # invoked by the trap below
cleanup() {
	docker rm -f "reap-control-$TAG" "reap-tini-$TAG" "reap-own-$TAG" "reap-init-$TAG" "reap-warn-$TAG" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

zombies() {
	docker exec "$1" sh -c 'n=0; for f in /proc/[0-9]*/status; do grep -q "^State:[[:space:]]*Z" "$f" 2>/dev/null && n=$((n+1)); done; echo $n'
}

start_orphaning() { # name, extra docker run args...
	name=$1
	shift
	docker run -d --name "$name" -e EIGHTLY_AGENT_SECRET=ci-only-secret \
		--health-cmd "$ORPHANER" --health-interval 1s --health-timeout 1s \
		--health-retries 100000 "$@" "$IMAGE" >/dev/null
}

start_orphaning "reap-control-$TAG" --entrypoint /app/eightly-agent || exit 1
start_orphaning "reap-tini-$TAG" || exit 1
start_orphaning "reap-init-$TAG" --init || exit 1
docker run -d --name "reap-warn-$TAG" --init -e EIGHTLY_AGENT_SECRET=ci-only-secret \
	--entrypoint /sbin/tini "$IMAGE" -- /app/eightly-agent >/dev/null || exit 1
docker run -d --name "reap-own-$TAG" -e EIGHTLY_AGENT_SECRET=ci-only-secret "$IMAGE" >/dev/null || exit 1

echo "waiting ${WAIT}s for health checks to run"
sleep "$WAIT"

fail=0
pid1_control=$(docker exec "reap-control-$TAG" cat /proc/1/comm)
pid1_tini=$(docker exec "reap-tini-$TAG" cat /proc/1/comm)
control=$(zombies "reap-control-$TAG")
tini=$(zombies "reap-tini-$TAG")
own=$(docker inspect --format '{{.State.Health.Status}}' "reap-own-$TAG")
init=$(zombies "reap-init-$TAG")
init_warn=$(docker logs "reap-init-$TAG" 2>&1 | grep -c 'Tini is not running as PID 1' || true)
warn_control=$(docker logs "reap-warn-$TAG" 2>&1 | grep -c 'Tini is not running as PID 1' || true)
checks=$(docker inspect --format '{{len .State.Health.Log}}' "reap-tini-$TAG")

echo "control (PID 1 = $pid1_control): $control zombies"
echo "tini    (PID 1 = $pid1_tini): $tini zombies (health log holds $checks checks)"
echo "own health check: $own"
echo "--init  : $init zombies, $init_warn tini warnings (control without -s: $warn_control)"

if [ "$control" -eq 0 ]; then
	echo "FAIL: the control left no zombies, so this check cannot see the defect it guards" >&2
	fail=1
fi
if [ "$pid1_tini" != "tini" ]; then
	echo "FAIL: PID 1 in the image is $pid1_tini, not tini" >&2
	fail=1
fi
if [ "$tini" -ne 0 ]; then
	echo "FAIL: the image left $tini zombies" >&2
	fail=1
fi
if [ "$own" != "healthy" ]; then
	echo "FAIL: the image's own health check reports $own" >&2
	docker inspect --format '{{json .State.Health}}' "reap-own-$TAG" >&2
	fail=1
fi
if [ "$init" -ne 0 ]; then
	echo "FAIL: under --init the image left $init zombies" >&2
	fail=1
fi
if [ "$warn_control" -eq 0 ]; then
	echo "FAIL: tini without -s under --init did not warn, so the warning check cannot see" >&2
	fail=1
fi
if [ "$init_warn" -ne 0 ]; then
	echo "FAIL: under --init tini warned:" >&2
	docker logs "reap-init-$TAG" 2>&1 | grep 'Tini is not running as PID 1' >&2
	fail=1
fi
[ "$fail" -eq 0 ] && echo "PASS"
exit "$fail"
