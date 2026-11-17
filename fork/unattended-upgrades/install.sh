#!/bin/sh
# Switches on the system's automatic security updates (unattended-upgrades) on a
# Debian or Ubuntu server, for the Cosmos fork.
#
#     sh fork/unattended-upgrades/install.sh --dry-run   # shows what it would do, changes nothing
#     sh fork/unattended-upgrades/install.sh             # shows it, asks, then does it
#     sh fork/unattended-upgrades/install.sh --yes       # unattended
#
# What it does: installs the package unattended-upgrades when it is missing and
# writes one file, /etc/apt/apt.conf.d/52cosmos-security-upgrades, which
#   - looks for updates and installs them once a day (apt's own daily timers),
#   - takes them from the distribution's security archive only (it replaces
#     the package's default list, which on Debian also takes point releases),
#   - never restarts the server (Automatic-Reboot "false"): the panel and
#     "needrestart" say when a restart would be needed.
# It never changes which packages are held (apt-mark): a held package, Docker's
# for example, stays as it is, and packages from other repositories (Docker's
# own) are never updated by it.
#
# When automatic updates are already set up on this system (someone set
# APT::Periodic::Unattended-Upgrade in apt's configuration, to on or to off),
# it says "already configured" and changes nothing.
#
# Options:
#   --dry-run          show what would be done and stop
#   --yes              do not ask
#   --record <file>    append one line per item installed to <file>
#   --print-packages   print the packages that would be installed, and stop
#   --help             this text
#
# Exit codes: 0 done (or already configured), 1 failed (nothing changed),
# 2 bad usage, 3 not applicable here, 5 failed and undone, 6 failed and not
# completely undone, 7 answered no. Undo it with fork/unattended-upgrades/uninstall.sh.

set -eu
umask 022

PIECE=unattended-upgrades
CONF=/etc/apt/apt.conf.d/52cosmos-security-upgrades
AUTO=/etc/apt/apt.conf.d/20auto-upgrades
STATE_DIR=/var/lib/cosmos-protection
STATE=$STATE_DIR/$PIECE.state
MANIFEST=$STATE_DIR/$PIECE.manifest

DRY_RUN=0
YES=0
RECORD=""
PRINT_PACKAGES=0

say()  { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
fail() { code=$1; shift; printf 'ERROR: %s\n' "$*" >&2; exit "$code"; }
record() {
	[ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ] || return 0
	printf '%s\t%s\t%s\n' "$PIECE" "$1" "$(printf '%s' "$2" | tr -cd ' -~' | cut -c1-300)" >> "$RECORD"
}
not_applicable() {
	record status "not-applicable $1"
	printf 'Not applicable here: %s\n' "$1" >&2
	exit 3
}
usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

installed() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"; }

while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		--print-packages) PRINT_PACKAGES=1 ;;
		--record) [ $# -ge 2 ] || fail 2 "--record needs a file"; RECORD=$2; shift ;;
		--help|-h) usage; exit 0 ;;
		*) fail 2 "unknown option: $1 (see --help)" ;;
	esac
	shift
done

if [ "$PRINT_PACKAGES" = 1 ]; then
	installed unattended-upgrades || say unattended-upgrades
	exit 0
fi
if [ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ]; then
	[ -e "$RECORD" ] || ( umask 077; : > "$RECORD" ) || fail 2 "--record: cannot write $RECORD"
fi

[ "$(id -u)" = 0 ] || fail 1 "run this as root (sudo sh $0)"

OS_ID=""
OS_LIKE=""
if [ -r /etc/os-release ]; then
	OS_ID=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
	OS_LIKE=$(sed -n 's/^ID_LIKE=//p' /etc/os-release | tr -d '"')
fi
case "$OS_ID" in
	debian) FLAVOUR=debian ;;
	ubuntu) FLAVOUR=ubuntu ;;
	*)
		case " $OS_LIKE " in
			*" ubuntu "*) FLAVOUR=ubuntu ;;
			*" debian "*) FLAVOUR=debian ;;
			*) not_applicable "this is for Debian and Ubuntu (this system says ${OS_ID:-nothing})" ;;
		esac ;;
esac
{ command -v apt-get >/dev/null 2>&1 && command -v apt-config >/dev/null 2>&1; } || not_applicable "apt-get was not found"
{ [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; } || not_applicable "systemd is not running: apt's daily timers run the updates"

REINSTALL=0
[ -f "$STATE" ] && REINSTALL=1

# Already set up by someone else? Our own file is left out of the question.
if [ "$REINSTALL" = 0 ]; then
	value=$(apt-config dump 2>/dev/null | sed -n 's/^APT::Periodic::Unattended-Upgrade "\(.*\)";$/\1/p' | tail -n 1)
	if [ -n "$value" ]; then
		where=$(grep -l -s 'APT::Periodic::Unattended-Upgrade' /etc/apt/apt.conf /etc/apt/apt.conf.d/* 2>/dev/null | head -n 3 | tr '\n' ' ')
		case "$value" in 0|false|no) state="off" ;; *) state="on" ;; esac
		say "Automatic updates are already configured on this system ($state, in ${where:-the apt configuration}): nothing was changed."
		record status already-configured
		exit 0
	fi
fi

PACKAGES=""
installed unattended-upgrades || PACKAGES=unattended-upgrades

# the ${distro_...} below are apt's own variables, not the shell's
# shellcheck disable=SC2016
if [ "$FLAVOUR" = ubuntu ]; then
	ORIGINS='#clear Unattended-Upgrade::Origins-Pattern;
#clear Unattended-Upgrade::Allowed-Origins;
Unattended-Upgrade::Allowed-Origins {
	"${distro_id}:${distro_codename}-security";
	"${distro_id}ESMApps:${distro_codename}-apps-security";
	"${distro_id}ESM:${distro_codename}-infra-security";
};'
else
	ORIGINS='#clear Unattended-Upgrade::Origins-Pattern;
#clear Unattended-Upgrade::Allowed-Origins;
Unattended-Upgrade::Origins-Pattern {
	"origin=Debian,codename=${distro_codename}-security,label=Debian-Security";
	"origin=Debian,codename=${distro_codename},label=Debian-Security";
};'
fi

WORK=$(mktemp -d)
# shellcheck disable=SC2317
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
cat > "$WORK/conf" <<EOF
// Written by fork/unattended-upgrades/install.sh (the Cosmos fork): run it again
// to change it, or fork/unattended-upgrades/uninstall.sh to remove it.
// Security updates only, once a day, and never a restart of the server.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
$ORIGINS
Unattended-Upgrade::Automatic-Reboot "false";
EOF

TIMERS=""
for t in apt-daily.timer apt-daily-upgrade.timer; do
	s=$(systemctl is-enabled "$t" 2>/dev/null || true)
	case "$s" in enabled|static|enabled-runtime) ;; *) TIMERS="$TIMERS $t ($s)" ;; esac
done

say "Automatic security updates for the Cosmos fork: what this will do"
say ""
if [ -n "$PACKAGES" ]; then
	say "  package    apt-get install unattended-upgrades (from this system's repositories)"
else
	say "  package    unattended-upgrades is installed: left as it is"
fi
if [ "$REINSTALL" = 1 ]; then
	say "  file       $CONF (written by this script before: replaced)"
else
	say "  file       $CONF (new)"
fi
say "  updates    once a day, from the $FLAVOUR security archive only"
say "  restart    never automatic"
say "  holds      not touched (apt-mark); other repositories (Docker's) are not updated"
[ -z "$TIMERS" ] || warn "apt's daily timers are not enabled:$TIMERS. Nothing runs until they are (systemctl enable --now apt-daily.timer apt-daily-upgrade.timer); this script leaves them as they are"
say ""

if [ "$DRY_RUN" = 1 ]; then
	say "Dry run: nothing was changed."
	exit 0
fi
if [ "$YES" != 1 ]; then
	printf 'Go on? [y/N] '
	read -r answer || answer=""
	case "$answer" in
		y|Y|yes|YES) ;;
		*) say "Nothing was changed."; record status declined; exit 7 ;;
	esac
fi

AUTO_BEFORE=0
[ -e "$AUTO" ] && AUTO_BEFORE=1
HOLDS_BEFORE=$(apt-mark showhold 2>/dev/null | sort | tr '\n' ' ')

if [ -n "$PACKAGES" ]; then
	say "installing the package unattended-upgrades ..."
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends unattended-upgrades >/dev/null \
		|| fail 1 "apt-get could not install unattended-upgrades: nothing was changed"
	record package unattended-upgrades
fi

install -m 0644 -o root -g root "$WORK/conf" "$CONF.cosmos-new"
mv -f "$CONF.cosmos-new" "$CONF"

# checked from the inside: apt reads the values as written
undo() {
	rm -f "$CONF"
	if [ "$AUTO_BEFORE" = 0 ] && [ -e "$AUTO" ]; then rm -f "$AUTO"; fi
}
dump=$(apt-config dump 2>/dev/null || true)
for want in 'APT::Periodic::Unattended-Upgrade "1";' 'APT::Periodic::Update-Package-Lists "1";' 'Unattended-Upgrade::Automatic-Reboot "false";'; do
	if ! printf '%s\n' "$dump" | grep -qxF "$want"; then
		undo
		record status "undone check-failed"
		fail 5 "apt does not read the setting as written ($want): the file was removed again"
	fi
done
# shellcheck disable=SC2016
if printf '%s\n' "$dump" | grep -q '^Unattended-Upgrade::Origins-Pattern:: "origin=Debian,codename=${distro_codename},label=Debian";'; then
	undo
	record status "undone check-failed"
	fail 5 "apt still takes all updates of the release, not only security ones: the file was removed again"
fi
HOLDS_AFTER=$(apt-mark showhold 2>/dev/null | sort | tr '\n' ' ')
[ "$HOLDS_BEFORE" = "$HOLDS_AFTER" ] || warn "the held packages changed during the installation (before: $HOLDS_BEFORE; now: $HOLDS_AFTER)"

mkdir -p "$STATE_DIR"
chmod 755 "$STATE_DIR"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
INSTALLED_AT=$NOW
if [ "$REINSTALL" = 1 ]; then
	old=$(sed -n 's/^installed_at=//p' "$STATE" | head -n 1)
	case "$old" in [0-9][0-9][0-9][0-9]-*Z) INSTALLED_AT=$old ;; esac
fi
{
	say "piece=$PIECE"
	say "format=1"
	say "installed_at=$INSTALLED_AT"
	say "updated_at=$NOW"
	say "config=$CONF"
} > "$STATE.new"
chmod 644 "$STATE.new"
mv -f "$STATE.new" "$STATE"
{
	if [ "$REINSTALL" = 1 ] && [ -f "$MANIFEST" ]; then
		grep -E '^(package|file)	' "$MANIFEST" | grep -v -F -x -e "file	$CONF" -e "file	$STATE" || true
	fi
	[ -z "$PACKAGES" ] || printf 'package\t%s\n' unattended-upgrades
	# the package writes 20auto-upgrades when it is installed: made by this run
	if [ "$AUTO_BEFORE" = 0 ] && [ -e "$AUTO" ]; then printf 'file\t%s\n' "$AUTO"; fi
	printf 'file\t%s\n' "$CONF"
	printf 'file\t%s\n' "$STATE"
} | awk '!seen[$0]++' > "$MANIFEST.new"
chmod 600 "$MANIFEST.new"
mv -f "$MANIFEST.new" "$MANIFEST"

if [ "$AUTO_BEFORE" = 0 ] && [ -e "$AUTO" ]; then record file "$AUTO"; fi
record file "$CONF"
record file "$STATE"
if [ "$REINSTALL" = 1 ]; then record status already-installed; else record status installed; fi

say "wrote    $CONF"
say ""
say "Done: security updates are installed once a day; the server is never restarted by them."
say "  unattended-upgrade --dry-run --debug     what it would install now"
say "  less /var/log/unattended-upgrades/unattended-upgrades.log"
