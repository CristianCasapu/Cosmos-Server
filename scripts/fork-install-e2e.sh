#!/bin/bash
# shellcheck disable=SC2016,SC2012,SC2317 # the snippets in single quotes run inside the test machines; SC2317: the functions are reached through main (see the last line)
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
ALL_CASES="fresh refusals tampered dryrun unavailable locked aptdaily aptfail netcut interrupt docker-there hop trust env pieces"
CASES=${CASES:-$ALL_CASES}
JOBS=${JOBS:-4}
REPO=example-owner/cosmos-fork
VA=0.24.0-fork.900 VB=0.24.0-fork.901 VC=0.24.0-fork.902
VO=0.24.0-fork.899 # older than A: the installer goes forward only
VR=0.24.0-fork.903 # with the repository's real protection pieces instead of the stubs
DIND=$PREFIX-dind
# Every image is pinned by its digest (the index of all its platforms):
# what the test bed runs privileged cannot change under a tag.
DIND_IMAGE=docker:29-dind@sha256:4bbc7223f81db790097c6dea42fb93e05b8fead4f0431fbe6ccaba6e8473201c
DEBIAN12=debian:12@sha256:268fb24c39bca92d326dd83ea3abebe4a3f7751c804ffef23bb6d857dd343ff8
DEBIAN13=debian:13@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6
UBUNTU2404=ubuntu:24.04@sha256:73b999393d07f2bf65a40373778a44dc9a6d54ad7718d29247d8ff9413c4d6a8
UBUNTU2604=ubuntu:26.04@sha256:d66b366dac61b575bee2aef07e9c26e8070096fbe84a3d4d3eb0da069b5d76db
GOIMAGE=golang:1.27@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
NODEIMAGE=node:22@sha256:e13726ca604184d7a91fcacab1fc09eefd642dc28e79ac34e85540dd93b78cfb
SHELLCHECK_IMAGE=koalaman/shellcheck:v0.10.0@sha256:2097951f02e735b613f4a34de20c40f937a6c8f18ecb170612c88c34517221fb

log() { printf '[e2e] %s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

image_of() {
	case $1 in
	debian12) echo "$DEBIAN12" ;;
	debian13) echo "$DEBIAN13" ;;
	ubuntu2404) echo "$UBUNTU2404" ;;
	ubuntu2604) echo "$UBUNTU2604" ;;
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
		docker run --rm -v "$(dirname "$SCRATCH"):/w" "$DEBIAN13" rm -rf "/w/$(basename "$SCRATCH")" >/dev/null 2>&1 || rm -rf "$SCRATCH"
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
		docker run --name "$PREFIX-build" "$DEBIAN13" sh -c 'apt-get -qq update && apt-get -qq install -y --no-install-recommends zip unzip openssl jq git ca-certificates >/dev/null'
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
	for v in $VO $VA $VB $VC $VR; do
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
		for v in '"$VO $VA $VB $VC $VR"'; do
			stage=$SCR/stage/$v
			rm -rf "$stage/src" "$stage/out"
			mkdir -p "$stage/src/scripts" "$stage/src/src/forkrelease"
			cp -r "$SRC/fork" "$stage/src/fork"
			for p in nftables fail2ban unattended-upgrades; do
				[ "$v" != '"$VR"' ] || break # R: the pieces as the repository has them
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
		# one byte in the middle of the zip, flipped (so it surely differs)
		mk tampered-zip; at=$(( $(stat -c %s tampered-zip/$zip) / 2 ))
		byte=$(od -An -tu1 -j "$at" -N1 tampered-zip/$zip | tr -d " ")
		printf "\\$(printf %o $(( 255 - byte )))" | dd of=tampered-zip/$zip bs=1 seek="$at" conv=notrunc status=none
		cmp -s tampered-zip/$zip "$a/$zip" && { echo "the tampered zip is not tampered" >&2; exit 1; }
		mk tampered-sig; sed -i "3s/^sha256 \(.\)/sha256 \1\1/; 3s/^\(sha256 .\{64\}\)./\1/" tampered-sig/$sig
		mk other-key
		head -n 3 other-key/$sig >other-key/manifest
		openssl pkeyutl -sign -inkey "$SCR/keys/other.pem" -rawin -in other-key/manifest -out other-key/s
		{ cat other-key/manifest; echo "signature $(base64 -w0 other-key/s)"; } >other-key/$sig; rm other-key/manifest other-key/s
		mk no-sig; rm no-sig/$sig
		# a signature file whose version line is another release (names and all)
		mk otherversion
		{ echo cosmos-fork-release-v1; echo "version '"$VB"'"; echo "sha256 $(sha256sum "$a/$zip" | cut -d" " -f1) cosmos-cloud-'"$VB"'-amd64.zip"; } >otherversion/manifest
		openssl pkeyutl -sign -inkey "$SCR/keys/test.pem" -rawin -in otherversion/manifest -out otherversion/s
		{ cat otherversion/manifest; echo "signature $(base64 -w0 otherversion/s)"; } >otherversion/$sig; rm otherversion/manifest otherversion/s
		# releases whose next install.sh must not run: tampered after signing,
		# its scripts signed by another key, its scripts file for another version
		scripts_sig() { # dir version key [version line]
			{ echo cosmos-fork-scripts-v1; echo "version ${4:-$2}"; for f in install.sh uninstall.sh; do echo "sha256 $(sha256sum "$1/$f" | cut -d" " -f1) $f"; done; } >"$1/m"
			openssl pkeyutl -sign -inkey "$SCR/keys/$3.pem" -rawin -in "$1/m" -out "$1/s"
			{ cat "$1/m"; echo "signature $(base64 -w0 "$1/s")"; } >"$1/cosmos-cloud-$2.scripts.sig"; rm "$1/m" "$1/s"
		}
		for n in 910 911 912; do
			d=hop/v0.24.0-fork.$n; mkdir -p "$d"
			cp "$a/install.sh" "$a/uninstall.sh" "$d/"
		done
		scripts_sig hop/v0.24.0-fork.910 0.24.0-fork.910 test; printf "\n# changed after signing\n" >>hop/v0.24.0-fork.910/install.sh
		scripts_sig hop/v0.24.0-fork.911 0.24.0-fork.911 other
		scripts_sig hop/v0.24.0-fork.912 0.24.0-fork.912 test 0.24.0-fork.913
		for kind in dotdot absolute symlink duplicate setuid worldwritable device ctrlchar bomb longlist-dotdot longlist-setuid longlist-symlink; do
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
		-v /var/lib/docker -v "$SCRATCH:/e2e" "$DIND_IMAGE" --registry-mirror=https://mirror.gcr.io >/dev/null
	for _ in $(seq 1 60); do D info >/dev/null 2>&1 && return 0; sleep 1; done
	die "the Docker in Docker did not start"
}

machine_image() { # system [variant]
	local sys=$1 variant=${2:-bare} base tag
	base=$(image_of "$sys")
	local extra=""
	# docker: Docker from the distribution and the tools already there
	# (docker-cli exists where Debian split the command from docker.io: it is
	# installed with it there, as apt does with its recommendations)
	[ "$variant" != docker ] || extra="docker.io ca-certificates curl unzip openssl sudo"
	tag=$PREFIX-$sys-$variant:$(printf '%s' "$base $extra" | md5sum | cut -c1-8)
	if D image inspect "$tag" >/dev/null 2>&1; then echo "$tag"; return; fi
	D build -q -t "$tag" - >/dev/null <<EOF || die "the machine image $tag could not be built"
FROM $base
ENV DEBIAN_FRONTEND=noninteractive
RUN rm -f /usr/sbin/policy-rc.d && apt-get -q update && apt-get -q install -y --no-install-recommends systemd systemd-sysv dbus $extra && \
    if [ -n "$extra" ] && apt-cache show docker-cli >/dev/null 2>&1; then apt-get -q install -y --no-install-recommends docker-cli; fi && \
    rm -rf /var/lib/apt/lists/* && { systemctl mask getty@tty1.service systemd-modules-load.service >/dev/null 2>&1 || true; } && test -x /lib/systemd/systemd
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
		-v "/e2e/bin:/bin-e2e:ro" -v "/e2e/world/$sys:/world" "$DEBIAN13" /bin-e2e/fake-github -releases /world/releases -shared /world/shared >/dev/null
	for _ in $(seq 1 30); do [ -s "$SCRATCH/world/$sys/shared/ca.pem" ] && return 0; sleep 1; done
	die "the GitHub stand-in of $sys did not start"
}

github() { # system, control path on the stand-in's plain port
	D run --rm --network "$PREFIX-$1" "$DEBIAN13" bash -c "exec 3<>/dev/tcp/$PREFIX-$1-github/8080; printf 'GET %s HTTP/1.0\r\n\r\n' '$2' >&3; cat <&3 >/dev/null"
}

start_machine() { # system, case, variant -> machine name
	local sys=$1 case=$2 variant=${3:-bare} name=$PREFIX-$1-$2 image
	image=$(machine_image "$sys" "$variant")
	D rm -f -v "$name" >/dev/null 2>&1 || true
	rm -rf "$SCRATCH/out/$sys-$case"
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
	M "$m" 'snap before; '"$INSTALL_A"' --yes --print-setup-code </dev/null | tee /e2e/out/install.txt; installed_ok 80; record_matches before
		check "the state says completed" grep -q "^completed=yes" /var/lib/cosmos-installer/state
		# the one-time setup code: printed, in a root-only file, asked by every setup route
		printed=$(sed -n "s/^ *Setup code: //p" /e2e/out/install.txt)
		check "install.sh printed the setup code" test -n "$printed"
		check "it is the code of the file" test "$printed" = "$(cat /var/lib/cosmos/setup-code)"
		check "the file is root'\''s, 0600" test "$(stat -c %a:%U /var/lib/cosmos/setup-code)" = 600:root
		check "a local user cannot read it" sh -c "! setpriv --reuid=65534 --regid=65534 --clear-groups cat /var/lib/cosmos/setup-code 2>/dev/null"
		check "the journal has it" sh -c "journalctl -u CosmosCloud --no-pager | grep -A1 \"SETUP CODE\" | grep -qF \"$printed\""
		check "cosmos.log does not" sh -c "! grep -qF \"$printed\" /var/lib/cosmos/cosmos.log"
		for route in setup setup-join newInstall; do
			code=$(curl -s -o /e2e/out/r.json -w "%{http_code}" -X POST -H "Content-Type: application/json" --data "{\"step\":\"4\",\"nickname\":\"x\",\"password\":\"Passw0rd-x!yz\"}" http://127.0.0.1/cosmos/api/$route)
			check "POST /cosmos/api/$route without the code is refused" sh -c "test $code = 403 && grep -q SC001 /e2e/out/r.json"
			code=$(setpriv --reuid=65534 --regid=65534 --clear-groups curl -s -o /e2e/out/r.json -w "%{http_code}" -X POST -H "Content-Type: application/json" -H "X-Cosmos-Setup-Code: AAAA-AAAA-AAAA-AAAA-AAAA" --data "{}" http://127.0.0.1/cosmos/api/$route)
			check "POST /cosmos/api/$route by a local user with a wrong code is refused" sh -c "test $code = 403 && grep -q SC002 /e2e/out/r.json"
		done
		check "the status stays open for the first page" test "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1/cosmos/api/status)" = 200
		# placement not given (unknown): the wizard answers on 127.0.0.1 only, through a tunnel
		check "the loopback marker is root'\''s, 0600" test "$(stat -c %a:%U /var/lib/cosmos/setup-loopback)" = 600:root
		check "the wizard does not answer on the network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1)/cosmos-ui/" || true)" = 000
		check "install.sh printed the tunnel" grep -q "ssh -L 8080:127.0.0.1:80 " /e2e/out/install.txt
		check "and why" grep -q "placement is unknown, not home" /e2e/out/install.txt
		echo "$printed" >/e2e/out/printed-code
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
	D run -d --name "$PREFIX-$sys-igd" --network "$PREFIX-$sys" -v /e2e/bin:/bin-e2e:ro "$DEBIAN13" /bin-e2e/fake-igd >/dev/null
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
	M "$m" 'wizard_and_login "$(cat /e2e/out/printed-code)" && update_to '"$VB"'
		check "the setup code file is gone once set up" test ! -e /var/lib/cosmos/setup-code
		check "so is the loopback marker" test ! -e /var/lib/cosmos/setup-loopback
		check "set up, the server answers on the network" sh -c "test \"\$(curl -s -o /dev/null -m 5 -w %{http_code} http://\$(hostname -I | cut -d\" \" -f1)/cosmos-ui/)\" != 000"
		code=$(curl -s -o /e2e/out/r.json -w "%{http_code}" --resolve srv-e2e.local:80:127.0.0.1 -X POST -H "Content-Type: application/json" -H "X-Cosmos-Setup-Code: $(cat /e2e/out/printed-code)" --data "{\"step\":\"4\"}" http://srv-e2e.local/cosmos/api/newInstall)
		check "the wizard is closed once set up, code or not" test "$code" = 403'
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
		real=$(readlink -f /etc/os-release); cp -a "$real" /root/os-release; sed -i "s/^VERSION_ID=.*/VERSION_ID=\"99\"/" "$real"
		expect_exit 3 '"$INSTALL_A"' --yes </dev/null; cp -a /root/os-release "$real"
		rm -f /root/bad-answers /root/os-release
		snap end2; same before end2'
}

case_tampered() { # each refused before anything is unpacked, nothing left (the tools it installed are taken back)
	local sys=$1 m v
	m=$(start_machine "$sys" tampered)
	M "$m" 'snap before'
	# longlist-*: the bad entry first in a listing far over 64 KiB (a check
	# written as "listing | grep -q" under pipefail would let it through)
	for v in tampered-zip tampered-sig other-key no-sig dotdot absolute symlink duplicate setuid worldwritable device ctrlchar bomb \
		longlist-dotdot longlist-setuid longlist-symlink; do
		M "$m" 'echo "== '"$v"'"; expect_exit 4 '"$INSTALL_A"' --yes --from /e2e/variants/'"$v"' </dev/null; snap after-'"$v"'; same before after-'"$v"'
			check "nothing planted" test ! -e /etc/cosmos-e2e-planted
			check "nothing unpacked" sh -c "! ls -d /opt/cosmos /opt/.cosmos-install.* 2>/dev/null"'
	done
	# the download path: the release on the stand-in without its signature
	mv "$SCRATCH/world/$sys/releases/v$VA/cosmos-cloud-$VA.sig" "$SCRATCH/world/$sys/nosig.sig"
	M "$m" 'echo "== no signature on the download path"; expect_exit 4 '"$INSTALL_A"' --yes </dev/null; same before after-nosigdl'
	mv "$SCRATCH/world/$sys/nosig.sig" "$SCRATCH/world/$sys/releases/v$VA/cosmos-cloud-$VA.sig"
	# another release without a signed file of scripts: its install.sh is not run
	mkdir -p "$SCRATCH/world/$sys/releases/v0.24.0-fork.950"
	cp "$SCRATCH/rel/v$VA/install.sh" "$SCRATCH/world/$sys/releases/v0.24.0-fork.950/"
	M "$m" 'echo "== another release without its scripts signature"; expect_exit 4 '"$INSTALL_A"' --yes --version 0.24.0-fork.950 </dev/null; same before after-noscriptsig'
	rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.950"
}

case_dryrun() {
	local sys=$1 m
	m=$(start_machine "$sys" dryrun)
	# hooks of the system'\''s apt configuration (as Ubuntu'\''s command-not-found
	# has): the simulation must not run them
	M "$m" 'printf "APT::Update::Post-Invoke { \"touch /e2e-hook-ran\"; };\nAPT::Update::Pre-Invoke { \"touch /e2e-hook-ran\"; };\nAPT::Update::Post-Invoke-Success { \"touch /e2e-hook-ran\"; };\nDPkg::Pre-Invoke { \"touch /e2e-hook-ran\"; };\n" >/etc/apt/apt.conf.d/50e2e-probe
		snap before; expect_exit 0 '"$INSTALL_A"' --dry-run </dev/null; snap after; same before after
		check "the simulation ran none of the apt hooks" test ! -e /e2e-hook-ran
		rm /etc/apt/apt.conf.d/50e2e-probe'
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
		rc=0; '"$INSTALL_A"' --yes --wizard-on-lan </dev/null >/e2e/out/install.txt 2>&1 || rc=$?; tail -n 5 /e2e/out/install.txt
		check "it waited for the lock" grep -qE "waiting for it|Waiting for cache lock" /e2e/out/install.txt
		check "then installed" test $rc = 0
		installed_ok 80
		check "--wizard-on-lan: the wizard answers on the network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1)/cosmos-ui/")" = 200'
}

case_aptdaily() { # the system's own apt-daily and unattended-upgrades hold apt when the installer starts (as on every new Ubuntu server)
	local sys=$1 m
	m=$(start_machine "$sys" aptdaily)
	M "$m" 'DEBIAN_FRONTEND=noninteractive apt-get -q update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends unattended-upgrades </dev/null >/dev/null
		check "the system has apt-daily and unattended-upgrades" sh -c "systemctl cat apt-daily.service apt-daily-upgrade.service >/dev/null && command -v unattended-upgrade"
		# the container image turns the daily runs off (a server does not): on again
		rm -f /etc/apt/apt.conf.d/docker-disable-periodic-update
		# the real units, made to hold apt longer, through their own configuration only:
		# apt-daily (apt-get update) holds the lists for 4 minutes, then apt-daily-upgrade
		# (unattended-upgrade) holds dpkg 60 s longer whenever it has an upgrade to install
		printf "APT::Periodic::RandomSleep \"0\";\nAPT::Update::Pre-Invoke { \"sleep 240\"; };\nDPkg::Pre-Invoke { \"sleep 60\"; };\n" >/etc/apt/e2e-daily.conf
		for u in apt-daily apt-daily-upgrade; do
			mkdir -p /etc/systemd/system/$u.service.d
			printf "[Service]\nEnvironment=APT_CONFIG=/etc/apt/e2e-daily.conf\n" >/etc/systemd/system/$u.service.d/e2e.conf
		done
		systemctl daemon-reload
		snap before
		systemctl start --no-block apt-daily.service apt-daily-upgrade.service
		for _ in $(seq 1 60); do locks=$(lslocks -n -r -o PATH); case $locks in */var/lib/apt/lists/lock*) break ;; esac; sleep 1; done
		check "apt-daily.service holds apt'\''s lists lock" sh -c "lslocks -n -r -o COMMAND,PID,PATH | grep /var/lib/apt/lists/lock"
		rc=0; '"$INSTALL_A"' --yes </dev/null >/e2e/out/install.txt 2>&1 || rc=$?; tail -n 8 /e2e/out/install.txt
		grep -E "Another program|Waiting for cache lock" /e2e/out/install.txt || true
		check "it said what it waited for" grep -q "Another program is using apt: .*apt-daily.service" /e2e/out/install.txt
		if grep -q "unattended-upgr" /e2e/out/install.txt; then echo "(it also met unattended-upgrade holding dpkg)"; else echo "(unattended-upgrade had nothing to install: it did not hold dpkg for long)"; fi
		check "then installed" test $rc = 0
		check "apt-daily.service ran to its end" sh -c "! systemctl is-failed --quiet apt-daily.service"
		for _ in $(seq 1 300); do [ "$(systemctl is-active apt-daily-upgrade.service)" = inactive ] && break; sleep 1; done
		installed_ok 80'
}

case_netcut() { # the download breaks off: refused, nothing left
	local sys=$1 m
	m=$(start_machine "$sys" netcut)
	M "$m" 'snap before'
	github "$sys" "/cut?asset=amd64.zip&after=3000000"
	M "$m" 'expect_exit 1 '"$INSTALL_A"' --yes </dev/null; snap after; same before after'
	github "$sys" "/cut?asset="
	M "$m" 'expect_exit 0 '"$INSTALL_A"' --yes --placement home </dev/null >/e2e/out/home.txt; installed_ok 80
		check "at home the wizard answers on the home network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1)/cosmos-ui/")" = 200
		check "no loopback marker at home" test ! -e /var/lib/cosmos/setup-loopback
		check "it says the wizard is in clear on that network" grep -q "anyone on that network could read" /e2e/out/home.txt
		check "and offers the tunnel" grep -q "ssh -L 8080:127.0.0.1:80 " /e2e/out/home.txt'
}

case_interrupt() { # kill -9 at three points, each followed by another run; packages of the admin's; uninstall with Docker stopped
	local sys=$1 m point
	m=$(start_machine "$sys" interrupt)
	M "$m" 'snap before'
	for point in packages cosmos starting; do
		M "$m" '(setsid bash -c "echo \$\$ >/e2e/out/pgid; exec '"$INSTALL_A"' --yes </dev/null >/e2e/out/run-'"$point"'.txt 2>&1" &)
			killed=0
			for i in $(seq 1 3000); do
				s=$(sed -n "s/^step=//p" /var/lib/cosmos-installer/state 2>/dev/null | tail -n1 || true)
				if [ "$s" = '"$point"' ]; then
					if [ '"$point"' = packages ]; then grep -qsx dpkg /proc/[0-9]*/comm || { sleep 0.2; continue; }; sleep 2; fi
					kill -9 -- -$(cat /e2e/out/pgid); echo "killed at '"$point"'"; killed=1; break
				fi
				sleep 0.2
			done
			check "the run reached '"$point"'" test $killed = 1'
		sleep 2
		# between an interrupted run and the next, the admin installs a package
		if [ "$point" = cosmos ]; then
			M "$m" 'DEBIAN_FRONTEND=noninteractive apt-get install -y -q -o DPkg::Lock::Timeout=600 less </dev/null >/dev/null && echo "the admin installed less"'
		fi
	done
	# and during the run that finishes, another one
	M "$m" '(DEBIAN_FRONTEND=noninteractive setsid apt-get install -y -q -o DPkg::Lock::Timeout=600 hello </dev/null >/e2e/out/admin.txt 2>&1 &)
		expect_exit 0 '"$INSTALL_A"' --yes </dev/null >/e2e/out/run-final.txt; installed_ok 80
		for _ in $(seq 1 120); do pkg_installed hello && break; sleep 1; done
		check "the admin'\''s packages are installed" sh -c "dpkg-query -W less hello >/dev/null"
		check "the record names neither of the admin'\''s packages" sh -c "! grep -qE \"^package (less|hello)\$\" /var/lib/cosmos-installer/record"
		check "with --yes the code is not printed, its file is named" sh -c "! grep -q \"Setup code:\" /e2e/out/run-final.txt && grep -q \"sudo cat /var/lib/cosmos/setup-code\" /e2e/out/run-final.txt"
		expect_exit 10 '"$INSTALL_A"' --yes </dev/null
		# lines in the record that are not install.sh'\''s own are left alone
		printf "file /\npackage -rf\npiece ../x\ndir /etc\nunit ../../etc/passwd\n" >>/var/lib/cosmos-installer/record
		# a container exists and the Docker daemon is stopped: Docker stays
		tar -cf - --files-from /dev/null | docker import - e2e-empty:1 >/dev/null
		docker create --name e2e-app e2e-empty:1 /none >/dev/null
		systemctl stop docker.socket docker.service
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages </dev/null | tee /e2e/out/uninstall-1.txt
		check "Docker is kept while its daemon cannot be asked" pkg_installed docker-ce
		check "and the uninstaller says why" grep -q "does not answer" /e2e/out/uninstall-1.txt
		check "the hostile lines did nothing" sh -c "test -d /etc && test -f /etc/passwd"
		check "the admin'\''s packages stayed" sh -c "dpkg-query -W less hello >/dev/null"
		systemctl start docker.service; docker rm e2e-app >/dev/null; docker image rm e2e-empty:1 >/dev/null
		bash /e2e/releases/v'"$VA"'/uninstall.sh --yes --remove-packages --yes-delete-data </dev/null | tee /e2e/out/uninstall-2.txt
		check "the hostile lines are all that is left" sh -c "test \"\$(sort /var/lib/cosmos-installer/record | tr \"\\n\" \" \")\" = \"dir /etc file / package -rf piece ../x unit ../../etc/passwd \""
		rm -rf /var/lib/cosmos-installer
		expect_exit 0 bash /e2e/releases/v'"$VA"'/uninstall.sh --yes </dev/null
		bash /e2e/releases/v'"$VA"'/uninstall.sh --yes </dev/null | grep -q "Nothing to remove" && echo "ok: a second uninstall has nothing to remove"
		DEBIAN_FRONTEND=noninteractive apt-get purge -y -q less hello </dev/null >/dev/null
		snap removed; packages_back before removed'
}

case_docker_there() { # Docker of the distribution is used and left alone; protection pieces through the interface
	local sys=$1 m
	m=$(start_machine "$sys" docker-there docker)
	M "$m" 'check "Docker runs" systemctl is-active --quiet docker
		dpkg-query -W docker.io >/e2e/out/docker-before
		dpkg -L docker.io | while read -r f; do if [ -f "$f" ]; then md5sum "$f"; fi; done >/e2e/out/docker-files-before
		# upstream'\''s Cosmos as a container (stopped): refused, pointed to migrate.sh, nothing changed
		tar -cf - --files-from /dev/null | docker import - azukaar/cosmos-server:e2e >/dev/null
		docker create --name cosmos-server azukaar/cosmos-server:e2e /none >/dev/null
		snap container; expect_exit 11 '"$INSTALL_A"' --yes </dev/null; same container after-container
		docker rm cosmos-server >/dev/null; docker image rm azukaar/cosmos-server:e2e >/dev/null
		snap before
		printf "placement=cloud\nssh=from:192.0.2.0/24,2001:db8::/32\n" >/root/answers
		expect_exit 0 '"$INSTALL_A"' --yes --reachable internet --http-port 8080 --https-port 8443 --answers /root/answers </dev/null; installed_ok 8080; record_matches before
		rm /root/answers
		check "a cloud server: the wizard on 127.0.0.1 only" test -f /var/lib/cosmos/setup-loopback
		check "the wizard does not answer on the network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1):8080/cosmos-ui/" || true)" = 000
		check "but on 127.0.0.1" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:8080/cosmos-ui/)" = 200
		check "the guard got where the server stands and how SSH answers" grep -q -- "--exposed --ssh from:192.0.2.0/24,2001:db8::/32" /run/cosmos-e2e-stub-nftables.install.args
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

case_aptfail() { # apt fails after the simulation passed: the lists held too long, then a package that cannot be installed
	local sys=$1 m
	m=$(start_machine "$sys" aptfail)
	M "$m" 'snap before
		(setsid /e2e/bin/helper lock-file /var/lib/apt/lists/lock 120 >/e2e/out/lock.txt 2>&1 &); sleep 2; cat /e2e/out/lock.txt
		rc=0; COSMOS_INSTALL_APT_WAIT=20 '"$INSTALL_A"' --yes </dev/null >/e2e/out/locked.txt 2>&1 || rc=$?; tail -n 4 /e2e/out/locked.txt
		check "the lists held too long end the run with 6" test $rc = 6
		check "it said so" grep -q "held apt for longer than 20 seconds" /e2e/out/locked.txt
		pkill -x helper || true
		same before after-locked
		# a package that apt simulates well but cannot install (its unpacking is refused)
		printf "DPkg::Pre-Install-Pkgs { \"grep -q snapraid && exit 1 || exit 0\"; };\n" >/etc/apt/apt.conf.d/99e2e-refuse-snapraid
		snap before2
		rc=0; '"$INSTALL_A"' --yes </dev/null >/e2e/out/failed.txt 2>&1 || rc=$?; tail -n 12 /e2e/out/failed.txt
		check "a failed install ends with 1" test $rc = 1
		check "and says what it changed" grep -q "after these changes" /e2e/out/failed.txt
		check "the run is not completed" sh -c "! grep -q ^completed=yes /var/lib/cosmos-installer/state"
		check "snapraid is not recorded" sh -c "! grep -q \"^package snapraid\$\" /var/lib/cosmos-installer/record"
		rm /etc/apt/apt.conf.d/99e2e-refuse-snapraid
		# a package left half-installed by the failed transaction: its
		# configuration script fails (the hook rewrites the downloaded .deb)
		printf "#!/bin/sh\nwhile read -r f; do case \$f in */snapraid_*.deb) d=\$(mktemp -d); dpkg-deb -R \"\$f\" \$d; printf \"#!/bin/sh\\\\nexit 1\\\\n\" >\$d/DEBIAN/postinst; chmod 755 \$d/DEBIAN/postinst; dpkg-deb -b \$d \"\$f\" >/dev/null; rm -rf \$d;; esac; done\n" >/root/break-snapraid
		chmod 755 /root/break-snapraid
		printf "DPkg::Pre-Install-Pkgs { \"/root/break-snapraid\"; };\n" >/etc/apt/apt.conf.d/99e2e-break-snapraid
		rc=0; '"$INSTALL_A"' --yes </dev/null >/e2e/out/half.txt 2>&1 || rc=$?; tail -n 12 /e2e/out/half.txt
		check "the failed transaction ends with 1" test $rc = 1
		check "snapraid is half-installed (dpkg)" sh -c "dpkg-query -W -f=\"\\\${db:Status-Abbrev}\" snapraid | grep -q \"^iF\""
		check "it is named half-installed" grep -q "half-installed (dpkg): .*snapraid" /e2e/out/half.txt
		check "never \"Nothing is left\"" sh -c "! grep -q \"Nothing is left\" /e2e/out/half.txt"
		check "snapraid stays recorded" grep -qx "package snapraid" /var/lib/cosmos-installer/record
		rm /etc/apt/apt.conf.d/99e2e-break-snapraid /root/break-snapraid
		printf "#!/bin/sh\nexit 0\n" >/var/lib/dpkg/info/snapraid.postinst
		expect_exit 0 '"$INSTALL_A"' --yes </dev/null; installed_ok 80; record_matches before2
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/dev/null
		snap removed; packages_back before2 removed'
}

case_trust() { # what must never be fetched or run: addresses outside GitHub, next scripts that do not verify, older releases
	local sys=$1 m to n
	m=$(start_machine "$sys" trust docker)
	M "$m" 'snap before'
	for to in https://evilgithubusercontent.com/x https://github.com.evil.example/x "https://github.com@evil.example/x" \
		https://release-assets.githubusercontent.com:8443/x http://release-assets.githubusercontent.com/x https://RELEASE-ASSETS.githubusercontent.com/x; do
		github "$sys" "/redirect?to=$to"
		M "$m" 'echo "== a download sent to '"$to"'"; rc=0; '"$INSTALL_A"' --yes --http-port 8080 --https-port 8443 </dev/null >/e2e/out/r.txt 2>&1 || rc=$?
			check "refused (exit $rc)" test $rc = 1
			check "it says why" grep -q "refusing to download from" /e2e/out/r.txt
			same before after-redirect'
	done
	github "$sys" "/redirect?to="
	for n in 910 911 912; do
		cp -r "$SCRATCH/variants/hop/v0.24.0-fork.$n" "$SCRATCH/world/$sys/releases/"
		M "$m" 'echo "== the next install.sh of '"$n"'"; expect_exit 4 '"$INSTALL_A"' --yes --version 0.24.0-fork.'"$n"' --http-port 8080 --https-port 8443 </dev/null; same before after-hop'
		rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.$n"
	done
	cp -r "$SCRATCH/variants/hop/v0.24.0-fork.910" "$SCRATCH/world/$sys/releases/"
	M "$m" 'echo "== latest is a release whose next install.sh was changed"; expect_exit 4 '"$INSTALL_A"' --yes --http-port 8080 --https-port 8443 </dev/null; same before after-hoplatest'
	rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.910"
	cp -r "$SCRATCH/rel/v$VO" "$SCRATCH/world/$sys/releases/"
	github "$sys" "/latest?tag=v$VO"
	M "$m" 'echo "== forward only"
		expect_exit 15 '"$INSTALL_A"' --yes --http-port 8080 --https-port 8443 </dev/null
		expect_exit 15 '"$INSTALL_A"' --yes --version '"$VO"' --http-port 8080 --https-port 8443 </dev/null
		expect_exit 15 '"$INSTALL_A"' --yes --from /e2e/releases/v'"$VO"'/cosmos-cloud-'"$VO"'-amd64.zip --http-port 8080 --https-port 8443 </dev/null
		expect_exit 0 '"$INSTALL_A"' --dry-run --allow-older --from /e2e/releases/v'"$VO"'/cosmos-cloud-'"$VO"'-amd64.zip --http-port 8080 --https-port 8443 </dev/null
		echo "== a signature file whose version line is another release"
		expect_exit 4 '"$INSTALL_A"' --yes --from /e2e/variants/otherversion --http-port 8080 --https-port 8443 </dev/null
		same before after-trust'
}

case_env() { # never from a pipe, whatever the current folder holds; a hostile environment changes nothing
	local sys=$1 m
	m=$(start_machine "$sys" env docker)
	M "$m" 'mkdir -p /root/evil && printf "#!/bin/sh\ntouch /root/pwned\n" >/root/evil/sh && chmod 755 /root/evil/sh && cp /root/evil/sh /root/evil/bash
		snap before
		cd /root/evil
		rc=0; cat /e2e/releases/v'"$VA"'/install.sh | sh >/e2e/out/pipe.txt 2>&1 || rc=$?
		check "curl | sh is refused (exit $rc)" sh -c "test $rc = 2 && grep -q \"not from a pipe\" /e2e/out/pipe.txt"
		rc=0; cat /e2e/releases/v'"$VA"'/install.sh | sudo sh >/dev/null 2>&1 || rc=$?
		check "curl | sudo sh is refused" test $rc = 2
		rc=0; cat /e2e/releases/v'"$VA"'/install.sh | bash >/dev/null 2>&1 || rc=$?
		check "curl | bash is refused" test $rc = 2
		rc=0; cat /e2e/releases/v'"$VA"'/uninstall.sh | sh >/dev/null 2>&1 || rc=$?
		check "uninstall.sh from a pipe is refused" test $rc = 2
		check "the sh of the current folder never ran" test ! -e /root/pwned
		cd /
		same before after-pipes
		# sudo -E with a hostile environment: a proxy that does not exist, a curlrc and an OPENSSL_CONF that would break both
		printf "proxy = \"http://127.0.0.1:9\"\ninsecure\n" >/root/.curlrc
		printf "this is not an openssl configuration\n[[[\n" >/root/bad-openssl.cnf
		rc=0; https_proxy=http://127.0.0.1:9 HTTPS_PROXY=http://127.0.0.1:9 OPENSSL_CONF=/root/bad-openssl.cnf CURL_HOME=/root \
			sudo -E bash /e2e/releases/v'"$VA"'/install.sh --yes --http-port 8080 --https-port 8443 </dev/null >/e2e/out/sudo-e.txt 2>&1 || rc=$?
		tail -n 3 /e2e/out/sudo-e.txt
		check "sudo -E with a hostile environment installs all the same" test $rc = 0
		rm -f /root/.curlrc /root/bad-openssl.cnf # (the checks below use curl too)
		installed_ok 8080
		mkdir -p /root/x && cp /e2e/releases/v'"$VA"'/install.sh /root/x/install.sh && cd /root/x
		expect_exit 10 sh install.sh --yes </dev/null
		expect_exit 10 su -c "sh /root/x/install.sh --yes" root </dev/null
		cd /
		rm -f /root/.curlrc /root/bad-openssl.cnf; rm -rf /root/x /root/evil
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/dev/null
		expect_exit 0 bash /e2e/releases/v'"$VA"'/uninstall.sh --yes </dev/null'
}

case_pieces() { # the real protection pieces of the repository (not the stubs), as far as they speak the interface
	local sys=$1 m
	cp -r "$SCRATCH/rel/v$VR" "$SCRATCH/world/$sys/releases/"
	m=$(start_machine "$sys" pieces docker)
	M "$m" 'rc=0; '"$INSTALL_A"' --yes --version '"$VR"' --reachable internet --http-port 8080 --https-port 8443 </dev/null >/e2e/out/pieces.txt 2>&1 || rc=$?
		grep -E "Protection|PIECE|piece|done\\.|not installed" /e2e/out/pieces.txt || true
		# Cosmos is installed whatever the pieces are; protection that was asked
		# for (reachable from the internet) and is missing ends with 16 and says so
		if grep -qE "not installed:|failed:" /e2e/out/pieces.txt; then
			check "protection asked for but missing: exit 16" test $rc = 16
			check "and the last line says so" sh -c "tail -n 1 /e2e/out/pieces.txt | grep -q \"installed WITHOUT the protection you asked for:\""
		else
			check "every piece ran: exit 0" test $rc = 0
		fi
		installed_ok 8080
		for p in unattended-upgrades fail2ban nftables; do
			if grep -q "PIECE NOT RUN: $p" /e2e/out/pieces.txt || grep -q "protection piece $p is not in this release" /e2e/out/pieces.txt; then
				echo "SKIPPED: the real piece $p does not speak the installer'\''s interface yet (work/guard not merged): it was not run"
				check "and nothing of $p is recorded" sh -c "! grep -qE \"^piece(-ran)? $p\" /var/lib/cosmos-installer/record"
			else
				check "the real piece $p ran and is recorded" grep -qx "piece-ran $p" /var/lib/cosmos-installer/record
			fi
		done
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/pieces-uninstall.txt 2>&1
		check "uninstall ends well" sh -c "test ! -e /opt/cosmos"'
	rm -rf "$SCRATCH/world/$sys/releases/v$VR"
}

run_case() { # system case
	local sys=$1 c=$2 f rc=0
	f=$RESULTS/$sys-$c
	log "$sys: $c ..."
	# every case starts with the GitHub stand-in in its plain state
	github "$sys" "/cut?asset=" >/dev/null 2>&1 || true
	github "$sys" "/latest?tag=" >/dev/null 2>&1 || true
	github "$sys" "/down?set=0" >/dev/null 2>&1 || true
	rm -rf "$SCRATCH/world/$sys/releases" "$SCRATCH/world/$sys/nosig.sig"
	mkdir -p "$SCRATCH/world/$sys/releases"
	cp -r "$SCRATCH/rel/v$VA" "$SCRATCH/world/$sys/releases/"
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
		docker run --rm -v "$HERE:/src:ro" -w /src "$SHELLCHECK_IMAGE" -s bash "${files[@]}"
		docker run --rm -v "$HERE:/src:ro" -w /src "$SHELLCHECK_IMAGE" -s sh scripts/fork-install-e2e/stub-piece.sh
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
	m=$(start_machine "$sys" fresh-fast)
	M "$m" 'snap before; '"$INSTALL_A"' --yes </dev/null; installed_ok 80; record_matches before
		code=$(curl -s -o /e2e/out/r.json -w "%{http_code}" -X POST -H "Content-Type: application/json" --data "{}" http://127.0.0.1/cosmos/api/setup)
		check "POST /cosmos/api/setup without the setup code is refused" sh -c "test $code = 403 && grep -q SC001 /e2e/out/r.json"
		snap installed; expect_exit 10 '"$INSTALL_A"' --yes </dev/null; same installed after-second
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null; snap removed
		packages_back before removed
		expect_exit 4 '"$INSTALL_A"' --yes --from /e2e/variants/tampered-zip </dev/null; snap after-tampered; same removed after-tampered'
}

# On one line with main: bash reads a script as it runs it, so nothing after
# this line is ever read, even if the file is changed or replaced meanwhile.
# shellcheck disable=SC2317 # reached when main returns
{ main "$@"; exit; }
