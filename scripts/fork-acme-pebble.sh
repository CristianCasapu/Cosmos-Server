#!/bin/sh
# Fork addition: orders real certificates with the server's own code from a
# real ACME certificate authority, without touching Let's Encrypt, Cloudflare
# or anyone's DNS: Pebble (Let's Encrypt's test CA) and the stand-in for
# Cloudflare (scripts/fork-fake-cloudflare.go, which is also the name server
# of what it holds) run on a Docker network made with --internal, and a
# throwaway Cosmos on the same network is driven through its API by
# scripts/fork-acme-pebble-check.go.
#
# What it shows: the first order for a domain connected through Cloudflare
# (the wildcard pair), a deeper name added later, a restart that orders
# nothing, a name switched to "through Cloudflare" and back, a refused token
# (HTTPS stays up with the previous certificate, the reason is told, the wait
# survives a restart), "Renew now", a domain at another DNS provider through
# a delegated challenge, a name that would need HTTP-01 (no order), and a
# renewal by itself when due (Pebble is told to issue for four minutes), and
# the renewals planned by the CA's renewal windows once they are asked for.
#
# How the test server is pointed at Pebble: FORK_ACME_TEST_DIRECTORY together
# with FORK_ACME_TEST_CA (src/certs/ca.go). Nothing else differs from a
# released server: same binary, same code path.
#
# Run it from the repository's root on a machine with Docker (no Go needed):
#
#   sh scripts/fork-acme-pebble.sh            # build, run, clean up
#   KEEP=1 sh scripts/fork-acme-pebble.sh     # leave everything running to look at
#   sh scripts/fork-acme-pebble.sh clean      # remove what a KEEP=1 run left
#
# PREFIX names the containers, the network and the scratch folder (default
# cosmos-acme); SCRATCH is where the build goes (default /tmp/$PREFIX);
# GO_MOD_CACHE and GO_BUILD_CACHE may point at caches to reuse. It takes
# about eight minutes. Exit code 0 means every check passed.
set -eu

PREFIX="${PREFIX:-cosmos-acme}"
SCRATCH="${SCRATCH:-/tmp/$PREFIX}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
GO_IMAGE="${GO_IMAGE:-golang:1.27}"
PEBBLE_IMAGE="${PEBBLE_IMAGE:-ghcr.io/letsencrypt/pebble:latest}"
NET="$PREFIX-net"
SUBNET="${SUBNET:-172.31.57}"

clean() {
	docker rm -f -v "$PREFIX-app" "$PREFIX-pebble" "$PREFIX-fake" "$PREFIX-check" "$PREFIX-build" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
	# the containers ran as root: their files are removed from a container too
	if [ -d "$SCRATCH" ]; then
		docker run --rm -v "$SCRATCH:/scratch" "$GO_IMAGE" sh -c 'rm -rf /scratch/config /scratch/shared /scratch/bin' >/dev/null 2>&1 || true
		rmdir "$SCRATCH" 2>/dev/null || true
	fi
}

if [ "${1:-}" = "clean" ]; then
	clean
	exit 0
fi

clean
mkdir -p "$SCRATCH/bin" "$SCRATCH/shared" "$SCRATCH/config"

caches=""
[ -n "${GO_MOD_CACHE:-}" ] && caches="$caches -v $GO_MOD_CACHE:/go/pkg/mod"
[ -n "${GO_BUILD_CACHE:-}" ] && caches="$caches -v $GO_BUILD_CACHE:/root/.cache/go-build"

echo "== building the server, the stand-in and the checks"
# shellcheck disable=SC2086
docker run --rm --name "$PREFIX-build" -v "$REPO:/src:ro" -v "$SCRATCH/bin:/out" $caches -w /src -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c '
	set -e
	mkdir -p /tmp/build && cp -r src go.mod go.sum /tmp/build/ && mkdir -p /tmp/build/scripts && cp scripts/fork-fake-cloudflare.go scripts/fork-acme-pebble-check.go /tmp/build/scripts/
	cd /tmp/build
	cp src/update.go src/launcher/update.go
	go build -o /out/cosmos src/*.go
	go build -o /out/fake-cloudflare scripts/fork-fake-cloudflare.go
	go build -o /out/acme-check scripts/fork-acme-pebble-check.go
	echo "{\"version\": \"0.0.0-acme-test\"}" > /out/meta.json
	# the server wants its pages next to it; the checks only use its API
	mkdir -p /out/static && echo "<!doctype html><title>test</title>" > /out/static/index.html
'

docker network create --internal --subnet "$SUBNET.0/24" "$NET" >/dev/null

echo "== the stand-in for Cloudflare (API and name server) and Pebble"
docker run -d --name "$PREFIX-fake" --network "$NET" --ip "$SUBNET.2" --network-alias api.cloudflare.com \
	-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared:/shared" "$GO_IMAGE" /work/fake-cloudflare >/dev/null

# Pebble's certificates last four minutes; its own API certificate is signed
# by the test authority that ships in its image
docker create --name "$PREFIX-pebble" --network "$NET" --ip "$SUBNET.3" --network-alias pebble \
	-e PEBBLE_VA_NOSLEEP=1 -e PEBBLE_WFE_NONCEREJECT=0 -e PEBBLE_AUTHZREUSE=0 \
	"$PEBBLE_IMAGE" -config /test/config/short.json -dnsserver "$SUBNET.2:53" >/dev/null
docker cp "$PREFIX-pebble:/test/certs/pebble.minica.pem" "$SCRATCH/shared/pebble.minica.pem"
cat > "$SCRATCH/short.json" <<'EOF'
{
  "pebble": {
    "listenAddress": "0.0.0.0:14000",
    "managementListenAddress": "0.0.0.0:15000",
    "certificate": "test/certs/localhost/cert.pem",
    "privateKey": "test/certs/localhost/key.pem",
    "httpPort": 5002,
    "tlsPort": 5001,
    "ocspResponderURL": "",
    "externalAccountBindingRequired": false,
    "retryAfter": { "authz": 1, "order": 1 },
    "keyAlgorithm": "ecdsa",
    "profiles": {
      "default": { "description": "four minutes", "validityPeriod": 240 }
    }
  }
}
EOF
docker cp "$SCRATCH/short.json" "$PREFIX-pebble:/test/config/short.json"
rm -f "$SCRATCH/short.json"
docker start "$PREFIX-pebble" >/dev/null

check() {
	docker run --rm --name "$PREFIX-check" --network "$NET" --ip "$SUBNET.20" --dns "$SUBNET.2" \
		-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared:/shared" \
		-e SERVER="$SUBNET.10" -e FAKE="http://$SUBNET.2:8080" -e FAKE_DNS="$SUBNET.2:53" \
		"$GO_IMAGE" /work/acme-check "$@"
}

# what the test server trusts as "the system's" authorities: Pebble's issuing
# root (so that its certificates count as trusted, like Let's Encrypt's on a
# real server) and the stand-in's authority (for api.cloudflare.com)
for i in 1 2 3 4 5 6 7 8 9 10; do
	[ -s "$SCRATCH/shared/ca.pem" ] && break
	sleep 1
done
check roots

# read by the test server at each start: the checks switch the CA's renewal
# windows on for the last part only (src/certs/ca.go, FORK_ACME_TEST_NO_ARI)
echo "FORK_ACME_TEST_NO_ARI=1" > "$SCRATCH/shared/app.env"

echo "== the test server"
docker run -d --name "$PREFIX-app" --network "$NET" --ip "$SUBNET.10" --dns "$SUBNET.2" \
	-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared:/shared:ro" -v "$SCRATCH/config:/config" -w /work \
	-e COSMOS_CONFIG_FOLDER=/config/ -e COSMOS_HTTP_PORT=2052 -e COSMOS_HTTPS_PORT=2053 \
	-e SSL_CERT_FILE=/shared/roots.pem \
	-e FORK_ACME_TEST_DIRECTORY=https://pebble:14000/dir -e FORK_ACME_TEST_CA=/shared/pebble.minica.pem -e FORK_ACME_TEST_TICK=5 \
	"$GO_IMAGE" sh -c 'while true; do set -a; . /shared/app.env; set +a; /work/cosmos; sleep 1; done' >/dev/null

result=0
check run || result=$?

echo "== what is on disk and in the log"
problems=0
modes="$(docker exec "$PREFIX-app" sh -c 'stat -c "%a %n" /config/acme-account-test.json /config/acme-state.json' 2>&1 || true)"
echo "$modes"
if [ "$(echo "$modes" | grep -c '^600 ')" != "2" ]; then
	echo "  FAIL the account and the state file are readable by their owner only"
	problems=1
else
	echo "  ok   the account and the state file are readable by their owner only"
fi
if docker exec "$PREFIX-app" test -e /config/acme-account.json; then
	echo "  FAIL the test authority's account is kept apart from Let's Encrypt's"
	problems=1
else
	echo "  ok   the test authority's account is kept apart from Let's Encrypt's"
fi
token='cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789'
if docker logs "$PREFIX-app" 2>&1 | grep -q "$token" || docker exec "$PREFIX-app" grep -q "$token" /config/acme-state.json; then
	echo "  FAIL the API token is in the server's log or in the state file"
	problems=1
else
	echo "  ok   the API token is neither in the server's log nor in the state file"
fi
if docker logs "$PREFIX-pebble" 2>&1 | grep -q 'is a replacement of'; then
	echo "  ok   the authority saw a renewal that names the certificate it replaces"
else
	echo "  note the authority's log does not mention a replaced certificate"
fi

if [ "$result" != "0" ] || [ "$problems" != "0" ]; then
	echo "== the server's last lines"
	docker logs "$PREFIX-app" 2>&1 | grep -i 'certificat\|zones:\|acme' | tail -40 || true
	result=1
fi

if [ "${KEEP:-}" = "1" ]; then
	echo "left running: $PREFIX-app, $PREFIX-pebble, $PREFIX-fake on $NET (remove with: sh scripts/fork-acme-pebble.sh clean)"
else
	clean
fi
exit "$result"
