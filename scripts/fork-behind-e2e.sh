#!/bin/sh
# Fork addition (plan 44): apps behind the proxy, end to end, with the real
# images. Sets up a docker:29-dind of its own (its images and data under
# SCRATCH), runs the test Cosmos inside it outside Docker (as on a bare-metal
# server) on HTTPS 2053 with a self-signed certificate, finishes its setup
# wizard, and runs scripts/fork-behind-e2e-inner.sh there (what it proves is
# in its header). The apps run on an --internal network inside the Docker in
# Docker; the Docker in Docker itself needs a way out to pull the images
# (REGISTRY_MIRROR, default mirror.gcr.io). Never the owner's server or Docker.
#
#   sh scripts/fork-behind-e2e.sh up        set up, run, keep (KEEP=1 implied)
#   sh scripts/fork-behind-e2e.sh           set up, run, remove everything
#   sh scripts/fork-behind-e2e.sh clean     remove what a kept run left
# Options: BIN (the cosmos binary, built with CGO_ENABLED=0), STATIC (a built
# ./static, optional: a stub page otherwise), PREFIX=fork-behind,
# SCRATCH=/storage/dev/.cache/$PREFIX, APPS (see the inner script), PASSWORD.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
PREFIX=${PREFIX:-fork-behind}
SCRATCH=${SCRATCH:-/storage/dev/.cache/$PREFIX}
BIN=${BIN:-$here/../build/cosmos}
STATIC=${STATIC:-}
PASSWORD=${PASSWORD:-Test-Passw0rd!x}
MIRROR=${REGISTRY_MIRROR:-https://mirror.gcr.io}
D="$PREFIX-dind"

clean() {
  docker rm -f -v "$D" >/dev/null 2>&1 || true
  docker network rm "$PREFIX-out" >/dev/null 2>&1 || true
  # the Docker in Docker's own files are root's: removed from a container
  [ -d "$SCRATCH" ] && docker run --rm -v "$(dirname "$SCRATCH"):/w" busybox:1.36 rm -rf "/w/$(basename "$SCRATCH")" || true
}
case "${1:-}" in clean) clean; exit 0;; up) KEEP=1;; esac
test -x "$BIN" || { echo "no cosmos binary at $BIN (BIN=...)"; exit 2; }

mkdir -p "$SCRATCH/dind"
docker network create "$PREFIX-out" >/dev/null 2>&1 || true
docker run -d --name "$D" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-out" \
  -v "$SCRATCH/dind:/var/lib/docker" docker:29-dind --registry-mirror="$MIRROR" >/dev/null
for _ in $(seq 1 60); do docker exec "$D" docker info >/dev/null 2>&1 && break; sleep 1; done
docker exec "$D" sh -c 'apk add -q curl jq >/dev/null; mkdir -p /cosmos/config /cosmos/static; docker network create --internal --subnet 172.30.0.0/16 behind >/dev/null'
docker cp "$BIN" "$D:/cosmos/cosmos"
if [ -n "$STATIC" ]; then docker cp "$STATIC/." "$D:/cosmos/static/"; else docker exec "$D" sh -c 'echo "<html>stub</html>" > /cosmos/static/index.html'; fi
docker exec "$D" sh -c 'echo "{\"version\":\"test\"}" > /cosmos/meta.json'
docker exec -d "$D" sh -c 'cd /cosmos && while [ ! -e /cosmos/stop ]; do COSMOS_CONFIG_FOLDER=/cosmos/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053 ./cosmos >> /cosmos/log 2>&1; sleep 1; done'
for _ in $(seq 1 60); do [ "$(docker exec "$D" curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:2052/cosmos-ui/)" = 200 ] && break; sleep 1; done
docker exec -e PASSWORD="$PASSWORD" "$D" sh -c '
  s() { curl -s -m 20 -X POST -H "Content-Type: application/json" -d "$1" http://127.0.0.1:2052/cosmos/api/newInstall >/dev/null; }
  s "{\"step\":\"2\",\"mongodbMode\":\"Create\"}"
  s "{\"step\":\"3\",\"httpsCertificateMode\":\"SELFSIGNED\",\"hostname\":\"server.example.org\",\"allowHTTPLocalIPAccess\":true}"
  s "{\"step\":\"4\",\"nickname\":\"admin\",\"password\":\"$PASSWORD\",\"email\":\"admin@example.org\"}"
  s "{\"step\":\"5\"}"'
for _ in $(seq 1 60); do [ "$(docker exec "$D" curl -sk -o /dev/null -w '%{http_code}' --resolve server.example.org:2053:127.0.0.1 https://server.example.org:2053/cosmos-ui/)" = 200 ] && break; sleep 1; done
docker cp "$here/fork-behind-e2e-inner.sh" "$D:/e2e.sh"
status=0
docker exec -e PASSWORD="$PASSWORD" ${APPS:+-e APPS="$APPS"} "$D" sh /e2e.sh || status=$?
[ "${KEEP:-}" = 1 ] || clean
exit $status
