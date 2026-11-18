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
# Options (each answer also has a key of the answers file, in brackets):
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
#                            than home, reachable from the internet, or a public IPv4
#                            address on an interface) answers on 127.0.0.1 only, through
#                            an SSH tunnel (the line is printed); at home it answers on
#                            the home network and the tunnel is offered
#   --reuse-data             install over the data folder the fork left (upstream's
#                            data is migrate.sh's job)
#   --allow-older            install a release older than this script (refused
#                            otherwise: the installer goes forward only)
#   --proxy http://<host>:<port>  the proxy for the downloads from GitHub (the
#                            proxy variables of the shell are not read)
#   --print-setup-code       print the one-time setup code even with --yes or
#                            without a terminal (otherwise its file is named)
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
# failed: the last line names it).
#
# The environment is not read, but for COSMOS_INSTALL_APT_WAIT (seconds apt
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
*install*.sh) cosmos_self=./$0 ;;
esac
if [ -z "$cosmos_self" ] || [ ! -f "$cosmos_self" ] || ! grep -q '^# COSMOS-FORK-INSTALL-SH$' "$cosmos_self" 2>/dev/null; then
	echo "install.sh must be run from a file, not from a pipe. Download it, then run it:" >&2
	echo "    curl -fsSLO https://github.com/<owner>/<repo>/releases/latest/download/install.sh && sudo bash install.sh" >&2
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
# ---------------------------------------------------------------------------

# Test mode: only a copy that the release build did NOT stamp reads its keys,
# version and repository from the environment (the test bed's own key, never
# the real one). A stamped script ignores these variables: nothing a user
# passes makes it trust another key.
TEST_MODE=0
if [ "$RELEASE_KEYS" = "@FORK_""RELEASE_KEYS@" ]; then
	TEST_MODE=1
	RELEASE_KEYS=${COSMOS_INSTALL_TEST_KEYS:-}
	RELEASE_VERSION=${COSMOS_INSTALL_TEST_VERSION:-}
	RELEASE_REPOSITORY=${COSMOS_INSTALL_TEST_REPOSITORY:-}
fi

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
ANSWER_KEYS="http-port https-port storage-tools reachable protection unattended-upgrades address upnp router outside-lookup keep-ssh-open placement ssh wizard-on-lan"

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

ALLOW_OLDER=0 PRINT_CODE=0 PROXY=""
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
	--version | --from | --answers | --proxy | --http-port | --https-port | --storage-tools | --reachable | --protection | \
		--unattended-upgrades | --address | --upnp | --router | --placement | --ssh)
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
flock -n 9 || die $EXIT_BUSY "another run of install.sh or uninstall.sh is going on"

WORK=""
STAGING=""
HOP_DIR=""
case $0 in /var/tmp/cosmos-install-next.*/install.sh) HOP_DIR=$(dirname -- "$0") ;; esac
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
	warn "TEST MODE: this copy of install.sh was not stamped by a release build."
	if [ -z "$RELEASE_KEYS" ] || [ -z "$RELEASE_REPOSITORY" ] || [ -z "$RELEASE_VERSION" ]; then
		die $EXIT_USAGE "this copy of install.sh is not the one of a release: download install.sh from the fork's releases"
	fi
fi
for key in $RELEASE_KEYS; do
	[[ $key =~ ^[A-Za-z0-9+/]{43}=$ ]] || die $EXIT_USAGE "a release key in this script is damaged"
done
[[ $RELEASE_VERSION =~ ^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}-fork\.[1-9][0-9]{0,8}$ ]] || die $EXIT_USAGE "the release version in this script is damaged"
[[ $RELEASE_REPOSITORY =~ ^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$ ]] || die $EXIT_USAGE "the repository in this script is damaged"

# BEGIN shared detection (the same block is in migrate.sh; a test compares them)
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
cosmos_folder_kind() { # folder -> prints fork|upstream|none|unknown
	local dir=$1 version=""
	if [ ! -e "$dir" ]; then echo none; return; fi
	if [ -f "$dir/meta.json" ]; then
		version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]\{1,64\}\)".*/\1/p' "$dir/meta.json" | head -n 1 | tr -cd 'A-Za-z0-9.+-')
	fi
	FOUND_VERSION=$version
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

RESUMING=0
if [ -f "$STATE" ] && [ "$(state_get completed)" = yes ]; then
	die $EXIT_FORK "the Cosmos fork $(state_get version) was installed by this script on $(state_get completed-at) in $INSTALL_DIR.
Updates are made from the panel (the update notice) or by the server itself at night,
never by running install.sh again. To remove it: sudo bash $INSTALL_DIR/fork/install/uninstall.sh"
fi
if [ -f "$STATE" ]; then
	RESUMING=1
fi

detect_cosmos
if [ "$FOUND" != none ]; then
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

if [ "$RESUMING" = 1 ]; then
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
	for f in /opt/.cosmos-install.* /var/tmp/cosmos-install.* /var/tmp/cosmos-install-apt.*; do
		if [ -d "$f" ] && [ ! -L "$f" ]; then rm -rf -- "$f"; fi
	done
fi

# ---- 2. The system ----------------------------------------------------------
step "Checking the system"
[ -r /etc/os-release ] || die $EXIT_SYSTEM "/etc/os-release is missing: this is not a system this script knows"
OS_ID=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_VERSION=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_CODENAME=$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_UBUNTU_CODENAME=$(sed -n 's/^UBUNTU_CODENAME=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_LIKE=$(sed -n 's/^ID_LIKE=//p' /etc/os-release | tr -d '"' | head -n 1)
OS_NAME=$(sed -n 's/^PRETTY_NAME=//p' /etc/os-release | tr -d '"' | head -n 1 | tr -cd 'A-Za-z0-9 ._()/-')
supported=0
case "$OS_ID $OS_VERSION" in
"debian 12" | "debian 13" | "ubuntu 24.04" | "ubuntu 26.04") supported=1 ;;
esac
if [ "$supported" = 0 ]; then
	case " $OS_ID $OS_LIKE " in
	*" debian "* | *" ubuntu "*) ;;
	*) die $EXIT_SYSTEM "$OS_NAME is not Debian or Ubuntu: this script only knows apt systems." ;;
	esac
	if [ "$FORCE" = 1 ]; then
		warn "$OS_NAME is not a supported system (Debian 12 or 13, Ubuntu 24.04 or 26.04): going on because of --force, at your own risk."
	else
		die $EXIT_SYSTEM "$OS_NAME is not supported (Debian 12 or 13, Ubuntu 24.04 or 26.04). Add --force to try anyway, at your own risk."
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
choose_ports
say "Ports: HTTP $HTTP_PORT, HTTPS $HTTPS_PORT."

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
if command -v docker >/dev/null 2>&1 || [ -e /snap/bin/docker ] || command -v dockerd >/dev/null 2>&1; then
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

# ---- 3. Which packages, and a simulation before any change -------------------
TOOLS=(ca-certificates curl unzip openssl)
BASE=(iproute2 procps util-linux psmisc avahi-daemon iptables)
STORAGE=(fuse3 mergerfs snapraid samba mdadm parted e2fsprogs)
DOCKER_PACKAGES=(docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin)
# What the protection pieces install themselves: asked from the pieces with
# --print-packages once the release is verified; until then this list.
PIECES="unattended-upgrades fail2ban nftables"
piece_fallback_packages() {
	case $1 in
	nftables) echo nftables ;;
	fail2ban) echo "fail2ban python3" ;;
	unattended-upgrades) echo unattended-upgrades ;;
	esac
}
chosen_pieces() {
	local p
	[ "$(answer protection)" = yes ] || { [ "$(answer unattended-upgrades)" = yes ] && echo unattended-upgrades; return 0; }
	for p in $PIECES; do
		if [ "$p" = unattended-upgrades ] && [ "$(answer unattended-upgrades)" != yes ]; then continue; fi
		echo "$p"
	done
}
wanted_packages() { # the distribution's packages wanted, before the pieces answered
	local p
	printf '%s\n' "${TOOLS[@]}" "${BASE[@]}"
	if [ "$(answer storage-tools)" = yes ]; then printf '%s\n' "${STORAGE[@]}"; fi
	for p in $(chosen_pieces); do piece_fallback_packages "$p" | tr ' ' '\n'; done
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
APT_TIMEOUT=$((5400 + APT_WAIT))
# --foreground: apt stays in this script's process group, so that whatever
# stops the script (Ctrl-C, a lost SSH session) stops apt with it, as before.
APT_RUN=(timeout --foreground --kill-after=120 "$APT_TIMEOUT")
apt_timed_out() { # what
	warn "$1 did not finish within $((APT_TIMEOUT / 60)) minutes and was stopped. What it did so far is kept: run install.sh again with the same options to continue."
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
		set +e +o pipefail
		"${APT_RUN[@]}" "${APT_ENV[@]}" apt-get "${APT_OPTS[@]}" "$@" </dev/null 2>&1 | tee "$log" |
			grep --line-buffered -vE '^(Get|Hit|Ign|Reading|Building|Preparing|Unpacking|Selecting|\(Reading|Setting up|Processing|Created symlink|Fetched|Need to get|After this|The following|Suggested|Recommended|  [a-z0-9])'
		statuses=("${PIPESTATUS[@]}")
		set -e -o pipefail
		rc=${statuses[0]}
		if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then apt_timed_out "apt-get $1"; rm -f "$log"; return "$rc"; fi
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
	printf 'Dir::Etc::Parts "%s/conf";\nDir::State::Lists "%s/lists";\nDir::Cache "%s/cache";\nDir::Etc::SourceParts "%s/parts";\nDebug::NoLocking "1";\n' \
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
		out=$(timeout --foreground --kill-after=60 1800 "${SIM_ENV[@]}" "${APT_ENV[@]}" apt-get -q -o Acquire::Retries=3 "${SIM_OPTS[@]}" update </dev/null 2>&1) || rc=$?
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
	out=$(timeout --foreground --kill-after=60 1800 "${SIM_ENV[@]}" "${APT_ENV[@]}" apt-get -q -s "${SIM_OPTS[@]}" install --no-install-recommends "$@" </dev/null 2>&1) || rc=$?
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

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
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
	"${APT_RUN[@]}" "${APT_ENV[@]}" dpkg --force-confdef --force-confold --configure -a </dev/null || rc=$?
	if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then apt_timed_out "dpkg --configure -a"; die $EXIT_FAILED "the package database is still unfinished"; fi
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
	say "Installing: ${missing[*]}"
	apt_get install --no-install-recommends "${missing[@]}" || rc=$?
	# the truth for exactly the packages apt planned: dpkg's status of each,
	# installed or left half-done; one that was there before is not ours
	local state
	for p in $planned; do
		state=$(pkg_state "$p")
		if grep -qxF -- "$p" "$WORK/before-transaction" || [ "$state" = n ] || [ "$state" = c ]; then
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
# curl and openssl run with a fixed, minimal environment: no ~/.curlrc
# (curl -q), no proxy or OPENSSL_CONF from the caller's shell (sudo -E, su).
# A server that needs a proxy for GitHub gives it with --proxy (or
# downloads the release by hand and uses --from).
SAFE_PATH=/usr/sbin:/usr/bin:/sbin:/bin
CURL=(env -i PATH="$SAFE_PATH" HOME=/nonexistent LC_ALL=C curl -q)
if [ -n "$PROXY" ]; then CURL+=(--proxy "$PROXY" --noproxy ''); fi
OSSL=(env -i PATH="$SAFE_PATH" HOME=/nonexistent LC_ALL=C openssl)
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
	say "- unpack the release to $INSTALL_DIR, data in $DATA_DIR, write and start $UNIT_FILE (ports $HTTP_PORT and $HTTPS_PORT)"
	for p in $(chosen_pieces); do say "- run fork/$p/install.sh --yes"; done
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
		say "This script belongs to $RELEASE_VERSION; $VERSION is asked for: taking that release's own install.sh."
		base="https://github.com/$RELEASE_REPOSITORY/releases/download/v$VERSION"
		mkdir -p "$WORK/next"
		fetch "$base/install.sh" "$WORK/next/install.sh" 1048576 || die $EXIT_FAILED "could not download install.sh of $VERSION"
		fetch "$base/cosmos-cloud-$VERSION.scripts.sig" "$WORK/next/scripts.sig" 4096 ||
			refuse "release $VERSION has no signed file of scripts (cosmos-cloud-$VERSION.scripts.sig); releases before install.sh existed cannot be installed by script"
		verify_signed "$WORK/next/scripts.sig" cosmos-fork-scripts-v1 "$WORK/next/signed" ||
			refuse "the scripts of $VERSION are not signed with a release key this script trusts"
		[ "$(sed -n 2p "$WORK/next/signed")" = "version $VERSION" ] || refuse "the signed file of scripts is for another version"
		want=$(awk '$1=="sha256" && $3=="install.sh"{print $2}' "$WORK/next/signed")
		if ! { [ -n "$want" ] && [ "$(sha256sum "$WORK/next/install.sh" | awk '{print $1}')" = "$want" ]; }; then refuse "install.sh of $VERSION does not match its signed SHA-256"; fi
		hop=$(mktemp -d /var/tmp/cosmos-install-next.XXXXXXXX)
		cp "$WORK/next/install.sh" "$hop/install.sh"
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
		exec bash "$hop/install.sh" "${args[@]}" --version "$VERSION"
	fi
	[ "$VERSION" = "$RELEASE_VERSION" ] || die $EXIT_FAILED "install.sh of $VERSION says it is $RELEASE_VERSION"
	base="https://github.com/$RELEASE_REPOSITORY/releases/download/v$VERSION"
	zipname=cosmos-cloud-$VERSION-$ARCH.zip
	say "Downloading $zipname ..."
	rc=0
	fetch "$base/cosmos-cloud-$VERSION.sig" "$WORK/release.sig" 4096 || rc=$?
	if [ "$rc" = 2 ]; then refuse "release $VERSION has no signature file (cosmos-cloud-$VERSION.sig): it is not installed unverified"; fi
	[ "$rc" = 0 ] || die $EXIT_FAILED "could not download the signature file of $VERSION (network?)"
	fetch "$base/$zipname.md5" "$WORK/release.md5" 1024 || die $EXIT_FAILED "could not download $zipname.md5"
	fetch "$base/$zipname" "$WORK/release.zip" "$MAX_ZIP" || die $EXIT_FAILED "the download of $zipname failed (network cut?). Run install.sh again."
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
if [ -f "$NEW/fork/install/install.sh" ]; then
	if cmp -s -- "$0" "$NEW/fork/install/install.sh"; then
		say "This install.sh is the one inside the signed release."
	else
		say "Note: this install.sh differs from the one inside the release (the release's copy is in $INSTALL_DIR/fork/install/)."
	fi
fi

# What the release's binary sees of the network (it changes nothing).
DETECT="$WORK/detect.json"
detect_args=(--timeout 10)
# a dry run tells no one: never an outside service
if [ "$(answer outside-lookup)" != yes ] || [ "$DRY_RUN" = 1 ]; then detect_args+=(--no-outside); fi
if timeout 30 env -i PATH="$SAFE_PATH" HOME="$WORK" RCLONE_CONFIG="$WORK/none.conf" "$NEW/cosmos" fork-detect "${detect_args[@]}" >"$DETECT" 2>/dev/null; then :; else : >"$DETECT"; fi
json_string() { # key in the network part (plain values only)
	sed -n "/\"network\"/,/}/s/.*\"$1\": \"\\([0-9a-zA-Z.:/_-]*\\)\".*/\\1/p" "$DETECT" | head -n 1
}
LAN_ADDRESS=$(json_string address)
if grep -q '"answers": true' "$DETECT"; then
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
WIZARD=lan WIZARD_WHY=""
PUBLIC4=$(public_ipv4)
if [ "$(answer wizard-on-lan)" != yes ]; then
	if [ "$(answer placement)" != home ]; then WIZARD=loopback WIZARD_WHY="the server's placement is $(answer placement), not home (--placement)"
	elif [ "$(answer reachable)" = internet ]; then WIZARD=loopback WIZARD_WHY="the server is to be reachable from the internet"
	elif [ -n "$PUBLIC4" ]; then WIZARD=loopback WIZARD_WHY="the server has a public address ($PUBLIC4) on an interface"
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
if [ -s "$CODE_FILE" ] && [ -f "$DATA_DIR/setup-loopback" ]; then
	say "The first-run wizard answers on this server only (127.0.0.1) until it is done,"
	say "because $WIZARD_WHY: the setup code and your password never"
	say "cross the network in clear. From your computer, open an SSH tunnel:"
	tunnel_lines
	say "(--wizard-on-lan opens the wizard on the network instead, in clear HTTP.)"
elif [ -s "$CODE_FILE" ]; then
	say "Open the panel from a computer at home:"
	say "    http://$LAN_ADDRESS$suffix/"
	say "The wizard answers on the home network in clear HTTP until it has set HTTPS up:"
	say "anyone on that network could read the setup code and your password. To keep"
	say "them off the network, open an SSH tunnel from your computer instead:"
	tunnel_lines
else
	say "Open the panel from a computer at home:"
	say "    http://$LAN_ADDRESS$suffix/"
fi
for _ in $(seq 1 20); do [ -s "$CODE_FILE" ] && break; sleep 0.5; done
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
