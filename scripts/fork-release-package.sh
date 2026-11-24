#!/bin/bash
# Fork addition: packages a release the way the updater expects it, with the
# install scripts inside every zip and next to them. The release workflow
# (.github/workflows/fork-release.yml, step "Package like upstream") runs it,
# and so does the install scripts' test bed (scripts/fork-install-e2e.sh),
# so that what is tested is what is published.
#
#   bash scripts/fork-release-package.sh --build <dir> --version <x.y.z-fork.n> \
#        --repository <owner/repo> --out <dir> [--keys <keys.go>] [--arches "amd64 arm64"] \
#        [--upstream <x.y.z> --build-number <n> --previous <tag>] [--no-deps-notes]
#
# <build> is what build.sh made (cosmos, cosmos-launcher, their -arm64
# twins, static/, meta.json, ...). It writes into <out>:
#   cosmos-cloud-<v>-<arch>.zip and .md5   (the top folder holds fork/, with
#                                          fork/install/install.sh stamped)
#   install.sh uninstall.sh SHA256SUMS     (the assets: names without an
#                                          architecture and not ending in .md5,
#                                          which the updater would take for a
#                                          zip or a checksum, src/update.go)
#   notes.md, dependencies.json            (only without --no-deps-notes)
# The release keys and the version are written into install.sh from
# keys.go (default src/forkrelease/keys.go). Any failure ends with a
# non-zero status and an "::error::" line, before anything is published.
set -euo pipefail
export LC_ALL=C

fail() { echo "::error::$*" >&2; exit 1; }

# The systems install.sh supports, in words, from its one list (the line
# SUPPORTED_SYSTEMS="debian:12 ..."): what the release notes say.
#   bash scripts/fork-release-package.sh --systems-of fork/install/install.sh
systems_of() {
	local list d="" u="" s
	list=$(sed -n 's/^SUPPORTED_SYSTEMS="\([a-z0-9.: ]*\)"$/\1/p' "$1")
	[ "$(printf '%s\n' "$list" | grep -c .)" = 1 ] || fail "$1 has no single SUPPORTED_SYSTEMS line"
	for s in $list; do
		case $s in
		debian:[0-9]*) d=${d:+$d/}${s#*:} ;;
		ubuntu:[0-9]*) u=${u:+$u/}${s#*:} ;;
		*) fail "$1: '$s' in SUPPORTED_SYSTEMS is neither debian:<n> nor ubuntu:<n>" ;;
		esac
	done
	if [ -z "$d" ] || [ -z "$u" ]; then fail "$1: SUPPORTED_SYSTEMS lacks Debian or Ubuntu"; fi
	printf 'Debian %s or Ubuntu %s\n' "$d" "$u"
}
if [ "${1:-}" = --systems-of ]; then systems_of "$2"; exit 0; fi

BUILD="" VERSION="" REPOSITORY="" OUT="" KEYS=src/forkrelease/keys.go ARCHES="amd64 arm64"
UPSTREAM="" BUILD_NUMBER="" PREVIOUS="" DEPS_NOTES=1
while [ $# -gt 0 ]; do
	case $1 in
	--build) BUILD=$2; shift ;;
	--version) VERSION=$2; shift ;;
	--repository) REPOSITORY=$2; shift ;;
	--out) OUT=$2; shift ;;
	--keys) KEYS=$2; shift ;;
	--arches) ARCHES=$2; shift ;;
	--upstream) UPSTREAM=$2; shift ;;
	--build-number) BUILD_NUMBER=$2; shift ;;
	--previous) PREVIOUS=$2; shift ;;
	--no-deps-notes) DEPS_NOTES=0 ;;
	*) fail "unknown option $1" ;;
	esac
	shift
done
[ -d "$BUILD" ] || fail "--build: $BUILD is not a folder"
[[ $VERSION =~ ^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}-fork\.[1-9][0-9]{0,8}$ ]] || fail "--version: '$VERSION' is not a fork version"
[[ $REPOSITORY =~ ^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$ ]] || fail "--repository: '$REPOSITORY' is not owner/name"
[ -n "$OUT" ] || fail "--out is missing"
[ -f "$KEYS" ] || fail "--keys: $KEYS is missing"
HERE=$(cd -- "$(dirname -- "$0")/.." && pwd)
FORK_DIR=$HERE/fork

# The release keys, the same way the build checks them in the binaries.
keys=$(grep -oE '^[[:space:]]*"[A-Za-z0-9+/]{43}=",' "$KEYS" | grep -oE '[A-Za-z0-9+/]{43}=' | tr '\n' ' ' | sed 's/ $//' || true)
[ -n "$keys" ] || fail "$KEYS holds no release key: install.sh could verify nothing"

mkdir -p "$OUT"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/fork-release-package.XXXXXXXX")
trap 'rm -rf -- "$WORK"' EXIT

# 1. The scripts, stamped once; the same bytes go into the zips and the assets.
src=$FORK_DIR/install/install.sh
[ "$(grep -c "^RELEASE_KEYS='@FORK_RELEASE_KEYS@'$" "$src")" = 1 ] || fail "install.sh lacks its placeholder line for the keys"
[ "$(grep -c "^RELEASE_VERSION='@FORK_RELEASE_VERSION@'$" "$src")" = 1 ] || fail "install.sh lacks its placeholder line for the version"
[ "$(grep -c "^RELEASE_REPOSITORY='@FORK_RELEASE_REPOSITORY@'$" "$src")" = 1 ] || fail "install.sh lacks its placeholder line for the repository"
sed -e "s|^RELEASE_KEYS='@FORK_RELEASE_KEYS@'\$|RELEASE_KEYS='$keys'|" \
	-e "s|^RELEASE_VERSION='@FORK_RELEASE_VERSION@'\$|RELEASE_VERSION='$VERSION'|" \
	-e "s|^RELEASE_REPOSITORY='@FORK_RELEASE_REPOSITORY@'\$|RELEASE_REPOSITORY='$REPOSITORY'|" "$src" >"$WORK/install.sh"
! grep -q '@FORK_RELEASE_[A-Z]*@' "$WORK/install.sh" || fail "a placeholder survived in install.sh"
[ "$(sed -n "s/^RELEASE_KEYS='\(.*\)'\$/\1/p" "$WORK/install.sh")" = "$keys" ] || fail "the keys in install.sh are not those of $KEYS"
bash -n "$WORK/install.sh" || fail "the stamped install.sh is not valid bash"
SYSTEMS_WORDS=$(systems_of "$WORK/install.sh") # what the notes say it supports (fails here when it cannot tell)
cp "$FORK_DIR/install/uninstall.sh" "$WORK/uninstall.sh"
bash -n "$WORK/uninstall.sh" || fail "uninstall.sh is not valid bash"
chmod 755 "$WORK/install.sh" "$WORK/uninstall.sh"

# 2. What the release changes in its dependencies (the workflow only).
if [ "$DEPS_NOTES" = 1 ]; then
	deps=$(node "$HERE/scripts/fork-deps-notes.mjs" "$PREVIOUS" "$OUT" "$(date -u +%F)")
	if [ "$deps" != "{}" ]; then
		jq --argjson d "$deps" '.forkDependencies = $d' "$BUILD/meta.json" >"$BUILD/meta.json.new"
		mv "$BUILD/meta.json.new" "$BUILD/meta.json"
	fi
fi

# 3. The zips, laid out like upstream's, with fork/ inside.
pkg="cosmos-cloud-$VERSION"
for arch in $ARCHES; do
	case $arch in
	amd64) top=$pkg ;;
	arm64) top=$pkg-arm64 ;;
	*) fail "unknown architecture $arch" ;;
	esac
	dir=$WORK/$top
	mkdir "$dir"
	cp -r "$BUILD"/. "$dir/"
	if [ "$arch" = amd64 ]; then
		rm -f "$dir/cosmos-launcher-arm64" "$dir/cosmos-arm64" "$dir/nebula-arm" "$dir/nebula-arm-cert" "$dir/restic-arm"
	else
		mv "$dir/cosmos-arm64" "$dir/cosmos"
		mv "$dir/cosmos-launcher-arm64" "$dir/cosmos-launcher"
		[ ! -f "$dir/restic-arm" ] || mv "$dir/restic-arm" "$dir/restic"
	fi
	for f in cosmos cosmos-launcher start.sh meta.json; do
		[ -f "$dir/$f" ] || fail "$top lacks $f"
	done
	rm -rf "$dir/fork"
	cp -r "$FORK_DIR" "$dir/fork"
	cp "$WORK/install.sh" "$dir/fork/install/install.sh"
	cp "$WORK/uninstall.sh" "$dir/fork/install/uninstall.sh"
	if find "$dir" -type l | grep -q .; then fail "$top holds a symbolic link: install.sh refuses links"; fi
	if find "$dir" | sed "s|^$WORK/||" | grep -vqE '^([A-Za-z0-9._+@(),~-]+/)*[A-Za-z0-9._+@(),~-]+$'; then
		find "$dir" | sed "s|^$WORK/||" | grep -vE '^([A-Za-z0-9._+@(),~-]+/)*[A-Za-z0-9._+@(),~-]+$' | head -n 5 >&2
		fail "$top holds a file name install.sh does not accept"
	fi
	(cd "$WORK" && zip -qr "$OUT/$pkg-$arch.zip" "$top")
	(cd "$OUT" && md5sum "$pkg-$arch.zip" >"$pkg-$arch.zip.md5")
	unzip -tq "$OUT/$pkg-$arch.zip" >/dev/null || fail "$pkg-$arch.zip does not test as a valid zip"
	cmp -s <(unzip -p "$OUT/$pkg-$arch.zip" "$top/fork/install/install.sh") "$WORK/install.sh" || fail "the install.sh inside $pkg-$arch.zip is not the stamped one"
done

# 4. The scripts as assets, and their checksums. Their signed list
# (cosmos-cloud-<v>.scripts.sig) is written by the publish job, which holds
# the key.
cp "$WORK/install.sh" "$WORK/uninstall.sh" "$OUT/"
(cd "$OUT" && sha256sum install.sh uninstall.sh >SHA256SUMS)
for name in install.sh uninstall.sh SHA256SUMS; do
	case $name in *amd64* | *arm64* | *.md5) fail "the asset name $name would be taken by the updater for a zip or a checksum" ;; esac
done

# 5. The notes (the workflow only).
if [ "$DEPS_NOTES" = 1 ]; then
	{
		echo "Fork build $BUILD_NUMBER on top of upstream Cosmos $UPSTREAM (commit ${GITHUB_SHA:-unknown})."
		echo
		echo "Install on a $SYSTEMS_WORDS server (amd64 or arm64), as root:"
		echo
		echo '```'
		echo "curl -fsSLO https://github.com/$REPOSITORY/releases/download/v$VERSION/install.sh"
		echo "less install.sh            # it runs as root: read it first"
		echo "sudo bash install.sh       # add --dry-run to see what it would do"
		echo '```'
		echo
		echo "The script installs what is missing (Docker from Docker's repository), verifies this release's signature before unpacking it, and refuses to run over an existing Cosmos. Removing it: \`sudo bash /opt/cosmos/fork/install/uninstall.sh\`. An installed server updates itself from the panel; do not run install.sh again."
		echo "To check install.sh by hand: \`cosmos-cloud-$VERSION.scripts.sig\` lists its SHA-256, signed with the release key (see the README)."
		if [ -n "$PREVIOUS" ]; then
			echo
			echo "Changes since ${PREVIOUS#v}:"
			echo
			git -C "$HERE" log --no-merges --format='- %s' "$PREVIOUS..HEAD" | head -n 40
		fi
		cat "$OUT/dependencies.md"
	} >"$OUT/notes.md"
	rm "$OUT/dependencies.md"
fi
ls -l "$OUT"
