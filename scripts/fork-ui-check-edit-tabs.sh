#!/bin/sh
# Fork addition (plan 47, I2c): the setup of scripts/fork-ui-check-edit-tabs.mjs, the browser check
# of a container's edit tabs (Docker, Network, Storage) and their proposals. A throwaway Cosmos with
# a Docker of its own, on --internal networks; never the production instance, nothing pulled from the
# internet (the test images are built from busybox:1.36, copied once from the host's Docker).
#
#   sh scripts/fork-ui-check-edit-tabs.sh up        build, start, set up
#   sh scripts/fork-ui-check-edit-tabs.sh build | start | rebuild | restart
#   sh scripts/fork-ui-check-edit-tabs.sh check [VAR=value…]   the check (ENGINES, SIZES, MODES)
#   sh scripts/fork-ui-check-edit-tabs.sh node <script.mjs> [VAR=value…]   another check script, same browser container
#   sh scripts/fork-ui-check-edit-tabs.sh clean     remove every container and network it made, and the scratch folder
#
# What `up` makes:
#   PREFIX-net  --internal, SUBNET.0/24 (198.51.100 by default: a documentation range)
#   PREFIX-lan  --internal, LAN.0/24 (192.168.78 by default: the server's home network)
#   .5 / LAN.5  PREFIX-dind: docker:29-dind on both, with a data disk of its own (a loop file of the
#               scratch folder, ext4, at /mnt/data), a default route to LAN.254 (the "router", an address nothing holds) and
#               LAN.53 as its resolver, and a stand-in render node /dev/dri/renderD129 made with
#               mknod inside its own /dev (a copy of /dev/null: no real device is given to anything).
#               The test Cosmos runs INSIDE it (the server's own network and folders), built with
#               -tags anyimage_testnet, HTTP 2052, HTTPS 2053 self-signed; its log is SCRATCH/run/cosmos.log.
#               Images inside it: busybox:1.36, nginx:1 (busybox serving port 80, for
#               fork-ui-check-sensitive-edit.mjs), lscr.io/linuxserver/fakeapp:1 (VOLUME /config,
#               EXPOSE 8080) and example/plainapp:1 (USER 1000:1000, VOLUME /data, EXPOSE 9000).
#   .9          PREFIX-browser: the Playwright image, run per check
# The containers of the check are made by the check itself.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
PREFIX=${PREFIX:-cosmos-formsc}
SCRATCH=${SCRATCH:-/storage/dev/.cache/cosmos-formsc/ui}
NET=$PREFIX-net
LANNET=$PREFIX-lan
SUBNET=${SUBNET:-198.51.100}
LAN=${LAN:-192.168.78}
PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.1-noble
GO_IMAGE=golang:1.27
NODE_IMAGE=node:22
DIND_IMAGE=docker:29-dind

say() { echo "edit-tabs: $*"; }
die() { say "$*"; exit 2; }
in_dind() { docker exec "$PREFIX-dind" "$@"; }

build() {
  mkdir -p "$SCRATCH/run" "$SCRATCH/bin"
  say "building the server (anyimage_testnet)"
  docker run --rm --name "$PREFIX-go" -v "$repo:/src:ro" -v "$SCRATCH:/out" -w /src \
    -v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
    -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c 'go build -tags anyimage_testnet -o /out/bin/cosmos.new src/*.go'
  say "building the panel in a hard-linked copy of the worktree"
  rm -rf "$SCRATCH/src"
  mkdir -p "$SCRATCH/src"
  (cd "$repo" && for f in * .[!.]*; do [ -e "$f" ] || continue; [ "$f" = .git ] && continue; cp -al "$f" "$SCRATCH/src/"; done)
  # never write through a hard link into the worktree: the build's outputs and caches are removed first
  rm -rf "$SCRATCH/src/static" "$SCRATCH/src/stats.html" "$SCRATCH/src/node_modules/.vite" "$SCRATCH/src/client/node_modules" "$SCRATCH/src/build"
  docker run --rm --name "$PREFIX-node" -v "$SCRATCH/src:/src" -w /src -v /storage/dev/.cache/npm:/root/.npm "$NODE_IMAGE" \
    sh -c 'npx vite build --base=/cosmos-ui/ > /tmp/vite.log 2>&1 || { tail -40 /tmp/vite.log; exit 1; }; tail -2 /tmp/vite.log'
  rm -rf "$SCRATCH/run/static"
  cp -r "$SCRATCH/src/static" "$SCRATCH/run/static"
  version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$repo/package.json" | head -1)
  printf '{\n  "version": "%s",\n  "buildDate": "%s",\n  "built from": "edit-tabs"\n}\n' "$version" "$(date)" > "$SCRATCH/run/meta.json"
  mv "$SCRATCH/bin/cosmos.new" "$SCRATCH/run/cosmos"
  rm -rf "$SCRATCH/src"
}

start_cosmos() {
  cat > "$SCRATCH/run/start.sh" <<EOF
#!/bin/sh
# the test Cosmos, started again whenever it ends (finishing the wizard and some saves end it on purpose)
cd /cosmos
export COSMOS_CONFIG_FOLDER=/cosmos/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053 \
  COSMOS_ANYIMAGE_TEST_ALLOW=$SUBNET.0/24 HOSTNAME=cosmos-test
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

browser() { # [VAR=value…] -- <script>
  envs=""
  while [ $# -gt 1 ] && [ "$1" != "--" ]; do envs="$envs -e $1"; shift; done
  [ "${1:-}" = "--" ] && shift
  docker rm -f "$PREFIX-browser" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker run --rm --name "$PREFIX-browser" --network "$NET" --ip "$SUBNET.9" --ipc=host \
    -v "$SCRATCH/pw:/work" -w /work -e SERVER_IP="$SUBNET.5" -e LAN_IP="$LAN.5" $envs "$PLAYWRIGHT_IMAGE" node "$@"
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
  cp "$here"/fork-ui-check-*.mjs "$here"/fork-ui-gallery*.mjs "$SCRATCH/pw/"
}

images() {
  # busybox:1.36 from the host's Docker once (or, when the test Cosmos' clean-up removed it, from the
  # busybox-based nginx:1 stand-in still in use); a stopped container per image keeps them from the clean-up
  if ! in_dind docker image inspect busybox:1.36 >/dev/null 2>&1; then
    in_dind docker tag nginx:1 busybox:1.36 2>/dev/null || docker save busybox:1.36 | docker exec -i "$PREFIX-dind" docker load -q >/dev/null
  fi
  in_dind sh -c '
    set -e
    mkdir -p /tmp/img/nginx /tmp/img/ls /tmp/img/plain
    printf "%s\n" "FROM busybox:1.36" "EXPOSE 80" "CMD [\"httpd\", \"-f\", \"-p\", \"80\", \"-h\", \"/etc\"]" > /tmp/img/nginx/Dockerfile
    printf "%s\n" "FROM busybox:1.36" "VOLUME /config" "EXPOSE 8080" "CMD [\"sh\", \"-c\", \"mkdir -p /www && echo fakeapp > /www/index.html && exec httpd -f -p 8080 -h /www\"]" > /tmp/img/ls/Dockerfile
    printf "%s\n" "FROM busybox:1.36" "USER 1000:1000" "VOLUME /data" "EXPOSE 9000" "CMD [\"httpd\", \"-f\", \"-p\", \"9000\", \"-h\", \"/etc\"]" > /tmp/img/plain/Dockerfile
    docker build -q -t nginx:1 /tmp/img/nginx >/dev/null
    docker build -q -t lscr.io/linuxserver/fakeapp:1 /tmp/img/ls >/dev/null
    docker build -q -t example/plainapp:1 /tmp/img/plain >/dev/null
    for i in busybox:1.36 nginx:1 lscr.io/linuxserver/fakeapp:1 example/plainapp:1; do
      n=keep-$(echo "$i" | tr "/:." "---"); docker inspect "$n" >/dev/null 2>&1 || docker create --name "$n" "$i" true >/dev/null
    done'
}

start_all() {
  docker network inspect "$NET" >/dev/null 2>&1 && die "$NET exists: run clean first"
  docker network create --internal --subnet "$SUBNET.0/24" "$NET" >/dev/null
  docker network create --internal --subnet "$LAN.0/24" "$LANNET" >/dev/null
  if [ ! -e "$SCRATCH/data.img.done" ]; then
    rm -f "$SCRATCH/data.img"; truncate -s 2G "$SCRATCH/data.img"
    docker run --rm --name "$PREFIX-mkfs" -v "$SCRATCH:/s" "$NODE_IMAGE" mkfs.ext4 -q /s/data.img || die "could not format the data disk"
    touch "$SCRATCH/data.img.done"
  fi
  mkdir -p "$SCRATCH/docker" "$SCRATCH/run"
  docker create --name "$PREFIX-dind" --privileged -e DOCKER_TLS_CERTDIR= --network "$NET" --ip "$SUBNET.5" \
    --hostname cosmos-test -v "$SCRATCH/run:/cosmos" -v "$SCRATCH/docker:/var/lib/docker" -v "$SCRATCH/data.img:/data.img" \
    --entrypoint sh "$DIND_IMAGE" -c "mkdir -p /mnt/data && mount -o loop /data.img /mnt/data || exit 1
      mkdir -p /dev/dri && [ -e /dev/dri/renderD129 ] || mknod /dev/dri/renderD129 c 1 3
      printf 'nameserver $LAN.53\n' > /etc/resolv.conf
      (sleep 3; ip route replace default via $LAN.254) &
      exec dockerd-entrypoint.sh" >/dev/null
  docker network connect --ip "$LAN.5" "$LANNET" "$PREFIX-dind"
  docker start "$PREFIX-dind" >/dev/null
  for _ in $(seq 1 60); do in_dind docker info >/dev/null 2>&1 && break; sleep 1; done
  in_dind docker info >/dev/null 2>&1 || die "the test Docker did not start"
  images
  start_cosmos
  for _ in $(seq 1 60); do
    browser -- -e "fetch('http://$SUBNET.5:2052/cosmos/api/status',{signal:AbortSignal.timeout(4000)}).then((r)=>process.exit(r.status<500?0:1),()=>process.exit(1))" >/dev/null 2>&1 && break
    sleep 2
  done
  say "up: https://$SUBNET.5:2053 (log $SCRATCH/run/cosmos.log)"
}

stop_all() {
  [ "$(docker ps -aq -f name="^$PREFIX-dind$")" ] && stop_cosmos
  for c in browser dind mkfs go node npm; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
  for n in "$NET" "$LANNET"; do docker network rm "$n" >/dev/null 2>&1 || true; done
}

case "${1:-}" in
  up) [ -x "$SCRATCH/run/cosmos" ] && [ "${REBUILD:-1}" = 0 ] || build; playwright; start_all; copy_scripts ;;
  build) build ;;
  start) start_all; copy_scripts ;;
  rebuild) build; stop_cosmos; start_cosmos; say "rebuilt and restarted" ;;
  restart) stop_cosmos; start_cosmos ;;
  # Cosmos' own image clean-up removes the images no container uses: they are put back before each run
  check) shift; images; copy_scripts; browser "$@" -- fork-ui-check-edit-tabs.mjs ;;
  node) shift; script=$1; shift; images; copy_scripts; browser "$@" -- "$script" ;;
  stop) stop_all ;;
  clean)
    stop_all
    # the test Docker's files are root's: removed from a container
    [ -d "$SCRATCH" ] && docker run --rm --entrypoint rm -v "$(dirname "$SCRATCH"):/p" "$DIND_IMAGE" -rf "/p/$(basename "$SCRATCH")"
    say "clean" ;;
  *) die "usage: $0 up|build|start|rebuild|restart|check|node|stop|clean" ;;
esac
