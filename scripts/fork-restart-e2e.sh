#!/bin/sh
# Fork addition (plan 46): restarts of the web server, hammered end to end.
# Sets up a docker:29-dind of its own (on a network of its own, nothing
# pulled: the test app is a program of this repository), runs the test
# Cosmos inside it outside Docker (as on a bare-metal server) on HTTPS 2053
# with a self-signed certificate and HTTP 2052, finishes its setup wizard,
# starts the app of scripts/fork-restart-e2e.go on 127.0.0.1:8081 and runs
# its hammer there (what it proves is in the Go file's header). The Cosmos
# process runs in a loop, so an exit shows up as a new process, not as a
# stuck test. Never the owner's server or Docker.
#
#   BIN=<cosmos built with CGO_ENABLED=0> sh scripts/fork-restart-e2e.sh        run, remove everything
#   BIN=... sh scripts/fork-restart-e2e.sh up      run, keep (KEEP=1 implied)
#   sh scripts/fork-restart-e2e.sh clean           remove what a kept run left
# Options: BIN, STATIC (a built ./static, optional: a stub page otherwise),
# PREFIX=fork-restart, SCRATCH=/storage/dev/.cache/$PREFIX, PASSWORD, and the
# hammer's SCALE, ONLY, GAPS. The Cosmos log is copied to $SCRATCH/cosmos.log.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
PREFIX=${PREFIX:-fork-restart}
SCRATCH=${SCRATCH:-/storage/dev/.cache/$PREFIX}
BIN=${BIN:-$here/../build/cosmos}
STATIC=${STATIC:-}
PASSWORD=${PASSWORD:-Test-Passw0rd!x}
GOCACHE_DIR=${GOCACHE_DIR:-/storage/dev/.cache}
D="$PREFIX-dind"

clean() {
  docker rm -f -v "$D" "$PREFIX-build" >/dev/null 2>&1 || true
  docker network rm "$PREFIX-net" >/dev/null 2>&1 || true
  # the Docker in Docker's own files are root's: removed from a container
  [ -d "$SCRATCH" ] && docker run --rm -v "$(dirname "$SCRATCH"):/w" busybox:1.36 rm -rf "/w/$(basename "$SCRATCH")" || true
}
case "${1:-}" in clean) clean; exit 0;; up) KEEP=1;; esac
test -x "$BIN" || { echo "no cosmos binary at $BIN (BIN=...)"; exit 2; }

mkdir -p "$SCRATCH/dind" "$SCRATCH/tool"
docker run --rm --name "$PREFIX-build" -v "$here/..:/src:ro" -w /src -v "$SCRATCH/tool:/out" \
  -v "$GOCACHE_DIR/go-mod:/go/pkg/mod" -v "$GOCACHE_DIR/go-build:/root/.cache/go-build" \
  -e CGO_ENABLED=0 golang:1.27 go build -o /out/restart-e2e scripts/fork-restart-e2e.go
docker network create --internal "$PREFIX-net" >/dev/null 2>&1 || true
docker run -d --name "$D" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-net" \
  -v "$SCRATCH/dind:/var/lib/docker" docker:29-dind >/dev/null
for _ in $(seq 1 60); do docker exec "$D" docker info >/dev/null 2>&1 && break; sleep 1; done
docker exec "$D" sh -c 'mkdir -p /cosmos/config /cosmos/static'
docker cp "$BIN" "$D:/cosmos/cosmos"
docker cp "$SCRATCH/tool/restart-e2e" "$D:/restart-e2e"
if [ -n "$STATIC" ]; then docker cp "$STATIC/." "$D:/cosmos/static/"; else docker exec "$D" sh -c 'echo "<html>stub</html>" > /cosmos/static/index.html'; fi
docker exec "$D" sh -c 'echo "{\"version\":\"test\"}" > /cosmos/meta.json'
docker exec -d "$D" sh -c '/restart-e2e app > /app.log 2>&1'
docker exec -d "$D" sh -c 'cd /cosmos && while [ ! -e /cosmos/stop ]; do echo "=== cosmos starts $(date +%T)" >> /cosmos/log; COSMOS_CONFIG_FOLDER=/cosmos/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053 ./cosmos >> /cosmos/log 2>&1; echo "=== cosmos ended with $? $(date +%T)" >> /cosmos/log; sleep 1; done'
c() { docker exec "$D" wget -q -O /dev/null "$@"; }
for _ in $(seq 1 60); do c http://127.0.0.1:2052/cosmos-ui/ 2>/dev/null && break; sleep 1; done
docker exec -e PASSWORD="$PASSWORD" "$D" sh -c '
  s() { wget -q -O /dev/null -T 20 --header "Content-Type: application/json" --post-data "$1" http://127.0.0.1:2052/cosmos/api/newInstall; }
  s "{\"step\":\"2\",\"mongodbMode\":\"Create\"}"
  s "{\"step\":\"3\",\"httpsCertificateMode\":\"SELFSIGNED\",\"hostname\":\"server.example.org\",\"allowHTTPLocalIPAccess\":true}"
  s "{\"step\":\"4\",\"nickname\":\"admin\",\"password\":\"$PASSWORD\",\"email\":\"admin@example.org\"}"
  s "{\"step\":\"5\"}"; true' # step 5 ends the server: no answer
sleep 3
status=0
docker exec -e PASSWORD="$PASSWORD" ${SCALE:+-e SCALE="$SCALE"} ${ONLY:+-e ONLY="$ONLY"} ${GAPS:+-e GAPS="$GAPS"} "$D" /restart-e2e hammer || status=$?
docker cp "$D:/cosmos/log" "$SCRATCH/cosmos.log" >/dev/null 2>&1 || true
echo "Cosmos log: $SCRATCH/cosmos.log ($(grep -c 'Restarting HTTP Server' "$SCRATCH/cosmos.log" 2>/dev/null || echo 0) restarts asked, $(grep -c '=== cosmos starts' "$SCRATCH/cosmos.log" 2>/dev/null || echo 0) process starts)"
[ "${KEEP:-}" = 1 ] || { cp "$SCRATCH/cosmos.log" "$(dirname "$SCRATCH")/$PREFIX-last.log" 2>/dev/null || true; clean; }
exit $status
