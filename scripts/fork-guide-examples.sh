#!/bin/sh
# Fork addition (plan 50): the examples test of the apps' how-to guides.
# Every code block of src/guides/s3.md is run, exactly as the panel shows it
# (client/src/utils/guides.js fills it from the test server's own
# GET /api/dashboard and /api/status), against RustFS through a test Cosmos,
# from three places:
#   home       a machine of the home network: https://s3.local:2053, Cosmos' own certificate authority
#   internet   a machine on the internet: https://s3.example.org:2053 through a stand-in for
#              Cloudflare's proxy (nginx) that refuses any request over 100 MB with 413, as Cloudflare does
#   container  another container of the server: http://rustfs:9000 on the app's Docker network
# Nothing reaches the internet during the run, nothing touches the server's own Docker but one
# Docker in Docker (PREFIX-dind): everything else runs inside it, on --internal networks.
#
#   BIN=<cosmos built with CGO_ENABLED=0> sh scripts/fork-guide-examples.sh up    the Docker in Docker, images (pulled once, with a way out)
#   sh scripts/fork-guide-examples.sh run [ONLY=<block id,…>] [PLACES=home,internet,container]
#   sh scripts/fork-guide-examples.sh browser PHASE=seed|home|away|user [ENGINES=…] [SIZES=…]
#        the browser check scripts/fork-ui-check-guides.mjs (needs STATIC=<built ./static> at `up`)
#   sh scripts/fork-guide-examples.sh clean    every container, network and image it made, and the scratch folder
#
# The log is SCRATCH/out/examples.log, the per-block result SCRATCH/out/results.json.
# Inside the Docker in Docker:
#   storage-net (internal)   RustFS 1.0.5 ("rustfs", administrator key made up below), the container place's runners
#   gx-lan  192.168.60.0/24 (internal)  home runners; the test Cosmos listens on the Docker in Docker itself (.1)
#   gx-inet 203.0.113.0/24  (internal, no address of the server on it: an internet address for Cosmos)
#                                       internet runners and the browser "away", and the stand-in proxy (.2)
#   gx-cf   173.245.48.0/24 (internal)  the stand-in (.2) and Cosmos (.1): an address in Cloudflare's ranges, so that
#                                       Cosmos serves the shielded name to it, as to Cloudflare
#   gx-build (with a way out, used only by `up` to build the runner images)
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
PREFIX=${PREFIX:-cosmos-guides}
SCRATCH=${SCRATCH:-/storage/dev/.cache/cosmos-guides/examples}
DIND_IMAGE=docker:29-dind
RUSTFS_IMAGE=${RUSTFS_IMAGE:-rustfs/rustfs:1.0.5}
say() { echo "guide-examples: $*"; }
# the scratch folder is removed by `clean` as root: it must be a folder of its own under the caches
# (written as a plain path: no "//", no "." or ".." segment, no trailing "/", nothing but [A-Za-z0-9._/-],
# and at least two levels below the caches, so that neither /storage/dev/.cache nor a cache of its own
# such as /storage/dev/.cache/go-mod can be it)
case "$SCRATCH" in
  *//*|*/./*|*/../*|*/.|*/..|*/|*[!A-Za-z0-9._/-]*) say "SCRATCH must be a plain path (no //, . or .., no trailing /): $SCRATCH"; exit 2 ;;
esac
case "$SCRATCH" in
  /storage/dev/.cache/*/*) ;;
  *) say "SCRATCH must be a folder two levels under /storage/dev/.cache/ (it is removed by clean): $SCRATCH"; exit 2 ;;
esac
in_dind() { docker exec -i "$PREFIX-dind" "$@"; }

images() {
  w=$SCRATCH/build
  mkdir -p "$w"
  cp "$here/fork-guide-examples/site.sql" "$here/fork-guide-examples/site-compare.sh" "$here/fork-guide-examples/php-40-runs.sh" "$w/"
  # a test "public" authority (it stands for the public authorities Cloudflare's certificates come from) and the stand-in's certificate
  in_dind sh -c 'docker network inspect gx-build >/dev/null 2>&1 || docker network create gx-build >/dev/null'
  if [ ! -s "$w/public-ca.pem" ]; then
    in_dind docker run --rm --network gx-build -v /scratch/build:/w -w /w alpine:3.22 sh -c '
      apk add -q openssl >/dev/null &&
      openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=Test public authority" -keyout public-ca.key -out public-ca.pem 2>/dev/null &&
      openssl req -newkey rsa:2048 -nodes -subj "/CN=s3.example.org" -keyout edge.key -out edge.csr 2>/dev/null &&
      printf "subjectAltName=DNS:s3.example.org\n" > ext &&
      openssl x509 -req -in edge.csr -CA public-ca.pem -CAkey public-ca.key -CAcreateserial -days 30 -extfile ext -out edge.pem 2>/dev/null && chmod 644 *'
  fi
  cat > "$w/nginx.conf" <<'EOF'
events {}
http {
  server {
    listen 2053 ssl;
    server_name s3.example.org;
    ssl_certificate /w/edge.pem;
    ssl_certificate_key /w/edge.key;
    client_max_body_size 100m;          # Cloudflare's limit on one request
    proxy_request_buffering off;
    proxy_buffering off;
    location / {
      proxy_pass https://173.245.48.1:2053;
      proxy_ssl_server_name on;
      proxy_ssl_name $host;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
      proxy_set_header CF-Connecting-IP $remote_addr;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_read_timeout 600s;
    }
  }
  # the panel through the stand-in, for the browser "away" (its own certificate is not checked by the test browser)
  server {
    listen 2053 ssl;
    server_name cosmos.example.org;
    ssl_certificate /w/edge.pem;
    ssl_certificate_key /w/edge.key;
    location / {
      proxy_pass https://173.245.48.1:2053;
      proxy_ssl_server_name on;
      proxy_ssl_name $host;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
      proxy_set_header CF-Connecting-IP $remote_addr;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection "upgrade";
    }
  }
}
EOF
  trust_deb='COPY public-ca.pem /usr/local/share/ca-certificates/test-public.crt
RUN update-ca-certificates >/dev/null 2>&1 || true'
  cat > "$w/linux.Dockerfile" <<EOF
FROM debian:13
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq mariadb-server mariadb-client ca-certificates diffutils systemd curl >/dev/null
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
COPY --from=rclone/rclone:latest /usr/local/bin/rclone /usr/local/bin/rclone
$trust_deb
RUN mkdir -p /var/www/site/uploads && echo '<?php echo "hello";' > /var/www/site/index.php && echo 'a page' > /var/www/site/about.html \
 && head -c 157286400 /dev/urandom > /var/www/site/uploads/video.bin
EOF
  cat > "$w/pg.Dockerfile" <<EOF
FROM postgres:18
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates >/dev/null
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
$trust_deb
ENV PGHOST=/var/run/postgresql
EOF
  cat > "$w/maria.Dockerfile" <<EOF
FROM mariadb:lts
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
$trust_deb
EOF
  cat > "$w/mongo.Dockerfile" <<EOF
FROM mongo:8
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
$trust_deb
ENV MONGO_URI=mongodb://127.0.0.1:27017
EOF
  cat > "$w/aws.Dockerfile" <<'EOF'
FROM amazon/aws-cli:latest
RUN (dnf install -y -q diffutils || yum install -y -q diffutils) >/dev/null 2>&1
COPY public-ca.pem /etc/pki/ca-trust/source/anchors/test-public.pem
RUN update-ca-trust
ENV AWS_CA_BUNDLE=/etc/pki/tls/certs/ca-bundle.crt
ENTRYPOINT []
EOF
  cat > "$w/python.Dockerfile" <<EOF
FROM python:3.13-slim
RUN pip install -q boto3
$trust_deb
ENV AWS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt PIP_NO_INDEX=1
EOF
  cat > "$w/node.Dockerfile" <<'EOF'
FROM node:22-slim
WORKDIR /work
RUN npm install --no-audit --no-fund -q @aws-sdk/client-s3 @aws-sdk/lib-storage @aws-sdk/s3-request-presigner >/dev/null
COPY public-ca.pem /usr/local/share/test-public.pem
ENV NODE_EXTRA_CA_CERTS=/usr/local/share/test-public.pem npm_config_offline=true
EOF
  cat > "$w/php.Dockerfile" <<EOF
FROM php:8.4-cli
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq libzip-dev unzip mariadb-server >/dev/null \
 && docker-php-ext-install -j4 zip pdo_mysql >/dev/null
COPY --from=composer:2 /usr/bin/composer /usr/local/bin/composer
WORKDIR /work
RUN composer require -q aws/aws-sdk-php
$trust_deb
RUN mkdir -p /home/you/public_html/wp-content && echo '<?php echo "hello";' > /home/you/public_html/index.php \
 && echo 'a page' > /home/you/public_html/about.html && echo '<?php // the site configuration' > /home/you/public_html/wp-config.php \
 && printf '[pdo_mysql]\npdo_mysql.default_socket=/run/mysqld/mysqld.sock\n' > /usr/local/etc/php/conf.d/socket.ini
COPY site.sql site-compare.sh php-40-runs.sh /usr/local/share/
EOF
  cat > "$w/go.Dockerfile" <<EOF
FROM golang:1.27
RUN mkdir /prep && cd /prep && go mod init example.org/prep >/dev/null 2>&1 \
 && go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/feature/s3/manager github.com/aws/aws-sdk-go-v2/service/s3 >/dev/null 2>&1
$trust_deb
ENV GOPROXY=file:///go/pkg/mod/cache/download GOSUMDB=off GOFLAGS=-mod=mod
EOF
  cat > "$w/curl.Dockerfile" <<'EOF'
FROM curlimages/curl:latest
USER root
COPY public-ca.pem /usr/local/share/ca-certificates/test-public.crt
RUN cat /usr/local/share/ca-certificates/test-public.crt >> /etc/ssl/certs/ca-certificates.crt
ENV CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
EOF
  cat > "$w/pwsh.Dockerfile" <<EOF
FROM mcr.microsoft.com/powershell:latest
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
$trust_deb
RUN mkdir -p /root/Documents && echo 'a letter' > /root/Documents/letter.txt
EOF
  cat > "$w/docker.Dockerfile" <<'EOF'
FROM docker:29-cli
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
RUN apk add -q --no-cache bash
EOF
  for kind in linux pg maria mongo aws python node php go curl pwsh docker; do
    say "image gx-$kind"
    in_dind docker build -q -t "gx-$kind" -f "/scratch/build/$kind.Dockerfile" /scratch/build >/dev/null
  done
  in_dind docker build -q -t gx-harness - >/dev/null <<'EOF'
FROM node:22-slim
COPY --from=docker:29-cli /usr/local/bin/docker /usr/local/bin/docker
EOF
}

case "${1:-}" in
up)
  test -x "${BIN:?BIN=<cosmos binary>}"
  mkdir -p "$SCRATCH/run" "$SCRATCH/out" "$SCRATCH/docker" "$SCRATCH/build"
  cmp -s "$BIN" "$SCRATCH/run/cosmos" || cp "$BIN" "$SCRATCH/run/cosmos"   # stop the test server first (clean) to change it
  echo '{"version":"test"}' > "$SCRATCH/run/meta.json"
  rm -rf "$SCRATCH/run/static" && mkdir -p "$SCRATCH/run/static"
  if [ -n "${STATIC:-}" ]; then cp -r "$STATIC/." "$SCRATCH/run/static/"; else echo '<!doctype html><title>t</title>' > "$SCRATCH/run/static/index.html"; fi
  if ! docker inspect "$PREFIX-dind" >/dev/null 2>&1; then
    docker network inspect "$PREFIX-out" >/dev/null 2>&1 || docker network create "$PREFIX-out" >/dev/null
    docker run -d --name "$PREFIX-dind" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-out" --dns 1.1.1.1 --dns 9.9.9.9 \
      --hostname cosmos-guides-server -v "$SCRATCH/docker:/var/lib/docker" -v "$SCRATCH:/scratch" -v "$repo:/repo:ro" \
      "$DIND_IMAGE" --registry-mirror https://mirror.gcr.io >/dev/null
    for _ in $(seq 1 60); do in_dind docker info >/dev/null 2>&1 && break; sleep 1; done
  fi
  for image in "$RUSTFS_IMAGE" restic/restic:latest restic/restic:0.19.1 rclone/rclone:latest debian:13 postgres:18 mariadb:lts mongo:8 \
      amazon/aws-cli:latest python:3.13-slim node:22-slim php:8.4-cli composer:2 golang:1.27 curlimages/curl:latest \
      mcr.microsoft.com/powershell:latest docker:29-cli alpine:3.22 nginx:1-alpine; do
    in_dind sh -c "docker image inspect $image >/dev/null 2>&1 || docker pull -q $image >/dev/null" || { say "could not pull $image"; exit 2; }
  done
  images
  in_dind sh -c '
    docker network inspect storage-net >/dev/null 2>&1 || docker network create --internal storage-net >/dev/null
    docker network inspect gx-lan >/dev/null 2>&1 || docker network create --internal --subnet 192.168.60.0/24 gx-lan >/dev/null
    docker network inspect gx-inet >/dev/null 2>&1 || docker network create --internal --subnet 203.0.113.0/24 -o com.docker.network.bridge.inhibit_ipv4=true gx-inet >/dev/null
    docker network inspect gx-cf >/dev/null 2>&1 || docker network create --internal --subnet 173.245.48.0/24 gx-cf >/dev/null
    docker rm -f gx-edge >/dev/null 2>&1
    docker run -d --name gx-edge --network gx-inet --ip 203.0.113.2 -v /scratch/build:/w:ro nginx:1-alpine nginx -c /w/nginx.conf -g "daemon off;" >/dev/null
    docker network connect --ip 173.245.48.2 gx-cf gx-edge'
  cat > "$SCRATCH/run/start.sh" <<'EOF'
#!/bin/sh
# the test Cosmos, started again whenever it ends (the setup wizard and some saves end it on purpose)
cd /scratch/run
export COSMOS_CONFIG_FOLDER=/scratch/run/config/ COSMOS_HTTP_PORT=2052 COSMOS_HTTPS_PORT=2053
while [ ! -e /scratch/run/stop ]; do ./cosmos >> /scratch/run/cosmos.log 2>&1; sleep 1; done
EOF
  rm -f "$SCRATCH/run/stop"
  mkdir -p "$SCRATCH/run/config"
  in_dind pgrep -f "sh /scratch/run/start.sh" >/dev/null || docker exec -d "$PREFIX-dind" sh /scratch/run/start.sh
  say "up: Cosmos (log $SCRATCH/run/cosmos.log), the stand-in at 203.0.113.2"
  ;;
run)
  mkdir -p "$SCRATCH/out"
  in_dind sh -c 'docker rm -f -v rustfs >/dev/null 2>&1; docker run -d --name rustfs --network storage-net \
      -e RUSTFS_ACCESS_KEY=gx-admin -e RUSTFS_SECRET_KEY=Gx-test-admin-secret-0420 '"$RUSTFS_IMAGE"' >/dev/null'
  in_dind docker rm -f gx-harness-run >/dev/null 2>&1 || true
  harness() {
    in_dind docker run --rm --name gx-harness-run --network gx-lan --add-host cosmos.example.org:192.168.60.1 \
      -v /var/run/docker.sock:/var/run/docker.sock -v /repo:/repo:ro -v /scratch:/scratch -w /repo \
      -e ONLY="${ONLY:-}" -e PLACES="${PLACES:-home,internet,container}" -e OUT=/scratch/out -e NODE_TLS_REJECT_UNAUTHORIZED=0 "$@" \
      gx-harness node scripts/fork-guide-examples.mjs
  }
  # the wizard and the URLs, then the names served through Cloudflare's proxy (the S3 address, and the
  # panel's name for the browser "away", which Cosmos then sees at its CF-Connecting-IP): Cosmos keeps that list
  # apart from what PUT /api/config may change (it is set by the Cloudflare card), so it is written
  # into the configuration file here and the test server is started again
  harness -e SETUP_ONLY=1
  in_dind docker run --rm -v /scratch/run/config:/c gx-harness node -e '
    const fs = require("fs"); const p = "/c/cosmos.config.json"; const c = JSON.parse(fs.readFileSync(p, "utf8"));
    c.HTTPConfig.Cloudflare = c.HTTPConfig.Cloudflare || {};
    if (!(c.HTTPConfig.Cloudflare.Shielded || []).includes("cosmos.example.org")) {
      c.HTTPConfig.Cloudflare.Shielded = ["s3.example.org", "cosmos.example.org"]; c.HTTPConfig.Cloudflare.TrustRanges = true;
      fs.writeFileSync(p, JSON.stringify(c, null, 2)); console.log("shielded"); }' > "$SCRATCH/out/shield.txt"
  if grep -q shielded "$SCRATCH/out/shield.txt"; then in_dind pkill -f "^./cosmos" || true; sleep 3; fi
  in_dind docker run --rm --name gx-harness-run --network gx-lan --add-host cosmos.example.org:192.168.60.1 \
    -v /var/run/docker.sock:/var/run/docker.sock -v /repo:/repo:ro -v /scratch:/scratch -w /repo \
    -e ONLY="${ONLY:-}" -e PLACES="${PLACES:-home,internet,container}" -e OUT=/scratch/out -e NODE_TLS_REJECT_UNAUTHORIZED=0 \
    gx-harness node scripts/fork-guide-examples.mjs
  ;;
browser)
  shift
  envs=""
  for a in "$@"; do envs="$envs -e $a"; done
  if ! in_dind docker image inspect mcr.microsoft.com/playwright:v1.65.1-noble >/dev/null 2>&1; then
    in_dind docker pull -q mcr.microsoft.com/playwright:v1.65.1-noble >/dev/null
  fi
  if [ ! -d "$SCRATCH/pw/node_modules/playwright" ]; then
    mkdir -p "$SCRATCH/pw" && cp "$here/fork-ci-smoke/package.json" "$here/fork-ci-smoke/package-lock.json" "$SCRATCH/pw/"
    in_dind docker run --rm -v /scratch/pw:/pw -w /pw mcr.microsoft.com/playwright:v1.65.1-noble npm ci --ignore-scripts --no-audit --no-fund --loglevel=error >/dev/null
  fi
  phase=$(echo "$envs" | sed -n 's/.*PHASE=\([a-z]*\).*/\1/p')
  if [ "$phase" = away ]; then net="--network gx-inet --add-host cosmos.example.org:203.0.113.2"; else net="--network gx-lan --add-host cosmos.example.org:192.168.60.1 --add-host rustfs.local:192.168.60.1"; fi
  in_dind docker rm -f gx-browser >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  in_dind docker run --rm --name gx-browser $net --ipc=host $envs -e OUT=/scratch/out/ui \
    -v /repo/scripts:/check/scripts:ro -v /scratch/pw/node_modules:/check/node_modules:ro -v /scratch:/scratch -w /check \
    mcr.microsoft.com/playwright:v1.65.1-noble node scripts/fork-ui-check-guides.mjs
  ;;
clean)
  touch "$SCRATCH/run/stop" 2>/dev/null || true
  docker rm -f -v "$PREFIX-dind" >/dev/null 2>&1 || true
  docker network rm "$PREFIX-out" >/dev/null 2>&1 || true
  [ -d "$SCRATCH" ] && docker run --rm --entrypoint rm -v "$(dirname "$SCRATCH"):/p" "$DIND_IMAGE" -rf "/p/$(basename "$SCRATCH")"
  say "cleaned"
  ;;
*) echo "usage: $0 up|run|clean" >&2; exit 2 ;;
esac
