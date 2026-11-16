#!/bin/sh
# Fork addition: the browser smoke of the CI. It starts a throwaway instance
# of a finished build (what .github/actions/fork-build leaves in build/), and
# runs scripts/fork-ci-smoke.mjs against it from the Playwright image: the
# setup wizard and the gallery's seed over the API, then sign-in, one page of
# each area, a settings save read back, the Storage tree and the monitoring
# charts, and the gallery's core and guarded views (SET=smoke, CHECK=1) in
# Chromium at 1366x900 and 390x844. Exit 1: a check failed (OUT/summary.txt
# says which); 2: the instance or the tools could not be set up.
#
# The instance has no Docker (DOCKER_HOST points at a socket that does not
# exist, also on a GitHub runner, whose Docker it would otherwise manage): the
# pages that list containers show what a server without Docker shows, and the
# gallery's fixtures answer Storage, Users, notifications and events.
# Never point it at the production instance.
#
#   sh scripts/fork-ci-smoke.sh
#     START=host        run build/cosmos on this machine, HTTP on PORT (Cosmos listens on every address;
#                       it is reached on 127.0.0.1), the browser in the Playwright image with --network host
#                       (the GitHub runner)
#     START=docker      run the binary in SERVER_IMAGE on a network of its own (no port published, no Docker
#                       socket), the browser in that container's network namespace (a local run)
#     BUILD=build       the build to start (the cosmos binary, static/, meta.json)
#     SCRATCH=$RUNNER_TEMP/fork-ci-smoke   copy of the build, configuration folder, server log (removed
#                       at the end unless KEEP=1)
#     OUT=$SCRATCH-out  summary.txt, smoke.json, screenshots, the gallery's pictures and JSON, server.log
#     PORT=18080 HTTPS_PORT=18443  PREFIX=fork-ci-smoke (names of containers and the network)
#     SERVER_IMAGE=debian:13   NPM_CACHE=<folder>  npm's cache for the install of playwright
# The browser's image is pinned here and must carry the version of playwright that
# scripts/fork-ci-smoke/package-lock.json pins (the script refuses to run otherwise).
set -eu

PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.1-noble

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
START=${START:-host}
BUILD=$(cd "${BUILD:-$repo/build}" && pwd)
SCRATCH=${SCRATCH:-${RUNNER_TEMP:-/tmp}/fork-ci-smoke}
OUT=${OUT:-$SCRATCH-out}
PORT=${PORT:-18080}
HTTPS_PORT=${HTTPS_PORT:-18443}
PREFIX=${PREFIX:-fork-ci-smoke}
SERVER_IMAGE=${SERVER_IMAGE:-debian:13}
# (an address, not localhost: the panel probes each app at <app>.<host>, and names under localhost would
# reach the test server itself, which answers the probe without CORS headers: noise in the console)
BASE=http://127.0.0.1:$PORT

say() { echo "fork-ci-smoke: $*"; }
die() { say "$*"; exit 2; }

# --- the browser image and the npm package must be the same version of Playwright
image_version=${PLAYWRIGHT_IMAGE##*:v}; image_version=${image_version%-*}
pinned=$(sed -n 's/^ *"playwright": *"\([^"]*\)".*/\1/p' "$here/fork-ci-smoke/package.json")
locked=$(sed -n '/"node_modules\/playwright"/,/}/s/^ *"version": *"\([^"]*\)".*/\1/p' "$here/fork-ci-smoke/package-lock.json")
if [ "$pinned" != "$image_version" ] || [ "$locked" != "$image_version" ]; then
  die "the Playwright image is $image_version, scripts/fork-ci-smoke/package.json pins '$pinned' and its lock '$locked': they must be the same"
fi
test -x "$BUILD/cosmos" && test -s "$BUILD/static/index.html" || die "$BUILD holds no cosmos binary with static/"
case "$START" in host|docker) ;; *) die "START must be host or docker" ;; esac

# --- scratch: a copy of the build, an empty configuration folder
rm -rf "$SCRATCH" "$OUT"
mkdir -p "$SCRATCH/config" "$SCRATCH/deps" "$OUT"
cp -r "$BUILD" "$SCRATCH/server"
cp "$here/fork-ci-smoke/package.json" "$here/fork-ci-smoke/package-lock.json" "$SCRATCH/deps/"

loop=""
cleanup() {
  code=$?
  set +e
  if [ "$START" = host ]; then
    touch "$SCRATCH/stop"
    [ -s "$SCRATCH/pid" ] && kill "$(cat "$SCRATCH/pid")" 2>/dev/null
    [ -n "$loop" ] && wait "$loop" 2>/dev/null
    cp "$SCRATCH/server.log" "$OUT/server.log" 2>/dev/null
  else
    docker logs "$PREFIX-server" > "$OUT/server.log" 2>&1
    docker rm -f -v "$PREFIX-server" >/dev/null 2>&1
    docker network rm "$PREFIX-net" >/dev/null 2>&1
  fi
  [ "${KEEP:-}" = 1 ] || rm -rf "$SCRATCH"
  exit $code
}
trap cleanup EXIT
trap 'exit 2' INT TERM

# --- the server, started again whenever it ends: finishing the wizard ends the process on purpose
user="$(id -u):$(id -g)"
t0=$(date +%s)
if [ "$START" = host ]; then
  (
    cd "$SCRATCH/server"
    while [ ! -e "$SCRATCH/stop" ]; do
      COSMOS_CONFIG_FOLDER="$SCRATCH/config/" COSMOS_HTTP_PORT=$PORT COSMOS_HTTPS_PORT=$HTTPS_PORT \
        DOCKER_HOST="unix://$SCRATCH/no-docker.sock" ./cosmos >> "$SCRATCH/server.log" 2>&1 &
      echo $! > "$SCRATCH/pid"
      wait $! || true
      sleep 1
    done
  ) &
  loop=$!
  net="--network host"
  probe() { curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$PORT/cosmos-ui/" || true; }
else
  docker network create "$PREFIX-net" >/dev/null
  docker run -d --name "$PREFIX-server" --hostname "$PREFIX-server" --network "$PREFIX-net" --user "$user" \
    -v "$SCRATCH:/smoke" -e COSMOS_CONFIG_FOLDER=/smoke/config/ -e COSMOS_HTTP_PORT="$PORT" -e COSMOS_HTTPS_PORT="$HTTPS_PORT" \
    -e DOCKER_HOST=unix:///smoke/no-docker.sock -w /smoke/server "$SERVER_IMAGE" \
    sh -c 'while :; do ./cosmos; sleep 1; done' >/dev/null
  net="--network container:$PREFIX-server"
  probe() {
    docker run --rm $net "$PLAYWRIGHT_IMAGE" node -e \
      "fetch('http://127.0.0.1:$PORT/cosmos-ui/', { signal: AbortSignal.timeout(5000) }).then((r) => console.log(r.status), () => console.log(0))" 2>/dev/null || true
  }
fi

# Retry (reason): the server takes a few seconds to open its database and listener; wait for its
# first answer (90 s at most, as the build's start check does)
up=""
for _ in $(seq 1 90); do
  [ "$(probe)" = 200 ] && { up=1; break; }
  sleep 1
done
[ -n "$up" ] || { tail -n 60 "$SCRATCH/server.log" 2>/dev/null || docker logs --tail 60 "$PREFIX-server"; die "the server did not answer on port $PORT"; }
say "the server answers after $(( $(date +%s) - t0 )) s"

# --- playwright from the lock file, without its install scripts (the image carries the browsers)
npm_cache=""
[ -n "${NPM_CACHE:-}" ] && npm_cache="-v $NPM_CACHE:/npm-cache -e npm_config_cache=/npm-cache"
# shellcheck disable=SC2086
docker run --rm --name "$PREFIX-npm" --user "$user" -e HOME=/tmp $npm_cache -v "$SCRATCH/deps:/deps" -w /deps "$PLAYWRIGHT_IMAGE" \
  npm ci --ignore-scripts --no-audit --no-fund --loglevel=error || die "npm ci of scripts/fork-ci-smoke failed"

# --- the checks; the scripts come read-only from the checkout, playwright from the install above
status=0
# shellcheck disable=SC2086
docker run --rm --name "$PREFIX-browser" $net --ipc=host --user "$user" -e HOME=/tmp \
  -e BASE="$BASE" -e OUT=/out -v "$here:/smoke/scripts:ro" -v "$SCRATCH/deps/node_modules:/smoke/node_modules:ro" \
  -v "$OUT:/out" -w /smoke "$PLAYWRIGHT_IMAGE" node scripts/fork-ci-smoke.mjs || status=$?
say "done in $(( $(date +%s) - t0 )) s, exit $status; see $OUT/summary.txt"
[ "$status" = 0 ] || exit 1
