#!/bin/bash
# shellcheck disable=SC2016,SC2012 # the snippets in single quotes run inside the test machines
# Fork addition: the test bed of the install scripts (fork/install/).
#
#   bash scripts/fork-install-e2e.sh fast     shellcheck + one machine (Debian 13):
#                                             install, refusals, a tampered zip, uninstall
#   bash scripts/fork-install-e2e.sh full     every case on every system (see CASES)
#   bash scripts/fork-install-e2e.sh one <system> <case>   one case, e.g. one debian13 fresh
#   bash scripts/fork-install-e2e.sh clean    remove what a KEEP=1 run left
#
# What it builds and starts, all named $PREFIX-* (default cosmos-inst) and
# removed at the end, also on failure and on Ctrl-C (KEEP=1 keeps them):
#   - test releases of this worktree: the server and the launcher built with
#     a TEST signing key in keys.go (made here with openssl, never the real
#     one) and an update source of example-owner/cosmos-fork, packaged by
#     scripts/fork-release-package.sh (what the release workflow runs) and
#     signed the way the workflow signs: 0.24.0-fork.900 (A), .901 (B, for the
#     update) and .902 (C, which does not start: for the rollback);
#   - a Docker in Docker of its own (docker:29-dind, privileged), and in it,
#     per system, a network, a stand-in for GitHub (scripts/fork-fake-github.go,
#     with a test CA the machines trust) and clean machines running systemd
#     (debian:12, debian:13, ubuntu:24.04, ubuntu:26.04 + systemd), privileged
#     inside that Docker only, never on the host's network or Docker socket.
# The machines reach Debian's, Ubuntu's and Docker's package servers through
# the Docker in Docker's own way out: the packages are real.
#
# Settings (environment): PREFIX, SCRATCH (default
# /storage/dev/.cache/$PREFIX/e2e; $RUNNER_TEMP/... on GitHub), SYSTEMS
# ("debian12 debian13 ubuntu2404 ubuntu2604"), CASES (the full list below),
# BUILD (a folder build.sh made, for its static/; else ./static or a UI
# build), KEEP=1, REUSE_DIND=1 (keep the running Docker in Docker and its
# images between runs while working on the scripts), JOBS (systems in
# parallel, default 4).
#
# Results: $RESULTS (default $SCRATCH-results, kept after the clean-up)
# /<system>-<case>.log and a summary; exit 1 when any case failed.
set -euo pipefail
export LC_ALL=C

HERE=$(cd -- "$(dirname -- "$0")/.." && pwd)
PREFIX=${PREFIX:-cosmos-inst}
if [ -n "${RUNNER_TEMP:-}" ]; then SCRATCH=${SCRATCH:-$RUNNER_TEMP/fork-install-e2e}; fi
SCRATCH=${SCRATCH:-/storage/dev/.cache/$PREFIX/e2e}
RESULTS=${RESULTS:-$SCRATCH-results}
SYSTEMS=${SYSTEMS:-debian12 debian13 ubuntu2404 ubuntu2604}
ALL_CASES="fresh refusals tampered dryrun unavailable locked netcut interrupt docker-there hop"
CASES=${CASES:-$ALL_CASES}
JOBS=${JOBS:-4}
REPO=example-owner/cosmos-fork
VA=0.24.0-fork.900 VB=0.24.0-fork.901 VC=0.24.0-fork.902
DIND=$PREFIX-dind
GOIMAGE=golang:1.27
NODEIMAGE=node:22

log() { printf '[e2e] %s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

image_of() {
	case $1 in
	debian12) echo debian:12 ;;
	debian13) echo debian:13 ;;
	ubuntu2404) echo ubuntu:24.04 ;;
	ubuntu2604) echo ubuntu:26.04 ;;
	*) die "unknown system $1" ;;
	esac
}

D() { docker exec -i "$DIND" docker "$@"; }

cleanup() {
	local rc=$?
	if [ "${KEEP:-0}" = 1 ]; then log "KEEP=1: left $DIND and $SCRATCH (clean with: $0 clean)"; return $rc; fi
	log "removing what the test bed started"
	docker rm -f -v "$DIND" "$PREFIX-build" "$PREFIX-ui" >/dev/null 2>&1 || true
	docker network rm "$PREFIX-net" >/dev/null 2>&1 || true
	docker image rm "$PREFIX-builder:1" >/dev/null 2>&1 || true
	# files written by containers as root
	if [ -d "$SCRATCH" ]; then
		docker run --rm -v "$(dirname "$SCRATCH"):/w" debian:13 rm -rf "/w/$(basename "$SCRATCH")" >/dev/null 2>&1 || rm -rf "$SCRATCH"
	fi
	return $rc
}

# ---- Builds (on the host's Docker, in throwaway containers; or natively on CI) ----
native_tools() { command -v go >/dev/null && command -v zip >/dev/null && command -v unzip >/dev/null && command -v openssl >/dev/null && command -v jq >/dev/null; }

in_go() { # command in the Go toolchain, the worktree at /src (read-only) and $SCRATCH at /scratch
	if native_tools; then (cd "$HERE" && SRC=$HERE SCR=$SCRATCH bash -c "$1"); return; fi
	docker run --rm --name "$PREFIX-build" -v "$HERE:/src:ro" -v "$SCRATCH:/scratch" \
		-v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
		-e SRC=/src -e SCR=/scratch -e CGO_ENABLED=0 -w /src "$GOIMAGE" bash -c "$1"
}
in_builder() { # zip, unzip, openssl, jq
	if native_tools; then (cd "$HERE" && SRC=$HERE SCR=$SCRATCH bash -c "$1"); return; fi
	if ! docker image inspect "$PREFIX-builder:1" >/dev/null 2>&1; then
		docker run --name "$PREFIX-build" debian:13 sh -c 'apt-get -qq update && apt-get -qq install -y --no-install-recommends zip unzip openssl jq git ca-certificates >/dev/null'
		docker commit "$PREFIX-build" "$PREFIX-builder:1" >/dev/null
		docker rm -f "$PREFIX-build" >/dev/null
	fi
	docker run --rm --name "$PREFIX-build" -v "$HERE:/src:ro" -v "$SCRATCH:/scratch" -e SRC=/src -e SCR=/scratch -w /src "$PREFIX-builder:1" bash -c "$1"
}

build_releases() {
	log "building the test releases in $SCRATCH"
	mkdir -p "$SCRATCH"/{bin,keys,gosrc,rel,stage,results,variants}
	# the test signing keys: one the releases are signed with, one other
	in_builder '
		set -e
		cd "$SCR/keys"
		for k in test other; do
			[ -f $k.pem ] || openssl genpkey -algorithm ed25519 -out $k.pem
			openssl pkey -in $k.pem -pubout -outform DER | tail -c 32 | base64 >$k.pub
		done'
	local pub
	pub=$(tr -d '\n' <"$SCRATCH/keys/test.pub")
	# a copy of the sources with the test key in keys.go
	rm -rf "$SCRATCH/gosrc"
	mkdir -p "$SCRATCH/gosrc"
	cp -r "$HERE/go.mod" "$HERE/go.sum" "$HERE/src" "$SCRATCH/gosrc/"
	sed -i "s|^\t\"[A-Za-z0-9+/]\{43\}=\", // slot 1.*|\t\"$pub\", // slot 1, the test bed's key|" "$SCRATCH/gosrc/src/forkrelease/keys.go"
	grep -qF "\"$pub\"" "$SCRATCH/gosrc/src/forkrelease/keys.go" || die "the test key did not go into keys.go"
	cp "$SCRATCH/gosrc/src/update.go" "$SCRATCH/gosrc/src/launcher/update.go"
	in_go '
		set -e
		cd "$SCR/gosrc"
		export CGO_ENABLED=0
		flags="-X=main.updateRepo='"$REPO"'"
		go build -ldflags "$flags" -o "$SCR/bin/cosmos" ./src/
		go build -ldflags "$flags" -o "$SCR/bin/cosmos-launcher" ./src/launcher/launcher.go ./src/launcher/update.go
		cd "$SRC"
		go build -o "$SCR/bin/fake-github" scripts/fork-fake-github.go
		go build -o "$SCR/bin/fake-igd" scripts/fork-fake-igd.go
		go build -o "$SCR/bin/helper" scripts/fork-install-e2e/helper.go'
	rm -rf "$SCRATCH/gosrc"

	# the panel
	local static=""
	if [ -n "${BUILD:-}" ] && [ -f "$BUILD/static/index.html" ]; then static=$BUILD/static
	elif [ -f "$HERE/static/index.html" ]; then static=$HERE/static
	else
		log "building the panel (vite) into ./static"
		docker run --rm --name "$PREFIX-ui" -v "$HERE:/src" -v /storage/dev/.cache/npm:/root/.npm -w /src "$NODEIMAGE" \
			sh -c 'npx vite build --base=/cosmos-ui/ >/dev/null'
		static=$HERE/static
	fi

	local v dir
	for v in $VA $VB $VC; do
		dir=$SCRATCH/stage/$v
		rm -rf "$dir"
		mkdir -p "$dir/build/images"
		cp "$SCRATCH/bin/cosmos" "$SCRATCH/bin/cosmos-launcher" "$dir/build/"
		cp "$HERE/start.sh" "$HERE/LICENCE" "$HERE/FORK-LICENCE.md" "$HERE/Logo.png" "$dir/build/"
		cp -r "$static" "$dir/build/static"
		printf '{\n  "version": "%s",\n  "upstream": "0.24.0",\n  "commit": "test",\n  "buildDate": "test"\n}\n' "$v" >"$dir/build/meta.json"
		if [ "$v" = "$VC" ]; then
			# C does not start: the launcher must go back by itself
			printf '#!/bin/sh\necho "test release C: does not start" >&2\nexit 3\n' >"$dir/build/cosmos"
		fi
		chmod 755 "$dir/build/cosmos" "$dir/build/cosmos-launcher" "$dir/build/start.sh"
	done
	# the release's own fork/ with the test stubs in place of the
	# protection pieces (their interface, not their work, is tested here)
	in_builder '
		set -e
		for v in '"$VA $VB $VC"'; do
			stage=$SCR/stage/$v
			rm -rf "$stage/src" "$stage/out"
			mkdir -p "$stage/src/scripts" "$stage/src/src/forkrelease"
			cp -r "$SRC/fork" "$stage/src/fork"
			for p in nftables fail2ban unattended-upgrades; do
				mkdir -p "$stage/src/fork/$p"
				cp "$SRC/scripts/fork-install-e2e/stub-piece.sh" "$stage/src/fork/$p/install.sh"
				cp "$SRC/scripts/fork-install-e2e/stub-piece.sh" "$stage/src/fork/$p/uninstall.sh"
			done
			cp "$SRC/scripts/fork-release-package.sh" "$stage/src/scripts/"
			pub=$(tr -d "\n" <"$SCR/keys/test.pub")
			printf "var releaseKeys = [2]string{\n\t\"%s\", // slot 1\n\tnoReleaseKey,\n}\n" "$pub" >"$stage/src/src/forkrelease/keys.go"
			bash "$stage/src/scripts/fork-release-package.sh" --build "$stage/build" --version "$v" \
				--repository '"$REPO"' --keys "$stage/src/src/forkrelease/keys.go" --out "$stage/out" --arches amd64 --no-deps-notes >/dev/null
			# signed as .github/workflows/fork-release.yml signs
			out=$stage/out
			{ echo cosmos-fork-release-v1; echo "version $v"; echo "sha256 $(sha256sum "$out/cosmos-cloud-$v-amd64.zip" | cut -d" " -f1) cosmos-cloud-$v-amd64.zip"; } >"$out/manifest"
			openssl pkeyutl -sign -inkey "$SCR/keys/test.pem" -rawin -in "$out/manifest" -out "$out/signature"
			{ cat "$out/manifest"; echo "signature $(base64 -w0 "$out/signature")"; } >"$out/cosmos-cloud-$v.sig"
			{ echo cosmos-fork-scripts-v1; echo "version $v"; for s in install.sh uninstall.sh; do echo "sha256 $(sha256sum "$out/$s" | cut -d" " -f1) $s"; done; } >"$out/manifest.scripts"
			openssl pkeyutl -sign -inkey "$SCR/keys/test.pem" -rawin -in "$out/manifest.scripts" -out "$out/signature.scripts"
			{ cat "$out/manifest.scripts"; echo "signature $(base64 -w0 "$out/signature.scripts")"; } >"$out/cosmos-cloud-$v.scripts.sig"
			rm "$out/manifest" "$out/signature" "$out/manifest.scripts" "$out/signature.scripts"
			rm -rf "$SCR/rel/v$v"
			mv "$out" "$SCR/rel/v$v"
		done

		# the variants that must be refused, all of release A
		cd "$SCR/variants"
		rm -rf ./*
		a=$SCR/rel/v'"$VA"'
		zip=cosmos-cloud-'"$VA"'-amd64.zip sig=cosmos-cloud-'"$VA"'.sig
		mk() { mkdir "$1"; cp "$a/$zip" "$a/$zip.md5" "$a/$sig" "$1/"; }
		mk good
		mk tampered-zip; printf "\x01" | dd of=tampered-zip/$zip bs=1 seek=$(( $(stat -c %s tampered-zip/$zip) / 2 )) conv=notrunc status=none
		mk tampered-sig; sed -i "3s/^sha256 \(.\)/sha256 \1\1/; 3s/^\(sha256 .\{64\}\)./\1/" tampered-sig/$sig
		mk other-key
		head -n 3 other-key/$sig >other-key/manifest
		openssl pkeyutl -sign -inkey "$SCR/keys/other.pem" -rawin -in other-key/manifest -out other-key/s
		{ cat other-key/manifest; echo "signature $(base64 -w0 other-key/s)"; } >other-key/$sig; rm other-key/manifest other-key/s
		mk no-sig; rm no-sig/$sig
		for kind in dotdot absolute symlink; do
			mkdir $kind
			"$SCR/bin/helper" bad-zip "$a/$zip" $kind/$zip $kind
			(cd $kind && md5sum $zip >$zip.md5)
			{ echo cosmos-fork-release-v1; echo "version '"$VA"'"; echo "sha256 $(sha256sum $kind/$zip | cut -d" " -f1) $zip"; } >$kind/manifest
			openssl pkeyutl -sign -inkey "$SCR/keys/test.pem" -rawin -in $kind/manifest -out $kind/s
			{ cat $kind/manifest; echo "signature $(base64 -w0 $kind/s)"; } >$kind/$sig; rm $kind/manifest $kind/s
		done'
	log "releases: $(ls "$SCRATCH/rel" | tr '\n' ' ')"
}

# ---- The Docker in Docker and the machines ---------------------------------------
start_dind() {
	if [ "${REUSE_DIND:-0}" = 1 ] && D info >/dev/null 2>&1; then
		log "reusing the running $DIND (REUSE_DIND=1)"
		D ps -aq | xargs -r docker exec -i "$DIND" docker rm -f -v >/dev/null 2>&1 || true
		cp "$HERE/scripts/fork-install-e2e/machine-lib.sh" "$SCRATCH/bin/"
		return 0
	fi
	docker rm -f -v "$DIND" >/dev/null 2>&1 || true
	docker network create "$PREFIX-net" >/dev/null 2>&1 || true
	cp "$HERE/scripts/fork-install-e2e/machine-lib.sh" "$SCRATCH/bin/"
	docker run -d --name "$DIND" --privileged --network "$PREFIX-net" -e DOCKER_TLS_CERTDIR= \
		-v /var/lib/docker -v "$SCRATCH:/e2e" docker:29-dind --registry-mirror=https://mirror.gcr.io >/dev/null
	for _ in $(seq 1 60); do D info >/dev/null 2>&1 && return 0; sleep 1; done
	die "the Docker in Docker did not start"
}

machine_image() { # system [variant]
	local sys=$1 variant=${2:-bare} base tag
	base=$(image_of "$sys")
	tag=$PREFIX-$sys-$variant:1
	if D image inspect "$tag" >/dev/null 2>&1; then echo "$tag"; return; fi
	local extra=""
	# docker: Docker from the distribution and the tools already there
	[ "$variant" != docker ] || extra="docker.io ca-certificates curl unzip openssl"
	D build -q -t "$tag" - >/dev/null <<EOF
FROM $base
ENV DEBIAN_FRONTEND=noninteractive
RUN rm -f /usr/sbin/policy-rc.d; apt-get -q update && apt-get -q install -y --no-install-recommends systemd systemd-sysv dbus $extra && rm -rf /var/lib/apt/lists/* && systemctl mask getty@tty1.service systemd-modules-load.service >/dev/null 2>&1 || true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
EOF
	echo "$tag"
}

start_world() { # system: its network and its GitHub stand-in
	local sys=$1 net=$PREFIX-$1
	D network create "$net" >/dev/null 2>&1 || true
	rm -rf "$SCRATCH/world/$sys"
	mkdir -p "$SCRATCH/world/$sys/shared" "$SCRATCH/world/$sys/releases"
	cp -r "$SCRATCH/rel/v$VA" "$SCRATCH/world/$sys/releases/"
	D rm -f "$PREFIX-$sys-github" >/dev/null 2>&1 || true
	D run -d --name "$PREFIX-$sys-github" --network "$net" \
		--network-alias github.com --network-alias api.github.com --network-alias release-assets.githubusercontent.com \
		-v "/e2e/bin:/bin-e2e:ro" -v "/e2e/world/$sys:/world" debian:13 /bin-e2e/fake-github -releases /world/releases -shared /world/shared >/dev/null
	for _ in $(seq 1 30); do [ -s "$SCRATCH/world/$sys/shared/ca.pem" ] && return 0; sleep 1; done
	die "the GitHub stand-in of $sys did not start"
}

github() { # system, control path on the stand-in's plain port
	D run --rm --network "$PREFIX-$1" debian:13 bash -c "exec 3<>/dev/tcp/$PREFIX-$1-github/8080; printf 'GET %s HTTP/1.0\r\n\r\n' '$2' >&3; cat <&3 >/dev/null"
}

start_machine() { # system, case, variant -> machine name
	local sys=$1 case=$2 variant=${3:-bare} name=$PREFIX-$1-$2 image
	image=$(machine_image "$sys" "$variant")
	D rm -f -v "$name" >/dev/null 2>&1 || true
	mkdir -p "$SCRATCH/out/$sys-$case"
	D run -d --name "$name" --hostname "srv-$case" --privileged --cgroupns=private \
		--tmpfs /run --tmpfs /run/lock --tmpfs /tmp -v /var/lib/docker -v /var/lib/containerd \
		--network "$PREFIX-$sys" \
		-v "/e2e/world/$sys/releases:/e2e/releases:ro" -v /e2e/variants:/e2e/variants:ro -v /e2e/bin:/e2e/bin:ro \
		-v "/e2e/out/$sys-$case:/e2e/out" \
		-v "/e2e/world/$sys/shared/ca.pem:/usr/local/share/ca-certificates/fork-e2e-test-ca.crt:ro" \
		"$image" >/dev/null
	local state
	for _ in $(seq 1 60); do
		state=$(D exec "$name" systemctl is-system-running 2>/dev/null || true)
		case $state in running | degraded) break ;; esac
		sleep 1
	done
	# a machine that already has ca-certificates trusts the test CA now
	D exec "$name" sh -c 'command -v update-ca-certificates >/dev/null && update-ca-certificates >/dev/null 2>&1 || true'
	echo "$name"
}

M() { # machine, bash script (machine-lib.sh is loaded)
	local m=$1
	shift
	D exec -i "$m" bash -c "source /e2e/bin/machine-lib.sh; $1"
}

INSTALL_A="bash /e2e/releases/v$VA/install.sh"

# ---- The cases ------------------------------------------------------------------
# Each prints what it checks; a failing check makes the case fail.

case_fresh() { # fresh install, refusals over it, C0, update, rollback, offline reinstall, uninstall to the state of before
	local sys=$1 m
	m=$(start_machine "$sys" fresh)
	M "$m" 'snap before; '"$INSTALL_A"' --yes </dev/null; installed_ok 80; record_matches before
		check "the state says completed" grep -q "^completed=yes" /var/lib/cosmos-installer/state
		check "nobody else can read the data folder (it is $(stat -c %a /var/lib/cosmos): 700 when made, 711 once Cosmos ran)" sh -c "stat -c %a /var/lib/cosmos | grep -qE \"^7[01][01]\$\""
		mkdir -p /root/up && cp /opt/cosmos/cosmos /root/up/ && printf "#!/bin/sh\n" >/root/up/start.sh && chmod 755 /root/up/start.sh
		cp /etc/systemd/system/CosmosCloud.service /e2e/out/ours.service
		(env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin COSMOS_HTTP_PORT=80 COSMOS_HTTPS_PORT=443 /root/up/cosmos service install >/dev/null 2>&1 || true)
		sed "s|/root/up|/opt/cosmos|g" /etc/systemd/system/CosmosCloud.service >/e2e/out/upstream.service
		cp /e2e/out/ours.service /etc/systemd/system/CosmosCloud.service; rm -rf /root/up; systemctl daemon-reload
		check "the unit is byte for byte what cosmos service install writes" cmp /e2e/out/ours.service /e2e/out/upstream.service
		snap installed
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same installed after-second
		out=$('"$INSTALL_A"' --yes </dev/null 2>&1 || true); echo "$out" | grep -q "from the" || fail "the refusal does not say updates come from the panel"
		systemctl stop CosmosCloud; snap stopped
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same stopped after-stopped
		systemctl disable CosmosCloud 2>/dev/null; snap disabled
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same disabled after-disabled
		systemctl enable --now CosmosCloud 2>/dev/null; wait_panel 80
		mv /var/lib/cosmos-installer /root/ci-aside; snap byhand
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same byhand after-byhand
		mv /etc/systemd/system/CosmosCloud.service /root/unit-aside; snap nounit
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same nounit after-nounit
		mv /root/unit-aside /etc/systemd/system/CosmosCloud.service; mv /opt/cosmos /root/opt-aside; snap nofolder
		expect_exit 12 '"$INSTALL_A"' --yes </dev/null; same nofolder after-nofolder
		mv /root/opt-aside /opt/cosmos; mv /root/ci-aside /var/lib/cosmos-installer; systemctl daemon-reload; wait_panel 80
		RCLONE_CONFIG=/nonexistent /opt/cosmos/cosmos fork-detect --no-outside --timeout 5 >/e2e/out/detect.json 2>/dev/null
		check "fork-detect printed its JSON" grep -q "\"schema\": 1" /e2e/out/detect.json
		check "fork-detect asked no one outside" grep -q "\"asked\": false" /e2e/out/detect.json'
	D rm -f "$PREFIX-$sys-igd" >/dev/null 2>&1 || true
	D run -d --name "$PREFIX-$sys-igd" --network "$PREFIX-$sys" -v /e2e/bin:/bin-e2e:ro debian:13 /bin-e2e/fake-igd >/dev/null
	sleep 2
	local igd
	igd=$(D inspect -f "{{(index .NetworkSettings.Networks \"$PREFIX-$sys\").IPAddress}}" "$PREFIX-$sys-igd")
	M "$m" 'old=$(ip -4 route show default | head -n1); ip route replace default via '"$igd"'
		RCLONE_CONFIG=/nonexistent timeout 15 /opt/cosmos/cosmos fork-detect --timeout 10 >/e2e/out/detect-igd.json 2>/dev/null || true
		ip route replace $old
		cat /e2e/out/detect-igd.json
		check "fork-detect: the router answers" grep -q "\"answers\": true" /e2e/out/detect-igd.json
		check "fork-detect: its address" grep -q "\"externalIP\": \"203.0.113.7\"" /e2e/out/detect-igd.json
		check "fork-detect: from the router" grep -q "\"publicIPSource\": \"router\"" /e2e/out/detect-igd.json
		check "fork-detect: not a shared address" grep -q "\"sharedAddress\": false" /e2e/out/detect-igd.json
		check "fork-detect: no outside service asked" grep -q "\"asked\": false" /e2e/out/detect-igd.json'
	D rm -f "$PREFIX-$sys-igd" >/dev/null
	cp -r "$SCRATCH/rel/v$VB" "$SCRATCH/world/$sys/releases/"
	M "$m" 'wizard_and_login && update_to '"$VB"
	cp -r "$SCRATCH/rel/v$VC" "$SCRATCH/world/$sys/releases/"
	M "$m" 'PANEL_HOST=srv-e2e.local; api POST login "{\"nickname\":\"admin\",\"password\":\"E2e-Test-Passw0rd!x\"}" >/dev/null; api POST sudo "{\"password\":\"E2e-Test-Passw0rd!x\"}" >/dev/null; rolls_back_from '"$VC $VB"
	rm -rf "$SCRATCH/world/$sys/releases/v$VC" "$SCRATCH/world/$sys/releases/v$VB"
	M "$m" 'bash /opt/cosmos/fork/install/uninstall.sh --yes </dev/null
		check "the folder is gone" test ! -e /opt/cosmos
		check "the data is kept" test -d /var/lib/cosmos
		check "the unit is gone" test ! -e /etc/systemd/system/CosmosCloud.service
		expect_exit 13 '"$INSTALL_A"' --yes </dev/null'
	D network disconnect "$PREFIX-$sys" "$m"
	M "$m" 'PANEL_HOST=srv-e2e.local; check "no network" sh -c "! getent hosts github.com"
		expect_exit 0 '"$INSTALL_A"' --yes --reuse-data --from /e2e/releases/v'"$VA"'/cosmos-cloud-'"$VA"'-amd64.zip </dev/null; installed_ok 80'
	D network connect "$PREFIX-$sys" "$m"
	M "$m" 'bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null
		check "everything removed" test ! -e /var/lib/cosmos-installer -a ! -e /var/lib/cosmos -a ! -e /opt/cosmos
		snap removed; differ before removed > /e2e/out/left.txt || true
		packages_back before removed
		echo "what is left:"; grep "^[<>]" /e2e/out/left.txt | head -n 80 || true'
}

case_refusals() { # existing installations that are not the fork's own run
	local sys=$1 m
	m=$(start_machine "$sys" refusals)
	M "$m" 'snap before
		mkdir -p /opt/cosmos && printf "{\n  \"version\": \"0.24.0\"\n}\n" >/opt/cosmos/meta.json && printf "#!/bin/sh\n" >/opt/cosmos/cosmos && chmod 755 /opt/cosmos/cosmos
		printf "[Service]\nWorkingDirectory=/opt/cosmos\nExecStart=/opt/cosmos/start.sh\n" >/etc/systemd/system/CosmosCloud.service
		snap upstream; expect_exit 11 '"$INSTALL_A"' --yes </dev/null; same upstream after-upstream
		out=$('"$INSTALL_A"' --yes </dev/null 2>&1 || true); echo "$out" | grep -q migrate.sh || fail "upstream: no word of migrate.sh"
		rm -rf /opt/cosmos; mkdir -p /opt/cosmos && printf "{\n  \"version\": \"0.24.0-fork.900\"\n}\n" >/opt/cosmos/meta.json && : >/opt/cosmos/FORK-LICENCE.md
		snap byhand; expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same byhand after-byhand
		rm -f /opt/cosmos/meta.json /opt/cosmos/FORK-LICENCE.md /etc/systemd/system/CosmosCloud.service; printf "x cosmos-fork-release-v1 x" >/opt/cosmos/cosmos
		snap binonly; expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same binonly after-binonly
		rm -rf /opt/cosmos; printf "[Service]\nWorkingDirectory=/opt/cosmos\n" >/etc/systemd/system/CosmosCloud.service
		snap unitonly; expect_exit 12 '"$INSTALL_A"' --yes </dev/null; same unitonly after-unitonly
		rm -f /etc/systemd/system/CosmosCloud.service
		mkdir -p /var/lib/cosmos && echo "{}" >/var/lib/cosmos/cosmos.config.json
		snap data; expect_exit 13 '"$INSTALL_A"' --yes </dev/null; same data after-data
		rm -rf /var/lib/cosmos
		snap end; same before end
		expect_exit 2 '"$INSTALL_A"' --nonsense </dev/null
		expect_exit 2 '"$INSTALL_A"' --http-port 99999 </dev/null
		printf "nonsense=1\n" >/root/bad-answers
		expect_exit 2 '"$INSTALL_A"' --answers /root/bad-answers </dev/null
		expect_exit 5 setpriv --reuid=65534 --regid=65534 --clear-groups bash /e2e/releases/v'"$VA"'/install.sh </dev/null
		cp /etc/os-release /root/os-release; sed -i "s/^VERSION_ID=.*/VERSION_ID=\"99\"/" /etc/os-release
		expect_exit 3 '"$INSTALL_A"' --yes </dev/null; cp /root/os-release /etc/os-release
		rm -f /root/bad-answers /root/os-release
		snap end2; same before end2'
}

case_tampered() { # each refused before anything is unpacked, nothing left (the tools it installed are taken back)
	local sys=$1 m v
	m=$(start_machine "$sys" tampered)
	M "$m" 'snap before'
	for v in tampered-zip tampered-sig other-key no-sig dotdot absolute symlink; do
		M "$m" 'echo "== '"$v"'"; expect_exit 4 '"$INSTALL_A"' --yes --from /e2e/variants/'"$v"' </dev/null; snap after-'"$v"'; same before after-'"$v"'
			check "nothing planted" test ! -e /etc/cosmos-e2e-planted
			check "nothing unpacked" sh -c "! ls -d /opt/cosmos /opt/.cosmos-install.* 2>/dev/null"'
	done
	mkdir -p "$SCRATCH/world/$sys/releases/v0.24.0-fork.950"
	M "$m" 'echo "== no scripts signature on the download path"; expect_exit 4 '"$INSTALL_A"' --yes --version 0.24.0-fork.950 </dev/null; snap after-nosigdl; same before after-nosigdl'
	rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.950"
}

case_dryrun() {
	local sys=$1 m
	m=$(start_machine "$sys" dryrun)
	M "$m" 'snap before; expect_exit 0 '"$INSTALL_A"' --dry-run </dev/null; snap after; same before after'
	m=$(start_machine "$sys" dryrun2 docker)
	M "$m" 'snap before; expect_exit 0 '"$INSTALL_A"' --dry-run --from /e2e/variants/good </dev/null; snap after; same before after
		expect_exit 0 '"$INSTALL_A"' --dry-run </dev/null; snap after2; same before after2'
}

case_unavailable() { # a package apt cannot install: stops before any change
	local sys=$1 m
	m=$(start_machine "$sys" unavailable)
	M "$m" 'printf "Package: snapraid\nPin: release *\nPin-Priority: -1\n" >/etc/apt/preferences.d/e2e-no-snapraid; snap before
		expect_exit 6 '"$INSTALL_A"' --yes </dev/null; snap after; same before after'
}

case_locked() { # apt is held by another program: the script waits, then installs
	local sys=$1 m
	m=$(start_machine "$sys" locked)
	M "$m" '(setsid /e2e/bin/helper lock-dpkg 45 >/e2e/out/lock.txt 2>&1 &); sleep 2; cat /e2e/out/lock.txt
		rc=0; '"$INSTALL_A"' --yes </dev/null >/e2e/out/install.txt 2>&1 || rc=$?; tail -n 5 /e2e/out/install.txt
		check "it waited for the lock" grep -qE "waiting for it|Waiting for cache lock" /e2e/out/install.txt
		check "then installed" test $rc = 0
		installed_ok 80'
}

case_netcut() { # the download breaks off: refused, nothing left
	local sys=$1 m
	m=$(start_machine "$sys" netcut)
	M "$m" 'snap before'
	github "$sys" "/cut?asset=amd64.zip&after=3000000"
	M "$m" 'expect_exit 1 '"$INSTALL_A"' --yes </dev/null; snap after; same before after'
	github "$sys" "/cut?asset="
	M "$m" 'expect_exit 0 '"$INSTALL_A"' --yes </dev/null; installed_ok 80'
}

case_interrupt() { # kill -9 at three points, each followed by another run
	local sys=$1 m point
	m=$(start_machine "$sys" interrupt)
	M "$m" 'snap before'
	for point in packages cosmos starting; do
		M "$m" '(setsid bash -c "echo \$\$ >/e2e/out/pgid; exec '"$INSTALL_A"' --yes </dev/null >/e2e/out/run-'"$point"'.txt 2>&1" &)
			killed=0
			for i in $(seq 1 3000); do
				s=$(sed -n "s/^step=//p" /var/lib/cosmos-installer/state 2>/dev/null | tail -n1)
				if [ "$s" = '"$point"' ]; then
					if [ '"$point"' = packages ]; then pgrep -x dpkg >/dev/null || { sleep 0.2; continue; }; sleep 2; fi
					kill -9 -- -$(cat /e2e/out/pgid); echo "killed at '"$point"'"; killed=1; break
				fi
				sleep 0.2
			done
			check "the run reached '"$point"'" test $killed = 1'
		sleep 2
	done
	M "$m" 'expect_exit 0 '"$INSTALL_A"' --yes </dev/null; installed_ok 80; record_matches before
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --yes-delete-data </dev/null; snap removed
		packages_back before removed'
}

case_docker_there() { # Docker of the distribution is used and left alone; protection pieces through the interface
	local sys=$1 m
	m=$(start_machine "$sys" docker docker)
	M "$m" 'check "Docker runs" systemctl is-active --quiet docker
		dpkg-query -W docker.io >/e2e/out/docker-before
		dpkg -L docker.io | while read -r f; do if [ -f "$f" ]; then md5sum "$f"; fi; done >/e2e/out/docker-files-before
		snap before
		expect_exit 0 '"$INSTALL_A"' --yes --reachable internet --http-port 8080 --https-port 8443 </dev/null; installed_ok 8080; record_matches before
		check "no Docker package in the record" sh -c "! grep -q \"^package docker\" /var/lib/cosmos-installer/record"
		check "Docker'\''s repository was not added" test ! -e /etc/apt/sources.list.d/docker.sources
		check "docker.io unchanged" sh -c "dpkg-query -W docker.io | cmp - /e2e/out/docker-before"
		for p in unattended-upgrades fail2ban nftables; do
			check "piece $p called with flags it accepts" grep -q "args ok" /run/cosmos-e2e-stub-$p.install
		done
		check "the pieces'\'' record lines are in the record" grep -qP "^piece nftables\tfile\t/etc/cosmos-e2e-stub-nftables$" /var/lib/cosmos-installer/record
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null
		for p in unattended-upgrades fail2ban nftables; do check "piece $p removed" test ! -e /etc/cosmos-e2e-stub-$p; done
		check "Docker still runs" systemctl is-active --quiet docker
		check "docker.io still installed, unchanged" sh -c "dpkg-query -W docker.io | cmp - /e2e/out/docker-before"
		check "none of Docker'\''s files changed" md5sum -c --quiet /e2e/out/docker-files-before
		snap removed; packages_back before removed'
}

case_hop() { # the script of A, the newest release is B: B's own verified install.sh installs B
	local sys=$1 m
	cp -r "$SCRATCH/rel/v$VB" "$SCRATCH/world/$sys/releases/"
	m=$(start_machine "$sys" hop)
	M "$m" 'expect_exit 0 '"$INSTALL_A"' --yes </dev/null; installed_ok 80
		check "B installed by its own script" test "$(version_installed)" = '"$VB"'
		check "no temporary folder left" sh -c "! ls -d /var/tmp/cosmos-install* 2>/dev/null"'
	rm -rf "$SCRATCH/world/$sys/releases/v$VB"
}

run_case() { # system case
	local sys=$1 c=$2 f rc=0
	f=$RESULTS/$sys-$c
	log "$sys: $c ..."
	local start=$SECONDS
	# not under || nor &&: set -e must stay in force inside the case
	set +e
	( set -euo pipefail; "case_${c//-/_}" "$sys" ) >"$f.log" 2>&1
	rc=$?
	set -e
	if [ "$rc" = 0 ] && ! grep -qE '^(FAIL|CHANGED)' "$f.log"; then
		echo "pass $((SECONDS - start))s" >"$f.result"
	else
		echo "FAIL (exit $rc) $((SECONDS - start))s" >"$f.result"
	fi
	log "$sys: $c: $(cat "$f.result")"
	# the case's machines go when the case is done
	D ps -aq --filter "name=^$PREFIX-$sys-$c" | xargs -r docker exec -i "$DIND" docker rm -f -v >/dev/null 2>&1 || true
}

run_system() { # system cases...
	local sys=$1 c
	shift
	start_world "$sys"
	for c in "$@"; do run_case "$sys" "$c"; done
	D rm -f -v "$PREFIX-$sys-github" >/dev/null 2>&1 || true
}

summary() {
	local failed=0 f
	echo
	echo "== Results"
	for f in "$RESULTS"/*.result; do
		printf '%-32s %s\n' "$(basename "$f" .result)" "$(cat "$f")"
		grep -q '^pass' "$f" || failed=1
	done
	return $failed
}

shellcheck_scripts() {
	log "shellcheck"
	local files=(fork/install/install.sh fork/install/uninstall.sh scripts/fork-release-package.sh scripts/fork-install-e2e.sh scripts/fork-install-e2e/machine-lib.sh)
	if command -v shellcheck >/dev/null; then
		(cd "$HERE" && shellcheck -s bash "${files[@]}" && shellcheck -s sh scripts/fork-install-e2e/stub-piece.sh)
	else
		docker run --rm -v "$HERE:/src:ro" -w /src koalaman/shellcheck:v0.10.0 -s bash "${files[@]}"
		docker run --rm -v "$HERE:/src:ro" -w /src koalaman/shellcheck:v0.10.0 -s sh scripts/fork-install-e2e/stub-piece.sh
	fi
}

main() {
	local cmd=${1:-}
	case $cmd in
	clean) KEEP=0; cleanup; exit 0 ;;
	fast | full | one) ;;
	*) sed -n '2,30p' "$0"; exit 2 ;;
	esac
	shellcheck_scripts
	mkdir -p "$SCRATCH"
	trap cleanup EXIT
	trap 'exit 130' INT TERM
	rm -rf "$RESULTS" "$SCRATCH/out"; mkdir -p "$RESULTS"
	build_releases
	start_dind
	case $cmd in
	fast)
		run_system debian13 fresh-fast
		;;
	one)
		run_system "$2" "$3"
		;;
	full)
		local sys pids=() running=0
		for sys in $SYSTEMS; do
			# shellcheck disable=SC2086
			run_system "$sys" $CASES &
			pids+=($!)
			running=$((running + 1))
			if [ "$running" -ge "$JOBS" ]; then wait "${pids[0]}" || true; pids=("${pids[@]:1}"); running=$((running - 1)); fi
		done
		wait || true
		;;
	esac
	summary
}

case_fresh_fast() { # the checks of every push: install, refusal over it, a tampered zip, uninstall
	local sys=$1 m
	m=$(start_machine "$sys" fast)
	M "$m" 'snap before; '"$INSTALL_A"' --yes </dev/null; installed_ok 80; record_matches before
		snap installed; expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same installed after-second
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null; snap removed
		packages_back before removed
		expect_exit 4 '"$INSTALL_A"' --yes --from /e2e/variants/tampered-zip </dev/null; snap after-tampered; same removed after-tampered'
}

main "$@"
