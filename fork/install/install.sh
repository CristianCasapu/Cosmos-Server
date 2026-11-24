#!/bin/bash
# Installs the Cosmos fork on a Debian or Ubuntu server, from nothing.
#
#     curl -fsSLO https://github.com/<owner>/<repo>/releases/latest/download/install.sh
#     less install.sh                # read it: it runs as root
#     sudo bash install.sh           # or: sudo sh install.sh
#
# What it does, in this order, and nothing else:
#   1. looks for a Cosmos that is already there (the fork, upstream's Cosmos,
#      a container, a unit, a folder, data) and stops if it finds one: this
#      script never runs over an installation (updates are made from the
#      panel; an upstream Cosmos is moved over with migrate.sh);
#   2. checks the system: root, Debian 12/13 or Ubuntu 24.04/26.04,
#      amd64 or arm64, systemd, free space, free ports;
#   3. works out which packages are missing and SIMULATES their installation
#      with apt (on a copy of the package lists), so that an unavailable
#      package or a dead network stops it before anything was changed;
#   4. installs the four tools it needs for the download (ca-certificates,
#      curl, unzip, openssl) when they are missing;
#   5. downloads the release from GitHub (or takes it from --from) and
#      VERIFIES it before a single byte is unpacked: the Ed25519 signature
#      file against the release keys written into this script when the
#      release was built, the zip's SHA-256 against the signed line, the MD5
#      as upstream publishes it, and every entry of the zip (no absolute
#      path, no "..", no link, nothing outside its top folder). A failure here
#      undoes step 4: nothing is left behind;
#   6. installs the other missing packages (unattended, apt waits for a
#      locked package database) and Docker from Docker's own apt repository
#      (Docker's signing key is carried in this script), unless a Docker is
#      already there: that one is used and left alone;
#   7. unpacks the release to /opt/cosmos, creates the data folder
#      /var/lib/cosmos (0700), writes the systemd unit CosmosCloud itself,
#      enables and starts it, and waits until the panel answers;
#   8. when protection was chosen, runs the release's protection pieces
#      (fork/<piece>/install.sh);
#   9. prints the panel's addresses.
# Everything it installs or writes is listed in /var/lib/cosmos-installer/record;
# uninstall.sh removes exactly that. It opens no port in any firewall.
#
# It can be run again after an interruption (a lost connection, a reboot,
# kill -9): it continues where it stopped, with the same options. Once an
# installation completed, every later run refuses: updates come from the
# panel (or by themselves at night), never from this script.
#
# migrate.sh is this same script: the release build writes it with the one
# line SCRIPT_ROLE changed. It moves upstream's bare-metal Cosmos (as
# upstream's get.sh installs it: /opt/cosmos, data /var/lib/cosmos, the unit
# CosmosCloud) to the fork, keeping start.sh, the unit and the data:
#   1. refuses, before anything else, the fork already there (10); then a
#      Cosmos container (17: not yet), other places than those, a Cosmos newer
#      than the fork's base, users in MongoDB, an unfinished first-run wizard,
#      an update of upstream waiting, a Cosmos that is not running (17), a
#      licence without --accept-community-edition (18), no Cosmos at all (19:
#      use install.sh);
#   2. checks the system, installs what is missing (as steps 2-6 above, jq
#      too) and downloads and verifies the release the same way;
#   3. notes how Cosmos answers (its settings, each of its addresses, the
#      containers), stops it, BACKS UP /opt/cosmos, /var/lib/cosmos and the
#      unit into a new folder (--backup-dir; proposed:
#      /var/backups/cosmos-before-fork-<time>; 0700) and checks the copy
#      against a list of every file with its SHA-256;
#   4. puts the fork's release in /opt/cosmos (upstream's folder is kept aside
#      until the end; start.sh, the unit and the data as they are), starts it,
#      and checks that it answers with the same settings, addresses and
#      containers and made no setup code (the server has its administrator);
#   5. on ANY failure from the stop on: puts everything back (the folder; the
#      data from the backup, checked against the list; the unit; enabled and
#      running as before), starts upstream's Cosmos again and says so (exit
#      20; 1 when even that failed, with what to do by hand);
#   6. writes GOING-BACK.txt in the backup: how to return to upstream by hand.
# Interrupted, it continues where it stopped when run again; --undo puts
# everything back instead. Once done, updates come from the panel; uninstall.sh
# does not remove a Cosmos migrate.sh moved (GOING-BACK.txt is the way back).
#
# Options (each answer also has a key of the answers file, in brackets; those
# that only make sense for one of the two scripts are refused by the other):
#   --yes                    accept every proposal, never ask
#   --answers <file>         answers as key=value lines (keys below)
#   --dry-run                show what would be done; change nothing
#   --version <x.y.z-fork.n> install that release instead of the newest
#   --from <zip or folder>   install a release already downloaded (the zip with
#                            its cosmos-cloud-<version>.sig next to it); no
#                            network is used for the release
#   --http-port <n>          [http-port]   default 80, else the first free of 8080, 2052
#   --https-port <n>         [https-port]  default 443, else 8443, 2053
#   --storage-tools yes|no   [storage-tools] the tools of the Storage pages
#                            (mergerfs, snapraid, samba, fuse3, mdadm, parted); default yes
#   --reachable internet|home [reachable]  default home: nothing is opened
#   --protection yes|no      [protection]  default: yes when reachable=internet
#   --unattended-upgrades yes|no [unattended-upgrades] default: as protection
#   --address fixed|changing|unknown [address]   (asked from a later version on)
#   --upnp yes|no|unknown    [upnp]        (asked from a later version on)
#   --router <text>          [router]      (asked from a later version on)
#   --no-outside             [outside-lookup=no] never ask an outside service
#                            for the public address (the router only)
#   --keep-ssh-open          [keep-ssh-open=yes] the guard keeps SSH open from outside
#   --placement home|exposed-host|cloud|unknown [placement] where the server stands:
#                            behind the router at home, the router's exposed host
#                            (DMZ), or a cloud server; for the guard (default unknown)
#   --ssh open|from:<addresses>|closed-i-have-a-console [ssh] how SSH answers from
#                            outside, for the guard of a server with no home network
#                            and no VPN (asked by a later version; given, it is passed on)
#   --wizard-on-lan          [wizard-on-lan=yes] the first-run wizard answers on the
#                            network, in clear HTTP, until it is done. Without it, the
#                            wizard of a server that is not at home (placement other
#                            than home, reachable from the internet, a public IPv4
#                            address on an interface, or a global IPv6 address, which
#                            the internet may reach) answers on 127.0.0.1 only, through
#                            an SSH tunnel (the line is printed); at home without
#                            either it answers on the home network and the tunnel is
#                            offered
#   --reuse-data             install over the data folder the fork left (upstream's
#                            data is migrate.sh's job)
#   --allow-older            install a release older than this script (refused
#                            otherwise: the installer goes forward only)
#   --proxy http://<host>:<port>  the proxy for the downloads from GitHub (the
#                            proxy variables of the shell are not read)
#   --print-setup-code       print the one-time setup code even with --yes or
#                            without a terminal (otherwise its file is named)
# migrate.sh only (it keeps the unit, its ports and the settings; of the options
# above it takes --yes, --answers, --dry-run, --version, --from, --allow-older,
# --proxy, --force and --storage-tools):
#   --backup-dir <dir>       [backup-dir] where the backup of before goes (a new
#                            folder: its parent must exist, not in memory)
#   --accept-community-edition  migrate a Cosmos that has a licence: the paid
#                            edition's features are not in the fork
#   --undo                   put back what an interrupted migration changed
#   --force                  go on on a system that is not supported (your risk)
#   --help
#
# Exit codes: 0 done (or dry run); 1 failed (what was changed is printed);
# 2 bad usage; 3 system not supported; 4 the release did not verify (nothing
# unpacked, nothing left); 5 not root; 6 a prerequisite cannot be installed
# (nothing changed); 7 another run is going; 10 the fork is already
# installed; 11 upstream's Cosmos is installed (use migrate.sh); 12 another
# Cosmos or a half-removed one is there; 13 data of an earlier Cosmos is
# there (--reuse-data); 14 an interrupted run used other options; 15 the
# release asked for is older than this script (--allow-older); 16 Cosmos
# is installed WITHOUT protection that was asked for (a piece skipped or
# failed: the last line names it). migrate.sh: 17 this Cosmos cannot be
# migrated (the reason is said; nothing changed); 18 it has a licence
# (--accept-community-edition); 19 there is no upstream Cosmos here (use
# install.sh); 20 the migration failed and everything was put back as before.
#
# The environment is not read, but for COSMOS_INSTALL_APT_TIMEOUT (seconds one
# apt or dpkg call may take, 60 to 86400) and COSMOS_INSTALL_APT_WAIT (seconds apt
# waits for another program that holds it, 10 to 3600, default 600). curl
# and openssl run with an empty environment (no proxy, no ~/.curlrc, no
# OPENSSL_CONF) and the PATH /usr/sbin:/usr/bin:/sbin:/bin; a proxy is given
# with --proxy.

# A script read from a pipe (curl ... | sudo sh) is refused: under a pipe $0
# is "sh" or "bash", and running "$0" again would run whatever file of that
# name the current folder holds, as root. Only a file that is this script
# (the marker line below) is run again with bash.
# COSMOS-FORK-INSTALL-SH
cosmos_self=""
case $0 in
*/*) cosmos_self=$0 ;;
*install*.sh | *migrate*.sh) cosmos_self=./$0 ;;
esac
if [ -z "$cosmos_self" ] || [ ! -f "$cosmos_self" ] || ! grep -q '^# COSMOS-FORK-INSTALL-SH$' "$cosmos_self" 2>/dev/null; then
	echo "install.sh and migrate.sh must be run from a file, not from a pipe. Download, then run:" >&2
	echo "    curl -fsSLO https://github.com/<owner>/<repo>/releases/latest/download/install.sh && sudo bash install.sh" >&2
	echo "    (or migrate.sh the same way, to move upstream's Cosmos to the fork)" >&2
	exit 2
fi
if [ -z "${BASH_VERSION:-}" ]; then
	# Started with sh (dash): bash is on every Debian and Ubuntu.
	exec bash "$cosmos_self" "$@"
fi

# Everything runs inside main, called on the last line: a file cut short by
# a broken download runs nothing.
main() {
set -euo pipefail
shopt -s nullglob
umask 077
export LC_ALL=C LANG=C
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# ---- Written by the release build (scripts/fork-release-package.sh) --------
# Unchanged here, the script runs only in the test mode described below.
RELEASE_KEYS='@FORK_RELEASE_KEYS@'
RELEASE_VERSION='@FORK_RELEASE_VERSION@'
RELEASE_REPOSITORY='@FORK_RELEASE_REPOSITORY@'
RELEASE_BASE='@FORK_RELEASE_BASE@'
# ---------------------------------------------------------------------------
# The role: install (install.sh) or migrate (migrate.sh). The release build
# writes migrate.sh as this same file with only this line changed.
SCRIPT_ROLE=install

# Test mode: only a copy that the release build did NOT stamp reads its keys,
# version and repository from the environment (the test bed's own key, never
# the real one). A stamped script ignores these variables: nothing a user
# passes makes it trust another key.
TEST_MODE=0 TEST_FAIL_AT=""
if [ "$RELEASE_KEYS" = "@FORK_""RELEASE_KEYS@" ]; then
	TEST_MODE=1
	RELEASE_KEYS=${COSMOS_INSTALL_TEST_KEYS:-}
	RELEASE_VERSION=${COSMOS_INSTALL_TEST_VERSION:-}
	RELEASE_REPOSITORY=${COSMOS_INSTALL_TEST_REPOSITORY:-}
	RELEASE_BASE=${COSMOS_INSTALL_TEST_BASE:-}
	if [ "${COSMOS_INSTALL_TEST_ROLE:-}" = migrate ]; then SCRIPT_ROLE=migrate; fi
	# a step of the migration made to fail, to prove the way back
	TEST_FAIL_AT=${COSMOS_INSTALL_TEST_FAIL_AT:-}
fi
case $SCRIPT_ROLE in
install) SELF=install.sh ;;
migrate) SELF=migrate.sh ;;
*) echo "ERROR: this script's role line is damaged" >&2; exit 2 ;;
esac

INSTALL_DIR=/opt/cosmos
DATA_DIR=/var/lib/cosmos
STATE_DIR=/var/lib/cosmos-installer
STATE=$STATE_DIR/state
RECORD=$STATE_DIR/record
UNIT=CosmosCloud
UNIT_FILE=/etc/systemd/system/$UNIT.service
LOCK=/run/cosmos-installer.lock
DOCKER_KEY=/etc/apt/keyrings/docker.asc
DOCKER_SOURCE=/etc/apt/sources.list.d/docker.sources
MIN_DOCKER=20.10
MAX_ZIP=$((1024 * 1024 * 1024))
MAX_UNPACKED=$((3 * 1024 * 1024 * 1024))

EXIT_FAILED=1 EXIT_USAGE=2 EXIT_SYSTEM=3 EXIT_UNVERIFIED=4 EXIT_ROOT=5 EXIT_PREREQ=6 EXIT_BUSY=7
EXIT_FORK=10 EXIT_UPSTREAM=11 EXIT_OTHER=12 EXIT_DATA=13 EXIT_RESUME=14 EXIT_OLDER=15 EXIT_UNPROTECTED=16
EXIT_CANNOT=17 EXIT_LICENCE=18 EXIT_NOTHING=19 EXIT_ROLLED_BACK=20

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { # code, message
	local code=$1
	shift
	printf 'ERROR: %s\n' "$*" >&2
	exit "$code"
}

usage() {
	sed -n '2,/^# with --proxy\./p' "$0" | sed 's/^# \{0,1\}//'
}

# ---- The answers: flags, an answers file, defaults -------------------------
# One variable per key. Flags win over the file; the file over the defaults.
declare -gA ANSWER=() ANSWER_FROM=()
ANSWER_KEYS="http-port https-port storage-tools reachable protection unattended-upgrades address upnp router outside-lookup keep-ssh-open placement ssh wizard-on-lan backup-dir"
# the keys only install.sh takes (migrate.sh keeps the unit and the settings)
INSTALL_ONLY_KEYS="http-port https-port reachable protection unattended-upgrades address upnp router outside-lookup keep-ssh-open placement ssh wizard-on-lan"

valid_answer() { # key, value
	case $1 in
	http-port | https-port) [[ $2 =~ ^[1-9][0-9]{0,4}$ ]] && [ "$2" -le 65535 ] ;;
	storage-tools | protection | unattended-upgrades | outside-lookup | keep-ssh-open | wizard-on-lan) [[ $2 =~ ^(yes|no)$ ]] ;;
	reachable) [[ $2 =~ ^(internet|home)$ ]] ;;
	address) [[ $2 =~ ^(fixed|changing|unknown)$ ]] ;;
	upnp) [[ $2 =~ ^(yes|no|unknown)$ ]] ;;
	router) [[ $2 =~ ^[A-Za-z0-9\ ._()/+-]{1,64}$ ]] ;;
	placement) [[ $2 =~ ^(home|exposed-host|cloud|unknown)$ ]] ;;
	# from:<addresses or networks, comma-separated, at most 20>
	ssh) [[ $2 =~ ^(open|closed-i-have-a-console|from:[0-9A-Fa-f.:/]{2,43}(,[0-9A-Fa-f.:/]{2,43}){0,19})$ ]] ;;
	backup-dir) [[ $2 =~ ^/[A-Za-z0-9._/-]{1,200}$ ]] && [[ $2 != *..* ]] ;;
	*) return 1 ;;
	esac
}

set_answer() { # key, value, where
	case " $ANSWER_KEYS " in *" $1 "*) ;; *) die $EXIT_USAGE "unknown answer '$1' ($3)" ;; esac
	valid_answer "$1" "$2" || die $EXIT_USAGE "'$2' is not a valid answer for $1 ($3)"
	ANSWER[$1]=$2
	ANSWER_FROM[$1]=$3
}

read_answers_file() { # file
	local file=$1 line key value n=0
	if [ ! -f "$file" ] || [ ! -r "$file" ]; then die $EXIT_USAGE "the answers file $file cannot be read"; fi
	[ "$(stat -c %s -- "$file")" -le 65536 ] || die $EXIT_USAGE "the answers file $file is larger than 64 KiB"
	while IFS= read -r line || [ -n "$line" ]; do
		n=$((n + 1))
		line=${line%$'\r'}
		[[ $line =~ ^[[:space:]]*(#.*)?$ ]] && continue
		[[ $line =~ ^[[:space:]]*([a-z-]+)[[:space:]]*=[[:space:]]*(.*[^[:space:]])[[:space:]]*$ ]] ||
			die $EXIT_USAGE "$file line $n is not key=value"
		key=${BASH_REMATCH[1]}
		value=${BASH_REMATCH[2]}
		# a flag given on the command line wins
		if [ "${ANSWER_FROM[$key]:-}" != "flag" ]; then set_answer "$key" "$value" "$file line $n"; else
			case " $ANSWER_KEYS " in *" $key "*) ;; *) die $EXIT_USAGE "unknown answer '$key' ($file line $n)" ;; esac
		fi
	done <"$file"
}

ALLOW_OLDER=0 PRINT_CODE=0 PROXY="" UNDO=0 ACCEPT_CE=0
YES=0 DRY_RUN=0 FORCE=0 REUSE_DATA=0 WANT_VERSION="" FROM="" ANSWERS_FILE=""
ORIGINAL_ARGS=("$@")
while [ $# -gt 0 ]; do
	case $1 in
	--yes | -y) YES=1 ;;
	--dry-run) DRY_RUN=1 ;;
	--force) FORCE=1 ;;
	--reuse-data) REUSE_DATA=1 ;;
	--allow-older) ALLOW_OLDER=1 ;;
	--print-setup-code) PRINT_CODE=1 ;;
	--no-outside) set_answer outside-lookup no flag ;;
	--keep-ssh-open) set_answer keep-ssh-open yes flag ;;
	--wizard-on-lan) set_answer wizard-on-lan yes flag ;;
	--undo) UNDO=1 ;;
	--accept-community-edition) ACCEPT_CE=1 ;;
	--version | --from | --answers | --proxy | --http-port | --https-port | --storage-tools | --reachable | --protection | \
		--unattended-upgrades | --address | --upnp | --router | --placement | --ssh | --backup-dir)
		[ $# -ge 2 ] || die $EXIT_USAGE "$1 needs a value"
		case $1 in
		--version) [[ $2 =~ ^v?(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})-fork\.[1-9][0-9]{0,8}$ ]] || die $EXIT_USAGE "--version takes a fork version such as 0.24.0-fork.35"
			WANT_VERSION=${2#v} ;;
		--from) FROM=$2 ;;
		--answers) ANSWERS_FILE=$2 ;;
		--proxy) [[ $2 =~ ^https?://[A-Za-z0-9]([A-Za-z0-9.-]{0,252}[A-Za-z0-9])?(:[1-9][0-9]{0,4})?/?$ ]] ||
			die $EXIT_USAGE "--proxy takes http://<host>:<port> or https://<host>:<port> (without a user or password)"
			PROXY=$2 ;;
		*) set_answer "${1#--}" "$2" flag ;;
		esac
		shift
		;;
	--help | -h) usage; exit 0 ;;
	*) die $EXIT_USAGE "unknown option '$1' (see --help)" ;;
	esac
	shift
done
if [ -n "$ANSWERS_FILE" ]; then read_answers_file "$ANSWERS_FILE"; fi
if [ -n "$FROM" ] && [ -n "$WANT_VERSION" ]; then die $EXIT_USAGE "--from and --version do not go together"; fi
# each script takes its own options only
if [ "$SCRIPT_ROLE" = migrate ]; then
	for k in $INSTALL_ONLY_KEYS; do
		[ -z "${ANSWER_FROM[$k]:-}" ] || die $EXIT_USAGE "'$k' (${ANSWER_FROM[$k]}) is an option of install.sh: migrate.sh keeps the unit, its ports and the settings as they are"
	done
	if [ "$REUSE_DATA" = 1 ] || [ "$PRINT_CODE" = 1 ]; then die $EXIT_USAGE "--reuse-data and --print-setup-code are options of install.sh"; fi
else
	if [ -n "${ANSWER_FROM[backup-dir]:-}" ] || [ "$UNDO" = 1 ] || [ "$ACCEPT_CE" = 1 ]; then
		die $EXIT_USAGE "--backup-dir, --undo and --accept-community-edition are options of migrate.sh"
	fi
fi

# curl and openssl run with a fixed, minimal environment: no ~/.curlrc
# (curl -q), no proxy or OPENSSL_CONF from the caller's shell (sudo -E, su).
# A server that needs a proxy for GitHub gives it with --proxy (or
# downloads the release by hand and uses --from).
SAFE_PATH=/usr/sbin:/usr/bin:/sbin:/bin
CURL=(env -i PATH="$SAFE_PATH" HOME=/nonexistent LC_ALL=C curl -q)
if [ -n "$PROXY" ]; then CURL+=(--proxy "$PROXY" --noproxy ''); fi
OSSL=(env -i PATH="$SAFE_PATH" HOME=/nonexistent LC_ALL=C openssl)

# Defaults that follow other answers (decisions D4 and Q6).
answer() { printf '%s' "${ANSWER[$1]:-}"; }
[ -n "$(answer reachable)" ] || ANSWER[reachable]=home
[ -n "$(answer storage-tools)" ] || ANSWER[storage-tools]=yes
[ -n "$(answer outside-lookup)" ] || ANSWER[outside-lookup]=yes
[ -n "$(answer keep-ssh-open)" ] || ANSWER[keep-ssh-open]=no
[ -n "$(answer wizard-on-lan)" ] || ANSWER[wizard-on-lan]=no
[ -n "$(answer placement)" ] || ANSWER[placement]=unknown
if [ -z "$(answer protection)" ]; then
	if [ "$(answer reachable)" = internet ]; then ANSWER[protection]=yes; else ANSWER[protection]=no; fi
fi
[ -n "$(answer unattended-upgrades)" ] || ANSWER[unattended-upgrades]=$(answer protection)
# address, upnp and router are kept for the questions of a later version.
# Questions will only ever be asked on a terminal and without --yes; today
# none is asked: every run takes the answers given and the proposals, so a
# run without a terminal never waits.
ASK=0
if [ "$YES" = 0 ] && [ -t 0 ] && [ -t 1 ]; then ASK=1; fi

# ---- Root, one run at a time ------------------------------------------------
[ "$(id -u)" = 0 ] || die $EXIT_ROOT "run this as root: sudo bash $0"
exec 9>"$LOCK"
flock -n 9 || die $EXIT_BUSY "another run of install.sh, migrate.sh or uninstall.sh is going on"

WORK=""
STAGING=""
HOP_DIR=""
case $0 in /var/tmp/cosmos-install-next.*/install.sh | /var/tmp/cosmos-install-next.*/migrate.sh) HOP_DIR=$(dirname -- "$0") ;; esac
cleanup() {
	if [ -n "$STAGING" ] && [ -d "$STAGING" ]; then rm -rf -- "$STAGING"; fi
	if [ -n "$WORK" ] && [ -d "$WORK" ]; then rm -rf -- "$WORK"; fi
	if [ -n "$HOP_DIR" ] && [ -d "$HOP_DIR" ]; then rm -rf -- "$HOP_DIR"; fi
	if [ -n "${SIM:-}" ] && [ -d "$SIM" ]; then rm -rf -- "$SIM"; fi
	# the lock file stays (in /run): removing it while held would let a second
	# run lock a new file
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

if [ "$TEST_MODE" = 1 ]; then
	warn "TEST MODE: this copy of $SELF was not stamped by a release build."
	if [ -z "$RELEASE_KEYS" ] || [ -z "$RELEASE_REPOSITORY" ] || [ -z "$RELEASE_VERSION" ] || [ -z "$RELEASE_BASE" ]; then
		die $EXIT_USAGE "this copy of $SELF is not the one of a release: download $SELF from the fork's releases"
	fi
fi
for key in $RELEASE_KEYS; do
	[[ $key =~ ^[A-Za-z0-9+/]{43}=$ ]] || die $EXIT_USAGE "a release key in this script is damaged"
done
[[ $RELEASE_VERSION =~ ^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}-fork\.[1-9][0-9]{0,8}$ ]] || die $EXIT_USAGE "the release version in this script is damaged"
[[ $RELEASE_REPOSITORY =~ ^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$ ]] || die $EXIT_USAGE "the repository in this script is damaged"
[[ $RELEASE_BASE =~ ^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$ ]] || die $EXIT_USAGE "the upstream version this script's release is built on is damaged"

# BEGIN shared detection (migrate.sh is this same script with its role: it runs this too)
# detect_cosmos: is a Cosmos of any kind on this machine? Changes nothing,
# installs nothing, needs only coreutils, grep and systemd (and docker when
# it is there). Sets:
#   FOUND       none | fork | upstream | unit-only | folder-unknown | container | data-only
#   FOUND_WHAT  a sentence saying what was found and where
#   FOUND_VERSION  the version from meta.json, when there is one
#   FOUND_DIR   the installation folder looked at
#   FOUND_UNIT  the unit file, when there is one
# What tells the fork from upstream (R1): meta.json's version carries
# "-fork."; the fork alone ships FORK-LICENCE.md, keeps cosmos-update.previous/,
# .proven or .trial, and its binaries carry the signature header
# "cosmos-fork-release-v1". Any one of these is the fork.
FOUND=none FOUND_WHAT="" FOUND_VERSION="" FOUND_DIR="" FOUND_UNIT=""
meta_version() { # folder -> the version its meta.json names, or nothing
	if [ -f "$1/meta.json" ]; then
		sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]\{1,64\}\)".*/\1/p' "$1/meta.json" | head -n 1 | tr -cd 'A-Za-z0-9.+-'
	fi
}
cosmos_folder_kind() { # folder -> prints fork|upstream|none|unknown (it runs in $(...): it sets nothing)
	local dir=$1 version
	if [ ! -e "$dir" ]; then echo none; return; fi
	version=$(meta_version "$dir")
	if [[ $version == *-fork.* ]] || [ -e "$dir/FORK-LICENCE.md" ] || [ -e "$dir/cosmos-update.previous" ] ||
		[ -e "$dir/cosmos-update.proven" ] || [ -e "$dir/cosmos-update.trial" ]; then
		echo fork; return
	fi
	local bin
	for bin in "$dir/cosmos" "$dir/cosmos-launcher"; do
		if [ -f "$bin" ] && grep -aqF 'cosmos-fork-release-v1' "$bin"; then echo fork; return; fi
	done
	if [ -n "$version" ] || [ -f "$dir/cosmos" ]; then echo upstream; return; fi
	echo unknown
}
detect_cosmos() {
	local kind dir unit="" files f
	FOUND=none FOUND_WHAT="" FOUND_VERSION="" FOUND_DIR="" FOUND_UNIT=""
	# the unit, enabled or not, running or not
	for f in /etc/systemd/system/CosmosCloud.service /run/systemd/system/CosmosCloud.service \
		/usr/local/lib/systemd/system/CosmosCloud.service /lib/systemd/system/CosmosCloud.service \
		/usr/lib/systemd/system/CosmosCloud.service; do
		if [ -e "$f" ] || [ -L "$f" ]; then unit=$f; break; fi
	done
	if [ -z "$unit" ] && command -v systemctl >/dev/null 2>&1; then
		f=$(timeout 10 systemctl show -p FragmentPath --value CosmosCloud.service 2>/dev/null || true)
		if [ -n "$f" ] && [ -e "$f" ]; then unit=$f; fi
	fi
	FOUND_UNIT=$unit
	dir=/opt/cosmos
	if [ -n "$unit" ]; then
		f=$(sed -n 's/^WorkingDirectory=//p' "$unit" 2>/dev/null | head -n 1)
		if [[ $f =~ ^/[A-Za-z0-9._/-]{1,200}$ ]]; then dir=$f; fi
	fi
	FOUND_DIR=$dir
	kind=$(cosmos_folder_kind "$dir")
	if [ "$kind" = none ] && [ "$dir" != /opt/cosmos ]; then
		kind=$(cosmos_folder_kind /opt/cosmos)
		[ "$kind" = none ] || FOUND_DIR=/opt/cosmos
	fi
	FOUND_VERSION=$(meta_version "$FOUND_DIR")
	case $kind in
	fork) FOUND=fork; FOUND_WHAT="the Cosmos fork${FOUND_VERSION:+ $FOUND_VERSION} is installed in $FOUND_DIR${unit:+ (unit $unit)}"; return ;;
	upstream) FOUND=upstream; FOUND_WHAT="upstream's Cosmos${FOUND_VERSION:+ $FOUND_VERSION} is installed in $FOUND_DIR${unit:+ (unit $unit)}"; return ;;
	unknown) FOUND=folder-unknown; FOUND_WHAT="the folder $FOUND_DIR exists but holds no Cosmos this script recognises"; return ;;
	esac
	if [ -n "$unit" ]; then
		FOUND='unit-only'; FOUND_WHAT="the unit $unit exists but its folder $dir does not"; return
	fi
	# a Cosmos container (upstream's image), running or stopped
	if command -v docker >/dev/null 2>&1; then
		files=$(timeout 20 docker ps -a --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE '(^cosmos-server )|azukaar/cosmos-server' | head -n 3 | tr -cd 'A-Za-z0-9 ._:/@\n-' || true)
		if [ -n "$files" ]; then
			FOUND=container; FOUND_WHAT="a Cosmos container exists: $(printf '%s' "$files" | head -n 1)"; return
		fi
	fi
	if [ -e /var/lib/cosmos/cosmos.config.json ] || [ -e /var/lib/cosmos/database.db ] || [ -e /var/lib/cosmos/auth.db ]; then
		FOUND=data-only; FOUND_WHAT="the data of an earlier Cosmos is in /var/lib/cosmos (no installation)"; return
	fi
}
# cosmos_data_kind <data folder>: whose data is it? Sets DATA_KIND (fork,
# upstream or none) and DATA_PROBLEM ("", mongodb: users still in MongoDB,
# from before 0.23; licence: a licence of the paid edition is set). The fork
# alone writes cosmos.security.log, revoked-sessions.json and the like, and
# its own settings (Telegram, Fail2ban, UPnP...) into the config, which
# upstream drops at its next save.
DATA_KIND=none DATA_PROBLEM=""
cosmos_data_kind() {
	local dir=$1 config=$1/cosmos.config.json f
	DATA_KIND=none DATA_PROBLEM=""
	[ -e "$config" ] || [ -e "$dir/database.db" ] || [ -e "$dir/auth.db" ] || return 0
	DATA_KIND=upstream
	for f in cosmos.security.log revoked-sessions.json reach-watch.json telegram.state.json fork-deps-told.json; do
		if [ -e "$dir/$f" ]; then DATA_KIND=fork; fi
	done
	if [ -f "$config" ] && grep -qE '"(Telegram|Fail2ban|UPnP|Snapshots|Turnstile|AppSignIn)"[[:space:]]*:' "$config"; then DATA_KIND=fork; fi
	if [ -f "$config" ] && grep -qE '"MongoDB"[[:space:]]*:[[:space:]]*"[^"]' "$config"; then DATA_PROBLEM=mongodb; return 0; fi
	if [ -f "$config" ] && grep -qE '"Licence"[[:space:]]*:[[:space:]]*"[^"]' "$config"; then DATA_PROBLEM=licence; fi
	return 0
}
# END shared detection

# ---- 1. An existing Cosmos? (before anything else) -------------------------
state_get() { # key -> value from the state file
	[ -f "$STATE" ] || return 0
	sed -n "s/^$1=//p" "$STATE" | tail -n 1
}
state_set() { # key, value
	[ "$DRY_RUN" = 1 ] && return 0
	local tmp
	tmp=$(mktemp "$STATE_DIR/.state.XXXXXX")
	{ if [ -f "$STATE" ]; then grep -v "^$1=" "$STATE" || true; fi; printf '%s=%s\n' "$1" "$2"; } >"$tmp"
	chmod 600 "$tmp"
	mv -f -- "$tmp" "$STATE"
	sync -f "$STATE" 2>/dev/null || true
}
record() { # one record line, written once
	[ "$DRY_RUN" = 1 ] && return 0
	touch "$RECORD"
	chmod 600 "$RECORD"
	grep -qxF -- "$*" "$RECORD" || printf '%s\n' "$*" >>"$RECORD"
	sync -f "$RECORD" 2>/dev/null || true
}
recorded() { [ -f "$RECORD" ] && grep -qxF -- "$*" "$RECORD"; }

# ---- The migration (migrate.sh's role) ---------------------------------------
# What migrate.sh keeps between its steps, besides the state file: what Cosmos
# looked like before (settings, URLs, containers), in $MIG_DIR. Upstream's
# folder waits in $MIG_OLD (a rename: the same disk) while the fork is tried.
MIG_DIR=$STATE_DIR/migration
MIG_OLD=/opt/.cosmos-upstream-before-fork
MIG_MARK=.made-by-migrate.sh
MIG_RESUME="" MIG_BACKUP="" MIG_FROM="" MIG_UNSTABLE=""
# test_fail_at <step>: the test mode (an unstamped copy) can make a step fail
# on purpose, to prove the way back; a release's script never does.
test_fail_at() {
	if [ "$TEST_MODE" = 1 ] && [ "$TEST_FAIL_AT" = "$1" ]; then
		die $EXIT_FAILED "TEST MODE: the step '$1' fails on purpose"
	fi
}
unit_enabled() { systemctl is-enabled "$UNIT.service" 2>/dev/null || true; }
unit_active() { systemctl is-active "$UNIT.service" 2>/dev/null || true; }
# manifest_of <folder>: every entry (type, mode, owner, path, link target)
# and every file's SHA-256, sorted. Equal lists: equal folders, byte for byte
# (times are not compared; cp -a keeps them anyway).
manifest_of() {
	(
		cd -- "$1" || exit 1
		find . -xdev -printf '%y %m %U:%G %p -> %l\n' | LC_ALL=C sort
		find . -xdev -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum
	)
}
# same_as_manifest <folder> <manifest>
same_as_manifest() {
	[ -d "$1" ] && [ -f "$2" ] || return 1
	local now
	now=$(mktemp "$STATE_DIR/.manifest.XXXXXX")
	if ! manifest_of "$1" >"$now"; then rm -f -- "$now"; return 1; fi
	if cmp -s -- "$now" "$2"; then rm -f -- "$now"; return 0; fi
	diff -- "$2" "$now" | grep '^[<>]' | head -n 5 >&2 || true
	rm -f -- "$now"
	return 1
}
# wait_answer <port> <seconds>: the panel answers (any answer of the server)
wait_answer() {
	local code i
	for ((i = 0; i < $2; i++)); do
		code=$("${CURL[@]}" -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$1/cosmos-ui/" || true)
		case $code in 200 | 30[1278] | 400 | 401 | 403 | 404) return 0 ;; esac
		sleep 1
	done
	return 1
}
# mig_observe <name>: how Cosmos answers now, into $MIG_DIR/<name>.*:
#   .settings    the settings a migration must keep (jq, sorted)
#   .urls        each enabled URL of the settings and the code it answers on
#                127.0.0.1 with its own Host (at most 50)
#   .containers  every container: ID, name, state
JQ_SETTINGS='{hostname: .HTTPConfig.Hostname, http: .HTTPConfig.HTTPPort, https: .HTTPConfig.HTTPSPort,
	tls: .HTTPConfig.HTTPSCertificateMode, newInstall: .NewInstall,
	routes: [(.HTTPConfig.ProxyConfig.Routes // [])[] | {Name, Host, UsePathPrefix, PathPrefix, Target, Mode, Disabled}]}'
JQ_URLS='[(.HTTPConfig.Hostname // "") + " /cosmos-ui/"] +
	[(.HTTPConfig.ProxyConfig.Routes // [])[] | select(.Disabled != true) | (.Host // "") + " " + (if .UsePathPrefix then (.PathPrefix // "/") else "/" end)] | .[]'
mig_observe() {
	local name=$1 config=$DATA_DIR/cosmos.config.json host path n=0 code
	jq -S "$JQ_SETTINGS" "$config" >"$MIG_DIR/$name.settings" || die $EXIT_FAILED "jq cannot read $config"
	: >"$MIG_DIR/$name.urls"
	while read -r host path && [ "$n" -lt 50 ]; do
		# only plain names and paths (they come from the settings file)
		[[ $host =~ ^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:[0-9]{1,5})?$ ]] || continue
		[[ $path =~ ^/[A-Za-z0-9._~%/-]{0,200}$ ]] || continue
		code=$("${CURL[@]}" -s -o /dev/null -m 10 -w '%{http_code}' -H "Host: $host" -- "http://127.0.0.1:$HTTP_PORT$path" || true)
		printf '%s %s %s\n' "$host" "$path" "$code" >>"$MIG_DIR/$name.urls"
		n=$((n + 1))
	done < <(jq -r "$JQ_URLS" "$config" 2>/dev/null || true)
	if command -v docker >/dev/null 2>&1; then
		timeout 30 docker ps -a --no-trunc --format '{{.ID}} {{.Names}} {{.State}}' 2>/dev/null | LC_ALL=C sort >"$MIG_DIR/$name.containers" || true
	else
		: >"$MIG_DIR/$name.containers"
	fi
}

# going_back_text <backup> <upstream version>: GOING-BACK.txt
going_back_text() {
	cat <<EOF
Going back from the Cosmos fork to upstream's Cosmos $2
(written by migrate.sh on $(date -u +%FT%TZ))

This folder holds what was there before the migration, copied while Cosmos was
stopped, and checked file by file:
  opt-cosmos/           /opt/cosmos: upstream's Cosmos $2
  var-lib-cosmos/       /var/lib/cosmos: its data (settings, users, certificates,
                        the server's keys: keep this folder private)
  $UNIT.service   the unit (migrate.sh did not change it)
  MANIFEST.*            every file with its SHA-256

1. Back to exactly what was there before (what changed since is lost):
     sudo systemctl stop $UNIT
     sudo mv /opt/cosmos /opt/cosmos.fork
     sudo cp -a $1/opt-cosmos /opt/cosmos
     sudo mv /var/lib/cosmos /var/lib/cosmos.fork
     sudo cp -a $1/var-lib-cosmos /var/lib/cosmos
     sudo systemctl start $UNIT
   then, once upstream's Cosmos works: sudo rm -rf /opt/cosmos.fork /var/lib/cosmos.fork

2. Back keeping the data as the fork left it: only to upstream 0.24.0 or newer
   (0.24 changed how the HTTPS settings are kept; an older one cannot read them).
   Upstream ignores the fork's own settings (Telegram, snapshots, ...) and drops
   them at its next save.
     sudo systemctl stop $UNIT
     unzip upstream's cosmos-cloud-<0.24.0 or newer>-<amd64 or arm64>.zip
     copy its folder's files over /opt/cosmos, keeping /opt/cosmos/start.sh
     sudo rm -rf /opt/cosmos/FORK-LICENCE.md /opt/cosmos/fork /opt/cosmos/cosmos-update.*
     sudo systemctl start $UNIT
   Upstream's own updater then follows upstream's releases again.

Either way the fork's own files in /var/lib/cosmos stay (upstream ignores them).

uninstall.sh does not remove a Cosmos that migrate.sh moved: going back is
done as above. Once you are sure you keep the fork, delete this folder: it
holds your server's keys and its users' password hashes.
EOF
}

# migrate_rollback: puts upstream's Cosmos back as it was, from any step of an
# unfinished migration: its folder, its data (from the backup, checked against
# the manifest), the unit, enabled and running as before. Each part is done
# only when needed and checked, so it can run again after an interruption.
# Prints what it did; returns 1 when something could not be put back.
migrate_rollback() {
	set +e
	# not cut short by Ctrl-C or a lost session: what it puts back is the
	# owner's installation (a run interrupted all the same is resumed or
	# undone by the next one)
	trap '' INT TERM HUP
	local ok=1 backup mstep aside
	backup=$(state_get backup)
	mstep=$(state_get mstep)
	step "Putting upstream's Cosmos back as it was"
	# the fork is (or was) in place: the unit is stopped whatever its state
	# (active, still starting, restarting after a crash); upstream's Cosmos
	# not yet stopped by the migration is left running
	if [ -d "$MIG_OLD" ] || [ "$(cosmos_folder_kind "$INSTALL_DIR")" != upstream ]; then
		systemctl stop "$UNIT.service" || ok=0
	fi
	# the installation folder: upstream's was only renamed
	if [ -d "$MIG_OLD" ]; then
		if [ -e "$INSTALL_DIR" ]; then
			aside=$(mktemp -u /opt/.cosmos-fork-tried.XXXXXXXX)
			mv -- "$INSTALL_DIR" "$aside" || ok=0
		fi
		if [ "$ok" = 1 ] && mv -- "$MIG_OLD" "$INSTALL_DIR"; then
			[ -z "${aside:-}" ] || rm -rf -- "$aside"
			say "$INSTALL_DIR: upstream's folder is back."
		else
			ok=0
		fi
	fi
	if [ -n "$backup" ] && [ -f "$backup/MANIFEST.opt-cosmos" ] && ! same_as_manifest "$INSTALL_DIR" "$backup/MANIFEST.opt-cosmos"; then
		say "$INSTALL_DIR differs from the backup: copying it back."
		restore_from "$backup/opt-cosmos" "$INSTALL_DIR" "$backup/MANIFEST.opt-cosmos" || ok=0
	fi
	# the data: the fork may have run on it
	if [ -n "$backup" ] && [ -f "$backup/MANIFEST.var-lib-cosmos" ]; then
		if same_as_manifest "$DATA_DIR" "$backup/MANIFEST.var-lib-cosmos"; then
			say "$DATA_DIR is as it was (checked against the backup's list)."
		else
			say "$DATA_DIR was changed: copying it back from the backup."
			restore_from "$backup/var-lib-cosmos" "$DATA_DIR" "$backup/MANIFEST.var-lib-cosmos" || ok=0
		fi
	else
		say "$DATA_DIR was not touched (the migration stopped before its backup was complete)."
	fi
	# the unit (migrate.sh does not change it; put back if anything did)
	if [ -n "$backup" ] && [ -f "$backup/$UNIT.service" ] && ! cmp -s -- "$backup/$UNIT.service" "$UNIT_FILE"; then
		cp -a -- "$backup/$UNIT.service" "$UNIT_FILE" || ok=0
		systemctl daemon-reload
	fi
	if [ "$ok" = 1 ]; then
		[ "$(state_get was-enabled)" != enabled ] || systemctl enable "$UNIT.service" >/dev/null 2>&1 || ok=0
		if [ "$(state_get was-active)" = active ] && [ "$(unit_active)" != active ]; then
			# the fork's starts may have used up the unit's start limit (5 in 10 s)
			systemctl reset-failed "$UNIT.service" 2>/dev/null || true
			systemctl start "$UNIT.service" || ok=0
		fi
		if [ "$(state_get was-active)" = active ] && ! wait_answer "$HTTP_PORT" 180; then
			warn "upstream's Cosmos is back in place but its panel does not answer yet: journalctl -u $UNIT"
			ok=0
		fi
	fi
	if [ "$ok" = 1 ]; then
		# nothing of the migration is left: its backup, its notes, its state
		# the backup folder this run made (the state names it; marked, or
		# killed before its mark was written and so still empty of it)
		if [ -n "$backup" ] && [ -d "$backup" ] && { [ -f "$backup/$MIG_MARK" ] || [ -z "$(ls -A -- "$backup")" ]; }; then rm -rf -- "$backup"; fi
		rm -rf -- "$MIG_DIR"
		rm -f -- "$STATE"
		[ -s "$RECORD" ] || rm -rf -- "$STATE_DIR"
		say "Upstream's Cosmos is back as it was (folder and data checked against the backup, which is now removed)."
		[ ! -s "$RECORD" ] || say "Packages this script installed stay (listed in $RECORD)."
		trap 'exit 130' INT TERM HUP
		set -e
		return 0
	fi
	warn "upstream's Cosmos could NOT be put back entirely (step reached: ${mstep:-none})."
	if [ -n "$backup" ]; then
		warn "The backup of before is complete in $backup: follow $backup/GOING-BACK.txt (part 1), or run $SELF --undo again."
	fi
	trap 'exit 130' INT TERM HUP
	set -e
	return 1
}
# restore_from <copy> <folder> <manifest>: the folder becomes the copy,
# checked; what was in its place is removed once the copy checked
restore_from() {
	local aside=""
	if [ -e "$2" ]; then
		aside=$(mktemp -u "$(dirname -- "$2")/.cosmos-migrate-replaced.XXXXXXXX")
		mv -- "$2" "$aside" || return 1
	fi
	cp -a -- "$1" "$2" || return 1
	if ! same_as_manifest "$2" "$3"; then
		warn "the copy of $1 in $2 does not match the backup's list"
		[ -z "$aside" ] || warn "what was in $2 before is in $aside"
		return 1
	fi
	[ -z "$aside" ] || rm -rf -- "$aside"
	return 0
}

# migrate_backup_place: the backup's folder (new, private, on a disk with
# room, that survives a reboot); refused before any change
migrate_backup_place() {
	local b parent need have fstype
	b=${ANSWER[backup-dir]:-/var/backups/cosmos-before-fork-$(date -u +%Y%m%dT%H%M%SZ)}
	[[ $b == /?* ]] || die $EXIT_USAGE "--backup-dir: '$b' is not an absolute path"
	[ "$(realpath -m -- "$b")" = "$b" ] || die $EXIT_USAGE "--backup-dir: '$b' is not its own real path (no link, no .., no / at the end)"
	case $b/ in
	/opt/cosmos/* | "$DATA_DIR"/* | /opt/.cosmos* | "$STATE_DIR"/*) die $EXIT_USAGE "--backup-dir: '$b' is inside what is backed up or changed" ;;
	esac
	if [ -e "$b" ] || [ -L "$b" ]; then die $EXIT_USAGE "--backup-dir: '$b' exists already: give a new folder (the script makes it)"; fi
	parent=$(dirname -- "$b")
	[ -d "$parent" ] || die $EXIT_PREREQ "the folder $parent, where the backup is to go, does not exist. Nothing was changed."
	need=$(( ($(du -skx -- "$INSTALL_DIR" | cut -f1) + $(du -skx -- "$DATA_DIR" | cut -f1)) * 11 / 10 + 102400 ))
	have=$(free_kib "$parent")
	if [ -z "$have" ] || [ "$have" -lt "$need" ]; then
		die $EXIT_PREREQ "not enough free space in $parent for the backup: $(( ${have:-0} / 1024 )) MiB free, $((need / 1024)) MiB needed (give another place with --backup-dir). Nothing was changed."
	fi
	fstype=$(stat -f -c %T -- "$parent" 2>/dev/null || echo unknown)
	case $fstype in tmpfs | ramfs) die $EXIT_PREREQ "$parent is in memory ($fstype): the backup would be lost at the next reboot (give another place with --backup-dir). Nothing was changed." ;; esac
	[ -w "$parent" ] || die $EXIT_PREREQ "$parent cannot be written (a read-only disk?): give another place with --backup-dir. Nothing was changed."
	MIG_BACKUP=$b
}

# migrate_checks: what must hold before a migration starts (only reads)
migrate_checks() {
	local unit_text config=$DATA_DIR/cosmos.config.json
	if [ "$FOUND_DIR" != "$INSTALL_DIR" ] || [ "$FOUND_UNIT" != "$UNIT_FILE" ]; then
		die $EXIT_CANNOT "$FOUND_WHAT, not in upstream's default places ($INSTALL_DIR, the unit $UNIT_FILE): migrate.sh moves only those. Nothing was changed."
	fi
	unit_text=$(cat -- "$UNIT_FILE")
	if [ -f "/etc/sysconfig/$UNIT" ]; then unit_text="$unit_text
$(cat -- "/etc/sysconfig/$UNIT")"; fi
	if has_line '(COSMOS_CONFIG_FOLDER|CONFIG_FILE)=' "$unit_text"; then
		die $EXIT_CANNOT "the unit sets COSMOS_CONFIG_FOLDER or CONFIG_FILE: this Cosmos keeps its data outside $DATA_DIR, which migrate.sh does not move. Nothing was changed."
	fi
	if has_line '^WorkingDirectory=' "$unit_text" && ! has_line "^WorkingDirectory=${INSTALL_DIR//./\\.}/?\$" "$unit_text"; then
		die $EXIT_CANNOT "the unit runs Cosmos from another folder than $INSTALL_DIR. Nothing was changed."
	fi
	[ -f "$config" ] || die $EXIT_CANNOT "$config is missing: there are no settings to keep. Nothing was changed."
	if [ -n "$(findmnt -rn -o TARGET 2>/dev/null | grep -E "^(${INSTALL_DIR//./\\.}|${DATA_DIR//./\\.})/" || true)" ]; then
		die $EXIT_CANNOT "a disk is mounted inside $INSTALL_DIR or $DATA_DIR: its backup would not be what it seems. Nothing was changed."
	fi
	cosmos_data_kind "$DATA_DIR"
	if [ "$DATA_PROBLEM" = mongodb ]; then
		die $EXIT_CANNOT "this Cosmos keeps its users in MongoDB (a Cosmos from before 0.23): no Cosmos of today reads them, and upstream did not import them either. Nothing was changed."
	fi
	if grep -qE '"NewInstall"[[:space:]]*:[[:space:]]*true' -- "$config"; then
		die $EXIT_CANNOT "this Cosmos's first-run wizard is not finished: there is no administrator to keep. Remove it, then use install.sh. Nothing was changed."
	fi
	if [ -e "$INSTALL_DIR/cosmos-update.zip" ]; then
		die $EXIT_CANNOT "an update of upstream's Cosmos waits in $INSTALL_DIR (cosmos-update.zip): let it finish first (sudo systemctl restart $UNIT). Nothing was changed."
	fi
	[[ $FOUND_VERSION =~ ^((0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5}))(-unstable[0-9]{1,6})?$ ]] ||
		die $EXIT_CANNOT "the version of this Cosmos ('$FOUND_VERSION' in $INSTALL_DIR/meta.json) is not one migrate.sh knows. Nothing was changed."
	MIG_FROM=${BASH_REMATCH[1]}
	MIG_UNSTABLE=${BASH_REMATCH[5]}
	base_allows "$MIG_FROM" "$MIG_UNSTABLE" "$RELEASE_BASE"
	[ "$(unit_active)" = active ] ||
		die $EXIT_CANNOT "upstream's Cosmos is not running: start it first (sudo systemctl start $UNIT). The migration compares how it answers before and after. Nothing was changed."
	if [ "$DATA_PROBLEM" = licence ] && [ "$ACCEPT_CE" = 0 ]; then
		die $EXIT_LICENCE "this Cosmos has a licence in its settings. The fork is upstream's community edition: the paid edition's features (and its updates) are not in it; their settings stay in the file, unused, and the licence's user limit stays as on the community edition. To migrate all the same, run again with --accept-community-edition. Nothing was changed."
	fi
	[ "$DATA_PROBLEM" != licence ] || warn "this Cosmos has a licence: migrating to the community edition as asked (--accept-community-edition); the paid edition's features are not in the fork."
}
# base_allows <x.y.z> <-unstableN or ""> <base x.y.z>: refuses an upstream newer than the fork's base
base_allows() {
	local have base_v
	IFS=. read -r -a have <<<"$1"
	IFS=. read -r -a base_v <<<"$3"
	if [ "${have[0]}" -gt "${base_v[0]}" ] || { [ "${have[0]}" = "${base_v[0]}" ] && { [ "${have[1]}" -gt "${base_v[1]}" ] || { [ "${have[1]}" = "${base_v[1]}" ] && [ "${have[2]}" -gt "${base_v[2]}" ]; }; }; }; then
		die $EXIT_CANNOT "this Cosmos is $1$2, newer than $3, which the fork is built on: moving to the fork would go back in upstream's code. Wait for a fork release on $1 or newer. Nothing was changed."
	fi
	if [ "$1" = "$3" ] && [ -n "$2" ]; then warn "this Cosmos is a test build ($1$2) of the version the fork is built on: migrating to the fork's $3."; fi
	if [ "$1" != "$3" ]; then say "This Cosmos is $1, older than $3: the fork runs the same forward migrations at its first start that upstream's own update to $3 would."; fi
}
# migrate_ports: the ports the unit gives (else the settings, else 80/443)
migrate_ports() {
	local text p
	text=$(cat -- "$UNIT_FILE" "/etc/sysconfig/$UNIT" 2>/dev/null || true)
	p=$(sed -n 's/^\(Environment=\)\{0,1\}"\{0,1\}COSMOS_HTTP_PORT=\([0-9]\{1,5\}\)"\{0,1\}$/\2/p' <<<"$text" | tail -n 1)
	[ -n "$p" ] || p=$(grep -oE '"HTTPPort"[[:space:]]*:[[:space:]]*"[0-9]{1,5}"' -- "$DATA_DIR/cosmos.config.json" | head -n 1 | grep -oE '[0-9]{1,5}"$' | tr -d '"' || true)
	HTTP_PORT=${p:-80}
	p=$(sed -n 's/^\(Environment=\)\{0,1\}"\{0,1\}COSMOS_HTTPS_PORT=\([0-9]\{1,5\}\)"\{0,1\}$/\2/p' <<<"$text" | tail -n 1)
	HTTPS_PORT=${p:-443}
}

# migrate_run: after the release is verified and the packages are in: the
# observation, the stop, the backup, the swap, the start and the checks
migrate_run() {
	local b=$MIG_BACKUP
	[ -d "$MIG_DIR" ] || mkdir -m 700 -- "$MIG_DIR"
	step "How Cosmos answers now (compared after the migration)"
	mig_observe before
	say "Settings noted; $(wc -l <"$MIG_DIR/before.urls") addresses answer ($(cut -d' ' -f3 "$MIG_DIR/before.urls" | sort | uniq -c | awk '{printf "%s%s x%s", sep, $2, $1; sep=", "}')); $(wc -l <"$MIG_DIR/before.containers") containers."
	state_set was-enabled "$(unit_enabled)"
	state_set was-active "$(unit_active)"
	state_set backup "$b"
	state_set migrated-from "$FOUND_VERSION"
	PHASE=migrating
	step "Stopping upstream's Cosmos (its containers keep running)"
	state_set mstep stopping
	systemctl stop "$UNIT.service"
	state_set mstep stopped
	test_fail_at stopped

	step "Backing up $INSTALL_DIR and $DATA_DIR to $b"
	mkdir -m 700 -- "$b"
	printf 'made by migrate.sh\n' >"$b/$MIG_MARK"
	cp -a -- "$INSTALL_DIR" "$b/opt-cosmos"
	cp -a -- "$DATA_DIR" "$b/var-lib-cosmos"
	cp -a -- "$UNIT_FILE" "$b/$UNIT.service"
	if [ -f "/etc/sysconfig/$UNIT" ]; then cp -a -- "/etc/sysconfig/$UNIT" "$b/sysconfig-$UNIT"; fi
	manifest_of "$INSTALL_DIR" >"$b/MANIFEST.opt-cosmos"
	manifest_of "$DATA_DIR" >"$b/MANIFEST.var-lib-cosmos"
	same_as_manifest "$b/opt-cosmos" "$b/MANIFEST.opt-cosmos" || die $EXIT_FAILED "the backup of $INSTALL_DIR does not match the original"
	same_as_manifest "$b/var-lib-cosmos" "$b/MANIFEST.var-lib-cosmos" || die $EXIT_FAILED "the backup of $DATA_DIR does not match the original"
	going_back_text "$b" "$FOUND_VERSION" >"$b/GOING-BACK.txt"
	sync -f "$b" 2>/dev/null || true
	say "Backup: $(du -sh -- "$b" | cut -f1), every file checked against the original ($(grep -c '^[0-9a-f]\{64\}  ' "$b/MANIFEST.var-lib-cosmos") files of data)."
	state_set mstep backed-up
	test_fail_at backed-up

	step "Putting the fork $VERSION in $INSTALL_DIR (start.sh, the unit and the data stay)"
	state_set mstep swapping
	mv -- "$INSTALL_DIR" "$MIG_OLD"
	mv -- "$NEW" "$INSTALL_DIR"
	cp -p -- "$MIG_OLD/start.sh" "$INSTALL_DIR/start.sh"
	chmod 755 "$INSTALL_DIR" "$INSTALL_DIR/start.sh"
	sync -f "$INSTALL_DIR" 2>/dev/null || true
	state_set mstep swapped
	test_fail_at swapped
	migrate_start_and_check
}
# changed_lines <before> <after> <n>: the first n lines that differ, on one
# line; always status 0 (diff ends with 1 when there are differences, which
# under pipefail would end the script before it says what differed)
changed_lines() {
	{ diff -- "$1" "$2" || true; } | grep '^[<>]' | head -n "$3" | tr '\n' ' ' || true
}
# migrate_start_and_check: also where a migration interrupted after the
# swap goes on
migrate_start_and_check() {
	PHASE=migrating
	local diffs="" b
	b=$(state_get backup)
	step "Starting the fork"
	state_set mstep started
	systemctl start "$UNIT.service"
	wait_answer "$HTTP_PORT" 180 || die $EXIT_FAILED "the fork did not answer on port $HTTP_PORT within 3 minutes"
	test_fail_at started
	# a moment for what Cosmos does at its start (containers it watches)
	sleep 10
	step "Checking that it answers as before"
	mig_observe after
	local what
	for what in settings urls containers; do
		if ! cmp -s -- "$MIG_DIR/before.$what" "$MIG_DIR/after.$what"; then
			case $what in
			settings) diffs="$diffs
  the settings (before < > after): $(changed_lines "$MIG_DIR/before.$what" "$MIG_DIR/after.$what" 4)" ;;
			urls) diffs="$diffs
  the addresses (host, path, code; before < > after): $(changed_lines "$MIG_DIR/before.$what" "$MIG_DIR/after.$what" 6)" ;;
			containers) diffs="$diffs
  the containers (before < > after): $(changed_lines "$MIG_DIR/before.$what" "$MIG_DIR/after.$what" 6)" ;;
			esac
		fi
	done
	cmp -s -- "$b/$UNIT.service" "$UNIT_FILE" || diffs="$diffs
  the unit $UNIT_FILE changed"
	grep -q "\"version\": *\"$VERSION\"" "$INSTALL_DIR/meta.json" || diffs="$diffs
  $INSTALL_DIR/meta.json does not say $VERSION"
	if [ -e "$DATA_DIR/setup-code" ]; then diffs="$diffs
  the server made a first-run setup code: it took itself for a new installation"; fi
	[ -z "$diffs" ] || die $EXIT_FAILED "the fork does not answer as upstream's Cosmos did:$diffs"
	test_fail_at checked
	say "Settings, $(wc -l <"$MIG_DIR/after.urls") addresses and $(wc -l <"$MIG_DIR/after.containers") containers: as before. No setup code (the administrator is the one of before)."

	# done: upstream's folder is in the backup
	rm -rf -- "$MIG_OLD"
	record "migrated-from $(state_get migrated-from)"
	record "backup $b"
	state_set version "$VERSION"
	state_set completed-at "$(date -u +%FT%TZ)"
	state_set completed yes
	PHASE="done"
	step "Cosmos runs the fork $VERSION"
	say "Moved from upstream's Cosmos $(state_get migrated-from): the same settings, users, addresses and containers."
	say "The backup of before is in $b ($(du -sh -- "$b" | cut -f1); private: it holds the server's keys)."
	say "How to go back to upstream by hand: $b/GOING-BACK.txt"
	say "Once you keep the fork, delete the backup: sudo rm -rf $b"
	say "Updates come from the panel (or by themselves at night), not from install.sh or migrate.sh."
	exit 0
}

RESUMING=0
STATE_ROLE=install
if [ -f "$STATE" ] && [ -n "$(state_get role)" ]; then STATE_ROLE=$(state_get role); fi
if [ -f "$STATE" ] && [ "$(state_get completed)" = yes ]; then
	if [ "$STATE_ROLE" = migrate ]; then
		die $EXIT_FORK "the Cosmos fork $(state_get version) runs here: migrate.sh moved it from upstream's Cosmos $(state_get migrated-from) on $(state_get completed-at).
Updates are made from the panel (the update notice) or by the server itself at night,
never by install.sh or migrate.sh. Going back to upstream: $(state_get backup)/GOING-BACK.txt"
	fi
	die $EXIT_FORK "the Cosmos fork $(state_get version) was installed by install.sh on $(state_get completed-at) in $INSTALL_DIR.
Updates are made from the panel (the update notice) or by the server itself at night,
never by install.sh or migrate.sh. To remove it: sudo bash $INSTALL_DIR/fork/install/uninstall.sh"
fi
if [ -f "$STATE" ]; then
	RESUMING=1
	if [ "$STATE_ROLE" != "$SCRIPT_ROLE" ]; then
		if [ "$STATE_ROLE" = migrate ]; then
			die $EXIT_RESUME "a migration by migrate.sh was interrupted here: run migrate.sh again to finish it, or migrate.sh --undo to put upstream's Cosmos back. Nothing was changed."
		fi
		die $EXIT_RESUME "an installation by install.sh was interrupted here: run install.sh again to finish it, or remove what it did with uninstall.sh. Nothing was changed."
	fi
fi

if [ "$SCRIPT_ROLE" = migrate ]; then
	# An interrupted migration: after the swap it is finished (the fork is
	# started and checked); before it, upstream is put back and the
	# migration starts again. --undo puts upstream back in both cases.
	if [ "$RESUMING" = 1 ]; then
		HTTP_PORT=$(state_get http-port) HTTPS_PORT=$(state_get https-port)
		[[ $HTTP_PORT =~ ^[1-9][0-9]{0,4}$ ]] || migrate_ports
		if [ "$UNDO" = 1 ]; then
			migrate_rollback || die $EXIT_FAILED "the migration could not be undone entirely (above)"
			say "The interrupted migration is undone: upstream's Cosmos is as it was."
			exit 0
		fi
		case $(state_get mstep) in
		swapped | started)
			MIG_RESUME=finish
			say "A migration was interrupted after the fork was put in place: it is started and checked now (or put back if the check fails)." ;;
		*)
			say "A migration was interrupted (at: $(state_get mstep)): upstream's Cosmos is put back first, then the migration starts again."
			migrate_rollback || die $EXIT_FAILED "the interrupted migration could not be put back entirely (above)"
			RESUMING=0 ;;
		esac
	elif [ "$UNDO" = 1 ]; then
		say "There is no interrupted migration to undo: nothing was changed."
		exit 0
	fi
	if [ "$MIG_RESUME" != finish ]; then
		detect_cosmos
		case $FOUND in
		upstream) say "Found: $FOUND_WHAT." ;;
		fork) die $EXIT_FORK "$FOUND_WHAT.
This script never runs over an installation of the fork. Updates are made from the
panel (the update notice) or by the server itself at night, not by install.sh or migrate.sh." ;;
		container) die $EXIT_CANNOT "$FOUND_WHAT.
migrate.sh moves only a Cosmos installed on the system itself (upstream's get.sh), not yet its Docker container. Nothing was changed." ;;
		none) die $EXIT_NOTHING "there is no Cosmos here to move to the fork: install the fork with install.sh from the same release. Nothing was changed." ;;
		*) die $EXIT_OTHER "$FOUND_WHAT.
This is not a complete installation of upstream's Cosmos: migrate.sh moves only one. Nothing was changed." ;;
		esac
	fi
fi

if [ "$SCRIPT_ROLE" = install ]; then
detect_cosmos
fi
if [ "$SCRIPT_ROLE" = install ] && [ "$FOUND" != none ]; then
	ours=0
	if [ "$RESUMING" = 1 ]; then
		# An interrupted run of this script: what is there must be what it made.
		ours=1
		case $FOUND in
		fork | folder-unknown) recorded "dir $INSTALL_DIR" || ours=0
			if [ -n "$FOUND_UNIT" ]; then recorded "unit $UNIT.service" || ours=0; fi ;;
		unit-only) recorded "unit $UNIT.service" || ours=0 ;;
		data-only) recorded "data $DATA_DIR" || [ "$(state_get reuse-data)" = yes ] || ours=0 ;;
		*) ours=0 ;;
		esac
	fi
	if [ "$ours" = 0 ]; then
		case $FOUND in
		fork) die $EXIT_FORK "$FOUND_WHAT.
This script never runs over an installation of the fork. Updates are made from the
panel (the update notice) or by the server itself at night, not by install.sh or migrate.sh." ;;
		upstream) die $EXIT_UPSTREAM "$FOUND_WHAT.
To move it to the fork, use migrate.sh from the same release; install.sh never replaces a Cosmos." ;;
		container) die $EXIT_UPSTREAM "$FOUND_WHAT.
Moving a Cosmos container to the fork is migrate.sh's job (not available yet for containers); install.sh does not install a second Cosmos beside it." ;;
		data-only)
			cosmos_data_kind "$DATA_DIR"
			if [ "$REUSE_DATA" = 1 ] && [ "$DATA_PROBLEM" = mongodb ]; then
				die $EXIT_DATA "the data in $DATA_DIR keeps its users in MongoDB (a Cosmos from before 0.23): no Cosmos of today reads it. Nothing was changed."
			elif [ "$REUSE_DATA" = 1 ] && [ "$DATA_KIND" != fork ]; then
				die $EXIT_UPSTREAM "the data in $DATA_DIR is upstream's Cosmos's${DATA_PROBLEM:+ (with a licence of the paid edition)}: moving it to the fork is migrate.sh's job, with its checks of the version and the licence. install.sh --reuse-data takes data the fork left only. Nothing was changed."
			elif [ "$REUSE_DATA" = 1 ]; then
				say "Using the data the fork left in $DATA_DIR (--reuse-data)."
			else
				die $EXIT_DATA "$FOUND_WHAT.
Add --reuse-data to install the fork over it, or move the folder away first."
			fi ;;
		*) die $EXIT_OTHER "$FOUND_WHAT.
Nothing was changed. Remove what is left by hand (or with uninstall.sh if this script made it), then run install.sh again." ;;
		esac
	fi
fi

if [ "$RESUMING" = 1 ] && [ "$SCRIPT_ROLE" = install ]; then
	prev_version=$(state_get version)
	prev_ports="$(state_get http-port)/$(state_get https-port)"
	if [ -n "$WANT_VERSION" ] && [ -n "$prev_version" ] && [ "$WANT_VERSION" != "$prev_version" ]; then
		die $EXIT_RESUME "an interrupted run of install.sh was installing $prev_version: run it again with the same options, or remove what it did with uninstall.sh first"
	fi
	if [ "$prev_ports" != "/" ] && { [ -n "${ANSWER[http-port]:-}" ] || [ -n "${ANSWER[https-port]:-}" ]; } &&
		[ "${ANSWER[http-port]:-$(state_get http-port)}/${ANSWER[https-port]:-$(state_get https-port)}" != "$prev_ports" ]; then
		die $EXIT_RESUME "an interrupted run of install.sh used the ports $prev_ports: run it again with the same options, or remove what it did with uninstall.sh first"
	fi
	[ -n "$WANT_VERSION" ] || WANT_VERSION=$prev_version
	[ "$(state_get reuse-data)" != yes ] || REUSE_DATA=1
	say "Continuing an install that was interrupted (state in $STATE)."
fi

# What a run killed with -9 left in its private folders (the lock says no
# other run is going).
if [ "$DRY_RUN" = 0 ]; then
	for f in /opt/.cosmos-install.* /var/tmp/cosmos-install.* /var/tmp/cosmos-install-apt.* /opt/.cosmos-fork-tried.*; do
		if [ -d "$f" ] && [ ! -L "$f" ]; then rm -rf -- "$f"; fi
	done
fi

# ---- 2. The system ----------------------------------------------------------
# The systems this script supports, as <ID>:<VERSION_ID> of /etc/os-release:
# the ONE list. The release notes name what it says
# (scripts/fork-release-package.sh reads this line; a test compares them).
SUPPORTED_SYSTEMS="debian:12 debian:13 ubuntu:24.04 ubuntu:26.04"
supported_words() { # "Debian 12/13 or Ubuntu 24.04/26.04"
	local d="" u="" s
	for s in $SUPPORTED_SYSTEMS; do
		case $s in debian:*) d=${d:+$d/}${s#*:} ;; ubuntu:*) u=${u:+$u/}${s#*:} ;; esac
	done
	printf 'Debian %s or Ubuntu %s' "$d" "$u"
}
step "Checking the system"
[ -r /etc/os-release ] || die $EXIT_SYSTEM "/etc/os-release is missing: this is not a system this script knows"
OS_ID=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_VERSION=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_CODENAME=$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_UBUNTU_CODENAME=$(sed -n 's/^UBUNTU_CODENAME=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_LIKE=$(sed -n 's/^ID_LIKE=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_NAME=$(sed -n 's/^PRETTY_NAME=//p' /etc/os-release | tr -d '"' | head -n 1 | tr -cd 'A-Za-z0-9 ._()/-')
supported=0
case " $SUPPORTED_SYSTEMS " in *" $OS_ID:$OS_VERSION "*) [ -z "$OS_VERSION" ] || supported=1 ;; esac
if [ "$supported" = 0 ]; then
	case " $OS_ID $OS_LIKE " in
	*" debian "* | *" ubuntu "*) ;;
	*) die $EXIT_SYSTEM "$OS_NAME is not Debian or Ubuntu: this script only knows apt systems." ;;
	esac
	if [ "$FORCE" = 1 ]; then
		warn "$OS_NAME is not a supported system ($(supported_words)): going on because of --force, at your own risk."
	else
		die $EXIT_SYSTEM "$OS_NAME is not supported (supported: $(supported_words)). Add --force to try anyway, at your own risk."
	fi
fi
# Docker's repository: by the system's own name and code name; on a
# derivative (--force) only when it names the Ubuntu or Debian release it
# follows (UBUNTU_CODENAME, or a Debian code name Docker serves); otherwise
# DOCKER_DIST stays empty and a missing Docker stops the run below.
DOCKER_DIST="" DOCKER_SUITE=""
case " $OS_ID $OS_LIKE " in
" ubuntu "*) DOCKER_DIST=ubuntu; DOCKER_SUITE=${OS_UBUNTU_CODENAME:-$OS_CODENAME} ;;
" debian "*) DOCKER_DIST=debian; DOCKER_SUITE=$OS_CODENAME ;;
*" ubuntu "*) if [ -n "$OS_UBUNTU_CODENAME" ]; then DOCKER_DIST=ubuntu; DOCKER_SUITE=$OS_UBUNTU_CODENAME; fi ;;
*" debian "*) case $OS_CODENAME in bookworm | trixie) DOCKER_DIST=debian; DOCKER_SUITE=$OS_CODENAME ;; esac ;;
esac
if [ -n "$DOCKER_DIST" ] && ! [[ $DOCKER_SUITE =~ ^[a-z]{2,20}$ ]]; then DOCKER_DIST="" DOCKER_SUITE=""; fi
if ! command -v dpkg >/dev/null || ! command -v apt-get >/dev/null; then die $EXIT_SYSTEM "dpkg and apt-get are needed"; fi
ARCH=$(dpkg --print-architecture)
case $ARCH in
amd64 | arm64) ;;
*) die $EXIT_SYSTEM "the processor architecture $ARCH is not supported (amd64 or arm64)" ;;
esac
[ -d /run/systemd/system ] || die $EXIT_SYSTEM "systemd is not running: Cosmos runs as a systemd service"
say "System: $OS_NAME, $ARCH."
[ "$ASK" = 0 ] || say "(This version asks no questions yet: it takes the options given and the proposals below.)"

free_kib() { df -Pk "$1" 2>/dev/null | awk 'NR==2{print $4}'; }
need_space() { # folder, KiB, what
	local have
	have=$(free_kib "$1")
	if [ -n "$have" ] && [ "$have" -lt "$2" ]; then
		die $EXIT_PREREQ "not enough free space in $1 for $3: $((have / 1024)) MiB free, $(($2 / 1024)) MiB needed. Nothing was changed."
	fi
}
need_space /opt $((1200 * 1024)) "the installation and the room the first update keeps for going back"
need_space /var/tmp $((700 * 1024)) "the download"
need_space /var/lib $((300 * 1024)) "the data"

# Ports: listening sockets from /proc (iproute2 may not be installed).
port_taken() { # port
	local hex
	hex=$(printf '%04X' "$1")
	awk -v p=":$hex" 'NR>1 && $4=="0A" && substr($2, length($2)-4)==p {found=1} END{exit !found}' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}
choose_ports() {
	local h s pair
	h=${ANSWER[http-port]:-} s=${ANSWER[https-port]:-}
	if [ -z "$h" ] && [ -z "$s" ] && [ "$RESUMING" = 1 ] && [ -n "$(state_get http-port)" ]; then
		h=$(state_get http-port) s=$(state_get https-port)
		HTTP_PORT=$h HTTPS_PORT=$s
		return
	fi
	if [ -n "$h" ] || [ -n "$s" ]; then
		h=${h:-80} s=${s:-443}
		[ "$h" != "$s" ] || die $EXIT_USAGE "the HTTP and the HTTPS port must differ"
		! port_taken "$h" || die $EXIT_FAILED "port $h is already in use on this machine. Nothing was changed."
		! port_taken "$s" || die $EXIT_FAILED "port $s is already in use on this machine. Nothing was changed."
		HTTP_PORT=$h HTTPS_PORT=$s
		return
	fi
	for pair in 80/443 8080/8443 2052/2053; do
		h=${pair%/*} s=${pair#*/}
		if ! port_taken "$h" && ! port_taken "$s"; then
			HTTP_PORT=$h HTTPS_PORT=$s
			[ "$pair" = 80/443 ] || say "Ports 80 and 443 are in use: the proposal is $h and $s."
			return
		fi
	done
	die $EXIT_FAILED "the ports 80/443, 8080/8443 and 2052/2053 are all in use: choose two with --http-port and --https-port. Nothing was changed."
}
if [ "$SCRIPT_ROLE" = install ]; then
	choose_ports
	say "Ports: HTTP $HTTP_PORT, HTTPS $HTTPS_PORT."
elif [ "$MIG_RESUME" != finish ]; then
	migrate_ports
	say "Ports (the unit's, kept): HTTP $HTTP_PORT, HTTPS $HTTPS_PORT."
fi

# ---- Docker that is already there -------------------------------------------
# Never "producer | grep -q" for a decision: under pipefail, grep -q stopping
# at the first match makes the producer die of SIGPIPE on a long listing, and
# the pipeline then reads as "no match". Text is captured first and matched
# with has_line (a here-string: no pipe).
has_line() { grep -qE -- "$1" <<<"$2"; } # regex, text
installed() { local st; st=$(dpkg-query -W -f='${db:Status-Abbrev}' "$1" 2>/dev/null) || return 1; [[ $st == ii* ]]; }
version_at_least() { # have, want (x.y)
	local hm hn wm wn
	hm=$(printf '%s' "$1" | sed -n 's/^\([0-9]\{1,4\}\)\.\([0-9]\{1,4\}\).*/\1/p')
	hn=$(printf '%s' "$1" | sed -n 's/^\([0-9]\{1,4\}\)\.\([0-9]\{1,4\}\).*/\2/p')
	wm=${2%.*} wn=${2#*.}
	[ -n "$hm" ] && { [ "$hm" -gt "$wm" ] || { [ "$hm" -eq "$wm" ] && [ "$hn" -ge "$wn" ]; }; }
}
DOCKER_PLAN=install DOCKER_NOTE=""
# docker_answers: the daemon's version, from the docker command, or when
# there is none (Debian's docker.io without docker-cli) from dockerd itself
# while its service is active (Cosmos talks to the socket, not the command).
docker_answers() {
	if command -v docker >/dev/null 2>&1 || [ -e /snap/bin/docker ]; then
		timeout 30 "$(command -v docker || echo /snap/bin/docker)" version --format '{{.Server.Version}}' 2>/dev/null | head -n 1 | tr -cd 'A-Za-z0-9.+~-' || true
	elif command -v dockerd >/dev/null 2>&1 && [ -S /var/run/docker.sock ] && systemctl is-active --quiet docker.service; then
		dockerd --version 2>/dev/null | sed -n 's/^Docker version \([0-9][0-9.]*\).*/\1/p' | head -n 1
	fi
}
if [ "$RESUMING" = 1 ] && recorded "package docker-ce"; then
	# Docker's packages of the interrupted run of this script (perhaps left
	# half-configured, so the daemon may not answer yet): its own, finished
	# with the rest (dpkg --configure -a, then the packages step)
	DOCKER_NOTE="Docker's packages were installed by the interrupted run of this script: they are finished with the rest."
elif command -v docker >/dev/null 2>&1 || [ -e /snap/bin/docker ] || command -v dockerd >/dev/null 2>&1; then
	docker_bin=$(command -v docker || command -v dockerd || echo /snap/bin/docker)
	server_version=$(docker_answers)
	origin="installed by hand"
	if [ "$docker_bin" = /snap/bin/docker ] || { command -v snap >/dev/null && snap list docker >/dev/null 2>&1; }; then
		origin="the snap package"
	elif installed docker-ce; then origin="Docker's own package (docker-ce)"
	elif installed docker.io; then origin="the distribution's package (docker.io)"
	elif installed podman-docker; then origin="podman-docker"
	fi
	if [ "$origin" = podman-docker ]; then
		die $EXIT_PREREQ "the docker command here is podman (podman-docker): Cosmos needs Docker itself. Nothing was changed."
	fi
	if [ -z "$server_version" ]; then
		die $EXIT_PREREQ "Docker is installed ($origin) but does not answer: start it (systemctl start docker) and run install.sh again. Nothing was changed; your Docker is left as it is."
	fi
	if ! version_at_least "$server_version" "$MIN_DOCKER"; then
		die $EXIT_PREREQ "Docker $server_version ($origin) is older than $MIN_DOCKER, which Cosmos needs. Update it, then run install.sh again. Nothing was changed."
	fi
	DOCKER_PLAN=use
	DOCKER_NOTE="Docker $server_version is already installed ($origin): it is used and left as it is."
	if [ "$origin" = "the snap package" ]; then
		DOCKER_NOTE="$DOCKER_NOTE The snap's confinement lets containers mount only some folders of the host (under /home and a few others): apps whose volumes are elsewhere will fail to start."
	fi
else
	if [ -z "$DOCKER_DIST" ]; then
		die $EXIT_SYSTEM "$OS_NAME does not say which Debian or Ubuntu release it follows, so Docker's repository for it is unknown. Install Docker yourself, then run install.sh again. Nothing was changed."
	fi
	for p in docker.io docker-compose docker-doc podman-docker containerd runc; do
		if installed "$p"; then
			die $EXIT_PREREQ "the package $p is installed but no working Docker was found. Docker's packages conflict with it, and it may be running containers: remove it yourself (apt-get remove $p) or make its Docker work, then run install.sh again. Nothing was changed."
		fi
	done
fi
[ -n "$DOCKER_NOTE" ] || DOCKER_NOTE="Docker is not installed: it will be installed from Docker's own repository (download.docker.com)."
say "$DOCKER_NOTE"

# What a migration needs, read before anything changes (migrate.sh)
if [ "$SCRIPT_ROLE" = migrate ] && [ "$MIG_RESUME" != finish ]; then
	step "Checking that this Cosmos can be moved to the fork"
	migrate_checks
	migrate_backup_place
	say "It can: upstream's Cosmos $FOUND_VERSION, settings in $DATA_DIR; the backup goes to $MIG_BACKUP."
fi

# ---- 3. Which packages, and a simulation before any change -------------------
TOOLS=(ca-certificates curl unzip openssl)
# migrate.sh reads the settings with jq
if [ "$SCRIPT_ROLE" = migrate ]; then TOOLS+=(jq); fi
BASE=(iproute2 procps util-linux psmisc avahi-daemon iptables)
STORAGE=(fuse3 mergerfs snapraid samba mdadm parted e2fsprogs)
DOCKER_PACKAGES=(docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin)
# The protection pieces. What they need is known only from the verified
# release: each is asked with --print-packages once it is unpacked, and only
# a piece that answers is run. So the first simulation holds none of their
# packages (a piece that will not run must not stop the install), and the
# second one, before any package beyond the download tools, holds exactly
# those of the pieces that will run.
PIECES="unattended-upgrades fail2ban nftables"
chosen_pieces() {
	local p
	[ "$(answer protection)" = yes ] || { [ "$(answer unattended-upgrades)" = yes ] && echo unattended-upgrades; return 0; }
	for p in $PIECES; do
		if [ "$p" = unattended-upgrades ] && [ "$(answer unattended-upgrades)" != yes ]; then continue; fi
		echo "$p"
	done
}
wanted_packages() { # the distribution's packages wanted, before the pieces answered (see PIECES)
	printf '%s\n' "${TOOLS[@]}" "${BASE[@]}"
	if [ "$(answer storage-tools)" = yes ]; then printf '%s\n' "${STORAGE[@]}"; fi
}
missing_of() { local p; for p in "$@"; do installed "$p" || echo "$p"; done; }

# How long apt waits for another program that holds the package database or
# its lists (unattended-upgrades at first boot): 10 minutes, or
# COSMOS_INSTALL_APT_WAIT seconds (10 to 3600; the test bed shortens it).
APT_WAIT=600
if [[ ${COSMOS_INSTALL_APT_WAIT:-} =~ ^[1-9][0-9]{1,3}$ ]] && [ "$COSMOS_INSTALL_APT_WAIT" -le 3600 ]; then APT_WAIT=$COSMOS_INSTALL_APT_WAIT; fi
# shellcheck disable=SC2034 # read by apt_get through the array
APT_ENV=(env DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1 APT_LISTCHANGES_FRONTEND=none)
# apt and dpkg are stopped when one call takes longer than this (a hung
# mirror, a package script that waits for ever): generous, said when it
# happens, and the run can be started again where it stopped.
# COSMOS_INSTALL_APT_TIMEOUT seconds (60 to 86400) replaces it (the test bed
# shortens it).
APT_TIMEOUT=$((5400 + APT_WAIT))
if [[ ${COSMOS_INSTALL_APT_TIMEOUT:-} =~ ^[1-9][0-9]{1,4}$ ]] && [ "$COSMOS_INSTALL_APT_TIMEOUT" -ge 60 ] && [ "$COSMOS_INSTALL_APT_TIMEOUT" -le 86400 ]; then
	APT_TIMEOUT=$COSMOS_INSTALL_APT_TIMEOUT
fi
# apt_run <seconds> <command...>: the command in a systemd scope of its own
# (systemd-run --scope runs it in place: still this script's child, in its
# process group, so Ctrl-C and a lost session stop it as before). At the
# bound systemd stops the WHOLE scope: apt, dpkg and every package script
# they started, not only apt. APT_RAN_FOR says how long it ran.
APT_SCOPES=0 APT_RAN_FOR=0
apt_run() {
	local bound=$1 started=$SECONDS rc=0
	shift
	APT_SCOPES=$((APT_SCOPES + 1))
	# the subshell's own pid too: a call in a pipeline or in $(...) counts in
	# its subshell, and two such calls in a row must not ask for the same
	# name while systemd still holds the first
	systemd-run --scope --quiet --collect --unit="cosmos-install-apt-$$-$BASHPID-$APT_SCOPES" \
		-p RuntimeMaxSec="$bound" -p TimeoutStopSec=20 -- "$@" || rc=$?
	APT_RAN_FOR=$((SECONDS - started))
	return "$rc"
}
# apt_timed_out <what> <status>: true (and said) when the call was stopped at the bound
apt_timed_out() {
	[ "$2" != 0 ] && [ "$APT_RAN_FOR" -ge "$APT_TIMEOUT" ] || return 1
	warn "$1 did not finish within $((APT_TIMEOUT / 60)) minutes: it was stopped, with everything it had started (dpkg, package scripts). The package database may be unfinished: run install.sh again with the same options; it finishes it first (dpkg --configure -a)."
}
APT_OPTS=(-y -q -o Acquire::Retries=3 -o "DPkg::Lock::Timeout=$APT_WAIT" -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)
# apt_holders: who holds apt's or dpkg's locks right now, in words (the
# program, its process and its systemd unit: apt-daily.service,
# unattended-upgrades.service...), or nothing.
apt_holders() {
	local locks line cmd pid path unit out=""
	locks=$(lslocks -n -r -o COMMAND,PID,PATH 2>/dev/null || true)
	while read -r cmd pid path; do
		case $path in /var/lib/dpkg/lock | /var/lib/dpkg/lock-frontend | /var/lib/apt/lists/lock | /var/cache/apt/archives/lock) ;; *) continue ;; esac
		[[ $pid =~ ^[0-9]+$ ]] || continue
		# its systemd unit, from its cgroup (0::/system.slice/apt-daily.service)
		unit=$(sed -n 's#^0::.*/\([^/]*\.\(service\|scope\)\)$#\1#p' "/proc/$pid/cgroup" 2>/dev/null | head -n 1 | tr -cd '[:alnum:]@._-' || true)
		line="$(printf '%s' "$cmd" | tr -cd '[:alnum:]._-') (process $pid${unit:+, $unit})"
		case " $out " in *" $line;"*) ;; *) out="$out $line;" ;; esac
	done <<<"$locks"
	out=${out%;}
	printf '%s' "${out# }"
}

# apt_get <args>: apt itself waits (APT_WAIT) while another program holds
# the package database, and says so as it waits; a lock it does not wait
# for (the lists) is tried again here for as long. Its output is shown as it
# comes, without the progress lines. The status is apt-get's own.
apt_get() {
	local rc waited=0 log statuses
	log=$(mktemp "$WORK/apt.XXXXXX")
	while :; do
		local t0=$SECONDS # (the pipeline runs apt_run in a subshell: timed here)
		set +e +o pipefail
		apt_run "$APT_TIMEOUT" "${APT_ENV[@]}" apt-get "${APT_OPTS[@]}" "$@" </dev/null 2>&1 | tee "$log" |
			grep --line-buffered -vE '^(Get|Hit|Ign|Reading|Building|Preparing|Unpacking|Selecting|\(Reading|Setting up|Processing|Created symlink|Fetched|Need to get|After this|The following|Suggested|Recommended|  [a-z0-9])'
		statuses=("${PIPESTATUS[@]}")
		set -e -o pipefail
		rc=${statuses[0]}
		APT_RAN_FOR=$((SECONDS - t0))
		if apt_timed_out "apt-get $1" "$rc"; then rm -f "$log"; return "$rc"; fi
		if [ "$rc" -ne 0 ] && grep -qE 'Could not get lock|Unable to acquire the dpkg frontend lock|is another process using it' "$log"; then
			if [ "$waited" -ge "$APT_WAIT" ]; then
				warn "another program held apt for longer than $APT_WAIT seconds"
				rm -f "$log"
				return "$rc"
			fi
			if [ $((waited % 60)) = 0 ]; then
				local who
				who=$(apt_holders)
				# to stderr: the callers often discard apt's own output
				say "Another program is using apt${who:+: $who}; waiting for it (${waited} of at most $APT_WAIT seconds; on a new server the system's own daily apt run often takes a few minutes)..." >&2
			fi
			sleep 5
			waited=$((waited + 5))
			continue
		fi
		rm -f "$log"
		return "$rc"
	done
}

mkdir_work() {
	[ -n "$WORK" ] && return 0
	WORK=$(mktemp -d /var/tmp/cosmos-install.XXXXXXXX)
	chmod 700 "$WORK"
}
mkdir_work

# simulate <extra apt options...> -- <packages>: on a private copy of the
# package lists, so that nothing on the system changes.
# The simulation's apt runs on copies: its own lists, cache and sources, and
# the system's configuration with the hooks that run on "apt-get update"
# cleared (Ubuntu's command-not-found, update-notifier, esm): the simulation
# writes nothing outside its folder. The folder is readable so that apt
# downloads as its own sandbox user (_apt), as it does normally.
SIM=$(mktemp -d /var/tmp/cosmos-install-apt.XXXXXXXX)
chmod 755 "$SIM"
sim_setup() {
	mkdir -p "$SIM/lists/partial" "$SIM/cache/archives/partial" "$SIM/parts" "$SIM/conf"
	if [ -d /var/lib/apt/lists ]; then
		find /var/lib/apt/lists -maxdepth 1 -type f ! -name lock -exec cp -p {} "$SIM/lists/" \;
	fi
	local f
	for f in /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources; do cp -p "$f" "$SIM/parts/"; done
	for f in /etc/apt/apt.conf.d/*; do [ -f "$f" ] && cp -p "$f" "$SIM/conf/"; done
	printf '%s\n' '#clear APT::Update::Pre-Invoke;' '#clear APT::Update::Post-Invoke;' '#clear APT::Update::Post-Invoke-Success;' \
		'#clear APT::Update::Post-Invoke-Stats;' '#clear DPkg::Pre-Invoke;' '#clear DPkg::Post-Invoke;' '#clear DPkg::Pre-Install-Pkgs;' >"$SIM/conf/zzzz-cosmos-simulation"
	# apt reads the file named by APT_CONFIG BEFORE its configuration folder:
	# only there does pointing it at the copy take effect (an -o option on
	# the command line comes after the hooks are read).
	# the main file too (/etc/apt/apt.conf, read after the folder): copied in
	# first place among the parts, so that the clearing applies to it; apt
	# itself then reads no main file
	if [ -f /etc/apt/apt.conf ]; then cp -p /etc/apt/apt.conf "$SIM/conf/00-cosmos-main-apt.conf"; fi
	printf 'Dir::Etc::Main "/dev/null";\nDir::Etc::Parts "%s/conf";\nDir::State::Lists "%s/lists";\nDir::Cache "%s/cache";\nDir::Etc::SourceParts "%s/parts";\nDebug::NoLocking "1";\n' \
		"$SIM" "$SIM" "$SIM" "$SIM" >"$SIM/apt.conf"
	chmod -R a+rX "$SIM"
	if id -u _apt >/dev/null 2>&1; then chown -R _apt "$SIM/lists" "$SIM/cache"; fi
}
SIM_OPTS=()
sim_opts() {
	SIM_OPTS=(-o "Dir::State::Lists=$SIM/lists" -o "Dir::Cache=$SIM/cache" -o "Dir::Etc::SourceParts=$SIM/parts" -o Debug::NoLocking=1)
}
SIM_ENV=(env "APT_CONFIG=$SIM/apt.conf")
simulate() { # packages
	local out rc=0
	[ $# -gt 0 ] || return 0
	sim_opts
	local try
	for try in 1 2 3; do
		rc=0
		out=$(apt_run 1800 "${SIM_ENV[@]}" "${APT_ENV[@]}" apt-get -q -o Acquire::Retries=3 "${SIM_OPTS[@]}" update </dev/null 2>&1) || rc=$?
		if [ "$rc" = 0 ] && ! has_line '^(W|E): (Failed to fetch|Some index files failed)' "$out"; then break; fi
		[ "$try" = 3 ] || sleep 10
	done
	if [ "$rc" != 0 ]; then
		printf '%s\n' "$out" | tail -n 5 >&2
		die $EXIT_PREREQ "apt could not read the package lists (is the network up, are the sources in /etc/apt reachable?). Nothing was changed."
	fi
	if has_line '^(W|E): (Failed to fetch|Some index files failed)' "$out"; then
		printf '%s\n' "$out" | grep -E '^(W|E):' | head -n 5 >&2
		die $EXIT_PREREQ "apt could not fetch every package list (network or source problem). Nothing was changed."
	fi
	out=$(apt_run 1800 "${SIM_ENV[@]}" "${APT_ENV[@]}" apt-get -q -s "${SIM_OPTS[@]}" install --no-install-recommends "$@" </dev/null 2>&1) || rc=$?
	if [ "$rc" -ne 0 ]; then
		printf '%s\n' "$out" | grep -E '^(E:|  |Package|The following packages have unmet)' | head -n 12 >&2
		die $EXIT_PREREQ "apt cannot install what Cosmos needs ($*). Nothing was changed."
	fi
	if has_line '^Remv ' "$out"; then
		printf '%s\n' "$out" | grep '^Remv ' | head -n 10 >&2
		die $EXIT_PREREQ "installing what Cosmos needs would REMOVE the packages above. Nothing was changed."
	fi
}

step "Checking that every missing package can be installed (simulation, nothing is changed)"
sim_setup
mapfile -t MISSING < <(wanted_packages | sort -u | while read -r p; do installed "$p" || echo "$p"; done)
if [ "${#MISSING[@]}" -gt 0 ]; then
	say "Missing: ${MISSING[*]}"
	simulate "${MISSING[@]}"
	say "apt can install them."
else
	say "Every package of the system that Cosmos needs is there."
fi

# The Docker repository can be checked only once apt can talk HTTPS
# (ca-certificates): then the same simulation with Docker's source added.
docker_source_text() { # signed-by path
	printf 'Types: deb\nURIs: https://download.docker.com/linux/%s\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: %s\n' \
		"$DOCKER_DIST" "$DOCKER_SUITE" "$ARCH" "$1"
}
write_docker_key() { # path
	cat >"$1" <<'DOCKER_KEY'
-----BEGIN PGP PUBLIC KEY BLOCK-----

mQINBFit2ioBEADhWpZ8/wvZ6hUTiXOwQHXMAlaFHcPH9hAtr4F1y2+OYdbtMuth
lqqwp028AqyY+PRfVMtSYMbjuQuu5byyKR01BbqYhuS3jtqQmljZ/bJvXqnmiVXh
38UuLa+z077PxyxQhu5BbqntTPQMfiyqEiU+BKbq2WmANUKQf+1AmZY/IruOXbnq
L4C1+gJ8vfmXQt99npCaxEjaNRVYfOS8QcixNzHUYnb6emjlANyEVlZzeqo7XKl7
UrwV5inawTSzWNvtjEjj4nJL8NsLwscpLPQUhTQ+7BbQXAwAmeHCUTQIvvWXqw0N
cmhh4HgeQscQHYgOJjjDVfoY5MucvglbIgCqfzAHW9jxmRL4qbMZj+b1XoePEtht
ku4bIQN1X5P07fNWzlgaRL5Z4POXDDZTlIQ/El58j9kp4bnWRCJW0lya+f8ocodo
vZZ+Doi+fy4D5ZGrL4XEcIQP/Lv5uFyf+kQtl/94VFYVJOleAv8W92KdgDkhTcTD
G7c0tIkVEKNUq48b3aQ64NOZQW7fVjfoKwEZdOqPE72Pa45jrZzvUFxSpdiNk2tZ
XYukHjlxxEgBdC/J3cMMNRE1F4NCA3ApfV1Y7/hTeOnmDuDYwr9/obA8t016Yljj
q5rdkywPf4JF8mXUW5eCN1vAFHxeg9ZWemhBtQmGxXnw9M+z6hWwc6ahmwARAQAB
tCtEb2NrZXIgUmVsZWFzZSAoQ0UgZGViKSA8ZG9ja2VyQGRvY2tlci5jb20+iQI3
BBMBCgAhBQJYrefAAhsvBQsJCAcDBRUKCQgLBRYCAwEAAh4BAheAAAoJEI2BgDwO
v82IsskP/iQZo68flDQmNvn8X5XTd6RRaUH33kXYXquT6NkHJciS7E2gTJmqvMqd
tI4mNYHCSEYxI5qrcYV5YqX9P6+Ko+vozo4nseUQLPH/ATQ4qL0Zok+1jkag3Lgk
jonyUf9bwtWxFp05HC3GMHPhhcUSexCxQLQvnFWXD2sWLKivHp2fT8QbRGeZ+d3m
6fqcd5Fu7pxsqm0EUDK5NL+nPIgYhN+auTrhgzhK1CShfGccM/wfRlei9Utz6p9P
XRKIlWnXtT4qNGZNTN0tR+NLG/6Bqd8OYBaFAUcue/w1VW6JQ2VGYZHnZu9S8LMc
FYBa5Ig9PxwGQOgq6RDKDbV+PqTQT5EFMeR1mrjckk4DQJjbxeMZbiNMG5kGECA8
g383P3elhn03WGbEEa4MNc3Z4+7c236QI3xWJfNPdUbXRaAwhy/6rTSFbzwKB0Jm
ebwzQfwjQY6f55MiI/RqDCyuPj3r3jyVRkK86pQKBAJwFHyqj9KaKXMZjfVnowLh
9svIGfNbGHpucATqREvUHuQbNnqkCx8VVhtYkhDb9fEP2xBu5VvHbR+3nfVhMut5
G34Ct5RS7Jt6LIfFdtcn8CaSas/l1HbiGeRgc70X/9aYx/V/CEJv0lIe8gP6uDoW
FPIZ7d6vH+Vro6xuWEGiuMaiznap2KhZmpkgfupyFmplh0s6knymuQINBFit2ioB
EADneL9S9m4vhU3blaRjVUUyJ7b/qTjcSylvCH5XUE6R2k+ckEZjfAMZPLpO+/tF
M2JIJMD4SifKuS3xck9KtZGCufGmcwiLQRzeHF7vJUKrLD5RTkNi23ydvWZgPjtx
Q+DTT1Zcn7BrQFY6FgnRoUVIxwtdw1bMY/89rsFgS5wwuMESd3Q2RYgb7EOFOpnu
w6da7WakWf4IhnF5nsNYGDVaIHzpiqCl+uTbf1epCjrOlIzkZ3Z3Yk5CM/TiFzPk
z2lLz89cpD8U+NtCsfagWWfjd2U3jDapgH+7nQnCEWpROtzaKHG6lA3pXdix5zG8
eRc6/0IbUSWvfjKxLLPfNeCS2pCL3IeEI5nothEEYdQH6szpLog79xB9dVnJyKJb
VfxXnseoYqVrRz2VVbUI5Blwm6B40E3eGVfUQWiux54DspyVMMk41Mx7QJ3iynIa
1N4ZAqVMAEruyXTRTxc9XW0tYhDMA/1GYvz0EmFpm8LzTHA6sFVtPm/ZlNCX6P1X
zJwrv7DSQKD6GGlBQUX+OeEJ8tTkkf8QTJSPUdh8P8YxDFS5EOGAvhhpMBYD42kQ
pqXjEC+XcycTvGI7impgv9PDY1RCC1zkBjKPa120rNhv/hkVk/YhuGoajoHyy4h7
ZQopdcMtpN2dgmhEegny9JCSwxfQmQ0zK0g7m6SHiKMwjwARAQABiQQ+BBgBCAAJ
BQJYrdoqAhsCAikJEI2BgDwOv82IwV0gBBkBCAAGBQJYrdoqAAoJEH6gqcPyc/zY
1WAP/2wJ+R0gE6qsce3rjaIz58PJmc8goKrir5hnElWhPgbq7cYIsW5qiFyLhkdp
YcMmhD9mRiPpQn6Ya2w3e3B8zfIVKipbMBnke/ytZ9M7qHmDCcjoiSmwEXN3wKYI
mD9VHONsl/CG1rU9Isw1jtB5g1YxuBA7M/m36XN6x2u+NtNMDB9P56yc4gfsZVES
KA9v+yY2/l45L8d/WUkUi0YXomn6hyBGI7JrBLq0CX37GEYP6O9rrKipfz73XfO7
JIGzOKZlljb/D9RX/g7nRbCn+3EtH7xnk+TK/50euEKw8SMUg147sJTcpQmv6UzZ
cM4JgL0HbHVCojV4C/plELwMddALOFeYQzTif6sMRPf+3DSj8frbInjChC3yOLy0
6br92KFom17EIj2CAcoeq7UPhi2oouYBwPxh5ytdehJkoo+sN7RIWua6P2WSmon5
U888cSylXC0+ADFdgLX9K2zrDVYUG1vo8CX0vzxFBaHwN6Px26fhIT1/hYUHQR1z
VfNDcyQmXqkOnZvvoMfz/Q0s9BhFJ/zU6AgQbIZE/hm1spsfgvtsD1frZfygXJ9f
irP+MSAI80xHSf91qSRZOj4Pl3ZJNbq4yYxv0b1pkMqeGdjdCYhLU+LZ4wbQmpCk
SVe2prlLureigXtmZfkqevRz7FrIZiu9ky8wnCAPwC7/zmS18rgP/17bOtL4/iIz
QhxAAoAMWVrGyJivSkjhSGx1uCojsWfsTAm11P7jsruIL61ZzMUVE2aM3Pmj5G+W
9AcZ58Em+1WsVnAXdUR//bMmhyr8wL/G1YO1V3JEJTRdxsSxdYa4deGBBY/Adpsw
24jxhOJR+lsJpqIUeb999+R8euDhRHG9eFO7DRu6weatUJ6suupoDTRWtr/4yGqe
dKxV3qQhNLSnaAzqW/1nA3iUB4k7kCaKZxhdhDbClf9P37qaRW467BLCVO/coL3y
Vm50dwdrNtKpMBh3ZpbB1uJvgi9mXtyBOMJ3v8RZeDzFiG8HdCtg9RvIt/AIFoHR
H3S+U79NT6i0KPzLImDfs8T7RlpyuMc4Ufs8ggyg9v3Ae6cN3eQyxcK3w0cbBwsh
/nQNfsA6uu+9H7NhbehBMhYnpNZyrHzCmzyXkauwRAqoCbGCNykTRwsur9gS41TQ
M8ssD1jFheOJf3hODnkKU+HKjvMROl1DK7zdmLdNzA1cvtZH/nCC9KPj1z8QC47S
xx+dTZSx4ONAhwbS/LN3PoKtn8LPjY9NP9uDWI+TWYquS2U+KHDrBDlsgozDbs/O
jCxcpDzNmXpWQHEtHU7649OXHP7UeNST1mCUCH5qdank0V1iejF6/CfTFU4MfcrG
YT90qFF93M3v01BbxP+EIY2/9tiIPbrd
=0YYh
-----END PGP PUBLIC KEY BLOCK-----
DOCKER_KEY
}
DOCKER_FINGERPRINT="9DC8 5822 9FC7 DD38 854A E2D8 8D81 803C 0EBF CD88"

# other_docker_source: Docker's repository is already among the sources (a
# file this script did not write): it is then used as it is.
other_docker_source() {
	local f
	for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources; do
		[ "$f" != "$DOCKER_SOURCE" ] || continue
		if [ -f "$f" ] && has_line '^[^#]*download\.docker\.com' "$(cat -- "$f")"; then return 0; fi
	done
	return 1
}
simulate_docker() {
	if ! other_docker_source; then
		write_docker_key "$SIM/docker.asc"
		chmod 644 "$SIM/docker.asc"
		rm -f "$SIM/parts/docker.sources"
		docker_source_text "$SIM/docker.asc" >"$SIM/parts/zz-cosmos-docker.sources"
	fi
	local missing
	mapfile -t missing < <(missing_of "${DOCKER_PACKAGES[@]}")
	simulate "${missing[@]}" "${MISSING[@]}"
}

# ---- What a failure means -------------------------------------------------
# Until the release is unpacked, a failure undoes what this run installed
# (the four download tools); after that, it says what is there and how
# uninstall.sh removes it.
PHASE=check
# present_now: every package dpkg has on the system in any state but "not
# installed" and "only its configuration left": half-installed, unpacked,
# half-configured, waiting for triggers included.
present_now() { dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n' 2>/dev/null | awk 'substr($1,2,1)!="n" && substr($1,2,1)!="c"{print $NF}' | sed 's/:'"$ARCH"'$//' | sort -u; }
# pkg_state <package>: dpkg's second status letter (i installed, n not
# installed, c config files, U unpacked, F half-configured, H half-installed,
# W/t triggers)
pkg_state() { local st; st=$(dpkg-query -W -f='${db:Status-Abbrev}' "$1" 2>/dev/null) || st=un; printf '%s' "${st:1:1}"; }
# The packages this run's own apt transactions installed, for the early undo.
RUN_PACKAGES=()
# Those a failed transaction of this run left half-done (dpkg's own word).
HALF_PACKAGES=()
unrecord() { # drop one record line
	[ -f "$RECORD" ] || return 0
	grep -vxF -- "$*" "$RECORD" >"$RECORD.new" || true
	chmod 600 "$RECORD.new"
	mv -f -- "$RECORD.new" "$RECORD"
}
# shellcheck disable=SC2317,SC2329 # called from the EXIT trap
undo_early() {
	local added=() p dirs half=()
	for p in "${RUN_PACKAGES[@]}"; do installed "$p" && added+=("$p"); done
	for p in "${HALF_PACKAGES[@]}"; do
		case $(pkg_state "$p") in n | c) unrecord "package $p" ;; *) half+=("$p") ;; esac
	done
	if [ "${#added[@]}" -gt 0 ]; then
		say "Removing what this run installed: ${added[*]}"
		local sim others
		sim=$("${APT_ENV[@]}" apt-get -s remove --purge "${added[@]}" </dev/null 2>/dev/null) || sim="Remv ? (apt could not simulate the removal)"
		others=$(printf '%s\n' "$sim" | sed -n 's/^Remv \([^ ]*\).*/\1/p' | sed 's/:'"$ARCH"'$//' | sort -u | comm -23 - <(printf '%s\n' "${added[@]}" | sort -u))
		if [ -n "$others" ]; then
			warn "removing them would also remove other packages: they are left installed (${added[*]}); uninstall.sh --remove-packages lists them"
			return 1
		fi
		# the folders under /etc that these packages own, to remove once empty
		dirs=$(dpkg -L "${added[@]}" 2>/dev/null | grep '^/etc/' | sort -ru || true)
		if ! apt_get remove --purge "${added[@]}" >/dev/null; then warn "apt could not remove ${added[*]}"; return 1; fi
		for p in "${added[@]}"; do unrecord "package $p"; done
		# empty folders the purged packages leave under /etc (/etc/ssl), if
		# they were not there before the first run
		if [ -n "$dirs" ] && [ -f "$STATE_DIR/etc-dirs-before" ]; then
			printf '%s\n' "$dirs" | while IFS= read -r p; do
				if [ -d "$p" ] && ! grep -qxF -- "$p" "$STATE_DIR/etc-dirs-before"; then rmdir -- "$p" 2>/dev/null || true; fi
			done
		fi
	fi
	if [ "${#half[@]}" -gt 0 ]; then
		warn "the failed apt transaction left these packages half-installed (dpkg): ${half[*]}. They are NOT removed and stay in $RECORD: repair dpkg (sudo dpkg --configure -a), then uninstall.sh --remove-packages removes them, or run install.sh again."
		rm -f -- "$STATE"
	elif [ -s "$RECORD" ]; then
		warn "an earlier, interrupted run installed what $RECORD lists: it is kept (uninstall.sh --remove-packages removes it)"
		rm -f -- "$STATE"
	else
		rm -rf -- "$STATE_DIR"
		say "Nothing is left of this run."
	fi
}
# shellcheck disable=SC2317,SC2329 # the EXIT trap
on_failure() {
	local rc=$?
	trap - ERR
	[ "$rc" -ne 0 ] || return 0
	if [ "$DRY_RUN" = 1 ]; then return 0; fi
	local p half=()
	for p in "${HALF_PACKAGES[@]}"; do case $(pkg_state "$p") in n | c | i) ;; *) half+=("$p") ;; esac; done
	# (the early undo names them itself)
	if [ "${#half[@]}" -gt 0 ] && ! { { [ "$PHASE" = tools ] || [ "$PHASE" = release ]; } && [ "$(state_get reached)" != packages ]; }; then
		warn "the failed apt transaction left these packages half-installed (dpkg): ${half[*]}. Repair dpkg first (sudo dpkg --configure -a)."
	fi
	if [ "$PHASE" = migrating ]; then
		printf '\nThe migration stopped (exit %s).\n' "$rc" >&2
		if migrate_rollback; then
			printf 'Nothing of the migration is left: upstream'"'"'s Cosmos runs as before. See the reason above.\n' >&2
			cleanup
			exit "$EXIT_ROLLED_BACK"
		fi
		cleanup
		exit "$EXIT_FAILED"
	fi
	if [ "$SCRIPT_ROLE" = migrate ] && [ "$PHASE" != check ] && [ "$PHASE" != "done" ] &&
		! { { [ "$PHASE" = tools ] || [ "$PHASE" = release ]; } && [ "$(state_get reached)" != packages ]; }; then
		printf '\nThe migration stopped (exit %s) before Cosmos was touched: upstream'"'"'s Cosmos runs as before.\n' "$rc" >&2
		if [ -s "$RECORD" ]; then printf 'Installed by this script so far (they stay; run migrate.sh again to continue):\n' >&2; sed 's/^/  /' "$RECORD" >&2; fi
		return 0
	fi
	case $PHASE in
	check | done) ;; # done: installed (exit 16 says what protection is missing)
	tools | release)
		if [ "$(state_get reached)" != packages ]; then undo_early || true; return 0; fi
		printf '\nThe installation stopped (exit %s) before the release was unpacked. Already installed by an earlier run:\n' "$rc" >&2
		if [ -f "$RECORD" ]; then sed 's/^/  /' "$RECORD" >&2; fi
		printf 'Run install.sh again with the same options to continue, or remove them with uninstall.sh --remove-packages (from the same release).\n' >&2 ;;
	*)
		printf '\nThe installation stopped (exit %s) after these changes:\n' "$rc" >&2
		if [ -f "$RECORD" ]; then sed 's/^/  /' "$RECORD" >&2; fi
		printf 'Run install.sh again with the same options to continue, or remove all of it with:\n  sudo bash %s\n' \
			"$( [ -f "$INSTALL_DIR/fork/install/uninstall.sh" ] && echo "$INSTALL_DIR/fork/install/uninstall.sh --remove-packages" || echo "uninstall.sh --remove-packages (from the same release)")" >&2
		;;
	esac
}
trap 'on_failure; cleanup' EXIT

# A migration interrupted after the swap: start and check the fork
if [ "$MIG_RESUME" = finish ]; then
	VERSION=$(state_get version)
	[[ $VERSION =~ ^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}-fork\.[1-9][0-9]{0,8}$ ]] || die $EXIT_FAILED "the state of the interrupted migration names no fork version: run migrate.sh --undo"
	migrate_start_and_check
fi

start_state() {
	[ "$DRY_RUN" = 1 ] && return 0
	if [ ! -d "$STATE_DIR" ]; then
		mkdir -m 700 "$STATE_DIR"
	fi
	if [ ! -f "$STATE_DIR/etc-dirs-before" ]; then
		find /etc -xdev -type d 2>/dev/null | sort >"$STATE_DIR/etc-dirs-before.new"
		mv "$STATE_DIR/etc-dirs-before.new" "$STATE_DIR/etc-dirs-before"
	fi
	[ -f "$STATE" ] || state_set started-at "$(date -u +%FT%TZ)"
	state_set role "$SCRIPT_ROLE"
	state_set http-port "$HTTP_PORT"
	state_set https-port "$HTTPS_PORT"
	if [ "$REUSE_DATA" = 1 ]; then state_set reuse-data yes; fi
}

# dpkg left half-done, by the interruption of an earlier run of this script
# (or by anything else): only when dpkg says so, and saying what it does.
# A package that another program is installing right now is not
# "unfinished": while dpkg is locked, this waits (APT_WAIT) and looks again.
repair_dpkg() {
	local pending waited=0
	[ "$RESUMING" = 1 ] || return 0
	while :; do
		pending=$(dpkg --audit 2>/dev/null || true)
		[ -n "$pending" ] || return 0
		# dpkg's own locks, as the kernel lists them (lslocks, util-linux)
		if ! has_line '^/var/lib/dpkg/lock(-frontend)?$' "$(lslocks -n -o PATH 2>/dev/null || true)"; then break; fi
		[ "$waited" -lt "$APT_WAIT" ] || die $EXIT_FAILED "another program holds dpkg for longer than $APT_WAIT seconds"
		if [ $((waited % 60)) = 0 ]; then
			local who
			who=$(apt_holders)
			say "Another program is using dpkg${who:+: $who}; waiting for it before looking for unfinished packages (${waited} of at most $APT_WAIT seconds)..."
		fi
		sleep 5
		waited=$((waited + 5))
	done
	say "The package database has unfinished packages (dpkg --audit):"
	printf '%s\n' "$pending" | head -n 20 | sed 's/^/  /'
	say "Finishing ALL of them, not only those of the interrupted run (dpkg --configure -a; apt-get -f install):"
	local rc=0
	apt_run "$APT_TIMEOUT" "${APT_ENV[@]}" dpkg --force-confdef --force-confold --configure -a </dev/null || rc=$?
	if apt_timed_out "dpkg --configure -a" "$rc"; then die $EXIT_FAILED "the package database is still unfinished"; fi
	[ "$rc" = 0 ] || die $EXIT_FAILED "dpkg --configure -a failed: finish the packages above by hand, then run install.sh again"
	apt_get -f install >/dev/null || die $EXIT_FAILED "apt-get -f install failed: repair the package database by hand, then run install.sh again"
}

# install_packages <packages>: installs those missing, in one transaction.
# What the record gets is apt's own answer for exactly this command (the
# packages it would newly install: "Inst" lines without an installed
# version), written BEFORE the transaction runs (so an interruption leaves
# them named) and confirmed after it: a planned package that this
# transaction did not newly install (someone else's) is taken off again.
install_packages() {
	local missing planned rc=0 p
	mapfile -t missing < <(missing_of "$@")
	[ "${#missing[@]}" -gt 0 ] || return 0
	state_set step "packages"
	planned=$("${APT_ENV[@]}" apt-get -s -q install --no-install-recommends "${missing[@]}" </dev/null 2>/dev/null |
		awk '$1=="Inst" && $3 !~ /^\[/ {print $2}' | sed 's/:'"$ARCH"'$//' | sort -u) ||
		die $EXIT_FAILED "apt cannot install ${missing[*]} (it could a moment ago): nothing more was changed"
	present_now >"$WORK/before-transaction"
	for p in $planned; do record "package $p"; done
	local hist=/var/log/apt/history.log hist_at=0
	[ ! -f "$hist" ] || hist_at=$(stat -c %s -- "$hist")
	say "Installing: ${missing[*]}"
	apt_get install --no-install-recommends "${missing[@]}" || rc=$?
	# The truth for exactly the packages apt planned. Another apt may have
	# installed some of them while ours waited for the lock: apt's own
	# history names what OUR transaction installed (its entry: our command
	# line), and only those are ours. Then dpkg's status of each: installed,
	# or left half-done. Without that entry (no history, or apt stopped before
	# it began), a package that was there before is not ours.
	local state ours="" entry
	if [ -f "$hist" ] && [ "$(stat -c %s -- "$hist")" -ge "$hist_at" ]; then
		entry=$(tail -c +"$((hist_at + 1))" -- "$hist" | awk -v want=" install --no-install-recommends ${missing[*]}" '
			/^Start-Date:/ { cmd = ""; inst = "" }
			/^Commandline:/ { cmd = $0 }
			/^Install:/ { inst = $0 }
			/^End-Date:/ || /^$/ { if (cmd != "" && substr(cmd, length(cmd) - length(want) + 1) == want) found = "E:" inst }
			END { if (cmd != "" && substr(cmd, length(cmd) - length(want) + 1) == want) found = "E:" inst; print found }')
		if [ -n "$entry" ]; then
			entry=${entry#E:}
			ours=$(printf '%s\n' "${entry#Install: }" | sed 's/), /)\n/g' | sed 's/[ :(].*//' | grep . | sort -u || true)
			[ -n "$ours" ] || ours="-" # our transaction installed nothing new
		fi
	fi
	for p in $planned; do
		state=$(pkg_state "$p")
		if [ -n "$ours" ] && ! grep -qxF -- "$p" <<<"$ours"; then
			unrecord "package $p" # installed by another program while ours waited, or not at all
		elif grep -qxF -- "$p" "$WORK/before-transaction" || [ "$state" = n ] || [ "$state" = c ]; then
			unrecord "package $p"
		elif [ "$state" = i ]; then
			RUN_PACKAGES+=("$p")
		else
			HALF_PACKAGES+=("$p") # stays recorded
		fi
	done
	[ "$rc" = 0 ] || die $EXIT_FAILED "apt could not install ${missing[*]} (apt-get ended with $rc)"
}

# ---- 4. The download tools -------------------------------------------------
if [ "$DRY_RUN" = 0 ]; then
	start_state
	repair_dpkg
	mapfile -t need_tools < <(missing_of "${TOOLS[@]}")
	if [ "${#need_tools[@]}" -gt 0 ]; then
		PHASE=tools
		step "Installing the tools for the download and the check: ${need_tools[*]}"
		apt_get update >/dev/null || die $EXIT_PREREQ "apt could not read the package lists"
		install_packages "${TOOLS[@]}"
	fi
fi
PHASE=release

# ---- 5. The release, verified before anything is unpacked -------------------
# fetch <url> <file> <max bytes>: HTTPS only, to github.com and GitHub's
# download hosts only, redirects followed by hand so each is checked.
allowed_url() {
	[[ $1 =~ ^https://([a-z0-9.-]{1,253})(/[A-Za-z0-9._~%/?=\&+:,;@!*\'()-]*)?$ ]] || return 1
	case ${BASH_REMATCH[1]} in
	github.com | *.githubusercontent.com) return 0 ;;
	esac
	return 1
}
# (SAFE_PATH, CURL and OSSL: set at the start, after the options)
# said once, when a download fails: the shell's proxy is not what is used
PROXY_TOLD=0
proxy_hint() {
	[ "$PROXY_TOLD" = 0 ] || return 0
	PROXY_TOLD=1
	if [ -n "$PROXY" ]; then
		warn "the downloads went through the proxy $PROXY (--proxy): check that it lets HTTPS to github.com through"
	elif [ -n "${https_proxy:-}${HTTPS_PROXY:-}${http_proxy:-}${HTTP_PROXY:-}${all_proxy:-}${ALL_PROXY:-}" ]; then
		warn "your shell sets a proxy, which install.sh does NOT read (sudo and a hostile environment are not trusted): if GitHub is reached through it, run install.sh again with --proxy http://<host>:<port>"
	else
		warn "if this server reaches GitHub through a proxy, give it with --proxy http://<host>:<port>"
	fi
}
fetch() {
	local url=$1 out=$2 max=$3 hops=0 result code location
	while :; do
		allowed_url "$url" || { warn "refusing to download from $(printf '%s' "$url" | cut -c1-120 | tr -cd '[:print:]')"; return 1; }
		result=$("${CURL[@]}" -sS --proto '=https' --tlsv1.2 --max-redirs 0 --connect-timeout 20 --max-time 3600 \
			--speed-limit 1024 --speed-time 60 --max-filesize "$max" --retry 2 --retry-connrefused \
			-o "$out" -w '%{http_code} %{redirect_url}' -- "$url") || { proxy_hint; return 1; }
		code=${result%% *}
		location=${result#* }
		case $code in
		200) [ "$(stat -c %s -- "$out")" -le "$max" ] || return 1; return 0 ;;
		301 | 302 | 303 | 307 | 308)
			hops=$((hops + 1))
			[ "$hops" -le 5 ] || return 1
			url=$location ;;
		404) warn "$(printf '%s' "$url" | cut -c1-160) is not there (HTTP 404)"; return 2 ;;
		*) warn "$(printf '%s' "$url" | cut -c1-160) answered HTTP $code"; return 1 ;;
		esac
	done
}
# The newest release, without GitHub's API: where releases/latest/download/
# sends the request for install.sh says the version.
latest_version() {
	local result location
	result=$("${CURL[@]}" -sS --proto '=https' --tlsv1.2 --max-redirs 0 --connect-timeout 20 --max-time 60 -o /dev/null \
		-w '%{http_code} %{redirect_url}' -- "https://github.com/$RELEASE_REPOSITORY/releases/latest/download/install.sh") || { proxy_hint; return 1; }
	location=${result#* }
	[[ $location =~ ^https://github\.com/${RELEASE_REPOSITORY//./\\.}/releases/download/v((0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})-fork\.[1-9][0-9]{0,8})/install\.sh$ ]] || return 1
	printf '%s\n' "${BASH_REMATCH[1]}"
}

# BEGIN verify_signed (a Go test feeds this and src/forkrelease the same files)
# verify_signed <file> <header> <out: the signed lines>: the form of the
# file exactly as src/forkrelease/manifest.go (parseSigned) reads it, and
# one signature by one of the release keys of this script. For the
# release's file (cosmos-fork-release-v1) the names are the version's two
# zips; for the scripts' (cosmos-fork-scripts-v1) the scripts' names.
verify_signed() {
	local file=$1 header=$2 out=$3 n lines signed=0 key s=0 line version names name size
	[ -f "$file" ] && [ ! -L "$file" ] || return 1
	size=$(stat -c %s -- "$file")
	[ "$size" -le 4096 ] && [ "$size" -gt 0 ] || return 1
	[ "$(tail -c 1 -- "$file" | od -An -tx1 | tr -d ' ')" = 0a ] || return 1
	! tr -d '\n' <"$file" | LC_ALL=C grep -q '[^ -~]' || return 1
	lines=$(wc -l <"$file")
	[ "$lines" -ge 4 ] && [ "$lines" -le 14 ] || return 1
	[ "$(sed -n 1p "$file")" = "$header" ] || return 1
	[[ $(sed -n 2p "$file") =~ ^version\ ((0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})-fork\.[1-9][0-9]{0,8})$ ]] || return 1
	version=${BASH_REMATCH[1]}
	case $header in
	cosmos-fork-release-v1) names=" cosmos-cloud-$version-amd64.zip cosmos-cloud-$version-arm64.zip " ;;
	cosmos-fork-scripts-v1) names=" install.sh uninstall.sh migrate.sh " ;;
	*) return 1 ;;
	esac
	n=2
	while [ "$n" -lt "$lines" ] && [[ $(sed -n "$((n + 1))p" "$file") =~ ^sha256\ [0-9a-f]{64}\ ([^ ]+)$ ]]; do
		name=${BASH_REMATCH[1]}
		[[ $names == *" $name "* ]] || return 1
		n=$((n + 1))
	done
	[ "$n" -gt 2 ] && [ "$((n - 2))" -le 8 ] || return 1
	# a name listed twice is refused
	[ -z "$(sed -n "3,${n}p" "$file" | awk '{print $3}' | sort | uniq -d)" ] || return 1
	head -n "$n" -- "$file" >"$out"
	mkdir -p "$WORK/sig"
	rm -f "$WORK"/sig/*
	while IFS= read -r line; do
		[[ $line =~ ^signature\ ([A-Za-z0-9+/]{86}==)$ ]] || return 1
		s=$((s + 1))
		[ "$s" -le 4 ] || return 1
		printf '%s' "${BASH_REMATCH[1]}" | base64 -d >"$WORK/sig/s.$s" 2>/dev/null || return 1
		[ "$(stat -c %s "$WORK/sig/s.$s")" = 64 ] || return 1
		# canonical base64 only, as Go's StdEncoding.Strict()
		[ "$(base64 -w0 <"$WORK/sig/s.$s")" = "${BASH_REMATCH[1]}" ] || return 1
	done < <(tail -n +"$((n + 1))" -- "$file")
	[ "$s" -ge 1 ] || return 1
	for key in $RELEASE_KEYS; do
		{ printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'; printf '%s' "$key" | base64 -d; } >"$WORK/sig/key.der"
		"${OSSL[@]}" pkey -pubin -inform DER -in "$WORK/sig/key.der" -out "$WORK/sig/key.pem" 2>/dev/null || continue
		for line in "$WORK"/sig/s.*; do
			if "${OSSL[@]}" pkeyutl -verify -pubin -inkey "$WORK/sig/key.pem" -rawin -in "$out" -sigfile "$line" >/dev/null 2>&1; then
				signed=1
				break 2
			fi
		done
	done
	[ "$signed" = 1 ]
}
# END verify_signed
# openssl must be able to verify Ed25519 at all: a check that cannot be made
# stops the run (there is no way to install unverified).
check_openssl() {
	"${OSSL[@]}" genpkey -algorithm ed25519 -out "$WORK/probe.pem" >/dev/null 2>&1 &&
		printf 'probe' >"$WORK/probe.txt" &&
		"${OSSL[@]}" pkeyutl -sign -inkey "$WORK/probe.pem" -rawin -in "$WORK/probe.txt" -out "$WORK/probe.sig" >/dev/null 2>&1 &&
		"${OSSL[@]}" pkey -in "$WORK/probe.pem" -pubout -out "$WORK/probe.pub" >/dev/null 2>&1 &&
		"${OSSL[@]}" pkeyutl -verify -pubin -inkey "$WORK/probe.pub" -rawin -in "$WORK/probe.txt" -sigfile "$WORK/probe.sig" >/dev/null 2>&1
	local rc=$?
	rm -f "$WORK"/probe.*
	return $rc
}
refuse() { # message: the release is refused; nothing was unpacked
	printf 'ERROR: %s\nThe release was NOT installed: nothing of it was unpacked.\n' "$*" >&2
	exit $EXIT_UNVERIFIED
}

# The zip's entries, before unzip runs: unzip's own protections (it drops
# "../" and a leading "/") are not relied on. Every entry must be a plain
# file or folder under the one top folder, with a plain name; links of any
# kind are refused; the unpacked size is bounded.
check_zip_entries() { # zip, top folder
	local zip=$1 top=$2 names types total
	names=$(unzip -Z1 -- "$zip" 2>/dev/null) || return 1
	[ -n "$names" ] || return 1
	local odd
	odd=$(grep -vE "^${top//./\\.}/([A-Za-z0-9._+@(),~-]+/)*[A-Za-z0-9._+@(),~-]*$" <<<"$names" || true)
	if [ -n "$odd" ]; then
		warn "the zip holds an entry outside $top/ or with an unusual name:"
		head -n 3 <<<"$odd" | tr -cd '[:print:]\n' >&2
		return 1
	fi
	if has_line '(^|/)\.\.?(/|$)' "$names"; then warn "the zip holds a '..' or '.' entry"; return 1; fi
	# a name twice: unzip would ask what to do (or overwrite)
	if [ -n "$(printf '%s\n' "$names" | sort | uniq -d)" ]; then warn "the zip holds an entry twice"; return 1; fi
	# zipinfo's long listing: the first column is the type and mode (unix
	# entries); anything but a plain file "-" or a folder "d" is refused, and
	# so are setuid, setgid and sticky bits and entries anyone may write.
	types=$(unzip -Z -- "$zip" 2>/dev/null | sed -n '3,$p' | sed '$d' | cut -c1-10) || return 1
	if [ -n "$(cut -c1 <<<"$types" | grep -v '^[-d]$' || true)" ]; then warn "the zip holds a link or a special file"; return 1; fi
	if has_line '^.{1,9}[sStT]|^.{8}w' "$types"; then warn "the zip holds an entry with setuid, setgid or sticky bits, or one anyone may write"; return 1; fi
	[ "$(printf '%s\n' "$types" | wc -l)" = "$(printf '%s\n' "$names" | wc -l)" ] || return 1
	total=$(unzip -Zt -- "$zip" 2>/dev/null | sed -n 's/.* \([0-9]\{1,15\}\) bytes uncompressed.*/\1/p')
	if ! { [ -n "$total" ] && [ "$total" -le "$MAX_UNPACKED" ]; }; then warn "the zip unpacks to more than allowed"; return 1; fi
}

if [ "$DRY_RUN" = 1 ] && [ -n "$(missing_of "${TOOLS[@]}")" ]; then
	step "Dry run: this is what a real run would do"
	say "- install the tools for the download first: $(missing_of "${TOOLS[@]}" | tr '\n' ' ')"
	say "- then download and verify the release, and check Docker's repository (not possible in a dry run before those tools are there)"
	say "- install the packages: ${MISSING[*]:-(none)}"
	[ "$DOCKER_PLAN" != install ] || say "- add Docker's repository and install: ${DOCKER_PACKAGES[*]}"
	if [ "$SCRIPT_ROLE" = migrate ]; then
		say "- note how Cosmos answers, stop it, back up $INSTALL_DIR, $DATA_DIR and the unit to $MIG_BACKUP (checked file by file)"
		say "- put the release in $INSTALL_DIR (start.sh, the unit and the data stay), start it, check that it answers as before; put everything back on any failure"
		say "Nothing was changed."
		exit 0
	fi
	say "- unpack the release to $INSTALL_DIR, data in $DATA_DIR, write and start $UNIT_FILE (ports $HTTP_PORT and $HTTPS_PORT)"
	for p in $(chosen_pieces); do say "- run fork/$p/install.sh --yes, if the release's piece speaks the installer's interface (its packages are checked once the release is verified)"; done
	say "Nothing was changed."
	exit 0
fi
check_openssl || refuse "the openssl here cannot verify an Ed25519 signature, so the release cannot be checked (OpenSSL 3 is needed)"

# Forward only: never a release older than this script's own (a stale
# "latest", a hand-picked --version, an old zip), unless --allow-older.
build_of() { printf '%s' "${1##*-fork.}"; }
not_older() { # version
	if [ "$(build_of "$1")" -lt "$(build_of "$RELEASE_VERSION")" ] && [ "$ALLOW_OLDER" = 0 ]; then
		printf 'ERROR: %s is older than this script (%s): the installer goes forward only.\nAdd --allow-older to install it anyway (it is still verified like any other).\n' "$1" "$RELEASE_VERSION" >&2
		exit $EXIT_OLDER
	fi
}
ZIP="" SIG="" MD5=""
if [ -n "$FROM" ]; then
	step "Taking the release from $FROM"
	if [ -d "$FROM" ]; then
		zips=("$FROM"/cosmos-cloud-*-"$ARCH".zip)
		[ "${#zips[@]}" = 1 ] || refuse "$FROM must hold exactly one cosmos-cloud-<version>-$ARCH.zip (found ${#zips[@]})"
		ZIP=${zips[0]}
	else
		ZIP=$FROM
	fi
	if ! { [ -f "$ZIP" ] && [ ! -L "$ZIP" ]; }; then refuse "$ZIP is not a file"; fi
	zipname=$(basename -- "$ZIP")
	[[ $zipname =~ ^cosmos-cloud-((0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})-fork\.[1-9][0-9]{0,8})-(amd64|arm64)\.zip$ ]] ||
		refuse "$zipname is not the name of a release zip of the fork (cosmos-cloud-<version>-<arch>.zip)"
	VERSION=${BASH_REMATCH[1]}
	[ "${BASH_REMATCH[5]}" = "$ARCH" ] || refuse "$zipname is for ${BASH_REMATCH[5]}, this machine is $ARCH"
	not_older "$VERSION"
	SIG=$(dirname -- "$ZIP")/cosmos-cloud-$VERSION.sig
	if ! { [ -f "$SIG" ] && [ ! -L "$SIG" ]; }; then refuse "the signature file cosmos-cloud-$VERSION.sig is not next to the zip"; fi
	# sizes first: a huge file is not copied at all
	[ "$(stat -c %s -- "$ZIP")" -le "$MAX_ZIP" ] || refuse "$zipname is larger than a release can be (1 GiB)"
	[ "$(stat -c %s -- "$SIG")" -le 4096 ] || refuse "cosmos-cloud-$VERSION.sig is larger than a signature file can be"
	need_space /var/tmp $(( $(stat -c %s -- "$ZIP") / 1024 + 10240 )) "the copy of the release"
	cp -- "$ZIP" "$WORK/release.zip"
	cp -- "$SIG" "$WORK/release.sig"
	if [ -f "$ZIP.md5" ] && [ ! -L "$ZIP.md5" ] && [ "$(stat -c %s -- "$ZIP.md5")" -le 1024 ]; then cp -- "$ZIP.md5" "$WORK/release.md5"; MD5="$WORK/release.md5"; fi
	ZIP="$WORK/release.zip" SIG="$WORK/release.sig"
else
	step "Finding the release on GitHub ($RELEASE_REPOSITORY)"
	if [ -n "$WANT_VERSION" ]; then
		VERSION=$WANT_VERSION
	else
		VERSION=$(latest_version) || die $EXIT_FAILED "could not find the newest release at https://github.com/$RELEASE_REPOSITORY/releases (network?)"
	fi
	not_older "$VERSION"
	if [ "$VERSION" != "$RELEASE_VERSION" ] && [ "${COSMOS_INSTALL_HOPPED:-}" != "$VERSION" ]; then
		# Another release: its own install.sh installs it, after its signed
		# file of scripts verified under this script's keys.
		say "This script belongs to $RELEASE_VERSION; $VERSION is asked for: taking that release's own $SELF."
		base="https://github.com/$RELEASE_REPOSITORY/releases/download/v$VERSION"
		mkdir -p "$WORK/next"
		fetch "$base/$SELF" "$WORK/next/$SELF" 1048576 || die $EXIT_FAILED "could not download $SELF of $VERSION (a release from before $SELF existed has none)"
		fetch "$base/cosmos-cloud-$VERSION.scripts.sig" "$WORK/next/scripts.sig" 4096 ||
			refuse "release $VERSION has no signed file of scripts (cosmos-cloud-$VERSION.scripts.sig); releases before install.sh existed cannot be installed by script"
		verify_signed "$WORK/next/scripts.sig" cosmos-fork-scripts-v1 "$WORK/next/signed" ||
			refuse "the scripts of $VERSION are not signed with a release key this script trusts"
		[ "$(sed -n 2p "$WORK/next/signed")" = "version $VERSION" ] || refuse "the signed file of scripts is for another version"
		want=$(awk -v n="$SELF" '$1=="sha256" && $3==n{print $2}' "$WORK/next/signed")
		if ! { [ -n "$want" ] && [ "$(sha256sum "$WORK/next/$SELF" | awk '{print $1}')" = "$want" ]; }; then refuse "$SELF of $VERSION does not match its signed SHA-256 (or its signed file does not list it)"; fi
		hop=$(mktemp -d /var/tmp/cosmos-install-next.XXXXXXXX)
		cp "$WORK/next/$SELF" "$hop/$SELF"
		trap - EXIT
		cleanup
		export COSMOS_INSTALL_HOPPED=$VERSION
		args=()
		skip=0
		for a in "${ORIGINAL_ARGS[@]}"; do
			if [ "$skip" = 1 ]; then skip=0; continue; fi
			if [ "$a" = --version ]; then skip=1; continue; fi
			args+=("$a")
		done
		exec bash "$hop/$SELF" "${args[@]}" --version "$VERSION"
	fi
	[ "$VERSION" = "$RELEASE_VERSION" ] || die $EXIT_FAILED "$SELF of $VERSION says it is $RELEASE_VERSION"
	base="https://github.com/$RELEASE_REPOSITORY/releases/download/v$VERSION"
	zipname=cosmos-cloud-$VERSION-$ARCH.zip
	say "Downloading $zipname ..."
	rc=0
	fetch "$base/cosmos-cloud-$VERSION.sig" "$WORK/release.sig" 4096 || rc=$?
	if [ "$rc" = 2 ]; then refuse "release $VERSION has no signature file (cosmos-cloud-$VERSION.sig): it is not installed unverified"; fi
	[ "$rc" = 0 ] || die $EXIT_FAILED "could not download the signature file of $VERSION (network?)"
	fetch "$base/$zipname.md5" "$WORK/release.md5" 1024 || die $EXIT_FAILED "could not download $zipname.md5"
	fetch "$base/$zipname" "$WORK/release.zip" "$MAX_ZIP" || die $EXIT_FAILED "the download of $zipname failed (network cut?). Run $SELF again."
	ZIP="$WORK/release.zip" SIG="$WORK/release.sig" MD5="$WORK/release.md5"
fi

step "Verifying $zipname"
verify_signed "$SIG" cosmos-fork-release-v1 "$WORK/signed" ||
	refuse "cosmos-cloud-$VERSION.sig is not signed with a release key of this script, or is not in the form of a signature file"
[ "$(sed -n 2p "$WORK/signed")" = "version $VERSION" ] || refuse "the signature file is for another version"
want=$(awk -v n="$zipname" '$1=="sha256" && $3==n{print $2}' "$WORK/signed")
[ -n "$want" ] || refuse "the signature file does not list $zipname"
[ "$(sha256sum -- "$ZIP" | awk '{print $1}')" = "$want" ] || refuse "$zipname does not have the SHA-256 its signature lists: it was damaged or changed"
say "Signature: good (Ed25519, a release key of this script); SHA-256: as signed."
if [ -n "$MD5" ]; then
	[ "$(awk 'NR==1{print $1}' "$MD5")" = "$(md5sum -- "$ZIP" | awk '{print $1}')" ] || refuse "$zipname does not match its .md5"
	say "MD5: as published."
else
	say "MD5: no .md5 next to the zip (the signed SHA-256 has checked it already)."
fi
TOP=cosmos-cloud-$VERSION
[ "$ARCH" = amd64 ] || TOP=$TOP-arm64
check_zip_entries "$ZIP" "$TOP" || refuse "$zipname holds entries that are not allowed (it is signed, which is stranger still)"
say "Entries: $(unzip -Z1 -- "$ZIP" | wc -l), all plain files and folders under $TOP/."

# Unpacked first into a private folder on the same disk as $INSTALL_DIR.
STAGING=$(mktemp -d /opt/.cosmos-install.XXXXXXXX)
# -n: never overwrite (no name is there twice, checked above); stdin closed:
# unzip can never ask a question
unzip -qq -n -- "$ZIP" -d "$STAGING" </dev/null || refuse "unzip failed"
NEW=$STAGING/$TOP
for f in cosmos cosmos-launcher start.sh meta.json; do
	if ! { [ -f "$NEW/$f" ] && [ ! -L "$NEW/$f" ]; }; then refuse "the release lacks $f"; fi
done
grep -q "\"version\": *\"$VERSION\"" "$NEW/meta.json" || refuse "meta.json of the zip names another version"
chmod 755 "$NEW/cosmos" "$NEW/cosmos-launcher" "$NEW/start.sh"
for f in restic nebula nebula-cert; do [ ! -f "$NEW/$f" ] || chmod 755 "$NEW/$f"; done
if [ -f "$NEW/fork/install/$SELF" ]; then
	if cmp -s -- "$0" "$NEW/fork/install/$SELF"; then
		say "This $SELF is the one inside the signed release."
	else
		say "Note: this $SELF differs from the one inside the release (the release's copy is in $INSTALL_DIR/fork/install/)."
	fi
fi
# migrate.sh: the release's own base, which a zip of another version may have
if [ "$SCRIPT_ROLE" = migrate ]; then
	zip_base=$(sed -n 's/.*"upstream"[[:space:]]*:[[:space:]]*"\([0-9.]\{5,20\}\)".*/\1/p' "$NEW/meta.json" | head -n 1)
	[[ $zip_base =~ ^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$ ]] || refuse "meta.json of the release does not say which upstream version it is built on"
	base_allows "$MIG_FROM" "$MIG_UNSTABLE" "$zip_base"
fi

# What the release's binary sees of the network (it changes nothing).
DETECT="$WORK/detect.json"
: >"$DETECT"
detect_args=(--timeout 10)
# a dry run tells no one: never an outside service
if [ "$(answer outside-lookup)" != yes ] || [ "$DRY_RUN" = 1 ]; then detect_args+=(--no-outside); fi
if [ "$SCRIPT_ROLE" = install ]; then
	if timeout 30 env -i PATH="$SAFE_PATH" HOME="$WORK" RCLONE_CONFIG="$WORK/none.conf" "$NEW/cosmos" fork-detect "${detect_args[@]}" >"$DETECT" 2>/dev/null; then :; else : >"$DETECT"; fi
fi
json_string() { # key in the network part (plain values only)
	sed -n "/\"network\"/,/}/s/.*\"$1\": \"\\([0-9a-zA-Z.:/_-]*\\)\".*/\\1/p" "$DETECT" | head -n 1
}
LAN_ADDRESS=$(json_string address)
if [ "$SCRIPT_ROLE" = migrate ]; then :
elif grep -q '"answers": true' "$DETECT"; then
	say "Router: answers UPnP ($(sed -n 's/.*"name": "\([^"\\]\{0,64\}\)".*/\1/p' "$DETECT" | head -n 1 | tr -cd '[:print:]'))."
else
	say "Router: does not answer UPnP (or none was found)."
fi
if grep -q '"asked": true' "$DETECT"; then say "The router gave no public address, so an outside service was asked for it (--no-outside forbids that)."; fi

# The pieces' own lists, then the last simulation (Docker's repository too).
PIECE_PACKAGES=()
for p in $(chosen_pieces); do
	# a piece that does not answer --print-packages will not be run (see
	# run_piece): nothing is installed for it
	if [ -f "$NEW/fork/$p/install.sh" ] && list=$(env -i PATH="$SAFE_PATH" sh "$NEW/fork/$p/install.sh" --print-packages </dev/null 2>/dev/null); then
		for q in $list; do if [[ $q =~ ^[a-z0-9][a-z0-9.+-]{0,60}$ ]]; then PIECE_PACKAGES+=("$q"); fi; done
	fi
done
mapfile -t MISSING < <({ printf '%s\n' "${BASE[@]}"; if [ "$(answer storage-tools)" = yes ]; then printf '%s\n' "${STORAGE[@]}"; fi; printf '%s\n' "${PIECE_PACKAGES[@]}"; } | sort -u | while read -r p; do [ -n "$p" ] && { installed "$p" || echo "$p"; }; done)
# python3 and nftables are the pieces' own tools: installed by this script;
# the rest of the pieces' packages are installed by the pieces themselves.
PREINSTALL=()
for q in "${MISSING[@]}"; do
	case " ${PIECE_PACKAGES[*]} " in
	*" $q "*) case $q in python3 | nftables) PREINSTALL+=("$q") ;; esac ;;
	*) PREINSTALL+=("$q") ;;
	esac
done
if [ "$DOCKER_PLAN" = install ]; then
	step "Checking Docker's repository (simulation)"
	simulate_docker
	say "Docker can be installed from download.docker.com ($DOCKER_DIST $DOCKER_SUITE)."
elif [ "${#MISSING[@]}" -gt 0 ]; then
	simulate "${MISSING[@]}"
fi

if [ "$DRY_RUN" = 1 ] && [ "$SCRIPT_ROLE" = migrate ]; then
	step "Dry run: this is what a real run would do"
	say "- install the packages: ${PREINSTALL[*]:-(none)}"
	say "- note how Cosmos answers (settings, its addresses, $(timeout 30 docker ps -aq 2>/dev/null | wc -l) containers), stop it"
	say "- back up $INSTALL_DIR ($(du -sh -- "$INSTALL_DIR" | cut -f1)), $DATA_DIR ($(du -sh -- "$DATA_DIR" | cut -f1)) and the unit to $MIG_BACKUP, checked file by file"
	say "- put $zipname (verified) in $INSTALL_DIR, keeping start.sh, the unit and the data; start it; check that it answers as before"
	say "- on any failure, put everything back and start upstream's Cosmos $FOUND_VERSION again"
	say "Nothing was changed."
	exit 0
fi
if [ "$DRY_RUN" = 1 ]; then
	step "Dry run: this is what a real run would do"
	say "- install the packages: ${PREINSTALL[*]:-(none)}"
	if [ "$DOCKER_PLAN" = install ]; then
		say "- add Docker's repository ($DOCKER_SOURCE, key $DOCKER_KEY, fingerprint $DOCKER_FINGERPRINT) and install: ${DOCKER_PACKAGES[*]}"
	else
		say "- use the Docker that is there"
	fi
	say "- unpack $zipname (verified) to $INSTALL_DIR, data in $DATA_DIR"
	say "- write $UNIT_FILE (ports $HTTP_PORT and $HTTPS_PORT), enable and start it"
	for p in $(chosen_pieces); do say "- run fork/$p/install.sh --yes"; done
	say "Nothing was changed."
	exit 0
fi

# ---- 6. Packages and Docker ---------------------------------------------------
PHASE=packages
state_set version "$VERSION"
state_set reached packages
if [ "${#PREINSTALL[@]}" -gt 0 ] || [ "$DOCKER_PLAN" = install ]; then
	step "Installing the packages Cosmos needs"
	if [ "$DOCKER_PLAN" = install ]; then
		if [ ! -d /etc/apt/keyrings ]; then
			install -m 0755 -d /etc/apt/keyrings
			record "dir /etc/apt/keyrings"
		fi
		if [ ! -f "$DOCKER_KEY" ] || recorded "file $DOCKER_KEY"; then
			record "file $DOCKER_KEY"
			write_docker_key "$DOCKER_KEY.new"
			chmod 644 "$DOCKER_KEY.new"
			mv -f "$DOCKER_KEY.new" "$DOCKER_KEY"
		fi
		if [ ! -f "$DOCKER_SOURCE" ] || recorded "file $DOCKER_SOURCE"; then
			if ! other_docker_source; then
				record "file $DOCKER_SOURCE"
				docker_source_text "$DOCKER_KEY" >"$DOCKER_SOURCE.new"
				chmod 644 "$DOCKER_SOURCE.new"
				mv -f "$DOCKER_SOURCE.new" "$DOCKER_SOURCE"
			else
				say "Docker's repository is already among the sources: it is used as it is."
			fi
		fi
	fi
	apt_get update >/dev/null || die $EXIT_FAILED "apt could not read the package lists"
	if [ "$DOCKER_PLAN" = install ]; then
		install_packages "${PREINSTALL[@]}" "${DOCKER_PACKAGES[@]}"
		systemctl enable --now docker.service >/dev/null 2>&1 || true
		[ -n "$(docker_answers)" ] || die $EXIT_FAILED "Docker was installed but does not answer (systemctl status docker)"
		say "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null | tr -cd 'A-Za-z0-9.+~-') is running."
	else
		install_packages "${PREINSTALL[@]}"
	fi
fi
# avahi-daemon (.local names) is started only when this script installed it:
# one the admin had, enabled or not, is left as it is
if recorded "package avahi-daemon"; then systemctl enable --now avahi-daemon.service >/dev/null 2>&1 || warn "avahi-daemon did not start (.local names will not work): systemctl status avahi-daemon"; fi

# migrate.sh: the observation, the stop, the backup, the swap, the checks
if [ "$SCRIPT_ROLE" = migrate ]; then migrate_run; fi

# ---- 7. Cosmos ------------------------------------------------------------------
PHASE=cosmos
state_set step "cosmos"
step "Installing Cosmos $VERSION"
if [ -e "$INSTALL_DIR" ]; then
	# only an interrupted run of this script gets here (checked at the start)
	if grep -q "\"version\": *\"$VERSION\"" "$INSTALL_DIR/meta.json" 2>/dev/null && [ -x "$INSTALL_DIR/start.sh" ]; then
		say "$INSTALL_DIR is already in place from the interrupted run."
	else
		die $EXIT_FAILED "$INSTALL_DIR is there but is not $VERSION: remove it with uninstall.sh and run install.sh again"
	fi
else
	record "dir $INSTALL_DIR"
	mv -- "$NEW" "$INSTALL_DIR"
	chmod 755 "$INSTALL_DIR"
	sync -f "$INSTALL_DIR" 2>/dev/null || true
fi
if [ ! -d "$DATA_DIR" ]; then
	record "data $DATA_DIR"
	mkdir -m 700 "$DATA_DIR"
fi

# The unit, as `cosmos service install` writes it (src/index.go), byte for
# byte, without its two defects: it is written here (that command exits with
# status 1 on success) and it carries only the ports, not whatever COSMOS_*
# variables the admin's shell holds.
write_unit() {
	printf '%s' "[Unit]
Description=Cosmos Cloud service
ConditionFileIsExecutable=$INSTALL_DIR/start.sh


[Service]
StartLimitInterval=10
StartLimitBurst=5
ExecStart=$INSTALL_DIR/start.sh

WorkingDirectory=$INSTALL_DIR





Restart=always

RestartSec=2
EnvironmentFile=-/etc/sysconfig/$UNIT

Environment=COSMOS_HTTPS_PORT=$HTTPS_PORT
Environment=COSMOS_HTTP_PORT=$HTTP_PORT
[Install]
WantedBy=multi-user.target"
}
if [ ! -e "$UNIT_FILE" ] || recorded "unit $UNIT.service"; then
	record "unit $UNIT.service"
	write_unit >"$UNIT_FILE.new"
	chmod 644 "$UNIT_FILE.new"
	mv -f "$UNIT_FILE.new" "$UNIT_FILE"
fi
systemctl daemon-reload
systemctl enable "$UNIT.service" >/dev/null 2>&1
# Where the first-run wizard answers (src/fork_setup_code.go): the setup
# code and the administrator's password cross the network in clear HTTP
# until the wizard has set HTTPS up. Only at home, on the home network, is
# that the owner's own wire; anywhere else the wizard answers on 127.0.0.1
# only, reached through an SSH tunnel, unless --wizard-on-lan.
public_ipv4() { # a public IPv4 address on an interface (not Docker's), or nothing
	command -v ip >/dev/null || return 0
	ip -4 -o addr show scope global 2>/dev/null | awk '$2 !~ /^(docker|br-|veth|virbr|nebula)/ {sub(/\/.*/, "", $4); print $4}' |
		awk -F. '!($1==10 || ($1==172 && $2>=16 && $2<=31) || ($1==192 && $2==168) || ($1==100 && $2>=64 && $2<=127) || ($1==169 && $2==254)) {print; exit}'
}
# A global IPv6 address (not a private fd00::/8 one): whether the router lets
# it be reached from outside cannot be told from here, and the wizard would
# answer on it too (the server listens on every address). So at home with
# one, the wizard answers on 127.0.0.1 as well: the tunnel always works, and
# --wizard-on-lan is there for a network the owner trusts.
global_ipv6() {
	command -v ip >/dev/null || return 0
	ip -6 -o addr show scope global 2>/dev/null | awk '$2 !~ /^(docker|br-|veth|virbr|nebula)/ {sub(/\/.*/, "", $4); print $4}' |
		awk 'tolower($0) !~ /^f[cd]/ {print; exit}'
}
WIZARD=lan WIZARD_WHY=""
PUBLIC4=$(public_ipv4)
GLOBAL6=$(global_ipv6)
if [ "$(answer wizard-on-lan)" != yes ]; then
	if [ "$(answer placement)" != home ]; then WIZARD=loopback WIZARD_WHY="the server's placement is $(answer placement), not home (--placement)"
	elif [ "$(answer reachable)" = internet ]; then WIZARD=loopback WIZARD_WHY="the server is to be reachable from the internet"
	elif [ -n "$PUBLIC4" ]; then WIZARD=loopback WIZARD_WHY="the server has a public address ($PUBLIC4) on an interface"
	elif [ -n "$GLOBAL6" ]; then WIZARD=loopback WIZARD_WHY="the server has a global IPv6 address ($GLOBAL6), which the internet may reach"
	fi
fi
if [ "$WIZARD" = loopback ] && [ "$REUSE_DATA" != 1 ] && [ ! -f "$DATA_DIR/cosmos.config.json" ]; then
	rm -f -- "$DATA_DIR/setup-loopback"
	(umask 077 && : >"$DATA_DIR/setup-loopback")
	chmod 600 "$DATA_DIR/setup-loopback"
fi
state_set step "starting"
systemctl start "$UNIT.service"

step "Waiting for the panel to answer on port $HTTP_PORT"
up=0
for _ in $(seq 1 180); do
	# 200 on a new install; with the data of an earlier Cosmos (--reuse-data)
	# it may answer only its own hostname (400) or send to HTTPS (30x): an
	# answer of the server all the same.
	code=$("${CURL[@]}" -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$HTTP_PORT/cosmos-ui/" || true)
	case $code in 200 | 30[1278] | 400 | 401 | 403 | 404) up=1; break ;; esac
	sleep 1
done
if [ "$up" = 0 ]; then
	journalctl -u "$UNIT" -n 30 --no-pager >&2 2>/dev/null || true
	die $EXIT_FAILED "Cosmos did not answer within 3 minutes (systemctl status $UNIT, journalctl -u $UNIT)"
fi
systemctl is-enabled --quiet "$UNIT.service" || die $EXIT_FAILED "the unit $UNIT is not enabled"
say "The panel answers."

# ---- 8. Protection ----------------------------------------------------------------
PIECES_FAILED="" PIECES_SKIPPED=""
run_piece() { # piece
	local piece=$1 script="$INSTALL_DIR/fork/$1/install.sh" rc=0 rec out line n=0
	local args=(--yes)
	if [ ! -f "$script" ]; then
		warn "the protection piece $piece is not in this release: skipped"
		PIECES_SKIPPED="$PIECES_SKIPPED $piece"
		return 0
	fi
	case $piece in
	nftables) args+=(--cosmos-config "$DATA_DIR/cosmos.config.json" --https-port "$HTTPS_PORT" --http-port "$HTTP_PORT")
		[ "$(answer keep-ssh-open)" != yes ] || args+=(--allow-ssh-from-outside)
		# where the server stands (the guard cannot tell): the router's exposed
		# host filters IPv4 too; a cloud server has no home network
		case $(answer placement) in exposed-host) args+=(--ipv4 yes) ;; cloud) args+=(--exposed) ;; esac
		# how SSH answers from outside, when the answer was given
		[ -z "$(answer ssh)" ] || args+=(--ssh "$(answer ssh)") ;;
	fail2ban) args+=(--log "$DATA_DIR/cosmos.security.log" --cosmos-config "$DATA_DIR/cosmos.config.json") ;;
	esac
	step "Protection: $piece"
	local environment=(PATH=/usr/sbin:/usr/bin:/sbin:/bin LANG=C.UTF-8 DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1)
	if [ -n "${SSH_CONNECTION:-}" ]; then environment+=("SSH_CONNECTION=$SSH_CONNECTION"); fi
	# only a piece that speaks the installer's interface (it answers
	# --print-packages) is run: an older one would refuse the flags
	if ! env -i "${environment[@]}" sh "$script" --print-packages </dev/null >/dev/null 2>&1; then
		warn "PIECE NOT RUN: $piece in this release does not speak the installer's interface (no --print-packages). Install it by hand later: sudo sh $script"
		PIECES_SKIPPED="$PIECES_SKIPPED $piece"
		return 0
	fi
	rec=$(mktemp "$WORK/record.$piece.XXXXXX")
	args+=(--record "$rec")
	out=$(env -i "${environment[@]}" sh "$script" "${args[@]}" </dev/null 2>&1) || rc=$?
	printf '%s\n' "$out" | tail -n 20 | sed 's/^/  /'
	while IFS= read -r line && [ "$n" -lt 200 ]; do
		if [[ $line =~ ^(nftables|fail2ban|unattended-upgrades)$'\t'(package|file|unit|table|status)$'\t'[\ -~]{1,300}$ ]] && [ "${BASH_REMATCH[1]}" = "$piece" ]; then
			record "piece $line"
			n=$((n + 1))
		elif [ -n "$line" ]; then
			warn "a record line of $piece was not taken: $(printf '%s' "$line" | tr -cd '[:print:]' | cut -c1-80)"
		fi
	done <"$rec"
	case $rc in
	0) record "piece-ran $piece"; say "$piece: done." ;;
	3 | 4) warn "$piece was not installed: $(printf '%s\n' "$out" | tail -n 1). Install it later with: sudo sh $script"
		PIECES_SKIPPED="$PIECES_SKIPPED $piece" ;;
	*) warn "$piece failed (exit $rc). Try again later with: sudo sh $script"
		PIECES_FAILED="$PIECES_FAILED $piece" ;;
	esac
}
PHASE=protection
if [ "$(answer protection)" = yes ] || [ "$(answer unattended-upgrades)" = yes ]; then
	state_set step "protection"
	for p in $(chosen_pieces); do
		if [ "$(state_get "piece-$p")" = "done" ]; then continue; fi
		run_piece "$p"
		state_set "piece-$p" "done"
	done
fi

# ---- 9. Done ------------------------------------------------------------------------
state_set version "$VERSION"
state_set completed-at "$(date -u +%FT%TZ)"
state_set completed yes
PHASE="done"

if [ -z "$LAN_ADDRESS" ] && command -v ip >/dev/null; then
	# no default route: the first address of an interface that is not Docker's
	LAN_ADDRESS=$(ip -4 -o addr show scope global 2>/dev/null | awk '$2 !~ /^(docker|br-|veth|virbr|nebula)/ {sub(/\/.*/, "", $4); print $4; exit}')
fi
suffix=":$HTTP_PORT"
[ "$HTTP_PORT" != 80 ] || suffix=""
step "Cosmos $VERSION is installed and running"
[ -n "$LAN_ADDRESS" ] || LAN_ADDRESS="<this server's address>"
CODE_FILE=$DATA_DIR/setup-code
# the tunnel: the same port on the owner's computer, or 8080 for a port only root may open
TUNNEL_PORT=$HTTP_PORT
[ "$HTTP_PORT" -ge 1024 ] || TUNNEL_PORT=8080
TUNNEL_USER=${SUDO_USER:-}
if ! [[ $TUNNEL_USER =~ ^[a-z_][a-z0-9_.-]{0,31}$ ]] || [ "$TUNNEL_USER" = root ]; then TUNNEL_USER="<your user>"; fi
TUNNEL_HOST=${PUBLIC4:-$LAN_ADDRESS}
tunnel_lines() {
	say "    ssh -L $TUNNEL_PORT:127.0.0.1:$HTTP_PORT $TUNNEL_USER@$TUNNEL_HOST"
	say "  and, while it is open, on that computer:"
	say "    http://127.0.0.1:$TUNNEL_PORT/"
	say "  (In the wizard's HTTPS step, give the server's real name or address, not 127.0.0.1.)"
}
for _ in $(seq 1 20); do [ -s "$CODE_FILE" ] && break; sleep 0.5; done
# Where the wizard ACTUALLY answers: the listening sockets of its port
# (/proc/net/tcp and tcp6), not the marker's presence.
listeners_of() { # port: the addresses listening on it, one per line (127.0.0.1, 0.0.0.0, ::, ...)
	local hex
	hex=$(printf '%04X' "$1")
	awk -v port="$hex" 'FNR > 1 && $4 == "0A" { split($2, a, ":"); if (a[2] == port) print a[1] }' /proc/net/tcp /proc/net/tcp6 2>/dev/null |
		sed -e 's/^0100007F$/127.0.0.1/' -e 's/^00000000$/0.0.0.0/' -e 's/^0\{32\}$/::/' -e 's/^0\{24\}01000000$/::1/' | sort -u
}
WIZARD_AT=$(listeners_of "$HTTP_PORT")
WIZARD_LOOPBACK=0
if [ -n "$WIZARD_AT" ] && [ -z "$(grep -vxF '127.0.0.1' <<<"$WIZARD_AT" || true)" ]; then WIZARD_LOOPBACK=1; fi
if [ -s "$CODE_FILE" ] && [ "$WIZARD" = loopback ] && [ "$WIZARD_LOOPBACK" = 0 ] && [ "$REUSE_DATA" != 1 ]; then
	warn "!!! THE FIRST-RUN WIZARD ANSWERS ON THE NETWORK, NOT ON 127.0.0.1 ONLY AS ASKED !!!"
	warn "Port $HTTP_PORT listens on: $(tr '\n' ' ' <<<"${WIZARD_AT:-nothing}")(see: journalctl -u $UNIT | grep 'Setup:')."
	warn "The setup code and your password would cross the network in clear: stop Cosmos (sudo systemctl stop $UNIT) until this is understood."
fi
if [ -s "$CODE_FILE" ] && [ "$WIZARD_LOOPBACK" = 1 ]; then
	say "The first-run wizard answers on this server only (127.0.0.1) until it is done,"
	say "because $WIZARD_WHY: the setup code and your password never"
	say "cross the network in clear. From your computer, open an SSH tunnel:"
	tunnel_lines
	say "(--wizard-on-lan opens the wizard on the network instead, in clear HTTP.)"
elif [ -s "$CODE_FILE" ]; then
	say "Open the panel from a computer at home:"
	say "    http://$LAN_ADDRESS$suffix/"
	say "The wizard answers on the network in clear HTTP until it has set HTTPS up:"
	say "anyone on that network could read the setup code and your password. To keep"
	say "them off the network, open an SSH tunnel from your computer instead:"
	tunnel_lines
else
	say "Open the panel from a computer at home:"
	say "    http://$LAN_ADDRESS$suffix/"
fi
if [ -s "$CODE_FILE" ]; then
	say "Finish the first-run wizard there now. Its first screen asks once for this"
	say "server's one-time setup code: nobody without it can set the server up."
	if [ "$PRINT_CODE" = 1 ] || { [ "$YES" = 0 ] && [ -t 1 ]; }; then
		say ""
		say "    Setup code: $(head -c 64 "$CODE_FILE" | tr -cd 'A-Z0-9-')"
		say ""
	else
		say "The code is in $CODE_FILE (root only): sudo cat $CODE_FILE"
		say "(This run had no terminal or --yes, so the code is not printed; --print-setup-code prints it.)"
	fi
	say "It is also in the journal: sudo journalctl -u $UNIT | grep -A1 'SETUP CODE'"
	say "This script opened nothing to the internet: no firewall rule, no router forward."
elif [ "$REUSE_DATA" = 1 ]; then
	say "It runs on the data that was kept in $DATA_DIR: sign in with its accounts"
	say "(the address may be the one its settings name rather than the one above)."
else
	warn "the server has made no setup code ($CODE_FILE): the wizard stays closed until it can. See: journalctl -u $UNIT"
fi
say ""
say "Installed in $INSTALL_DIR, data in $DATA_DIR, service $UNIT (enabled)."
say "What was installed is listed in $RECORD."
say "Updates come from the panel (or by themselves at night), not from this script."
say "To remove it: sudo bash $INSTALL_DIR/fork/install/uninstall.sh"
if [ -n "$PIECES_SKIPPED$PIECES_FAILED" ]; then
	[ -z "$PIECES_SKIPPED" ] || warn "not installed:$PIECES_SKIPPED"
	[ -z "$PIECES_FAILED" ] || warn "failed:$PIECES_FAILED"
	say ""
	say "Cosmos $VERSION is installed WITHOUT the protection you asked for:$PIECES_SKIPPED$PIECES_FAILED"
	exit $EXIT_UNPROTECTED
fi
exit 0
}

# On one line with main: bash reads a script as it runs it, so nothing after
# this line is ever read, even if the file is changed or replaced meanwhile.
# shellcheck disable=SC2317 # reached when main returns
{ main "$@"; exit; }
