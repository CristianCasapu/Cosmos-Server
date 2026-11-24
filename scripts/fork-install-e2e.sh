#!/bin/bash
# shellcheck disable=SC2016,SC2012,SC2317 # the snippets in single quotes run inside the test machines; SC2317: the functions are reached through main (see the last line)
# Fork addition: the test bed of the install scripts (fork/install/).
#
#   bash scripts/fork-install-e2e.sh fast     shellcheck, the guard checks + one machine (Debian 13):
#                                             install, refusals, a tampered zip, uninstall,
#                                             and a fork installed by hand left untouched
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
# What it may touch on the host, and nothing else:
#   - two folders it makes itself, SCRATCH and RESULTS, each exactly one
#     folder directly under /storage/dev/.cache/$PREFIX/ (or $RUNNER_TEMP/ on
#     GitHub), their real path (no link, no ..), marked with
#     .fork-install-e2e-folder when made; it deletes only a folder holding that
#     mark (remove_marked_folder), and refuses a path or a PREFIX outside this;
#   - containers, networks and images it starts, all named $PREFIX-* and
#     labelled fork-install-e2e.run=<this run's id>; it removes only by that
#     label (or, for "clean", by fork-install-e2e.prefix=$PREFIX), and refuses
#     to start when a container named $PREFIX-dind is already there;
#   - mounts: the worktree read-only, SCRATCH, and the Go caches
#     /storage/dev/.cache/go-mod and go-build.
#   Nothing here calls apt, dpkg, systemctl, install.sh or uninstall.sh, nor
#   runs a command held in a variable: the lint (host-lint.awk) proves it at
#   every run. The steps that do run in the test machines
#   (fork-install-e2e/machine-cases.sh), which refuse anywhere else.
# Privileged nesting: the Docker in Docker and the machines in it run
# --privileged, on the host's Docker. They see the host's block devices and
# could write the kernel settings that are not per container (/proc/sys);
# the test bed's own steps do neither, but nothing stops a step that would.
# Run it on a host where that is acceptable, or in a VM.
#
# Settings (environment): PREFIX, SCRATCH (default
# /storage/dev/.cache/$PREFIX/e2e; $RUNNER_TEMP/... on GitHub), SYSTEMS
# ("debian12 debian13 ubuntu2404 ubuntu2604"), CASES (the full list below),
# BUILD (a folder build.sh made, for its static/; else ./static, which
# "npx vite build --base=/cosmos-ui/" makes), KEEP=1, JOBS (systems in
# parallel, default 4). SYSTEMS and CASES take only the names listed here.
# UPSTREAM_CACHE: a folder that may already hold upstream's release zips the
# migration cases use (checked by their SHA-256 all the same; else they are
# downloaded from upstream's GitHub releases into SCRATCH).
#
# Results: $RESULTS (default $SCRATCH-results, kept after the clean-up)
# /<system>-<case>.log and a summary; exit 1 when any case failed.
set -euo pipefail
export LC_ALL=C

HERE=$(cd -- "$(dirname -- "$0")/.." && pwd)
PREFIX=${PREFIX:-cosmos-inst}
if [ "${GITHUB_ACTIONS:-}" = true ] && [ -n "${RUNNER_TEMP:-}" ]; then SCRATCH=${SCRATCH:-$RUNNER_TEMP/fork-install-e2e}; fi
SCRATCH=${SCRATCH:-/storage/dev/.cache/$PREFIX/e2e}
RESULTS=${RESULTS:-$SCRATCH-results}
ALL_SYSTEMS="debian12 debian13 ubuntu2404 ubuntu2604"
SYSTEMS=${SYSTEMS:-$ALL_SYSTEMS}
ALL_CASES="fresh byhand refusals tampered dryrun unavailable locked aptdaily aptrace apthang aptfail netcut interrupt docker-there hop trust env pieces wizardbind migrate migrate-refusals migrate-fail migrate-interrupt"
CASES=${CASES:-$ALL_CASES}
JOBS=${JOBS:-4}
REPO=example-owner/cosmos-fork
VA=0.24.0-fork.900 VB=0.24.0-fork.901 VC=0.24.0-fork.902
VO=0.24.0-fork.899 # older than A: the installer goes forward only
# this run's id: on every container, network and image it makes (a label),
# and in /.cosmos-e2e-machine of every test machine
RUN_ID=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
VR=0.24.0-fork.903 # with the repository's real protection pieces instead of the stubs
DIND=$PREFIX-dind
MARK=.fork-install-e2e-folder
# Every image is pinned by its digest (the index of all its platforms):
# what the test bed runs privileged cannot change under a tag.
DIND_IMAGE=docker:29-dind@sha256:4bbc7223f81db790097c6dea42fb93e05b8fead4f0431fbe6ccaba6e8473201c
DEBIAN12=debian:12@sha256:268fb24c39bca92d326dd83ea3abebe4a3f7751c804ffef23bb6d857dd343ff8
DEBIAN13=debian:13@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6
UBUNTU2404=ubuntu:24.04@sha256:73b999393d07f2bf65a40373778a44dc9a6d54ad7718d29247d8ff9413c4d6a8
UBUNTU2604=ubuntu:26.04@sha256:d66b366dac61b575bee2aef07e9c26e8070096fbe84a3d4d3eb0da069b5d76db
GOIMAGE=golang:1.27@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
SHELLCHECK_IMAGE=koalaman/shellcheck:v0.10.0@sha256:2097951f02e735b613f4a34de20c40f937a6c8f18ecb170612c88c34517221fb
# upstream's public release zips (the community edition) the migration cases
# install the upstream way, pinned by their SHA-256
UPSTREAM_ZIPS="0.24.0:0d73174d2d6eb340721fd3a5b9f4c7523a7a85cd4f47216256cd9b9564a1e4ae 0.23.4:9202db1f5332d92c84a9e1f6f11f99da87795943234cb7ee6df3e530bde29884"

log() { printf '[e2e] %s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }
LABELS=(--label "fork-install-e2e.prefix=$PREFIX" --label "fork-install-e2e.run=$RUN_ID")

# The only places the test bed writes on the host: refused unless PREFIX has
# the test bed's shape and SCRATCH and RESULTS are, by their real path, one
# folder directly under /storage/dev/.cache/$PREFIX/ (or $RUNNER_TEMP/).
check_paths() {
	[[ $PREFIX =~ ^cosmos-[a-z0-9-]{3,32}$ ]] || die "PREFIX must be cosmos-<3 to 32 of a-z, 0-9, ->: '$PREFIX'"
	# the systems and cases become parts of paths under SCRATCH and RESULTS
	# (removed with rm -rf): only the known names
	local s c
	for s in $SYSTEMS; do
		case " $ALL_SYSTEMS " in *" $s "*) ;; *) die "unknown system '$s' (SYSTEMS)" ;; esac
	done
	for c in $CASES; do
		case " $ALL_CASES fresh-fast " in *" $c "*) ;; *) die "unknown case '$c' (CASES)" ;; esac
	done
	local p real base allowed
	for p in "$SCRATCH" "$RESULTS"; do
		[[ $p == /?* ]] || die "'$p' is not an absolute path"
		real=$(realpath -m -- "$p")
		[ "$real" = "$p" ] || die "'$p' is not its own real path ('$real'): no link, no .., no / at the end"
		base=$(dirname -- "$real")
		allowed=0
		[ "$base" != "/storage/dev/.cache/$PREFIX" ] || allowed=1
		# RUNNER_TEMP only on GitHub's runners, and never a top folder
		if [ "${GITHUB_ACTIONS:-}" = true ] && [ -n "${RUNNER_TEMP:-}" ] && [[ $(realpath -m -- "$RUNNER_TEMP") == /?*/?* ]] &&
			[ "$base" = "$(realpath -m -- "$RUNNER_TEMP")" ]; then allowed=1; fi
		[ "$allowed" = 1 ] || die "'$p' must be one folder directly under /storage/dev/.cache/$PREFIX/ (or \$RUNNER_TEMP/ on GitHub)"
		if [ -e "$p" ] && [ ! -f "$p/$MARK" ] && [ -n "$(ls -A -- "$p" 2>/dev/null)" ]; then
			die "'$p' holds files and was not made by the test bed (no $MARK in it): remove it yourself, or choose another"
		fi
	done
	[ "$SCRATCH" != "$RESULTS" ] || die "SCRATCH and RESULTS must differ"
}
make_folder() { # one of SCRATCH, RESULTS
	mkdir -p -- "$1"
	printf 'made by scripts/fork-install-e2e.sh (prefix %s)\n' "$PREFIX" >"$1/$MARK"
}
# remove_marked_folder <SCRATCH or RESULTS>: the only recursive removal of a
# folder of the host, and only of one of the two checked folders that holds
# the test bed's mark. Files in it may be root's (written by containers): it
# empties it in a container that sees only that folder.
remove_marked_folder() {
	local folder=$1
	[ "$folder" = "$SCRATCH" ] || [ "$folder" = "$RESULTS" ] || die "remove_marked_folder: '$folder' is neither SCRATCH nor RESULTS"
	[ -d "$folder" ] || return 0
	if [ ! -f "$folder/$MARK" ]; then log "not removing '$folder': it has no $MARK"; return 0; fi
	docker run --rm --name "$PREFIX-clean" "${LABELS[@]}" -v "$folder:/w" "$DEBIAN13" find /w -mindepth 1 -delete # lint-ok: the checked folder only
	rmdir -- "$folder"
}

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
	if [ "${KEEP:-0}" = 1 ]; then log "KEEP=1: left $DIND and $SCRATCH (clean with: $0 clean)"; return "$rc"; fi
	log "removing what the test bed started"
	remove_labelled "fork-install-e2e.run=$RUN_ID"
	remove_marked_folder "$SCRATCH" >/dev/null 2>&1 || log "could not remove $SCRATCH"
	return "$rc"
}
# remove_labelled <label=value>: the containers, networks and images carrying it
remove_labelled() {
	local ids
	mapfile -t ids < <(docker ps -aq --filter "label=$1")
	[ "${#ids[@]}" = 0 ] || docker rm -f -v "${ids[@]}" >/dev/null 2>&1 || true
	mapfile -t ids < <(docker network ls -q --filter "label=$1")
	[ "${#ids[@]}" = 0 ] || docker network rm "${ids[@]}" >/dev/null 2>&1 || true
	mapfile -t ids < <(docker image ls -q --filter "label=$1")
	[ "${#ids[@]}" = 0 ] || docker image rm -f "${ids[@]}" >/dev/null 2>&1 || true
}

# ---- Builds (on the host's Docker, in throwaway containers; or natively on CI) ----
native_tools() { command -v go >/dev/null && command -v zip >/dev/null && command -v unzip >/dev/null && command -v openssl >/dev/null && command -v jq >/dev/null; }

in_go() { # command in the Go toolchain, the worktree at /src (read-only) and $SCRATCH at /scratch
	if native_tools; then (cd "$HERE" && SRC=$HERE SCR=$SCRATCH bash -c "$1"); return; fi # lint-ok: the literal build steps of this file, which the lint reads too
	docker run --rm --name "$PREFIX-build" "${LABELS[@]}" -v "$HERE:/src:ro" -v "$SCRATCH:/scratch" \
		-v /storage/dev/.cache/go-mod:/go/pkg/mod -v /storage/dev/.cache/go-build:/root/.cache/go-build \
		-e SRC=/src -e SCR=/scratch -e CGO_ENABLED=0 -w /src "$GOIMAGE" bash -c "$1" # lint-ok: the literal build steps of this file
}
in_builder() { # zip, unzip, openssl, jq
	if native_tools; then (cd "$HERE" && SRC=$HERE SCR=$SCRATCH bash -c "$1"); return; fi # lint-ok: the literal build steps of this file, which the lint reads too
	if ! docker image inspect "$PREFIX-builder:$RUN_ID" >/dev/null 2>&1; then
		docker build -q "${LABELS[@]}" -t "$PREFIX-builder:$RUN_ID" - >/dev/null <<BUILDER
FROM $DEBIAN13
RUN apt-get -qq update && apt-get -qq install -y --no-install-recommends zip unzip openssl jq git ca-certificates >/dev/null
BUILDER
	fi
	docker run --rm --name "$PREFIX-build" "${LABELS[@]}" -v "$HERE:/src:ro" -v "$SCRATCH:/scratch" -e SRC=/src -e SCR=/scratch -w /src "$PREFIX-builder:$RUN_ID" bash -c "$1" # lint-ok: the literal build steps of this file
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
		die "no panel: give BUILD (a folder build.sh made) or build ./static first (npx vite build --base=/cosmos-ui/)"
	fi

	local v dir
	for v in $VO $VA $VB $VC $VR; do
		dir=$SCRATCH/stage/$v
		rm -rf "$SCRATCH/stage/$v"
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
			rm -rf "$SCR/stage/$v/src" "$SCR/stage/$v/out"
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
			{ echo cosmos-fork-scripts-v1; echo "version $v"; for s in install.sh migrate.sh uninstall.sh; do echo "sha256 $(sha256sum "$out/$s" | cut -d" " -f1) $s"; done; } >"$out/manifest.scripts"
			openssl pkeyutl -sign -inkey "$SCR/keys/test.pem" -rawin -in "$out/manifest.scripts" -out "$out/signature.scripts"
			{ cat "$out/manifest.scripts"; echo "signature $(base64 -w0 "$out/signature.scripts")"; } >"$out/cosmos-cloud-$v.scripts.sig"
			rm "$out/manifest" "$out/signature" "$out/manifest.scripts" "$out/signature.scripts"
			rm -rf "$SCR/rel/v$v"
			mv "$out" "$SCR/rel/v$v"
		done

		# the variants that must be refused, all of release A
		rm -rf "$SCR/variants"
		mkdir "$SCR/variants"
		cd "$SCR/variants"
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
	# the test mode of the migration: the repository's unstamped script and the test key
	cp "$HERE/fork/install/install.sh" "$SCRATCH/bin/install-unstamped.sh"
	cp "$SCRATCH/keys/test.pub" "$SCRATCH/bin/test.pub"
	upstream_zips
}

# upstream_zips: upstream's release zips into $SCRATCH/bin/upstream (the
# machines see them read-only), from UPSTREAM_CACHE or upstream's releases,
# each checked against its pinned SHA-256
upstream_zips() {
	local pair v sum f
	mkdir -p "$SCRATCH/bin/upstream"
	for pair in $UPSTREAM_ZIPS; do
		v=${pair%%:*} sum=${pair#*:} f=cosmos-cloud-${pair%%:*}-amd64.zip
		if [ -n "${UPSTREAM_CACHE:-}" ] && [ -f "$UPSTREAM_CACHE/$f" ]; then
			cp "$UPSTREAM_CACHE/$f" "$SCRATCH/bin/upstream/$f"
		else
			curl -fsSL --proto '=https' --max-time 900 -o "$SCRATCH/bin/upstream/$f" "https://github.com/azukaar/Cosmos-Server/releases/download/v$v/$f"
		fi
		[ "$(sha256sum "$SCRATCH/bin/upstream/$f" | cut -d' ' -f1)" = "$sum" ] || die "upstream's $f is not the one pinned"
	done
	log "upstream's releases for the migration: $(ls "$SCRATCH/bin/upstream" | tr '\n' ' ')"
}

# ---- The Docker in Docker and the machines ---------------------------------------
start_dind() {
	local ids
	if [ -n "$(docker ps -aq --filter "name=^$DIND\$")" ] || [ -n "$(docker network ls -q --filter "name=^$PREFIX-net\$")" ]; then
		die "a container $DIND or a network $PREFIX-net is already there, not made by this run: if it is what an earlier run of the test bed left, remove it with '$0 clean'; otherwise choose another PREFIX"
	fi
	docker network create "${LABELS[@]}" "$PREFIX-net" >/dev/null
	cp "$HERE/scripts/fork-install-e2e/machine-lib.sh" "$HERE/scripts/fork-install-e2e/machine-cases.sh" "$SCRATCH/bin/"
	docker run -d --name "$DIND" "${LABELS[@]}" --privileged --network "$PREFIX-net" -e DOCKER_TLS_CERTDIR= \
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
		-v "/e2e/out/$sys-$case:/e2e/out" -v /e2e/machine-id:/.cosmos-e2e-machine:ro \
		-v "/e2e/world/$sys/shared/ca.pem:/usr/local/share/ca-certificates/fork-e2e-test-ca.crt:ro" \
		"$image" >/dev/null
	local state
	for _ in $(seq 1 60); do
		state=$(M "$name" machine_state 2>/dev/null || true)
		case $state in running | degraded) break ;; esac
		sleep 1
	done
	# a machine that already has ca-certificates trusts the test CA now
	M "$name" trust_test_ca
	echo "$name"
}

# M <machine> <function> [names of host variables it reads]: runs one
# function of machine-lib.sh / machine-cases.sh in a test machine. This
# script itself holds no command that changes a system (no apt, dpkg,
# systemctl, install.sh, uninstall.sh): those are only in the two files that
# run in the machines, which refuse to run anywhere without this run's
# marker (guard_checks tests both).
M() {
	local m=$1 fn=$2 v env=(-e "E2E_RUN_ID=$RUN_ID" -e "VO=$VO" -e "VA=$VA" -e "VB=$VB" -e "VC=$VC" -e "VR=$VR")
	shift 2
	[[ $m == "$PREFIX"-* ]] || die "M: $m is not a test machine of this test bed"
	[[ $fn =~ ^[a-z_][a-z0-9_]*$ ]] || die "M: $fn is not a function name"
	for v in "$@"; do
		[[ $v =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "M: $v is not a variable name"
		env+=(-e "$v=${!v}")
	done
	# shellcheck disable=SC2016 # expanded in the machine
	D exec -i "${env[@]}" "$m" bash -c 'source /e2e/bin/machine-lib.sh && source /e2e/bin/machine-cases.sh && "$0"' "$fn"
}

# ---- The cases ------------------------------------------------------------------
# Each prints what it checks; a failing check makes the case fail.

case_fresh() { # fresh install, refusals over it, C0, update, rollback, offline reinstall, uninstall to the state of before
	local sys=$1 m
	m=$(start_machine "$sys" fresh)
	M "$m" m_fresh_1
	D rm -f "$PREFIX-$sys-igd" >/dev/null 2>&1 || true
	D run -d --name "$PREFIX-$sys-igd" --network "$PREFIX-$sys" -v /e2e/bin:/bin-e2e:ro "$DEBIAN13" /bin-e2e/fake-igd >/dev/null
	sleep 2
	local igd
	# shellcheck disable=SC2034 # read by M (passed by name)
	igd=$(D inspect -f "{{(index .NetworkSettings.Networks \"$PREFIX-$sys\").IPAddress}}" "$PREFIX-$sys-igd")
	M "$m" m_fresh_2 igd
	D rm -f "$PREFIX-$sys-igd" >/dev/null
	cp -r "$SCRATCH/rel/v$VB" "$SCRATCH/world/$sys/releases/"
	M "$m" m_fresh_3 VB
	cp -r "$SCRATCH/rel/v$VC" "$SCRATCH/world/$sys/releases/"
	M "$m" m_fresh_4 VB VC
	rm -rf "$SCRATCH/world/$sys/releases/v$VC" "$SCRATCH/world/$sys/releases/v$VB"
	M "$m" m_fresh_5
	D network disconnect "$PREFIX-$sys" "$m"
	M "$m" m_fresh_6 VA
	D network connect "$PREFIX-$sys" "$m"
	M "$m" m_fresh_7
}

case_refusals() { # existing installations that are not the fork's own run
	local sys=$1 m
	m=$(start_machine "$sys" refusals)
	M "$m" m_refusals_1 VA
}

case_tampered() { # each refused before anything is unpacked, nothing left (the tools it installed are taken back)
	local sys=$1 m v
	m=$(start_machine "$sys" tampered)
	M "$m" m_tampered_1
	# longlist-*: the bad entry first in a listing far over 64 KiB (a check
	# written as "listing | grep -q" under pipefail would let it through)
	for v in tampered-zip tampered-sig other-key no-sig dotdot absolute symlink duplicate setuid worldwritable device ctrlchar bomb \
		longlist-dotdot longlist-setuid longlist-symlink; do
		M "$m" m_tampered_2 v
	done
	# the download path: the release on the stand-in without its signature
	mv "$SCRATCH/world/$sys/releases/v$VA/cosmos-cloud-$VA.sig" "$SCRATCH/world/$sys/nosig.sig"
	M "$m" m_tampered_3
	mv "$SCRATCH/world/$sys/nosig.sig" "$SCRATCH/world/$sys/releases/v$VA/cosmos-cloud-$VA.sig"
	# another release without a signed file of scripts: its install.sh is not run
	mkdir -p "$SCRATCH/world/$sys/releases/v0.24.0-fork.950"
	cp "$SCRATCH/rel/v$VA/install.sh" "$SCRATCH/world/$sys/releases/v0.24.0-fork.950/"
	M "$m" m_tampered_4
	rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.950"
}

case_dryrun() {
	local sys=$1 m
	m=$(start_machine "$sys" dryrun)
	# hooks of the system'\''s apt configuration (as Ubuntu'\''s command-not-found
	# has): the simulation must not run them
	M "$m" m_dryrun_1
	m=$(start_machine "$sys" dryrun2 docker)
	M "$m" m_dryrun_2
}

case_unavailable() { # a package apt cannot install: stops before any change
	local sys=$1 m
	m=$(start_machine "$sys" unavailable)
	M "$m" m_unavailable_1
}

case_locked() { # apt is held by another program: the script waits, then installs
	local sys=$1 m
	m=$(start_machine "$sys" locked)
	M "$m" m_locked_1
}

case_aptdaily() { # the system's own apt-daily and unattended-upgrades hold apt when the installer starts (as on every new Ubuntu server)
	local sys=$1 m
	m=$(start_machine "$sys" aptdaily)
	M "$m" m_aptdaily_1
}

case_netcut() { # the download breaks off: refused, nothing left
	local sys=$1 m
	m=$(start_machine "$sys" netcut)
	M "$m" m_netcut_1
	github "$sys" "/cut?asset=amd64.zip&after=3000000"
	M "$m" m_netcut_2
	github "$sys" "/cut?asset="
	M "$m" m_netcut_3
}

case_interrupt() { # kill -9 at three points, each followed by another run; packages of the admin's; uninstall with Docker stopped
	local sys=$1 m point
	m=$(start_machine "$sys" interrupt)
	M "$m" m_interrupt_1
	for point in packages cosmos starting; do
		M "$m" m_interrupt_2 point
		sleep 2
		# between an interrupted run and the next, the admin installs a package
		if [ "$point" = cosmos ]; then
			M "$m" m_interrupt_3
		fi
	done
	# and during the run that finishes, another one
	M "$m" m_interrupt_4 VA
}

case_docker_there() { # Docker of the distribution is used and left alone; protection pieces through the interface
	local sys=$1 m
	m=$(start_machine "$sys" docker-there docker)
	M "$m" m_docker_there_1
}

case_hop() { # the script of A, the newest release is B: B's own verified install.sh installs B
	local sys=$1 m
	cp -r "$SCRATCH/rel/v$VB" "$SCRATCH/world/$sys/releases/"
	m=$(start_machine "$sys" hop)
	M "$m" m_hop_1 VB
	rm -rf "$SCRATCH/world/$sys/releases/v$VB"
}

case_aptfail() { # apt fails after the simulation passed: the lists held too long, then a package that cannot be installed
	local sys=$1 m
	m=$(start_machine "$sys" aptfail)
	M "$m" m_aptfail_1
}

case_trust() { # what must never be fetched or run: addresses outside GitHub, next scripts that do not verify, older releases
	local sys=$1 m to n
	m=$(start_machine "$sys" trust docker)
	M "$m" m_trust_1
	for to in https://evilgithubusercontent.com/x https://github.com.evil.example/x "https://github.com@evil.example/x" \
		https://release-assets.githubusercontent.com:8443/x http://release-assets.githubusercontent.com/x https://RELEASE-ASSETS.githubusercontent.com/x; do
		github "$sys" "/redirect?to=$to"
		M "$m" m_trust_2 to
	done
	github "$sys" "/redirect?to="
	for n in 910 911 912; do
		cp -r "$SCRATCH/variants/hop/v0.24.0-fork.$n" "$SCRATCH/world/$sys/releases/"
		M "$m" m_trust_3 n
		rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.$n"
	done
	cp -r "$SCRATCH/variants/hop/v0.24.0-fork.910" "$SCRATCH/world/$sys/releases/"
	M "$m" m_trust_4
	rm -rf "$SCRATCH/world/$sys/releases/v0.24.0-fork.910"
	cp -r "$SCRATCH/rel/v$VO" "$SCRATCH/world/$sys/releases/"
	github "$sys" "/latest?tag=v$VO"
	M "$m" m_trust_5 VO
}

case_env() { # never from a pipe, whatever the current folder holds; a hostile environment changes nothing
	local sys=$1 m
	m=$(start_machine "$sys" env docker)
	M "$m" m_env_1 VA
}

case_pieces() { # the real protection pieces of the repository (not the stubs), as far as they speak the interface
	local sys=$1 m
	cp -r "$SCRATCH/rel/v$VR" "$SCRATCH/world/$sys/releases/"
	m=$(start_machine "$sys" pieces docker)
	M "$m" m_pieces_1 VR
	rm -rf "$SCRATCH/world/$sys/releases/v$VR"
	# the pieces' packages: simulated only for pieces that run (the stubs of A)
	m=$(start_machine "$sys" pieces2 docker)
	M "$m" m_pieces_2
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
	local ids
	mapfile -t ids < <(D ps -aq --filter "name=^$PREFIX-$sys-$c")
	[ "${#ids[@]}" = 0 ] || D rm -f -v "${ids[@]}" >/dev/null 2>&1 || true
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
	local files=(fork/install/install.sh fork/install/uninstall.sh scripts/fork-release-package.sh scripts/fork-install-e2e.sh scripts/fork-install-e2e/machine-lib.sh scripts/fork-install-e2e/machine-cases.sh)
	if command -v shellcheck >/dev/null; then
		(cd "$HERE" && shellcheck -s bash "${files[@]}" && shellcheck -s sh scripts/fork-install-e2e/stub-piece.sh)
	else
		docker run --rm --name "$PREFIX-shellcheck" "${LABELS[@]}" -v "$HERE:/src:ro" -w /src "$SHELLCHECK_IMAGE" -s bash "${files[@]}"
		docker run --rm --name "$PREFIX-shellcheck" "${LABELS[@]}" -v "$HERE:/src:ro" -w /src "$SHELLCHECK_IMAGE" -s sh scripts/fork-install-e2e/stub-piece.sh
	fi
}

# The test bed can never change the host (the fast part runs this on every
# push): (1) this script holds no call of apt, dpkg, systemctl, install.sh or
# uninstall.sh and no recursive removal of a system path, those being only
# in machine-lib.sh and machine-cases.sh; (2) those two refuse to run where
# the run's marker is missing or holds another id.
guard_checks() {
	log "guard checks"
	local lint=$HERE/scripts/fork-install-e2e/host-lint.awk bad out rc
	# the lint flags exactly the bad lines of its sample (the very line that
	# once ran on a host among them), and none of the good ones
	local sample=$HERE/scripts/fork-install-e2e/host-lint-sample.txt
	bad=$(awk -f "$lint" "$sample" 2>/dev/null | cut -d: -f1 | tr '\n' ' ')
	[ "$bad" = "$(awk '/^# ---- bad/ { b = 1; next } /^# ---- good/ { b = 0 } b && !/^#/ { printf "%d ", NR }' "$sample")" ] ||
		die "guard: the lint does not flag exactly the bad lines of $sample (it flagged lines: $bad)"
	bad=$(awk -f "$lint" "$HERE/scripts/fork-install-e2e.sh")
	if [ -n "$bad" ]; then
		printf '%s\n' "$bad" >&2
		die "guard: the host side of the test bed holds calls that belong in machine-cases.sh (above)"
	fi
	# the machine side refuses in a container without the marker, and with another run's marker
	printf 'another-run\n' >"$SCRATCH/guard-marker"
	for marker in none other; do
		local mount=()
		[ "$marker" = none ] || mount=(-v "$SCRATCH/guard-marker:/.cosmos-e2e-machine:ro")
		rc=0
		out=$(docker run --rm --name "$PREFIX-guard" "${LABELS[@]}" "${mount[@]}" -v "$HERE/scripts/fork-install-e2e:/e2e/bin:ro" -e E2E_RUN_ID=this-run -e VA="$VA" "$DEBIAN13" \
			bash -c 'source /e2e/bin/machine-lib.sh && source /e2e/bin/machine-cases.sh && m_fresh_fast_1; echo STEP-RAN' 2>&1) || rc=$?
		if [ "$rc" != 99 ] || [[ $out != *"REFUSED: this is not a test machine"* ]] || [[ $out == *STEP-RAN* ]]; then
			printf '%s\n' "$out" >&2
			die "guard: a machine step ran (exit $rc) in a container with marker '$marker'"
		fi
		rc=0
		out=$(docker run --rm --name "$PREFIX-guard" "${LABELS[@]}" "${mount[@]}" -v "$HERE/scripts/fork-install-e2e:/e2e/bin:ro" -e E2E_RUN_ID=this-run "$DEBIAN13" \
			bash -c 'source /e2e/bin/machine-cases.sh; echo STEP-RAN' 2>&1) || rc=$?
		if [ "$rc" != 99 ] || [[ $out == *STEP-RAN* ]]; then die "guard: machine-cases.sh ran on its own in a container with marker '$marker'"; fi
	done
	rm -f "$SCRATCH/guard-marker"
	# the folders and the prefix it may use: anything else is refused before
	# anything is removed (each tried with "clean", the command that removes)
	local try base
	base=$(dirname "$SCRATCH") # /storage/dev/.cache/$PREFIX, or $RUNNER_TEMP
	for try in "SCRATCH=/storage/dev/.cache" "SCRATCH=/" "SCRATCH=/storage/dev" "SCRATCH=$base" "SCRATCH=$base/a/b" \
		"SCRATCH=$base/../x" "SCRATCH=$base/x/" "SCRATCH=x" "RESULTS=/storage/dev/.cache" "RESULTS=$SCRATCH" "PREFIX=cosmos" "PREFIX=../x" \
		"PREFIX=cosmos-a b" "SYSTEMS=../../.." "SYSTEMS=debian13 /" "CASES=../x" "CASES=fresh *"; do
		rc=0
		out=$(env SCRATCH="$SCRATCH" RESULTS="$RESULTS" "$try" bash "$HERE/scripts/fork-install-e2e.sh" clean 2>&1) || rc=$?
		if [ "$rc" = 0 ] || [[ $out != *ERROR* ]]; then die "guard: '$try' was not refused (exit $rc): $out"; fi
	done
	mkdir -p "$base/guard-link-target"
	ln -sfn "$base/guard-link-target" "$base/guard-link"
	rc=0
	out=$(env SCRATCH="$base/guard-link" RESULTS="$RESULTS" bash "$HERE/scripts/fork-install-e2e.sh" clean 2>&1) || rc=$?
	rm -f "$base/guard-link"
	rmdir "$base/guard-link-target"
	if [ "$rc" = 0 ] || [[ $out != *"not its own real path"* ]]; then die "guard: a SCRATCH through a link was not refused (exit $rc): $out"; fi
	log "guard checks: the host side calls nothing of a system; the machine side refuses without this run's marker"
}

main() {
	local cmd=${1:-}
	case $cmd in
	clean)
		check_paths
		remove_labelled "fork-install-e2e.prefix=$PREFIX"
		remove_marked_folder "$SCRATCH"
		exit 0 ;;
	fast | full) ;;
	one) # one system, one case (their names checked by check_paths)
		if [ "$#" != 3 ] || [[ $2$3 == *[[:space:]]* ]]; then die "one <system> <case>"; fi
		SYSTEMS=$2 CASES=$3 ;;
	*) sed -n '2,60p' "$0"; exit 2 ;;
	esac
	check_paths
	shellcheck_scripts
	trap cleanup EXIT
	trap 'exit 130' INT TERM
	make_folder "$SCRATCH"
	mkdir -p "$SCRATCH/bin"
	guard_checks
	# only the machines this run makes carry its id (/.cosmos-e2e-machine)
	printf '%s\n' "$RUN_ID" >"$SCRATCH/machine-id"
	remove_marked_folder "$RESULTS"
	make_folder "$RESULTS"
	rm -rf "$SCRATCH/out"
	build_releases
	start_dind
	case $cmd in
	fast)
		run_system debian13 fresh-fast byhand
		;;
	one)
		run_system "$SYSTEMS" "$CASES"
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

case_byhand() { # a fork installed by hand (no record), as the owner's server is: uninstall.sh and install.sh touch nothing
	local sys=$1 m
	m=$(start_machine "$sys" byhand docker)
	M "$m" m_byhand_1
}

case_apthang() { # a package script that hangs: the whole transaction is stopped at the bound, the next run finishes
	local sys=$1 m
	m=$(start_machine "$sys" apthang)
	M "$m" m_apthang_1
}

case_aptrace() { # another apt installs a package of the plan while install.sh waits: not recorded as install.sh's
	local sys=$1 m
	m=$(start_machine "$sys" aptrace docker)
	M "$m" m_aptrace_1
}

case_wizardbind() { # where the wizard really listens: an untrusted marker said loudly; at home with a global IPv6, loopback
	local sys=$1 m
	m=$(start_machine "$sys" wizardbind)
	M "$m" m_wizardbind_1
	m=$(start_machine "$sys" wizardbind6)
	D network create --ipv6 --subnet 2001:db8:60:"$(printf '%x' "$RANDOM")"::/64 "${LABELS[@]}" "$PREFIX-$sys-v6" >/dev/null
	D network connect "$PREFIX-$sys-v6" "$m"
	M "$m" m_wizardbind_2
	D network disconnect "$PREFIX-$sys-v6" "$m" >/dev/null 2>&1 || true
	D network rm "$PREFIX-$sys-v6" >/dev/null 2>&1 || true
}

case_migrate() { # upstream's Cosmos 0.24.0 (the fork's base) and 0.23.4 moved to the fork; then the fork's own update
	local sys=$1 m UPV
	for UPV in 0.24.0 0.23.4; do
		[ -z "${m:-}" ] || D rm -f -v "$m" >/dev/null
		m=$(start_machine "$sys" "migrate-${UPV//./}" docker)
		M "$m" m_migrate_1 UPV
	done
	cp -r "$SCRATCH/rel/v$VB" "$SCRATCH/world/$sys/releases/"
	M "$m" m_migrate_2 VB
	rm -rf "$SCRATCH/world/$sys/releases/v$VB"
}

case_migrate_refusals() { # what is not migrated, each refused and nothing changed; a licence migrated with the flag
	local sys=$1 m
	# one machine at a time (each removed when done: privileged systemd
	# machines of four systems at once can leave the next one dying at start)
	m=$(start_machine "$sys" migrate-refusals-none docker)
	M "$m" m_migrate_refusals_1
	D rm -f -v "$m" >/dev/null
	m=$(start_machine "$sys" migrate-refusals-fork docker)
	M "$m" m_migrate_refusals_2
	D rm -f -v "$m" >/dev/null
	m=$(start_machine "$sys" migrate-refusals-upstream docker)
	M "$m" m_migrate_refusals_3
}

case_migrate_fail() { # a failure at each step, a release that does not start, a container that changes: rolled back byte for byte
	local sys=$1 m
	m=$(start_machine "$sys" migrate-fail docker)
	M "$m" m_migrate_fail_1
	cp -r "$SCRATCH/rel/v$VC" "$SCRATCH/world/$sys/releases/"
	M "$m" m_migrate_fail_2 VC
	rm -rf "$SCRATCH/world/$sys/releases/v$VC"
}

case_migrate_interrupt() { # kill -9 at each step then --undo, byte for byte; killed after the swap then run again
	local sys=$1 m
	m=$(start_machine "$sys" migrate-interrupt docker)
	M "$m" m_migrate_interrupt_1
}

case_fresh_fast() { # the checks of every push: install, refusal over it, a tampered zip, uninstall
	local sys=$1 m
	m=$(start_machine "$sys" fresh-fast)
	M "$m" m_fresh_fast_1
}

# On one line with main: bash reads a script as it runs it, so nothing after
# this line is ever read, even if the file is changed or replaced meanwhile.
# shellcheck disable=SC2317 # reached when main returns
{ main "$@"; exit; }
