#!/bin/sh
# Fork addition: the reachability watch (src/reachwatch) for real, on a
# throwaway instance, never the production one and never the real
# Cloudflare or a real router. Everything is on Docker networks made with
# --internal (no way out):
#
#   home 192.168.1.0/24:  the test Cosmos (.10), the Telegram stand-in (.20),
#                         the router's inside (.254), the browser (.30)
#   wan  203.0.113.0/24:  the router's outside (.7, the "public address"),
#                         the Cloudflare stand-in (.50, also answering as
#                         173.245.48.5, one of Cloudflare's addresses)
#
# The path of a request of the watch: the test Cosmos asks the stand-in's
# name server for example.org (173.245.48.5), connects there through the
# router (SNAT to 203.0.113.7), the stand-in passes it on as Cloudflare's
# proxy does to 203.0.113.7:2053, from 173.245.48.5, and the router forwards
# it (DNAT) to the test Cosmos only while the stand-in router's UPnP table
# (scripts/fork-fake-igd.go, IGD_NAT_CHAIN) holds the forward. Without it the
# router drops the connection, and the stand-in answers 522.
#
#   sh scripts/fork-reach-watch-e2e.sh up      build and start everything, set up the instance
#   sh scripts/fork-reach-watch-e2e.sh ctl ... run a command in the control container (curl to the stand-ins)
#   sh scripts/fork-reach-watch-e2e.sh clean   remove what it started
#
# Needs ./static built (npx vite build --base=/cosmos-ui/). Variables: PREFIX
# (default cosmos-watch), SCRATCH (default /storage/dev/.cache/$PREFIX/e2e),
# GO_IMAGE, BOX_IMAGE (an image with ip and iptables: docker:29-dind),
# NODE_IMAGE, GO_MOD_CACHE, GO_BUILD_CACHE.
set -eu

PREFIX="${PREFIX:-cosmos-watch}"
SCRATCH="${SCRATCH:-/storage/dev/.cache/$PREFIX/e2e}"
GO_IMAGE="${GO_IMAGE:-golang:1.27}"
BOX_IMAGE="${BOX_IMAGE:-docker:29-dind}"
NODE_IMAGE="${NODE_IMAGE:-node:22}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
HOME_NET="$PREFIX-home"
WAN_NET="$PREFIX-wan"
PASSWORD='Test-Passw0rd!x'

clean() {
	for c in app router edge telegram ctl browser; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
	docker network rm "$HOME_NET" "$WAN_NET" >/dev/null 2>&1 || true
	if [ -d "$SCRATCH" ]; then
		docker run --rm -v "$(dirname "$SCRATCH"):/scratch" "$BOX_IMAGE" rm -rf "/scratch/$(basename "$SCRATCH")" >/dev/null 2>&1 || true
	fi
}

ctl() { docker exec "$PREFIX-ctl" "$@"; }

case "${1:-}" in
clean) clean; exit 0 ;;
ctl) shift; ctl "$@"; exit $? ;;
up) ;;
*) echo "usage: $0 up|ctl ...|clean"; exit 2 ;;
esac

clean
mkdir -p "$SCRATCH/bin" "$SCRATCH/config" "$SCRATCH/shared/edge" "$SCRATCH/shared/tg"
caches=""
[ -n "${GO_MOD_CACHE:-}" ] && caches="$caches -v $GO_MOD_CACHE:/go/pkg/mod"
[ -n "${GO_BUILD_CACHE:-}" ] && caches="$caches -v $GO_BUILD_CACHE:/root/.cache/go-build"

echo "== building the server and the stand-ins"
# shellcheck disable=SC2086
docker run --rm --name "$PREFIX-build" -v "$REPO:/src:ro" -v "$SCRATCH/bin:/out" $caches -w /src -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c '
	set -e
	mkdir -p /tmp/build && cp -r src go.mod go.sum /tmp/build/ && mkdir -p /tmp/build/scripts && cp scripts/fork-fake-*.go /tmp/build/scripts/
	cd /tmp/build
	go build -o /out/cosmos src/*.go
	go build -o /out/fake-igd scripts/fork-fake-igd.go
	go build -o /out/fake-edge scripts/fork-fake-edge.go
	go build -o /out/fake-telegram scripts/fork-fake-telegram.go
	echo "{\"version\": \"0.0.0-watch-test\"}" > /out/meta.json
'
cp -r "$REPO/static" "$SCRATCH/bin/static"

docker network create --internal --subnet 192.168.1.0/24 "$HOME_NET" >/dev/null
docker network create --internal --subnet 203.0.113.0/24 "$WAN_NET" >/dev/null

echo "== the router: NAT both ways, forwards from the stand-in's UPnP table"
docker run -d --name "$PREFIX-router" --network "$HOME_NET" --ip 192.168.1.254 --cap-add NET_ADMIN --sysctl net.ipv4.ip_forward=1 \
	-v "$SCRATCH/bin:/work:ro" --entrypoint sh "$BOX_IMAGE" -c 'sleep 3600' >/dev/null
docker network connect --ip 203.0.113.7 "$WAN_NET" "$PREFIX-router"
docker exec "$PREFIX-router" sh -c '
	set -e
	WAN=$(ip -o -4 addr show | awk "/203\.0\.113\.7\// {print \$2}")
	iptables -t nat -N IGD
	iptables -t nat -A PREROUTING -i "$WAN" -j IGD
	iptables -t nat -A POSTROUTING -o "$WAN" -s 192.168.1.0/24 -j SNAT --to-source 203.0.113.7
	# no forward: the router keeps the port closed without a word (522 at Cloudflare)
	iptables -A INPUT -i "$WAN" -p tcp --dport 2053 -j DROP
	ip route add 173.245.48.5/32 via 203.0.113.50
'
docker exec -d "$PREFIX-router" sh -c 'IGD_IP=192.168.1.254 IGD_NAT_CHAIN=IGD /work/fake-igd > /tmp/igd.log 2>&1'

echo "== the Cloudflare stand-in (edge and name server) and the Telegram stand-in"
docker run -d --name "$PREFIX-edge" --network "$WAN_NET" --ip 203.0.113.50 --cap-add NET_ADMIN \
	-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared/edge:/shared" --entrypoint sh "$BOX_IMAGE" \
	-c 'ip addr add 173.245.48.5/32 dev eth0 && exec /work/fake-edge 203.0.113.7:2053 173.245.48.5 api.telegram.org=192.168.1.20' >/dev/null
docker run -d --name "$PREFIX-telegram" --network "$HOME_NET" --ip 192.168.1.20 \
	-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared/tg:/shared" --entrypoint /work/fake-telegram "$BOX_IMAGE" >/dev/null
for i in 1 2 3 4 5 6 7 8 9 10; do
	[ -s "$SCRATCH/shared/edge/ca.pem" ] && [ -s "$SCRATCH/shared/tg/ca.pem" ] && break
	sleep 1
done
cat "$SCRATCH/shared/edge/ca.pem" "$SCRATCH/shared/tg/ca.pem" > "$SCRATCH/shared/roots.pem"
echo "nameserver 173.245.48.5" > "$SCRATCH/shared/resolv.conf"

echo "== the control container (both networks)"
docker run -d --name "$PREFIX-ctl" --network "$HOME_NET" --ip 192.168.1.40 "$GO_IMAGE" sleep 3600 >/dev/null
docker network connect --ip 203.0.113.40 "$WAN_NET" "$PREFIX-ctl"

echo "== the test Cosmos"
docker run -d --name "$PREFIX-app" --hostname cosmos-watch-app --network "$HOME_NET" --ip 192.168.1.10 --cap-add NET_ADMIN \
	-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared:/shared:ro" -v "$SCRATCH/config:/config" -v "$SCRATCH/shared/resolv.conf:/etc/resolv.conf:ro" -w /work \
	-e COSMOS_CONFIG_FOLDER=/config/ -e COSMOS_HTTP_PORT=2052 -e COSMOS_HTTPS_PORT=2053 -e SSL_CERT_FILE=/shared/roots.pem -e TZ=UTC \
	--entrypoint sh "$BOX_IMAGE" -c 'ip route replace default via 192.168.1.254; while true; do /work/cosmos; sleep 1; done' >/dev/null

wait_for() {
	for i in $(seq 1 60); do
		if ctl curl -sk -o /dev/null -w '%{http_code}' "$1" 2>/dev/null | grep -q '^[234]'; then return 0; fi
		sleep 1
	done
	echo "  FAIL $1 does not answer"; return 1
}
wait_for http://192.168.1.10:2052/cosmos/api/status
step() { ctl curl -s -o /dev/null -w "step: %{http_code}\n" -H 'Content-Type: application/json' -d "$1" http://192.168.1.10:2052/cosmos/api/newInstall; }
step '{"step":"2","mongodbMode":"Create"}'
step '{"step":"3","httpsCertificateMode":"SELFSIGNED","hostname":"example.org","allowHTTPLocalIPAccess":true}'
step "{\"step\":\"4\",\"nickname\":\"admin\",\"password\":\"$PASSWORD\",\"email\":\"admin@example.org\"}"
step '{"step":"5"}' || true
sleep 3

echo "== the instance's settings: the panel and one app through Cloudflare, UPnP, Telegram"
docker stop "$PREFIX-app" >/dev/null
# a bot token of the right shape, made here (it is nobody's)
token="$(printf '%s:%s' 123456789 "AAFake$(printf 'x%.0s' $(seq 1 29))")"
docker run --rm -v "$SCRATCH/config:/config" -e TOKEN="$token" "$NODE_IMAGE" node -e '
	const fs = require("fs");
	const file = "/config/cosmos.config.json";
	const c = JSON.parse(fs.readFileSync(file, "utf8"));
	const h = c.HTTPConfig;
	h.Hostname = "example.org";
	h.HTTPSPort = "2053"; h.HTTPPort = "2052";
	h.AllowHTTPLocalIPAccess = true;
	h.UPnP = { ...(h.UPnP || {}), Enabled: true };
	h.Cloudflare = { ...(h.Cloudflare || {}), Shielded: ["example.org", "app.example.org"], TrustRanges: true, Watch: { Minutes: 2 } };
	h.ProxyConfig = h.ProxyConfig || {};
	h.ProxyConfig.Routes = [{ Name: "app", Description: "test app", Mode: "SERVAPP", UseHost: true, Host: "app.example.org",
		Target: "http://192.168.1.20:8080", SmartShield: { Enabled: true } },
		{ Name: "direct", Description: "served directly", Mode: "SERVAPP", UseHost: true, Host: "direct.example.org", Target: "http://192.168.1.20:8080" }];
	c.Telegram = { Enabled: true, BotToken: process.env.TOKEN, ChatID: "100200300", ServerName: "test server", Categories: { system: true, security: true } };
	fs.writeFileSync(file, JSON.stringify(c, null, 2));
'
docker start "$PREFIX-app" >/dev/null
wait_for https://192.168.1.10:2053/cosmos/api/status
echo "== up: the panel is https://example.org:2053 (from the browser container: --add-host example.org:192.168.1.10)"
