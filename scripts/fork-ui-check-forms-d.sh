#!/bin/sh
# Fork addition (plan 47, I2d/I2e): the setup of scripts/fork-ui-check-forms-d.mjs, the browser
# check of the Telegram chat choice, the guided scheduled job, the alerts' server check, the
# monitoring database proposal, the snapshot excludes and the wizard without the cluster step.
# A throwaway Cosmos with a Docker of its own and a Telegram stand-in on an --internal network:
# never the production instance, never the real Telegram.
#
#   sh scripts/fork-ui-check-forms-d.sh up        build, load the images (once), start; the wizard is left open
#   sh scripts/fork-ui-check-forms-d.sh setup     finish the wizard over the API (after PHASE=wizard)
#   sh scripts/fork-ui-check-forms-d.sh check [VAR=value…]   run the check (PHASE, ENGINES, SIZES)
#   sh scripts/fork-ui-check-forms-d.sh rebuild   build the server and the panel again, restart Cosmos
#   sh scripts/fork-ui-check-forms-d.sh shell <cmd…>   run a command in the test Docker (the "server")
#   sh scripts/fork-ui-check-forms-d.sh clean     remove every container and network it made, and the scratch folder
#
# What `up` makes, on one --internal network SUBNET.0/24 (192.0.2.0/24 by default, a documentation range):
#   .2  PREFIX-telegram  scripts/fork-fake-telegram.go with the alias api.telegram.org (its CA in SCRATCH/shared)
#   .5  PREFIX-dind      docker:29-dind, privileged; the test Cosmos runs INSIDE it (the server's own path to
#                        containers, as on a bare-metal install), HTTP 2052, HTTPS off, hostname
#                        forms-d.example.org, SSL_CERT_FILE the stand-in's CA, no licence; a loop file of the
#                        scratch folder is a data disk at /mnt/data. In its Docker: "maindb" (postgres:18,
#                        POSTGRES_USER=main, POSTGRES_PASSWORD=Main-test-pw-0420, POSTGRES_DB=maindb,
#                        data bound from /mnt/data/maindb, 5432 published on 127.0.0.1). Prepared folder
#                        /srv/appdata (cache, .thumbnails, tmp, *.part, photos) for the snapshot excludes.
#   .9  PREFIX-browser   the Playwright image, run per check (scripts/fork-ci-smoke's pinned playwright)
# The images (postgres:18) are copied once by PREFIX-loader, a Docker in Docker on a network of its
# own with a way out and mirror.gcr.io as its mirror, through a tar file in the scratch folder.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
PREFIX=${PREFIX:-cosmos-formsd}
SCRATCH=${SCRATCH:-/storage/dev/.cache/cosmos-formsd/ui}
NET=$PREFIX-net
SUBNET=${SUBNET:-192.0.2}
HOST=forms-d.example.org
PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.1-noble
GO_IMAGE=golang:1.27
NODE_IMAGE=node:22
DIND_IMAGE=docker:29-dind
IMAGES=${IMAGES:-"postgres:18"}

say() { echo "forms-d: $*"; }
die() { say "$*"; exit 2; }
in_dind() { docker exec "$PREFIX-dind" "$@"; }

build() {
  mkdir -p "$SCRATCH/run" "$SCRATCH/bin"
  say "building the server and the Telegram stand-in"
  docker run --rm --name "$PREFIX-go" -v "$repo:/src:ro" -v "$SCRATCH:/out" -w /src \
    -v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
    -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c '
      go build -o /out/bin/cosmos.new src/*.go &&
      go build -o /out/bin/fake-telegram scripts/fork-fake-telegram.go'
  say "building the panel in a hard-linked copy of the worktree"
  rm -rf "$SCRATCH/src"
  mkdir -p "$SCRATCH/src"
  (cd "$repo" && for f in * .[!.]*; do [ -e "$f" ] || continue; [ "$f" = .git ] && continue; cp -al "$f" "$SCRATCH/src/"; done)
  # never write through a hard link into the worktree: the build's outputs and caches are removed first
  rm -rf "$SCRATCH/src/static" "$SCRATCH/src/stats.html" "$SCRATCH/src/node_modules/.vite" "$SCRATCH/src/client/node_modules" "$SCRATCH/src/build"
  docker run --rm --name "$PREFIX-node" -v "$SCRATCH/src:/src" -w /src -v /storage/dev/.cache/npm:/root/.npm "$NODE_IMAGE" \
    sh -c 'npx vite build --base=/cosmos-ui/ > /tmp/vite.log 2>&1 || { tail -40 /tmp/vite.log; exit 1; }; tail -3 /tmp/vite.log'
  rm -rf "$SCRATCH/run/static"
  cp -r "$SCRATCH/src/static" "$SCRATCH/run/static"
  version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$repo/package.json" | head -1)
  printf '{\n  "version": "%s",\n  "buildDate": "%s",\n  "built from": "forms-d"\n}\n' "$version" "$(date)" > "$SCRATCH/run/meta.json"
  mv "$SCRATCH/bin/cosmos.new" "$SCRATCH/run/cosmos"
  rm -rf "$SCRATCH/src"
}

load() { # copies IMAGES into a tar once, through a Docker in Docker with a way out
  [ -s "$SCRATCH/images.tar" ] && return 0
  say "copying $IMAGES"
  docker network create "$PREFIX-load" >/dev/null
  docker run -d --name "$PREFIX-loader" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-load" \
    -v "$SCRATCH:/out" "$DIND_IMAGE" --registry-mirror=https://mirror.gcr.io >/dev/null
  for _ in $(seq 1 60); do docker exec "$PREFIX-loader" docker info >/dev/null 2>&1 && break; sleep 1; done
  for image in $IMAGES; do
    docker exec "$PREFIX-loader" docker pull -q --platform linux/amd64 "$image" >/dev/null || die "could not pull $image"
  done
  # shellcheck disable=SC2086
  docker exec "$PREFIX-loader" docker save -o /out/images.tar $IMAGES || die "could not save the images"
  docker rm -f -v "$PREFIX-loader" >/dev/null
  docker network rm "$PREFIX-load" >/dev/null
}

playwright() {
  [ -d "$SCRATCH/pw/node_modules/playwright" ] && return 0
  mkdir -p "$SCRATCH/pw"
  cp "$here/fork-ci-smoke/package.json" "$here/fork-ci-smoke/package-lock.json" "$SCRATCH/pw/"
  docker run --rm --name "$PREFIX-npm" -v /storage/dev/.cache/npm:/root/.npm -v "$SCRATCH/pw:/work" -w /work "$PLAYWRIGHT_IMAGE" \
    npm ci --ignore-scripts --no-audit --no-fund --loglevel=error
}

copy_scripts() {
  mkdir -p "$SCRATCH/pw/shots"
  cp "$here/fork-ui-check-forms-d.mjs" "$SCRATCH/pw/"
}

browser() { # [VAR=value…] -- <script>
  envs=""
  while [ $# -gt 1 ] && [ "$1" != "--" ]; do envs="$envs -e $1"; shift; done
  [ "${1:-}" = "--" ] && shift
  docker rm -f "$PREFIX-browser" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker run --rm --name "$PREFIX-browser" --network "$NET" --ip "$SUBNET.9" --ipc=host \
    --add-host "$HOST:$SUBNET.5" -v "$SCRATCH/pw:/work" -w /work \
    -e BASE="http://$HOST:2052" -e FAKE="http://$SUBNET.2:8080" -e DIND="http://$SUBNET.5:2375" $envs "$PLAYWRIGHT_IMAGE" node "$@"
}

start_cosmos() {
  cat > "$SCRATCH/run/start.sh" <<EOF
#!/bin/sh
# the test Cosmos, started again whenever it ends (finishing the wizard and some saves end it on purpose)
cd /cosmos
export COSMOS_CONFIG_FOLDER=/cosmos/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053 SSL_CERT_FILE=/shared/ca.pem HOSTNAME=forms-d
while [ ! -e /cosmos/stop ]; do ./cosmos >> /cosmos/cosmos.log 2>&1; sleep 1; done
EOF
  rm -f "$SCRATCH/run/stop"
  mkdir -p "$SCRATCH/run/config"
  docker exec -d "$PREFIX-dind" sh /cosmos/start.sh
}

stop_cosmos() {
  touch "$SCRATCH/run/stop" 2>/dev/null || true
  docker exec "$PREFIX-dind" sh -c 'pkill -f "^./cosmos" ; sleep 2; pkill -9 -f "^./cosmos"' 2>/dev/null || true
  for _ in $(seq 1 10); do docker exec "$PREFIX-dind" pgrep -f "sh /cosmos/start.sh" >/dev/null 2>&1 || break; sleep 1; done
}

wait_up() {
  for _ in $(seq 1 60); do
    browser -- -e "fetch('http://$SUBNET.5:2052/cosmos/api/status',{signal:AbortSignal.timeout(4000)}).then((r)=>process.exit(r.status<500?0:1),()=>process.exit(1))" >/dev/null 2>&1 && return 0
    sleep 2
  done
  die "the test Cosmos does not answer (log $SCRATCH/run/cosmos.log)"
}

start_all() {
  docker network create --internal --subnet "$SUBNET.0/24" "$NET" >/dev/null
  mkdir -p "$SCRATCH/shared"
  docker run -d --name "$PREFIX-telegram" --network "$NET" --ip "$SUBNET.2" --network-alias api.telegram.org \
    -v "$SCRATCH/bin:/bin-test:ro" -v "$SCRATCH/shared:/shared" "$GO_IMAGE" /bin-test/fake-telegram >/dev/null
  for _ in $(seq 1 20); do [ -s "$SCRATCH/shared/ca.pem" ] && break; sleep 1; done
  if [ ! -e "$SCRATCH/data.img.done" ]; then
    rm -f "$SCRATCH/data.img"; truncate -s 2G "$SCRATCH/data.img"
    docker run --rm --name "$PREFIX-mkfs" -v "$SCRATCH:/s" "$NODE_IMAGE" mkfs.ext4 -q /s/data.img || die "could not format the data disk"
    touch "$SCRATCH/data.img.done"
  fi
  mkdir -p "$SCRATCH/dind-docker" "$SCRATCH/run"
  docker run -d --name "$PREFIX-dind" --privileged -e DOCKER_TLS_CERTDIR= --network "$NET" --ip "$SUBNET.5" \
    --hostname forms-d -v "$SCRATCH/shared:/shared:ro" -v "$SCRATCH/run:/cosmos" -v "$SCRATCH/dind-docker:/var/lib/docker" \
    -v "$SCRATCH/data.img:/data.img" -v "$SCRATCH/images.tar:/images.tar:ro" \
    --entrypoint sh "$DIND_IMAGE" -c 'mkdir -p /mnt/data; mount -o loop /data.img /mnt/data || exit 1; exec dockerd-entrypoint.sh' >/dev/null
  for _ in $(seq 1 60); do in_dind docker info >/dev/null 2>&1 && break; sleep 1; done
  in_dind docker info >/dev/null 2>&1 || die "the test Docker did not start"
  in_dind docker image inspect postgres:18 >/dev/null 2>&1 || in_dind docker load -q -i /images.tar >/dev/null
  if ! in_dind docker inspect maindb >/dev/null 2>&1; then
    in_dind docker run -d --name maindb --restart unless-stopped -e POSTGRES_USER=main -e POSTGRES_PASSWORD=Main-test-pw-0420 \
      -e POSTGRES_DB=maindb -p 127.0.0.1:5432:5432 -v /mnt/data/maindb:/var/lib/postgresql postgres:18 >/dev/null || die "could not make maindb"
  else
    in_dind docker start maindb >/dev/null 2>&1 || true
  fi
  # the folder whose excludes are offered: what a media app leaves next to its data
  in_dind sh -c 'mkdir -p /srv/appdata/photos/2025 /srv/appdata/cache/images /srv/appdata/.thumbnails/large /srv/appdata/tmp /srv/appdata/jellyfin/transcodes &&
    head -c 3000000 /dev/zero > /srv/appdata/cache/images/blob.bin && head -c 200000 /dev/zero > /srv/appdata/.thumbnails/large/a.png &&
    head -c 1000 /dev/zero > /srv/appdata/tmp/upload.tmp && head -c 5000 /dev/zero > /srv/appdata/photos/2025/beach.jpg &&
    head -c 7000 /dev/zero > /srv/appdata/movie.part && head -c 9000 /dev/zero > /srv/appdata/jellyfin/transcodes/seg.ts &&
    ln -sfn / /srv/appdata/rootlink'
  start_cosmos
  wait_up
  say "up: http://$HOST:2052 (log $SCRATCH/run/cosmos.log); the wizard is open: PHASE=wizard, then setup"
}

setup() { # the wizard over the API, as CLAUDE.md says; step 5 ends the process, the loop starts it again
  copy_scripts
  browser PHASE=setup -- fork-ui-check-forms-d.mjs
  sleep 6
  wait_up
}

stop_all() {
  [ "$(docker ps -aq -f name="^$PREFIX-dind$")" ] && stop_cosmos
  for c in browser dind telegram loader mkfs go node npm; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
  for n in "$NET" "$PREFIX-load"; do docker network rm "$n" >/dev/null 2>&1 || true; done
}

case "${1:-}" in
  up)
    docker network inspect "$NET" >/dev/null 2>&1 && die "$NET exists: run clean first"
    { [ -x "$SCRATCH/run/cosmos" ] && [ "${REBUILD:-1}" = 0 ]; } || build
    load
    playwright
    start_all
    copy_scripts ;;
  setup) setup ;;
  rebuild) build; stop_cosmos; start_cosmos; wait_up; say "rebuilt and restarted" ;;
  restart) stop_cosmos; start_cosmos; wait_up ;;
  check) shift; copy_scripts; browser "$@" -- fork-ui-check-forms-d.mjs ;;
  node) shift; script=$1; shift; copy_scripts; cp "$here/$script" "$SCRATCH/pw/"; browser "$@" -- "$script" ;;
  shell) shift; in_dind "$@" ;;
  stop) stop_all ;;
  clean)
    stop_all
    # the test Docker's files are root's: removed from a container
    [ -d "$SCRATCH" ] && docker run --rm -v "$(dirname "$SCRATCH"):/p" "$DIND_IMAGE" rm -rf "/p/$(basename "$SCRATCH")"
    say "clean" ;;
  *) die "usage: $0 up|setup|check|rebuild|restart|node|shell|stop|clean" ;;
esac
