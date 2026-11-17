#!/bin/sh
# Fork addition (plan 49): the browser check of an app's links from home and
# from away (scripts/fork-ui-check-app-links.mjs), on a throwaway instance.
#
#   BIN=<cosmos built with CGO_ENABLED=0> STATIC=<built ./static> sh scripts/fork-ui-check-app-links.sh up
#   sh scripts/fork-ui-check-app-links.sh check      # PHASE=all|seed|home|away|user ENGINES= SIZES=
#   sh scripts/fork-ui-check-app-links.sh clean
#
# Two --internal Docker networks (no way out):
#   <PREFIX>-home  HOME_NET (192.168.77.0/24): the test Cosmos (.10), its own Docker in Docker (.20), the
#                  stand-in proxy (.2) and the browser "at home" (.50)
#   <PREFIX>-inet  INET_NET (203.0.113.0/24): the stand-in proxy (.2) and the browser "away" (.50), which
#                  reaches the panel at http://example.org:18080 through the proxy only (example.org is
#                  that proxy for it); the test Cosmos lists the proxy as trusted, so the visitor it sees
#                  is the browser's own internet address.
# The Docker in Docker runs busybox under the names of real images (jellyfin, radarr, RustFS with its S3
# and console ports, qBittorrent with its peer port, PostgreSQL) so that the server proposes their roles
# from what it knows of those images; nothing is pulled into it. Never the server's Docker for apps.
# PREFIX=cosmos-links SCRATCH=/storage/dev/.cache/cosmos-links/check OUT=$SCRATCH/out
set -eu
here=$(cd "$(dirname "$0")" && pwd)
PREFIX=${PREFIX:-cosmos-links}
SCRATCH=${SCRATCH:-/storage/dev/.cache/cosmos-links/check}
OUT=${OUT:-$SCRATCH/out}
HOME_NET=${HOME_NET:-192.168.77}
INET_NET=${INET_NET:-203.0.113}
PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.1-noble
say() { echo "app-links: $*"; }

case "${1:-}" in
up)
  test -x "${BIN:?BIN=<cosmos binary>}" && test -s "${STATIC:?STATIC=<built static folder>}/index.html"
  mkdir -p "$SCRATCH/server/static" "$SCRATCH/server/config" "$SCRATCH/proxy" "$OUT" "$SCRATCH/deps"
  cp "$BIN" "$SCRATCH/server/cosmos"
  cp -r "$STATIC/." "$SCRATCH/server/static/"
  echo '{"version":"test"}' > "$SCRATCH/server/meta.json"
  cp "$here/fork-fake-proxy.go" "$SCRATCH/proxy/main.go"
  cp "$here/fork-ci-smoke/package.json" "$here/fork-ci-smoke/package-lock.json" "$SCRATCH/deps/"
  docker network create --internal --subnet "$HOME_NET.0/24" "$PREFIX-home" >/dev/null
  docker network create --internal --subnet "$INET_NET.0/24" "$PREFIX-inet" >/dev/null

  # the instance's own Docker, with busybox under the images' names
  docker run -d --name "$PREFIX-dind" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-home" --ip "$HOME_NET.20" docker:29-dind >/dev/null
  for _ in $(seq 1 30); do docker exec "$PREFIX-dind" docker info >/dev/null 2>&1 && break; sleep 1; done
  docker save busybox:1.36 | docker exec -i "$PREFIX-dind" docker load >/dev/null
  for image in jellyfin/jellyfin:10.10 lscr.io/linuxserver/radarr:latest rustfs/rustfs:latest lscr.io/linuxserver/qbittorrent:latest postgres:18; do
    docker exec "$PREFIX-dind" docker tag busybox:1.36 "$image"
  done
  run() { name=$1; image=$2; shift 2; docker exec "$PREFIX-dind" docker run -d --name "$name" "$@" "$image" sleep 86400 >/dev/null; }
  run jellyfin jellyfin/jellyfin:10.10
  run radarr lscr.io/linuxserver/radarr:latest
  run rustfs rustfs/rustfs:latest -p 9000:9000 -p 9001:9001
  run qbittorrent lscr.io/linuxserver/qbittorrent:latest -p 8080:8080 -p 6881:6881 -p 6881:6881/udp
  run postgres postgres:18 -p 5432:5432

  # the test Cosmos (restarted by its loop when a save ends the process)
  docker run -d --name "$PREFIX-server" --hostname cosmos-links-server --network "$PREFIX-home" --ip "$HOME_NET.10" \
    -v "$SCRATCH/server:/cosmos" -w /cosmos -e COSMOS_CONFIG_FOLDER=/cosmos/config/ -e COSMOS_HTTP_PORT=18080 -e COSMOS_HTTPS_PORT=18443 \
    -e DOCKER_HOST="tcp://$HOME_NET.20:2375" debian:13 sh -c 'while :; do ./cosmos; sleep 1; done' >/dev/null

  # the public address: the stand-in proxy on both networks
  docker run -d --name "$PREFIX-proxy" --network "$PREFIX-home" --ip "$HOME_NET.2" -v "$SCRATCH/proxy:/p:ro" -w /p \
    -v /storage/dev/.cache/go-build:/root/.cache/go-build golang:1.27 go run main.go :18080 "http://$HOME_NET.10:18080" >/dev/null
  docker network connect --ip "$INET_NET.2" "$PREFIX-inet" "$PREFIX-proxy"

  docker run --rm --name "$PREFIX-npm" -v "$SCRATCH/deps:/deps" -w /deps -v /storage/dev/.cache/npm:/root/.npm "$PLAYWRIGHT_IMAGE" \
    npm ci --ignore-scripts --no-audit --no-fund --loglevel=error >/dev/null
  say "up: Cosmos at http://$HOME_NET.10:18080 (home), http://example.org:18080 through the proxy (away)"
  ;;
check)
  PHASE=${PHASE:-all}
  browse() { # <network> <ip> <phase> <base>
    docker run --rm --name "$PREFIX-browser-$3" --network "$PREFIX-$1" --ip "$2" --ipc=host \
      --add-host "example.org:$( [ "$1" = inet ] && echo "$INET_NET.2" || echo "$HOME_NET.2" )" \
      -e PHASE="$3" -e BASE="$4" -e PROXY_IP="$HOME_NET.2" -e OUT=/out -e ENGINES="${ENGINES:-chromium,firefox,webkit}" -e SIZES="${SIZES:-d,m}" \
      -v "$here:/check/scripts:ro" -v "$SCRATCH/deps/node_modules:/check/node_modules:ro" -v "$OUT:/out" -w /check \
      "$PLAYWRIGHT_IMAGE" node scripts/fork-ui-check-app-links.mjs
  }
  status=0
  case "$PHASE" in all|seed) browse home "$HOME_NET.50" seed "http://example.org:18080" || status=1 ;; esac
  case "$PHASE" in all|home) browse home "$HOME_NET.50" home "http://$HOME_NET.10:18080" || status=1 ;; esac
  case "$PHASE" in all|away) browse inet "$INET_NET.50" away "http://example.org:18080" || status=1 ;; esac
  case "$PHASE" in all|user) browse inet "$INET_NET.50" user "http://example.org:18080" || status=1 ;; esac
  case "$PHASE" in all|role) browse inet "$INET_NET.50" role "http://example.org:18080" || status=1 ;; esac
  exit $status
  ;;
clean)
  for c in server proxy dind; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
  docker network rm "$PREFIX-home" "$PREFIX-inet" >/dev/null 2>&1 || true
  rm -rf "$SCRATCH"
  say "cleaned"
  ;;
*) echo "usage: $0 up|check|clean" >&2; exit 2 ;;
esac
