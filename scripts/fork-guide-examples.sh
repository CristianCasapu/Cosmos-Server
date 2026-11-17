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
#   sh scripts/fork-guide-examples.sh clean    every container, network and image it made, and the scratch folder
#
# The log is SCRATCH/out/examples.log, the per-block result SCRATCH/out/results.json.
# Inside the Docker in Docker:
#   storage-net (internal)   RustFS 1.0.5 ("rustfs", administrator key made up below), the container place's runners
#   gx-lan  192.168.60.0/24 (internal)  home runners; the test Cosmos listens on the Docker in Docker itself (.1)
#   gx-inet 203.0.113.0/24  (internal)  internet runners and the stand-in proxy (.2)
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
in_dind() { docker exec -i "$PREFIX-dind" "$@"; }

images() {
  w=$SCRATCH/build
  mkdir -p "$w"
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
}
EOF
  trust_deb='COPY public-ca.pem /usr/local/share/ca-certificates/test-public.crt
RUN update-ca-certificates >/dev/null 2>&1 || true'
  cat > "$w/linux.Dockerfile" <<EOF
FROM debian:13
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq mariadb-server mariadb-client ca-certificates diffutils systemd >/dev/null
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
COPY --from=rclone/rclone:latest /usr/local/bin/rclone /usr/local/bin/rclone
$trust_deb
RUN mkdir -p /var/www/site/uploads && echo '<?php echo "hello";' > /var/www/site/index.php && echo 'a page' > /var/www/site/about.html \
 && head -c 157286400 /dev/urandom > /var/www/site/uploads/video.bin
EOF
  cat > "$w/pg.Dockerfile" <<EOF
FROM postgres:18
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
$trust_deb
ENV PGHOST=/var/run/postgresql PGUSER=postgres
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
RUN mkdir -p /var/www/site && echo '<?php echo "hello";' > /var/www/site/index.php && echo 'a page' > /var/www/site/about.html
EOF
  cat > "$w/go.Dockerfile" <<EOF
FROM golang:1.27
RUN mkdir /prep && cd /prep && go mod init example.org/prep >/dev/null 2>&1 \
 && go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/feature/s3/manager github.com/aws/aws-sdk-go-v2/service/s3 >/dev/null 2>&1
$trust_deb
ENV GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
EOF
  cat > "$w/curl.Dockerfile" <<'EOF'
FROM curlimages/curl:latest
USER root
COPY public-ca.pem /usr/local/share/ca-certificates/test-public.crt
RUN cat /usr/local/share/ca-certificates/test-public.crt >> /etc/ssl/certs/ca-certificates.crt
EOF
  cat > "$w/pwsh.Dockerfile" <<EOF
FROM mcr.microsoft.com/powershell:latest
COPY --from=restic/restic:latest /usr/bin/restic /usr/local/bin/restic
$trust_deb
RUN mkdir -p /root/Documents && echo 'a letter' > /root/Documents/letter.txt
EOF
  cat > "$w/docker.Dockerfile" <<'EOF'
FROM docker:29-cli
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
  cp "$BIN" "$SCRATCH/run/cosmos"
  echo '{"version":"test"}' > "$SCRATCH/run/meta.json"
  mkdir -p "$SCRATCH/run/static" && [ -s "$SCRATCH/run/static/index.html" ] || echo '<!doctype html><title>t</title>' > "$SCRATCH/run/static/index.html"
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
    docker network inspect gx-inet >/dev/null 2>&1 || docker network create --internal --subnet 203.0.113.0/24 gx-inet >/dev/null
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
  in_dind sh -c 'pgrep -f "sh /scratch/run/start.sh" >/dev/null || (setsid sh /scratch/run/start.sh >/dev/null 2>&1 &)'
  say "up: Cosmos (log $SCRATCH/run/cosmos.log), the stand-in at 203.0.113.2"
  ;;
run)
  mkdir -p "$SCRATCH/out"
  in_dind sh -c 'docker rm -f -v rustfs >/dev/null 2>&1; docker run -d --name rustfs --network storage-net \
      -e RUSTFS_ACCESS_KEY=gx-admin -e RUSTFS_SECRET_KEY=Gx-test-admin-secret-0420 '"$RUSTFS_IMAGE"' >/dev/null'
  in_dind docker rm -f gx-harness-run >/dev/null 2>&1 || true
  in_dind docker run --rm --name gx-harness-run --network gx-lan --add-host cosmos.example.org:192.168.60.1 \
    -v /var/run/docker.sock:/var/run/docker.sock -v /repo:/repo:ro -v /scratch:/scratch -w /repo \
    -e ONLY="${ONLY:-}" -e PLACES="${PLACES:-home,internet,container}" -e OUT=/scratch/out -e NODE_TLS_REJECT_UNAUTHORIZED=0 \
    gx-harness node scripts/fork-guide-examples.mjs
  ;;
clean)
  touch "$SCRATCH/run/stop" 2>/dev/null || true
  docker rm -f -v "$PREFIX-dind" >/dev/null 2>&1 || true
  docker network rm "$PREFIX-out" >/dev/null 2>&1 || true
  [ -d "$SCRATCH" ] && docker run --rm -v "$(dirname "$SCRATCH"):/p" "$DIND_IMAGE" rm -rf "/p/$(basename "$SCRATCH")"
  say "cleaned"
  ;;
*) echo "usage: $0 up|run|clean" >&2; exit 2 ;;
esac
