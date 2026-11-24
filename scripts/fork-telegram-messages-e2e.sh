#!/bin/sh
# Fork addition (plan 48): the Telegram messages end to end. A throwaway
# Cosmos (built from this tree, its panel from ./static), a Docker in Docker
# of its own, and the stand-in for Telegram (scripts/fork-fake-telegram.go)
# answering as api.telegram.org, all on an --internal network: nothing ever
# reaches the real Telegram. Real events are caused and the messages the
# stand-in received are printed as a chat shows them.
#
# What it may touch on the host: it starts containers and one --internal
# network named $PREFIX-*, writes under $SCRATCH, and nothing else. The test
# Cosmos runs WITHOUT extra rights (no --privileged, no added capability, no
# device): its test disk is a size-limited tmpfs (2 GiB of RAM at most), it
# sees no block device of the host, and its S.M.A.R.T. watch is switched off
# in its configuration. The test's Docker is rootless Docker in Docker
# (docker:29-dind-rootless): its daemon runs as uid 1000, it gets only the
# TUN device, no host disk, no capability added and no writable kernel
# setting; it needs seccomp, AppArmor and the masked /proc paths unconfined.
# Nothing runs --privileged. It is a test bed: do not run it on a production
# server (it was run on the owner's server only in this narrowed form), and
# never point it at a real Telegram bot.
#
#   sh scripts/fork-telegram-messages-e2e.sh up        build and start (needs ./static and busybox:1.36 on the host)
#   sh scripts/fork-telegram-messages-e2e.sh events    cause the events: a burst of failed sign-ins, an app that
#                                                      keeps stopping, an app stopped and started, a disk alert,
#                                                      a restart of Cosmos
#   sh scripts/fork-telegram-messages-e2e.sh events2   plan 48's watches that can be faked here: the test's own
#                                                      Docker paused for four minutes (Docker not answering, then
#                                                      again), a scheduled job that fails and then works again
#   sh scripts/fork-telegram-messages-e2e.sh events3   the test disk (a 2 GiB tmpfs at /mnt/data) filled by 1.25 GiB
#                                                      in two minutes, then emptied: "filling fast" opens, then ends
#                                                      (the server is built with the tag watch_fastclock: samples
#                                                      every 20 s over 4 min, and COSMOS_FILL_TEST_MOUNTS names the
#                                                      test disk; a release never has the tag)
#   sh scripts/fork-telegram-messages-e2e.sh messages  print what the stand-in received (also to $SCRATCH/messages.txt)
#   sh scripts/fork-telegram-messages-e2e.sh clean     remove everything it made
#
# Options: PREFIX (cosmos-tgmsg), SCRATCH (/storage/dev/.cache/$PREFIX/e2e),
# SUBNET (192.168.1; the first three parts of a free /24). The browser check
# scripts/fork-ui-check-telegram-messages.mjs runs on the same network
# (its header has the command).
set -eu

PREFIX="${PREFIX:-cosmos-tgmsg}"
SCRATCH="${SCRATCH:-/storage/dev/.cache/$PREFIX/e2e}"
SUBNET="${SUBNET:-192.168.1}"
GO_IMAGE="${GO_IMAGE:-golang:1.27}"
BOX_IMAGE="${BOX_IMAGE:-docker:29-dind}"
DIND_IMAGE="${DIND_IMAGE:-docker:29-dind-rootless}"
NODE_IMAGE="${NODE_IMAGE:-node:22}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
NET="$PREFIX-net"
PASSWORD='Test-Passw0rd!x'
APP="$SUBNET.10"
FAKE="$SUBNET.20"

clean() {
	for c in app dind telegram ctl attacker browser; do docker rm -f -v "$PREFIX-$c" >/dev/null 2>&1 || true; done
	docker network rm "$NET" >/dev/null 2>&1 || true
	if [ -d "$SCRATCH" ]; then
		docker run --rm -v "$(dirname "$SCRATCH"):/scratch" --entrypoint rm "$BOX_IMAGE" -rf "/scratch/$(basename "$SCRATCH")" >/dev/null 2>&1 || true
	fi
}
ctl() { docker exec "$PREFIX-ctl" "$@"; }
inner() { docker exec "$PREFIX-dind" docker -H tcp://127.0.0.1:2375 "$@"; }
api() { # api METHOD PATH [JSON]: as the admin, with a session of the control container
	ctl curl -s -m 20 -b /tmp/jar -c /tmp/jar -X "$1" -H 'Content-Type: application/json' ${3:+-d "$3"} "http://cosmos.example.org:2052$2"
}
login() {
	ctl rm -f /tmp/jar
	api POST /cosmos/api/login "{\"nickname\":\"admin\",\"password\":\"$PASSWORD\"}" >/dev/null
	api POST /cosmos/api/sudo "{\"password\":\"$PASSWORD\"}" >/dev/null
}
wait_for() {
	for i in $(seq 1 90); do
		if ctl curl -s -o /dev/null -w '%{http_code}' "$1" 2>/dev/null | grep -q '^[234]'; then return 0; fi
		sleep 1
	done
	echo "  FAIL $1 does not answer"; return 1
}

up() {
	clean
	test -s "$REPO/static/index.html" || { echo "build ./static first (npx vite build --base=/cosmos-ui/)"; exit 2; }
	mkdir -p "$SCRATCH/bin" "$SCRATCH/config" "$SCRATCH/shared"
	echo "== building the server and the stand-in"
	docker run --rm --name "$PREFIX-build" -v "$REPO:/src:ro" -v "$SCRATCH/bin:/out" \
		-v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
		-w /src -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "$GO_IMAGE" sh -c '
		set -e
		mkdir -p /tmp/build/scripts && cp -r src go.mod go.sum /tmp/build/ && cp scripts/fork-fake-telegram.go /tmp/build/scripts/
		cd /tmp/build
		go build -tags watch_fastclock -o /out/cosmos src/*.go
		go build -o /out/fake-telegram scripts/fork-fake-telegram.go
		echo "{\"version\": \"0.0.0-tgmsg-test\"}" > /out/meta.json
	'
	cp -r "$REPO/static" "$SCRATCH/bin/static"

	docker network create --internal --subnet "$SUBNET.0/24" "$NET" >/dev/null
	echo "== the stand-in for Telegram, a Docker of the test's own, a control container"
	docker run -d --name "$PREFIX-telegram" --network "$NET" --ip "$FAKE" --network-alias api.telegram.org \
		-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared:/shared" --entrypoint /work/fake-telegram "$BOX_IMAGE" >/dev/null
	# rootless Docker in Docker: its daemon runs as uid 1000 in a user
	# namespace; no --privileged, no capability added; only the TUN device
	# (its network) and the three security options rootless Docker needs
	docker run -d --name "$PREFIX-dind" --network "$NET" --ip "$SUBNET.30" --device /dev/net/tun \
		--security-opt seccomp=unconfined --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
		-e DOCKER_TLS_CERTDIR= "$DIND_IMAGE" --tls=false >/dev/null
	docker run -d --name "$PREFIX-ctl" --network "$NET" --ip "$SUBNET.40" "$GO_IMAGE" sleep 7200 >/dev/null
	# the failed sign-ins come from another address: SmartShield then throttles that one, not the admin's
	docker run -d --name "$PREFIX-attacker" --network "$NET" --ip "$SUBNET.66" "$GO_IMAGE" sleep 7200 >/dev/null
	for i in $(seq 1 30); do [ -s "$SCRATCH/shared/ca.pem" ] && inner info >/dev/null 2>&1 && break; sleep 1; done
	docker save busybox:1.36 | docker exec -i "$PREFIX-dind" docker -H tcp://127.0.0.1:2375 load >/dev/null

	echo "== the test Cosmos"
	# no extra rights: the test disk (events3) is a size-limited tmpfs
	docker run -d --name "$PREFIX-app" --hostname cosmos-tgmsg-app --network "$NET" --ip "$APP" --network-alias cosmos.example.org \
		--mount type=tmpfs,destination=/mnt/data,tmpfs-size=2147483648 -e COSMOS_FILL_TEST_MOUNTS=/mnt/data \
		-v "$SCRATCH/bin:/work:ro" -v "$SCRATCH/shared:/shared:ro" -v "$SCRATCH/config:/config" -w /work \
		-e COSMOS_CONFIG_FOLDER=/config/ -e COSMOS_HTTP_PORT=2052 -e COSMOS_HTTPS_PORT=2053 -e SSL_CERT_FILE=/shared/ca.pem -e TZ=UTC \
		-e DOCKER_HOST="tcp://$SUBNET.30:2375" \
		--entrypoint sh "$BOX_IMAGE" -c 'while true; do /work/cosmos; sleep 1; done' >/dev/null
	wait_for "http://cosmos.example.org:2052/cosmos/api/status"
	step() { ctl curl -s -o /dev/null -m 30 -H 'Content-Type: application/json' -d "$1" "http://$APP:2052/cosmos/api/newInstall" || true; }
	step '{"step":"2","mongodbMode":"Create"}'
	step '{"step":"3","httpsCertificateMode":"DISABLED","hostname":"cosmos.example.org"}'
	step "{\"step\":\"4\",\"nickname\":\"admin\",\"password\":\"$PASSWORD\",\"email\":\"admin@example.org\"}"
	step '{"step":"5"}'
	sleep 3
	docker stop "$PREFIX-app" >/dev/null

	echo "== the settings: Telegram on, failed sign-ins switched on by themselves, the disk alert at 1 %, no S.M.A.R.T. watch"
	# a bot token of the right shape, made here (it is nobody's)
	token="$(printf '%s:%s' 123456789 "AAFake$(printf 'x%.0s' $(seq 1 29))")"
	docker run --rm -v "$SCRATCH/config:/config" -e TOKEN="$token" "$NODE_IMAGE" node -e '
		const fs = require("fs");
		const file = "/config/cosmos.config.json";
		const c = JSON.parse(fs.readFileSync(file, "utf8"));
		c.HTTPConfig.Hostname = "cosmos.example.org";
		c.Telegram = { Enabled: true, BotToken: process.env.TOKEN, ChatID: "100200300", ServerName: "test server",
			Kinds: { "auth.login.failed": true } };
		// a scheduled job that fails until /config/ok exists (events2)
		c.CRON = { ...(c.CRON || {}), flaky: { Enabled: true, Name: "flaky", Crontab: "*/20 * * * * *", Command: "test -f /config/ok" } };
		for (const [name, alert] of Object.entries(c.MonitoringAlerts || {})) {
			if (name.startsWith("Disk Full Notification")) { alert.Condition.Value = 1; alert.Enabled = true; }
		}
		// the test Cosmos reads no disk of the host
		c.Watch = { ...(c.Watch || {}), SMARTOff: true };
		fs.writeFileSync(file, JSON.stringify(c, null, 2));
	'
	docker start "$PREFIX-app" >/dev/null
	wait_for "http://cosmos.example.org:2052/cosmos/api/status"
	echo "== up: the panel is http://cosmos.example.org:2052 on $NET (admin / $PASSWORD); the stand-in's control port is http://$FAKE:8080"
}

events() {
	login
	echo "== a burst of failed sign-ins from one address"
	for i in $(seq 1 25); do
		docker exec "$PREFIX-attacker" curl -s -o /dev/null -m 10 -X POST -H 'Content-Type: application/json' \
			-d '{"nickname":"admin<b>@everyone","password":"wrong-'"$i"'"}' "http://cosmos.example.org:2052/cosmos/api/login" || true
	done
	echo "== an app that keeps stopping"
	inner rm -f loopy >/dev/null 2>&1 || true
	inner run -d --name loopy --restart always busybox:1.36 sh -c 'sleep 2; exit 3' >/dev/null
	echo "== an app stopped and started from the panel"
	inner rm -f quiet >/dev/null 2>&1 || true
	inner run -d --name quiet busybox:1.36 sleep 100000 >/dev/null
	sleep 5
	login
	api POST /cosmos/api/servapps/quiet/manage/stop >/dev/null || true
	sleep 2
	api POST /cosmos/api/servapps/quiet/manage/start >/dev/null || true
	echo "== waiting for the app to stop four times and the disk alert (the monitoring runs every minute)"
	sleep 90
	inner rm -f loopy >/dev/null 2>&1 || true
	echo "== a restart of Cosmos from the panel"
	login
	api POST /cosmos/api/restart >/dev/null || true
	sleep 15
	wait_for "http://cosmos.example.org:2052/cosmos/api/status"
	sleep 10
}

events2() {
	echo "== a scheduled job that fails (every 20 s) and then works again"
	sleep 45
	docker run --rm -v "$SCRATCH/config:/config" "$BOX_IMAGE" touch /config/ok
	sleep 45
	echo "== the test's own Docker paused for four minutes, then running again"
	docker pause "$PREFIX-dind" >/dev/null
	sleep 240
	docker unpause "$PREFIX-dind" >/dev/null
	sleep 60
}

events3() {
	echo "== the test disk filled by 1.25 GiB in two minutes"
	for i in $(seq 1 10); do
		docker exec "$PREFIX-app" dd if=/dev/zero of=/mnt/data/fill-$i bs=1M count=128 conv=fsync 2>/dev/null
		sleep 10
	done
	echo "== waiting for the warning (a sample every 20 s)"
	sleep 120
	echo "== emptied: the warning ends"
	docker exec "$PREFIX-app" sh -c 'rm -f /mnt/data/fill-*; sync'
	sleep 300
}

messages() {
	ctl curl -s "http://$FAKE:8080/messages" > "$SCRATCH/messages.json"
	docker run --rm -v "$SCRATCH:/s" "$NODE_IMAGE" node -e '
		const list = JSON.parse(require("fs").readFileSync("/s/messages.json", "utf8"));
		const out = list.map((m) => [
			"#" + m.id + (m.reply_to ? " (a reply to #" + m.reply_to + ")" : "") + " · " + (m.silent ? "silent" : "with sound")
				+ " · parse mode: " + (m.parse_mode || "none") + " · " + m.at,
			m.text,
			...(m.buttons || []).map((b) => "[ " + b.text + " ] -> " + b.url),
		].join("\n")).join("\n\n----\n\n");
		require("fs").writeFileSync("/s/messages.txt", out + "\n");
		console.log(out);
	'
	ctl curl -s "http://$FAKE:8080/pins"; echo
}

case "${1:-}" in
up) up ;;
events) events ;;
events2) events2 ;;
events3) events3 ;;
messages) messages ;;
clean) clean ;;
ctl) shift; ctl "$@" ;;
*) echo "usage: $0 up|events|events2|events3|messages|clean|ctl ..."; exit 2 ;;
esac
