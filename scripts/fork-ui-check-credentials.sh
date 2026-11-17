#!/bin/sh
# Fork addition (plan 45): the setup of scripts/fork-ui-check-credentials.mjs,
# the browser check of sign-in details for new apps, the e-mail at
# installation and the web managers. A throwaway Cosmos with a Docker of its
# own; never the production instance, never the real Docker Hub from it, never
# a real mail server.
#
#   sh scripts/fork-ui-check-credentials.sh up        build, load the images (once), start, set up
#   sh scripts/fork-ui-check-credentials.sh prepare | start | setup   the three parts of up
#   sh scripts/fork-ui-check-credentials.sh rebuild   build the server and the panel again from the worktree, restart Cosmos
#   sh scripts/fork-ui-check-credentials.sh check [VAR=value…]   run the check (PHASE, ENGINES, SIZES, MODES)
#   sh scripts/fork-ui-check-credentials.sh node <script.mjs> [VAR=value…]   another check script, same browser container
#   sh scripts/fork-ui-check-credentials.sh other     a second, fresh test Cosmos at SUBNET.10 for the checks that use its IP
#   sh scripts/fork-ui-check-credentials.sh stop      stop the test containers (keeps the scratch folder and the images)
#   sh scripts/fork-ui-check-credentials.sh clean     remove every container and network it made, and the scratch folder
#
# What `up` makes, on one --internal network, SUBNET.0/24 (203.0.113.0/24 by default, a documentation
# range, routed nowhere, that COSMOS_ANYIMAGE_TEST_ALLOW lets the image reader's guard treat as the
# internet; SUBNET=198.51.100 when no other test network holds that one):
#   .2  PREFIX-mail      scripts/fork-fake-mail: the only name server of the test Cosmos (and of the
#                        browser), and its mail server (fakemail.test, STARTTLS on 587, account
#                        cosmos@fakemail.test / right-password); /state on 8025 tells what was sent.
#                        FAKE_HOSTS makes cosmos-test.example.org, every name below it and every .local name .5 (the apps' addresses)
#   .5  PREFIX-dind      docker:29-dind; its system bundle trusts the test CA, registry-1.docker.io and
#                        index.docker.io are .7 and hub.docker.com is .6 in its /etc/hosts; a loop file
#                        of the scratch folder (another device) is mounted at /mnt/data. The test Cosmos
#                        runs INSIDE it (the server's own path to containers), built with
#                        -tags anyimage_testnet, COSMOS_ANYIMAGE_TEST_ALLOW=SUBNET.0/24, SSL_CERT_FILE
#                        the test CA, HTTP 2052, HTTPS 2053 self-signed, hostname cosmos-test.example.org;
#                        its log is SCRATCH/run/cosmos.log
#   .6  PREFIX-hub       scripts/fork-fake-dockerhub.go as hub.docker.com
#   .7  PREFIX-registry  registry:3 over HTTPS as registry-1.docker.io, holding IMAGES (copied once from
#                        the real registries by PREFIX-loader, a Docker in Docker on a network of its own
#                        with a way out and mirror.gcr.io as its mirror, which is removed afterwards)
#   .9  PREFIX-browser   the Playwright image, run per check (scripts/fork-ci-smoke's pinned playwright)
# and, in the test Docker before the story, "olddb" (postgres:18, POSTGRES_USER=olduser,
# POSTGRES_PASSWORD=Old-test-pw-0420, POSTGRES_DB=olddb). The wizard, the e-mail settings, the
# admin's e-mail address and a second user without the credentials permission are made by the
# check's PHASE=setup, which `up` runs.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
PREFIX=${PREFIX:-cosmos-creds-ui}
SCRATCH=${SCRATCH:-/storage/dev/.cache/cosmos-creds/ui}
NET=$PREFIX-net
SUBNET=${SUBNET:-203.0.113}
PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.1-noble
GO_IMAGE=golang:1.27
NODE_IMAGE=node:22
DIND_IMAGE=docker:29-dind
REGISTRY_IMAGE=registry:3
HOST=cosmos-test.example.org
# what the registry holds: <published path>=<source to pull>; the tags are those the recipes
# propose (src/anyimage/recipes.json: postgres pins its major, mariadb prefers lts, pgAdmin and
# phpMyAdmin pin their major, Adminer has latest only), with the extras of the other checks
IMAGES=${IMAGES:-"library/postgres:18=postgres:18 library/mariadb:lts=mariadb:lts dpage/pgadmin4:9=dpage/pgadmin4:9 library/phpmyadmin:5=phpmyadmin:5 library/adminer:latest=adminer:latest library/busybox:1.36=busybox:1.36 library/redis:8=redis:8 redis/redisinsight:latest=redis/redisinsight:latest"}
EXTRA_IMAGES=${EXTRA_IMAGES:-"library/postgres:17=postgres:17 library/nginx:1=nginx:1 library/redis:8=redis:8 library/nginx:1-alpine=nginx:1-alpine bitnami/postgresql:latest=bitnami/postgresql:latest library/hello-world:latest=hello-world:latest"}

say() { echo "credentials-ui: $*"; }
die() { say "$*"; exit 2; }
in_dind() { docker exec "$PREFIX-dind" "$@"; }

build() {
  mkdir -p "$SCRATCH/run" "$SCRATCH/bin"
  say "building the server (anyimage_testnet) and the stand-ins"
  docker run --rm --name "$PREFIX-go" -v "$repo:/src:ro" -v "$SCRATCH:/out" -w /src \
    -v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
    -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c '
      go build -tags anyimage_testnet -o /out/bin/cosmos.new src/*.go &&
      go build -o /out/bin/fake-mail ./scripts/fork-fake-mail &&
      go build -o /out/bin/fake-hub scripts/fork-fake-dockerhub.go'
  say "building the panel in a hard-linked copy of the worktree"
  rm -rf "$SCRATCH/src"
  mkdir -p "$SCRATCH/src"
  (cd "$repo" && for f in * .[!.]*; do [ -e "$f" ] || continue; [ "$f" = .git ] && continue; cp -al "$f" "$SCRATCH/src/"; done)
  # never write through a hard link into the worktree: the build's outputs and caches are removed first
  rm -rf "$SCRATCH/src/static" "$SCRATCH/src/stats.html" "$SCRATCH/src/node_modules/.vite" "$SCRATCH/src/client/node_modules" "$SCRATCH/src/build"
  docker run --rm --name "$PREFIX-node" -v "$SCRATCH/src:/src" -w /src -v /storage/dev/.cache/npm:/root/.npm "$NODE_IMAGE" \
    sh -c 'npx vite build --base=/cosmos-ui/ > /tmp/vite.log 2>&1 || { tail -40 /tmp/vite.log; exit 1; }; tail -3 /tmp/vite.log'
  rm -rf "$SCRATCH/run/static.new"
  cp -r "$SCRATCH/src/static" "$SCRATCH/run/static.new"
  rm -rf "$SCRATCH/run/static"
  mv "$SCRATCH/run/static.new" "$SCRATCH/run/static"
  version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$repo/package.json" | head -1)
  printf '{\n  "version": "%s",\n  "buildDate": "%s",\n  "built from": "credentials-ui"\n}\n' "$version" "$(date)" > "$SCRATCH/run/meta.json"
  mv "$SCRATCH/bin/cosmos.new" "$SCRATCH/run/cosmos"
  rm -rf "$SCRATCH/src"
}

certs() {
  [ -s "$SCRATCH/ca/ca.crt" ] && return 0
  mkdir -p "$SCRATCH/ca"
  say "making the test authority"
  docker run --rm --name "$PREFIX-ca" -v "$SCRATCH/ca:/ca" -w /ca "$GO_IMAGE" sh -c '
    set -e
    openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj "/CN=credentials check test CA" -keyout ca.key -out ca.crt 2>/dev/null
    for n in registry hub; do
      if [ $n = registry ]; then san="DNS:registry-1.docker.io,DNS:index.docker.io,DNS:registry.test"; else san="DNS:hub.docker.com"; fi
      openssl req -newkey rsa:2048 -nodes -subj "/CN=$n" -keyout $n.key -out $n.csr 2>/dev/null
      printf "subjectAltName=%s\nextendedKeyUsage=serverAuth\n" "$san" > $n.ext
      openssl x509 -req -in $n.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 7 -extfile $n.ext -out $n.crt 2>/dev/null
    done
    chmod 644 *.key'
}

registry_run() { # <network> [extra args]: the registry over HTTPS on 443
  net=$1; shift
  docker run -d --name "$PREFIX-registry" --network "$net" "$@" -v "$SCRATCH/registry:/var/lib/registry" -v "$SCRATCH/ca:/certs:ro" \
    -e REGISTRY_HTTP_ADDR=0.0.0.0:443 -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
    "$REGISTRY_IMAGE" >/dev/null
}

load() { # <list>: copies each image into the registry once (linux/amd64)
  list=$1
  mkdir -p "$SCRATCH/registry" "$SCRATCH/loaded"
  todo=""
  for pair in $list; do [ -e "$SCRATCH/loaded/$(echo "${pair%%=*}" | tr '/:' '__')" ] || todo="$todo $pair"; done
  [ -n "$todo" ] || return 0
  say "loading into the test registry:$todo"
  docker network create "$PREFIX-load" >/dev/null
  registry_run "$PREFIX-load" --network-alias registry.test
  mkdir -p "$SCRATCH/loader/certs.d/registry.test"
  cp "$SCRATCH/ca/ca.crt" "$SCRATCH/loader/certs.d/registry.test/ca.crt"
  docker run -d --name "$PREFIX-loader" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-load" \
    -v "$SCRATCH/loader/certs.d:/etc/docker/certs.d:ro" -v "$SCRATCH/loader/docker:/var/lib/docker" \
    "$DIND_IMAGE" --registry-mirror=https://mirror.gcr.io >/dev/null
  for _ in $(seq 1 60); do docker exec "$PREFIX-loader" docker info >/dev/null 2>&1 && break; sleep 1; done
  for pair in $todo; do
    dest=${pair%%=*}; src=${pair#*=}
    docker exec "$PREFIX-loader" sh -c "docker pull -q --platform linux/amd64 '$src' && docker tag '$src' 'registry.test/$dest' && docker push -q 'registry.test/$dest'" \
      || die "could not copy $src"
    touch "$SCRATCH/loaded/$(echo "$dest" | tr '/:' '__')"
  done
  # example/dbapp:1 and example/armonly:1 (scripts/fork-ui-check-any-image.mjs), when the extras are loaded
  if echo "$list" | grep -q 'library/redis'; then
    docker exec "$PREFIX-loader" sh -c '
      set -e; mkdir -p /tmp/dbapp; cd /tmp/dbapp
      printf "FROM busybox:1.36\nENV DB_HOST= DB_PORT=5432 DB_USER= DB_PASSWORD= DB_NAME= APP_SECRET=\nEXPOSE 8080\nCMD [\"httpd\", \"-f\", \"-p\", \"8080\", \"-h\", \"/etc\"]\n" > Dockerfile
      docker build -q -t registry.test/example/dbapp:1 . && docker push -q registry.test/example/dbapp:1
      docker pull -q --platform linux/arm64 busybox:1.36 && docker tag busybox:1.36 registry.test/example/armonly:1 && docker push -q registry.test/example/armonly:1' \
      || die "could not make example/dbapp and example/armonly"
  fi
  docker rm -f -v "$PREFIX-loader" "$PREFIX-registry" >/dev/null
  docker network rm "$PREFIX-load" >/dev/null
  rm -rf "$SCRATCH/loader"
}

start_cosmos() {
  cat > "$SCRATCH/run/start.sh" <<EOF
#!/bin/sh
# the test Cosmos, started again whenever it ends (finishing the wizard and some saves end it on purpose)
cd /cosmos
export COSMOS_CONFIG_FOLDER=/cosmos/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053 SSL_CERT_FILE=/ca/ca.crt \
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

browser() { # [VAR=value…] -- <script>: runs a node script of SCRATCH/pw in the browser container
  envs=""
  while [ $# -gt 1 ] && [ "$1" != "--" ]; do envs="$envs -e $1"; shift; done
  [ "${1:-}" = "--" ] && shift
  docker rm -f "$PREFIX-browser" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker run --rm --name "$PREFIX-browser" --network "$NET" --ip "$SUBNET.9" --dns "$SUBNET.2" --ipc=host \
    -v "$SCRATCH/pw:/work" -v "$SCRATCH/run:/cosmos:ro" -w /work -e SUBNET="$SUBNET" $envs "$PLAYWRIGHT_IMAGE" node "$@"
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

up() {
  docker network inspect "$NET" >/dev/null 2>&1 && die "$NET exists: run clean first, or stop/start"
  [ -x "$SCRATCH/run/cosmos" ] && [ "${REBUILD:-1}" = 0 ] || build
  certs
  load "$IMAGES"
  [ "${EXTRA:-0}" = 1 ] && load "$EXTRA_IMAGES"
  playwright
  start_all
  copy_scripts
  browser PHASE=setup -- fork-ui-check-credentials.mjs
}

dind_run() { # <name> <address> <run folder> <data name>: a Docker in Docker with the test CA, the stand-ins' names and a data disk
  if [ ! -e "$SCRATCH/$4.img.done" ]; then
    rm -f "$SCRATCH/$4.img"; truncate -s 4G "$SCRATCH/$4.img"
    docker run --rm --name "$PREFIX-mkfs" -v "$SCRATCH:/s" "$NODE_IMAGE" mkfs.ext4 -q "/s/$4.img" || die "could not format the data disk"
    touch "$SCRATCH/$4.img.done"
  fi
  mkdir -p "$SCRATCH/$1-docker" "$SCRATCH/$3"
  docker run -d --name "$PREFIX-$1" --privileged -e DOCKER_TLS_CERTDIR= --network "$NET" --ip "$2" \
    --hostname cosmos-test --dns "$SUBNET.2" \
    --add-host "registry-1.docker.io:$SUBNET.7" --add-host "index.docker.io:$SUBNET.7" --add-host "hub.docker.com:$SUBNET.6" \
    -v "$SCRATCH/ca:/ca:ro" -v "$SCRATCH/$3:/cosmos" -v "$SCRATCH/$1-docker:/var/lib/docker" -v "$SCRATCH/$4.img:/data.img" \
    --entrypoint sh "$DIND_IMAGE" -c 'cat /ca/ca.crt >> /etc/ssl/certs/ca-certificates.crt; mkdir -p /mnt/data; mount -o loop /data.img /mnt/data || exit 1; exec dockerd-entrypoint.sh' >/dev/null
  for _ in $(seq 1 60); do docker exec "$PREFIX-$1" docker info >/dev/null 2>&1 && break; sleep 1; done
  docker exec "$PREFIX-$1" docker info >/dev/null 2>&1 || die "the test Docker $1 did not start"
}

# a second test Cosmos at SUBNET.10 for the older checks that address the server by its IP
# (scripts/fork-ui-check-any-image.mjs, -sensitive-edit.mjs: SERVER_IP=SUBNET.10 DIND=http://SUBNET.10:2375),
# fresh (those checks run the wizard), with maindb (postgres:17) and web1 (nginx:1) in its Docker
other() {
  dind_run other "$SUBNET.10" run-other data-other
  for f in cosmos meta.json; do cp "$SCRATCH/run/$f" "$SCRATCH/run-other/"; done
  rm -rf "$SCRATCH/run-other/static"; cp -r "$SCRATCH/run/static" "$SCRATCH/run-other/static"
  cp "$SCRATCH/run/start.sh" "$SCRATCH/run-other/start.sh"
  docker exec "$PREFIX-other" sh -c 'docker run -d --name maindb -e POSTGRES_USER=main -e POSTGRES_PASSWORD=main-test-pw -e POSTGRES_DB=maindb postgres:17 && docker run -d --name web1 nginx:1 && docker pull -q busybox:1.36 && docker pull -q hello-world' >/dev/null || true
  rm -f "$SCRATCH/run-other/stop"
  docker exec -d "$PREFIX-other" sh /cosmos/start.sh
  say "the second Cosmos: https://$SUBNET.10:2053 (log $SCRATCH/run-other/cosmos.log)"
}

start_all() {
  docker network create --internal --subnet "$SUBNET.0/24" "$NET" >/dev/null
  docker run -d --name "$PREFIX-mail" --network "$NET" --ip "$SUBNET.2" -e FAKE_IP="$SUBNET.2" -e FAKE_HOSTS="$HOST=$SUBNET.5,local=$SUBNET.5" \
    -v "$SCRATCH/bin:/bin-test:ro" "$GO_IMAGE" /bin-test/fake-mail >/dev/null
  registry_run "$NET" --ip "$SUBNET.7"
  docker run -d --name "$PREFIX-hub" --network "$NET" --ip "$SUBNET.6" --add-host "registry-1.docker.io:$SUBNET.7" \
    -e REGISTRY=https://registry-1.docker.io -e CERT=/ca/hub.crt -e KEY=/ca/hub.key -e SSL_CERT_FILE=/ca/ca.crt \
    -e DESCRIPTIONS=/readmes -v "$repo/src/anyimage/testdata/readmes:/readmes:ro" \
    -v "$SCRATCH/bin:/bin-test:ro" -v "$SCRATCH/ca:/ca:ro" "$GO_IMAGE" /bin-test/fake-hub >/dev/null
  dind_run dind "$SUBNET.5" run data
  if ! in_dind docker inspect olddb >/dev/null 2>&1; then
    say "making olddb, the database that was there before"
    in_dind docker run -d --name olddb --restart unless-stopped -e POSTGRES_USER=olduser -e POSTGRES_PASSWORD=Old-test-pw-0420 \
      -e POSTGRES_DB=olddb postgres:18 >/dev/null || die "could not make olddb"
  else
    in_dind docker start olddb >/dev/null 2>&1 || true
  fi
  start_cosmos
  for _ in $(seq 1 60); do
    browser -- -e "fetch('http://$SUBNET.5:2052/cosmos/api/status',{signal:AbortSignal.timeout(4000)}).then((r)=>process.exit(r.status<500?0:1),()=>process.exit(1))" >/dev/null 2>&1 && break
    sleep 2
  done
  say "up: https://$HOST:2053 (log $SCRATCH/run/cosmos.log)"
}

stop_all() {
  [ "$(docker ps -aq -f name="^$PREFIX-dind$")" ] && stop_cosmos
  docker exec "$PREFIX-other" sh -c 'touch /cosmos/stop; pkill -f "^./cosmos"' >/dev/null 2>&1 || true
  for c in browser dind other hub registry mail loader mkfs; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
  for n in "$NET" "$PREFIX-load"; do docker network rm "$n" >/dev/null 2>&1 || true; done
}

case "${1:-}" in
  up) up ;;
  prepare) build; certs; load "$IMAGES"; [ "${EXTRA:-0}" = 1 ] && load "$EXTRA_IMAGES"; playwright ;;
  start) start_all; copy_scripts ;;
  setup) copy_scripts; browser PHASE=setup -- fork-ui-check-credentials.mjs ;;
  stop) stop_all ;;
  rebuild)
    build
    stop_cosmos
    start_cosmos
    say "rebuilt and restarted" ;;
  restart) stop_cosmos; start_cosmos ;;
  other) other ;;
  extra)
    # the loader runs its own copy of the registry: the test network's one steps aside meanwhile
    running=$(docker ps -q -f name="^$PREFIX-registry$")
    [ -n "$running" ] && docker rm -f -v "$PREFIX-registry" >/dev/null
    load "$EXTRA_IMAGES"
    [ -n "$running" ] && registry_run "$NET" --ip "$SUBNET.7"
    true ;;
  check) shift; copy_scripts; browser "$@" -- fork-ui-check-credentials.mjs ;;
  node) shift; script=$1; shift; copy_scripts; browser "$@" -- "$script" ;;
  clean)
    stop_all
    # the test Docker's files are root's and partly read-only bind mounts of overlay layers: removed from a container
    [ -d "$SCRATCH" ] && docker run --rm -v "$(dirname "$SCRATCH"):/p" "$DIND_IMAGE" rm -rf "/p/$(basename "$SCRATCH")"
    say "clean" ;;
  *) die "usage: $0 up|start|stop|rebuild|restart|extra|check|node|clean" ;;
esac
