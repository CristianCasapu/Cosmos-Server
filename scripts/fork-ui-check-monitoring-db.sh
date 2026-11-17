#!/bin/sh
# Fork addition (plan 51): the setup of scripts/fork-ui-check-monitoring-db.mjs, the browser replay
# of the owner's attempts at a PostgreSQL monitoring database (the masked password, localhost:5432,
# a .local guess, the proposal's chip, the save, the tables, the database stopped and started again,
# a Cosmos start with the database down). A throwaway Cosmos with a Docker of its own on an
# --internal network: never the production instance.
#
#   sh scripts/fork-ui-check-monitoring-db.sh up        build, load the images (once), start, finish the wizard
#   sh scripts/fork-ui-check-monitoring-db.sh check [VAR=value…]   run the check (PHASE, ENGINES, SIZES)
#   sh scripts/fork-ui-check-monitoring-db.sh rebuild   build the server and the panel again, restart Cosmos
#   sh scripts/fork-ui-check-monitoring-db.sh restart   restart Cosmos only (the loop starts it again)
#   sh scripts/fork-ui-check-monitoring-db.sh reset     back to the local file and the databases as at `up`
#   sh scripts/fork-ui-check-monitoring-db.sh shell <cmd…>   run a command in the test Docker (the "server")
#   sh scripts/fork-ui-check-monitoring-db.sh clean     remove every container and network it made, and the scratch folder
#
# What `up` makes, on one --internal network SUBNET.0/24 (192.0.2.0/24 by default, a documentation range):
#   .5  PREFIX-dind   docker:29-dind, privileged; the test Cosmos runs INSIDE it, outside its Docker: the
#                     bare-metal shape (HTTP 2052, HTTPS off, hostname monitoring-db.example.org). SUBNET.5
#                     is the "server's home-network address". In its Docker, on the default bridge:
#                     "postgresql"  bitnami/postgresql, the owner's shape: POSTGRESQL_USERNAME=cosmos,
#                                   POSTGRESQL_PASSWORD=Cosmos-test-pw-51, POSTGRESQL_DATABASE=cosmos_monitoring,
#                                   port 5432 published on SUBNET.5 ONLY (not on 127.0.0.1); a role
#                                   "reader" (reader-test-pw) that may sign in but not create tables
#                     "pgofficial"  postgres:18 with the official names (POSTGRES_USER=app,
#                                   POSTGRES_PASSWORD=App-test-pw-51, POSTGRES_DB=appdb), port 5432 not published
#   .9  PREFIX-browser the Playwright image, run per check
# The images come from IMAGES_DIR (bitnami.tar, pg18.tar: `docker save` of bitnami/postgresql:latest and
# postgres:18), loaded into the test Docker.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
PREFIX=${PREFIX:-cosmos-pgfix}
SCRATCH=${SCRATCH:-/storage/dev/.cache/cosmos-pgfix/mondb}
IMAGES_DIR=${IMAGES_DIR:-/storage/dev/.cache/cosmos-pgfix/ui}
NET=$PREFIX-net
SUBNET=${SUBNET:-192.0.2}
HOST=monitoring-db.example.org
PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.1-noble
GO_IMAGE=golang:1.27
NODE_IMAGE=node:22
DIND_IMAGE=docker:29-dind

say() { echo "monitoring-db: $*"; }
die() { say "$*"; exit 2; }
in_dind() { docker exec "$PREFIX-dind" "$@"; }

build() {
  mkdir -p "$SCRATCH/run" "$SCRATCH/bin"
  say "building the server"
  docker run --rm --name "$PREFIX-go" -v "$repo:/src:ro" -v "$SCRATCH:/out" -w /src \
    -v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
    -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c 'go build -o /out/bin/cosmos.new src/*.go'
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
  printf '{\n  "version": "%s",\n  "buildDate": "%s",\n  "built from": "monitoring-db"\n}\n' "$version" "$(date)" > "$SCRATCH/run/meta.json"
  mv "$SCRATCH/bin/cosmos.new" "$SCRATCH/run/cosmos"
  rm -rf "$SCRATCH/src"
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
  cp "$here/fork-ui-check-monitoring-db.mjs" "$SCRATCH/pw/"
}

browser() { # [VAR=value…] -- <script>
  envs=""
  while [ $# -gt 1 ] && [ "$1" != "--" ]; do envs="$envs -e $1"; shift; done
  [ "${1:-}" = "--" ] && shift
  docker rm -f "$PREFIX-browser" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker run --rm --name "$PREFIX-browser" --network "$NET" --ip "$SUBNET.9" --ipc=host \
    --add-host "$HOST:$SUBNET.5" -v "$SCRATCH/pw:/work" -w /work \
    -e BASE="http://$HOST:2052" -e DIND="http://$SUBNET.5:2375" -e LAN="$SUBNET.5" $envs "$PLAYWRIGHT_IMAGE" node "$@"
}

start_cosmos() {
  cat > "$SCRATCH/run/start.sh" <<EOF
#!/bin/sh
# the test Cosmos, started again whenever it ends (finishing the wizard and some saves end it on purpose)
cd /cosmos
export COSMOS_CONFIG_FOLDER=/cosmos/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053 HOSTNAME=monitoring-db
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

databases() { # the two PostgreSQL containers as at `up`
  in_dind sh -c 'docker rm -f -v postgresql pgofficial >/dev/null 2>&1; true'
  in_dind docker run -d --name postgresql -e POSTGRESQL_USERNAME=cosmos -e POSTGRESQL_PASSWORD=Cosmos-test-pw-51 -e POSTGRESQL_POSTGRES_PASSWORD=Root-test-pw-51 \
    -e POSTGRESQL_DATABASE=cosmos_monitoring -p "$SUBNET.5:5432:5432" bitnami/postgresql:latest >/dev/null || die "could not make postgresql"
  in_dind docker run -d --name pgofficial -e POSTGRES_USER=app -e POSTGRES_PASSWORD=App-test-pw-51 -e POSTGRES_DB=appdb \
    postgres:18 >/dev/null || die "could not make pgofficial"
  for _ in $(seq 1 60); do
    in_dind docker exec postgresql sh -c 'PGPASSWORD=Cosmos-test-pw-51 psql -h 127.0.0.1 -U cosmos -d cosmos_monitoring -c "select 1"' >/dev/null 2>&1 && break
    sleep 1
  done
  # a role that may sign in but not create tables (the live test of the check)
  in_dind docker exec postgresql sh -c "PGPASSWORD=Root-test-pw-51 psql -h 127.0.0.1 -U postgres -d cosmos_monitoring -c \"CREATE ROLE reader LOGIN PASSWORD 'reader-test-pw'\"" >/dev/null 2>&1 || true
}

start_all() {
  docker network create --internal --subnet "$SUBNET.0/24" "$NET" >/dev/null
  mkdir -p "$SCRATCH/dind-docker" "$SCRATCH/run"
  docker run -d --name "$PREFIX-dind" --privileged -e DOCKER_TLS_CERTDIR= --network "$NET" --ip "$SUBNET.5" \
    --hostname monitoring-db -v "$SCRATCH/run:/cosmos" -v "$SCRATCH/dind-docker:/var/lib/docker" \
    -v "$IMAGES_DIR:/images:ro" "$DIND_IMAGE" >/dev/null
  for _ in $(seq 1 60); do in_dind docker info >/dev/null 2>&1 && break; sleep 1; done
  in_dind docker info >/dev/null 2>&1 || die "the test Docker did not start"
  in_dind docker image inspect bitnami/postgresql:latest >/dev/null 2>&1 || in_dind docker load -q -i /images/bitnami.tar >/dev/null
  in_dind docker image inspect postgres:18 >/dev/null 2>&1 || in_dind docker load -q -i /images/pg18.tar >/dev/null
  databases
  start_cosmos
  wait_up
}

setup() { # the wizard over the API; step 5 ends the process, the loop starts it again
  copy_scripts
  browser PHASE=setup -- fork-ui-check-monitoring-db.mjs
  sleep 6
  wait_up
}

stop_all() {
  [ "$(docker ps -aq -f name="^$PREFIX-dind$")" ] && stop_cosmos
  for c in browser dind go node npm; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
  docker network rm "$NET" >/dev/null 2>&1 || true
}

case "${1:-}" in
  up)
    docker network inspect "$NET" >/dev/null 2>&1 && die "$NET exists: run clean first"
    { [ -x "$SCRATCH/run/cosmos" ] && [ "${REBUILD:-1}" = 0 ]; } || build
    playwright
    start_all
    setup
    say "up: http://$HOST:2052 (log $SCRATCH/run/cosmos.log)" ;;
  rebuild) build; stop_cosmos; start_cosmos; wait_up; say "rebuilt and restarted" ;;
  restart) stop_cosmos; start_cosmos; wait_up ;;
  reset) # back to the local file, the databases made again
    stop_cosmos
    docker run --rm --name "$PREFIX-reset" -v "$SCRATCH/run/config:/c" "$NODE_IMAGE" node -e '
      const fs=require("fs");const f="/c/cosmos.config.json";const c=JSON.parse(fs.readFileSync(f));
      c.Database={...c.Database,PostgresHost:"",PostgresDatabase:"",PostgresUsername:"",PostgresPassword:""};
      fs.writeFileSync(f,JSON.stringify(c,null,2));'
    databases; start_cosmos; wait_up ;;
  check) shift; copy_scripts; browser "$@" -- fork-ui-check-monitoring-db.mjs ;;
  shell) shift; in_dind "$@" ;;
  stop) stop_all ;;
  clean)
    stop_all
    # the test Docker's files are root's: removed from a container
    [ -d "$SCRATCH" ] && docker run --rm --name "$PREFIX-clean" --entrypoint rm -v "$(dirname "$SCRATCH"):/p" "$DIND_IMAGE" -rf "/p/$(basename "$SCRATCH")"
    say "clean" ;;
  *) die "usage: $0 up|check|rebuild|restart|reset|shell|stop|clean" ;;
esac
