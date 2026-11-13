#!/bin/sh
# Undoes fork/fail2ban/install.sh.
#
# Run as root:
#
#     sh fork/fail2ban/uninstall.sh --dry-run   # shows what it would do, changes nothing
#     sh fork/fail2ban/uninstall.sh             # shows it, asks, then does it
#
# What it does:
#   1. stops Fail2ban and turns it off at boot. Stopping lifts every ban at
#      once (Fail2ban removes its own table from the packet filter);
#   2. removes the files the fork put under /etc/fail2ban (its filters, its
#      jail files, the file Cosmos' panel writes), after copying them to
#      /var/backups/cosmos-fail2ban/<time>/;
#   3. with --purge also removes the package (apt-get purge fail2ban), its
#      configuration and its database of bans.
#
# Without --purge the package stays installed but stopped and disabled: with
# the fork's files gone, Debian's own configuration would watch SSH without
# sparing your network, so the service is not left running. To use Fail2ban
# without the fork, configure it and run "systemctl enable --now fail2ban".
#
# Cosmos' security log (cosmos.security.log) is Cosmos' own and is left alone.
# Running it twice does no harm.
#
# Options:
#   --dry-run   show what would be done and stop
#   --yes       do not ask before changing
#   --purge     also remove the package fail2ban
#   --help      this text

set -eu

ETC=/etc/fail2ban
BACKUP_ROOT=/var/backups/cosmos-fail2ban
TABLE="inet f2b-cosmos"

FILES="
$ETC/filter.d/cosmos-auth.conf
$ETC/filter.d/cosmos-probe.conf
$ETC/filter.d/cosmos-scan.conf
$ETC/filter.d/cosmos-abuse.conf
$ETC/jail.d/cosmos-10-jails.local
$ETC/jail.d/cosmos-50-host.local
$ETC/jail.d/cosmos-90-panel.local
$ETC/fail2ban.d/cosmos-fork.local
"

DRY_RUN=0
YES=0
PURGE=0

say()  { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		--purge) PURGE=1 ;;
		--help|-h) usage; exit 0 ;;
		*) die "unknown option: $1 (see --help)" ;;
	esac
	shift
done

[ "$(id -u)" = 0 ] || die "run this as root"

INSTALLED=0
if dpkg-query -W -f='${Status}' fail2ban 2>/dev/null | grep -q "install ok installed"; then INSTALLED=1; fi
HAVE_SYSTEMD=0
[ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 && HAVE_SYSTEMD=1

PRESENT=""
for file in $FILES; do
	[ -e "$file" ] && PRESENT="$PRESENT $file"
done

say "Removing the Cosmos fork's Fail2ban setup: what this will do"
say ""
if [ "$HAVE_SYSTEMD" = 1 ]; then
	say "  service    fail2ban is stopped and disabled (every ban is lifted)"
elif command -v fail2ban-client >/dev/null 2>&1; then
	say "  service    Fail2ban is stopped with 'fail2ban-client stop' (every ban is lifted)"
fi
say ""
say "  files"
if [ -z "$PRESENT" ]; then
	say "    (none of the fork's files are there)"
else
	for file in $PRESENT; do say "    remove    $file"; done
	say "    copies go to $BACKUP_ROOT/<time>/"
fi
say ""
if [ "$PURGE" = 1 ]; then
	if [ "$INSTALLED" = 1 ]; then say "  package    apt-get purge fail2ban (its configuration and its database of bans go too)"
	else say "  package    fail2ban is not installed"
	fi
else
	say "  package    fail2ban stays installed, stopped and disabled (remove it with --purge)"
fi
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
		*) say "Nothing was changed."; exit 0 ;;
	esac
fi

if [ "$HAVE_SYSTEMD" = 1 ]; then
	if [ "$INSTALLED" = 1 ]; then
		systemctl disable --now fail2ban.service >/dev/null 2>&1 || warn "could not stop or disable fail2ban.service"
		say "stopped  fail2ban.service (disabled)"
	fi
elif command -v fail2ban-client >/dev/null 2>&1; then
	fail2ban-client stop >/dev/null 2>&1 || true
	say "stopped  Fail2ban"
fi

# Fail2ban removes its table when it stops; this is for a Fail2ban that did
# not stop cleanly.
if command -v nft >/dev/null 2>&1 && nft list table $TABLE >/dev/null 2>&1; then
	nft delete table $TABLE && say "removed  the table $TABLE from the packet filter"
fi

if [ -n "$PRESENT" ]; then
	BACKUP="$BACKUP_ROOT/$(date +%Y%m%d-%H%M%S)-uninstall"
	mkdir -p "$BACKUP"
	chmod 700 "$BACKUP_ROOT" "$BACKUP"
	for file in $PRESENT; do
		cp -p -- "$file" "$BACKUP/$(basename -- "$file")"
		rm -f -- "$file"
		say "removed  $file"
	done
	say "copies are in $BACKUP"
fi

if [ "$PURGE" = 1 ] && [ "$INSTALLED" = 1 ]; then
	DEBIAN_FRONTEND=noninteractive apt-get purge -y fail2ban
	say "removed  the package fail2ban"
fi

say ""
say "Done."
