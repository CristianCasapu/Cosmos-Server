#!/bin/sh
# Removes the host guard of the Cosmos fork (what fork/nftables/install.sh made).
#
#     sh fork/nftables/uninstall.sh --dry-run   # shows what it would do, changes nothing
#     sh fork/nftables/uninstall.sh             # shows it, asks, then does it
#     sh fork/nftables/uninstall.sh --yes       # unattended
#
# It stops and disables the guard's units (stopping removes the table inet
# cosmos_host_guard, and nothing else), removes the files and units the
# installer listed in /var/lib/cosmos-protection/nftables.manifest, and leaves
# every other rule of the packet filter as it is. The package nftables stays
# installed: Fail2ban's bans use the same command (the fork's installer removes
# the packages it installed with its own --remove-packages). Running it twice,
# or without an installation, does no harm.
#
# Options:
#   --dry-run          show what would be done and stop
#   --yes              do not ask
#   --record <file>    append one line per item removed to <file>
#   --help             this text
#
# Exit codes: 0 done (or nothing to remove), 1 failed (nothing changed),
# 2 bad usage, 3 not applicable here, 6 failed part-way (what is left is
# said), 7 answered no.

set -eu

PIECE=nftables
TNAME=cosmos_host_guard
TABLE="inet $TNAME"
ETC=/etc/cosmos-host-guard
SBIN=/usr/local/sbin/cosmos-host-guard
UNIT_DIR=/etc/systemd/system
UNITS="cosmos-host-guard-refresh.timer cosmos-host-guard-refresh.service cosmos-host-guard-watch.service cosmos-host-guard.service"
STATE_DIR=/var/lib/cosmos-protection
STATE=$STATE_DIR/$PIECE.state
MANIFEST=$STATE_DIR/$PIECE.manifest
RUN_DIR=/run/cosmos-host-guard
UNDO_UNIT=cosmos-host-guard-undo

DRY_RUN=0
YES=0
RECORD=""

say()  { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
fail() { code=$1; shift; printf 'ERROR: %s\n' "$*" >&2; exit "$code"; }
record() {
	[ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ] || return 0
	printf '%s\t%s\t%s\n' "$PIECE" "$1" "$(printf '%s' "$2" | tr -cd ' -~' | cut -c1-300)" >> "$RECORD"
}
usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		--record) [ $# -ge 2 ] || fail 2 "--record needs a file"; RECORD=$2; shift ;;
		--help|-h) usage; exit 0 ;;
		*) fail 2 "unknown option: $1 (see --help)" ;;
	esac
	shift
done

[ "$(id -u)" = 0 ] || fail 1 "run this as root (sudo sh $0)"
if [ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ]; then
	[ -e "$RECORD" ] || ( umask 077; : > "$RECORD" ) || fail 2 "--record: cannot write $RECORD"
fi

# What is there. Files come from the manifest; the known names are a fallback
# for an installation interrupted before it wrote the manifest. A unit or a
# folder of these names that this installer did not make (no state file) is
# not touched.
FILES=""
if [ -f "$MANIFEST" ]; then
	FILES=$(awk -F '\t' '$1 == "file" {print $2}' "$MANIFEST" | grep -E '^/(etc/cosmos-host-guard/[A-Za-z0-9._-]+|usr/local/sbin/cosmos-host-guard|var/lib/cosmos-protection/nftables\.state)$' || true)
fi
OURS=0
[ -f "$STATE" ] || [ -f "$MANIFEST" ] && OURS=1
if [ "$OURS" = 1 ]; then
	for f in "$ETC/cosmos-host-guard.nft" "$ETC/settings.nft" "$ETC/README" "$SBIN" "$STATE"; do
		case "
$FILES
" in *"
$f
"*) ;; *) [ ! -e "$f" ] || FILES="$FILES
$f" ;; esac
	done
fi
PRESENT_UNITS=""
if [ "$OURS" = 1 ]; then
	for unit in $UNITS; do [ ! -e "$UNIT_DIR/$unit" ] || PRESENT_UNITS="$PRESENT_UNITS $unit"; done
fi
TABLE_LOADED=0
if [ "$OURS" = 1 ] && command -v nft >/dev/null 2>&1 && nft list table inet "$TNAME" >/dev/null 2>&1; then TABLE_LOADED=1; fi

if [ "$OURS" = 0 ]; then
	say "The host guard of the Cosmos fork is not installed here: nothing to remove."
	if [ -e "$ETC" ] || [ -e "$UNIT_DIR/cosmos-host-guard.service" ]; then
		warn "$ETC or the unit cosmos-host-guard.service exists without this installer's state: left alone"
	fi
	record status not-installed
	exit 0
fi

say "Removing the host guard of the Cosmos fork: what this will do"
say ""
[ "$TABLE_LOADED" = 0 ] || say "  table      $TABLE is removed (every other table stays as it is)"
for unit in $PRESENT_UNITS; do say "  unit       $unit: stopped, disabled, removed"; done
printf '%s\n' "$FILES" | while read -r f; do [ -z "$f" ] || say "  file       $f"; done
say "  manifest   $MANIFEST"
say "  package    nftables stays installed"
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
		*) say "Nothing was changed."; exit 7 ;;
	esac
fi

LEFT=""
systemctl stop "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
for unit in $PRESENT_UNITS; do
	systemctl disable --now "$unit" >/dev/null 2>&1 || systemctl stop "$unit" >/dev/null 2>&1 || true
done
if command -v nft >/dev/null 2>&1 && nft list table inet "$TNAME" >/dev/null 2>&1; then
	nft delete table inet "$TNAME" || LEFT="$LEFT table:$TABLE"
fi
[ "$TABLE_LOADED" = 0 ] || record table "$TABLE"
for unit in $PRESENT_UNITS; do
	rm -f "$UNIT_DIR/$unit" || LEFT="$LEFT $UNIT_DIR/$unit"
	record unit "$unit"
	say "removed  $unit"
done
systemctl daemon-reload >/dev/null 2>&1 || true
# shellcheck disable=SC2086
systemctl reset-failed $PRESENT_UNITS >/dev/null 2>&1 || true
OLD_IFS=$IFS
IFS='
'
for f in $FILES; do
	IFS=$OLD_IFS
	[ -n "$f" ] || continue
	rm -f -- "$f" || LEFT="$LEFT $f"
	record file "$f"
	say "removed  $f"
done
IFS=$OLD_IFS
rm -f "$MANIFEST"
rmdir "$ETC" 2>/dev/null || true
rm -rf "$RUN_DIR"
rmdir "$STATE_DIR" 2>/dev/null || true

if [ -n "$LEFT" ]; then
	fail 6 "could not remove:$LEFT"
fi
record status removed
say ""
say "Done: the guard is removed; the rest of the packet filter is as it was."
