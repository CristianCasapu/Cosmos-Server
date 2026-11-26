#!/bin/sh
# Switches off the automatic security updates that fork/unattended-upgrades/install.sh
# switched on: removes the files it listed in
# /var/lib/cosmos-protection/unattended-upgrades.manifest (its apt setting, and
# 20auto-upgrades when the package wrote that during the installation). The
# package unattended-upgrades stays installed (the fork's installer removes the
# packages it installed with its own --remove-packages). A configuration made by
# someone else is never touched. Running it twice, or without an installation,
# does no harm.
#
# Options:
#   --dry-run          show what would be done and stop
#   --yes              do not ask
#   --record <file>    append one line per item removed to <file>
#   --help             this text
#
# Exit codes: 0 done (or nothing to remove), 1 failed, 2 bad usage,
# 6 failed part-way, 7 answered no.

set -eu

PIECE=unattended-upgrades
STATE_DIR=/var/lib/cosmos-protection
STATE=$STATE_DIR/$PIECE.state
MANIFEST=$STATE_DIR/$PIECE.manifest
CONF=/etc/apt/apt.conf.d/52cosmos-security-upgrades

DRY_RUN=0
YES=0
RECORD=""

say()  { printf '%s\n' "$*"; }
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

if [ ! -f "$STATE" ] && [ ! -f "$MANIFEST" ]; then
	say "The fork's automatic security updates are not installed here: nothing to remove."
	record status not-installed
	exit 0
fi

# only the files this piece may have made
FILES=""
if [ -f "$MANIFEST" ]; then
	FILES=$(awk -F '\t' '$1 == "file" {print $2}' "$MANIFEST" | grep -x -E "/etc/apt/apt.conf.d/(52cosmos-security-upgrades|20auto-upgrades)|$STATE" || true)
fi
for f in "$CONF" "$STATE"; do
	case "
$FILES
" in *"
$f
"*) ;; *) [ ! -e "$f" ] || FILES="$FILES
$f" ;; esac
done

say "Switching off the fork's automatic security updates: what this will do"
say ""
printf '%s\n' "$FILES" | while read -r f; do [ -z "$f" ] || [ ! -e "$f" ] || say "  remove     $f"; done
say "  package    unattended-upgrades stays installed (without these files it does nothing)"
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
OLD_IFS=$IFS
IFS='
'
for f in $FILES; do
	IFS=$OLD_IFS
	if [ -z "$f" ] || [ ! -e "$f" ]; then continue; fi
	rm -f -- "$f" || LEFT="$LEFT $f"
	record file "$f"
	say "removed  $f"
done
IFS=$OLD_IFS
rm -f "$MANIFEST"
rmdir "$STATE_DIR" 2>/dev/null || true
[ -z "$LEFT" ] || fail 6 "could not remove:$LEFT"
record status removed
say ""
say "Done: the automatic security updates of the fork are off."
