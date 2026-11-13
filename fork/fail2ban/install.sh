#!/bin/sh
# Installs Fail2ban with the jails of the Cosmos fork on a Debian server.
#
# Run as root, from a checkout of the fork:
#
#     sh fork/fail2ban/install.sh --dry-run     # shows what it would do, changes nothing
#     sh fork/fail2ban/install.sh               # shows it, asks, then does it
#
# What it does, in this order:
#   1. works out the addresses that are never banned (this machine, the
#      private networks, the IPv6 networks of the local link, the address of
#      your SSH session, whatever you add with --ignore);
#   2. writes the fork's filter and jail files under /etc/fail2ban, BEFORE
#      the package is installed: Debian's package starts an sshd jail at
#      once, and these files are what keeps your own network out of it;
#   3. creates the empty file Cosmos writes its security log to, if Cosmos
#      has not made it yet (a jail does not start without its log);
#   4. installs the package "fail2ban" with apt-get from the repositories
#      this system already uses (nothing else is downloaded);
#   5. checks the configuration, enables the service and (re)loads it;
#   6. shows the jails that are running.
#
# It can be run again at any time: files that are already as they should be
# are left alone, files it replaces are copied to
# /var/backups/cosmos-fail2ban/<time>/ first. uninstall.sh undoes it.
#
# Options:
#   --dry-run            show what would be done and stop
#   --yes                do not ask before changing
#   --ignore <address>   an address or network (CIDR) that is never banned;
#                        may be given several times
#   --no-sshd            do not watch SSH (Debian's own sshd jail is turned off too)
#   --no-cosmos-jails    only watch SSH: leave the jails that read Cosmos' log off
#   --behind-proxy       install the Cosmos jails although Cosmos is set up
#                        behind a reverse proxy (read the warning it prints)
#   --log <path>         Cosmos' security log
#                        (default /var/lib/cosmos/cosmos.security.log)
#   --cosmos-config <path>
#                        Cosmos' configuration, read to see whether a reverse
#                        proxy is set up (default /var/lib/cosmos/cosmos.config.json)
#   --accept-nftables-service
#                        go on although nftables.service is enabled
#   --help               this text

set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ETC=/etc/fail2ban
LOG=/var/lib/cosmos/cosmos.security.log
COSMOS_CONFIG=/var/lib/cosmos/cosmos.config.json
BACKUP_ROOT=/var/backups/cosmos-fail2ban

# Never banned unless a later file says otherwise: this machine and the
# networks at home (the same list as jail.d/cosmos-10-jails.local).
BASE_IGNORE="127.0.0.0/8 ::1 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 fc00::/7 fe80::/10"

FILTERS="cosmos-auth cosmos-probe cosmos-scan cosmos-abuse"
COSMOS_JAILS="cosmos-auth cosmos-probe cosmos-scan cosmos-abuse"

DRY_RUN=0
YES=0
WITH_SSHD=1
WITH_COSMOS=1
BEHIND_PROXY=0
ACCEPT_NFTABLES_SERVICE=0
EXTRA_IGNORE=""

say()  { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		--no-sshd) WITH_SSHD=0 ;;
		--no-cosmos-jails) WITH_COSMOS=0 ;;
		--behind-proxy) BEHIND_PROXY=1 ;;
		--accept-nftables-service) ACCEPT_NFTABLES_SERVICE=1 ;;
		--ignore) [ $# -ge 2 ] || die "--ignore needs an address or a network"; EXTRA_IGNORE="$EXTRA_IGNORE
$2"; shift ;;
		--log) [ $# -ge 2 ] || die "--log needs a path"; LOG=$2; shift ;;
		--cosmos-config) [ $# -ge 2 ] || die "--cosmos-config needs a path"; COSMOS_CONFIG=$2; shift ;;
		--help|-h) usage; exit 0 ;;
		*) die "unknown option: $1 (see --help)" ;;
	esac
	shift
done

# ---------------------------------------------------------------- checks ---

[ "$(id -u)" = 0 ] || die "run this as root"
command -v apt-get >/dev/null 2>&1 || die "this script is for Debian (apt-get was not found)"
command -v python3 >/dev/null 2>&1 || die "python3 was not found (Debian installs it by default; Fail2ban needs it too)"
command -v nft >/dev/null 2>&1 || die "the command nft was not found: install the package nftables first (apt-get install nftables)"
nft list tables >/dev/null 2>&1 || die "nft cannot read the packet filter here: bans could not be enforced"

for name in $FILTERS; do
	[ -f "$HERE/filter.d/$name.conf" ] || die "missing $HERE/filter.d/$name.conf: run the script from a checkout of the fork"
done
[ -f "$HERE/jail.d/cosmos-10-jails.local" ] || die "missing $HERE/jail.d/cosmos-10-jails.local"
[ -f "$HERE/fail2ban.d/cosmos-fork.local" ] || die "missing $HERE/fail2ban.d/cosmos-fork.local"

# The log's path ends up in a configuration file: plain characters only.
case "$LOG" in
	/*) ;;
	*) die "--log must be an absolute path" ;;
esac
case "$LOG" in
	*[!A-Za-z0-9._/-]*) die "--log may only contain letters, digits, dots, dashes, underscores and slashes" ;;
esac
if [ "$WITH_COSMOS" = 1 ] && [ ! -d "$(dirname -- "$LOG")" ]; then
	die "the folder of $LOG does not exist: is Cosmos installed? (use --log, or --no-cosmos-jails)"
fi

# nftables.service loads /etc/nftables.conf, which starts with "flush
# ruleset", and runs "nft flush ruleset" when it stops: every start, reload
# or stop of it would remove Fail2ban's bans (and Docker's rules) from the
# kernel. Rules of your own belong in a table of their own, loaded by a unit
# of their own, not in that service.
if command -v systemctl >/dev/null 2>&1 && [ "$(systemctl is-enabled nftables.service 2>/dev/null || true)" = enabled ]; then
	if [ "$ACCEPT_NFTABLES_SERVICE" = 1 ]; then
		warn "nftables.service is enabled: when it starts or reloads it empties the packet filter, bans included, until Fail2ban is restarted"
	else
		die "nftables.service is enabled: whenever it starts, reloads or stops it empties the whole packet filter ('flush ruleset'), the bans and Docker's rules included. Disable it, or run again with --accept-nftables-service"
	fi
fi

# ------------------------------------------- addresses that are never banned ---

# All address arithmetic is done by Python's ipaddress module; nothing below
# is evaluated by the shell.
py() { python3 -I -c "$@"; }

# prints its arguments as canonical networks, one per line; fails on anything else
canonical() {
	py '
import ipaddress, sys
for value in sys.argv[1:]:
    try:
        network = ipaddress.ip_network(value, strict=False)
    except ValueError:
        sys.exit("not an address or a network: %r" % value)
    print(network.network_address if network.num_addresses == 1 else network)
' "$@"
}

# covered <address> <network>...: succeeds when one of the networks holds the address
covered() {
	py '
import ipaddress, sys
try:
    address = ipaddress.ip_address(sys.argv[1])
except ValueError:
    sys.exit(2)
if getattr(address, "ipv4_mapped", None):
    address = address.ipv4_mapped
for value in sys.argv[2:]:
    network = ipaddress.ip_network(value, strict=False)
    if address.version == network.version and address in network:
        sys.exit(0)
sys.exit(1)
' "$@"
}

# the public IPv6 networks of the local link: devices at home reach the
# server with addresses from them
onlink6() {
	command -v ip >/dev/null 2>&1 || return 0
	ip -o -6 addr show scope global 2>/dev/null | awk '{print $4}' | py '
import ipaddress, sys
seen = set()
for line in sys.stdin:
    try:
        network = ipaddress.ip_interface(line.strip()).network
    except ValueError:
        continue
    if network.prefixlen < 48 or network.prefixlen > 64 or network.is_private or network in seen:
        continue
    seen.add(network)
    print(network)
'
}

IGNORE=$BASE_IGNORE
ONLINK=$(onlink6 | tr '\n' ' ')
[ -z "$ONLINK" ] || IGNORE="$IGNORE $ONLINK"

# each --ignore value is one line of EXTRA_IGNORE and is checked on its own:
# whatever is not exactly one address or network stops the script
if [ -n "$EXTRA_IGNORE" ]; then
	OLD_IFS=$IFS
	IFS='
'
	for value in $EXTRA_IGNORE; do
		IFS=$OLD_IFS
		[ -n "$value" ] || continue
		one=$(canonical "$value") || die "--ignore: give an address (203.0.113.7) or a network (203.0.113.0/24)"
		IGNORE="$IGNORE $one"
	done
	IFS=$OLD_IFS
fi

# The address this session comes from must never be banned by what is
# installed here.
SESSION=""
if [ -n "${SSH_CONNECTION:-}" ]; then
	SESSION=${SSH_CONNECTION%% *}
elif [ -n "${SSH_CLIENT:-}" ]; then
	SESSION=${SSH_CLIENT%% *}
else
	SESSION=$(who -m 2>/dev/null | sed -n 's/.*(\([0-9A-Fa-f.:]*\))[[:space:]]*$/\1/p' | head -n 1)
fi
SESSION_NOTE=""
if [ -n "$SESSION" ]; then
	status=0
	# shellcheck disable=SC2086
	covered "$SESSION" $IGNORE || status=$?
	case "$status" in
		0) SESSION_NOTE="your SSH session ($SESSION) is inside the networks that are never banned" ;;
		1)
			IGNORE="$IGNORE $(canonical "$SESSION")"
			SESSION_NOTE="your SSH session comes from $SESSION, which is not a local address: it is added to the addresses that are never banned"
			;;
		*) ;; # not an address: nothing to protect
	esac
fi

# Settings saved in Cosmos' panel are read after this script's files and
# replace the list for the fork's jails: if they are there and do not hold
# this session's address, saying "it is added" would not be true.
PANEL_FILE="$ETC/jail.d/cosmos-90-panel.local"
PANEL_NOTE=""
if [ -n "$SESSION" ] && [ -f "$PANEL_FILE" ]; then
	status=0
	py '
import ipaddress, sys
try:
    address = ipaddress.ip_address(sys.argv[1])
except ValueError:
    sys.exit(0)
if getattr(address, "ipv4_mapped", None):
    address = address.ipv4_mapped
lists = [line.split("=", 1)[1].split() for line in open(sys.argv[2]) if line.startswith("ignoreip")]
for entries in lists:
    covered = False
    for entry in entries:
        try:
            network = ipaddress.ip_network(entry, strict=False)
        except ValueError:
            continue
        if network.version == address.version and address in network:
            covered = True
    if not covered:
        sys.exit(1)
' "$SESSION" "$PANEL_FILE" || status=$?
	if [ "$status" = 1 ]; then
		PANEL_NOTE="the settings saved in Cosmos' panel ($PANEL_FILE) replace this list for the fork's jails and do NOT hold your SSH session's address ($SESSION): add it in the panel (Addresses that are never banned), or it can be banned"
	fi
fi

# one canonical list, no duplicates, order kept
# shellcheck disable=SC2086
LIST=$(canonical $IGNORE) || die "could not work out the addresses that are never banned"
IGNORE=$(printf '%s\n' "$LIST" | awk '!seen[$0]++' | tr '\n' ' ' | sed 's/ $//')
[ -n "$IGNORE" ] || die "the list of addresses that are never banned came out empty"

# ------------------------------------------------------- reverse proxy check ---

PROXIES=unknown
if [ -r "$COSMOS_CONFIG" ]; then
	PROXIES=$(py '
import json, sys
try:
    config = json.load(open(sys.argv[1]))
    proxies = (config.get("HTTPConfig") or {}).get("TrustedProxies") or []
    print(len([p for p in proxies if str(p).strip()]))
except Exception:
    print("unknown")
' "$COSMOS_CONFIG")
fi
if [ "$WITH_COSMOS" = 1 ] && [ "$PROXIES" != unknown ] && [ "$PROXIES" != 0 ]; then
	if [ "$BEHIND_PROXY" = 1 ]; then
		warn "Cosmos trusts $PROXIES reverse proxy address(es) (HTTPConfig.TrustedProxies)."
		warn "Visitors who come through the proxy cannot be blocked by this server's firewall, and a"
		warn "proxy that passes on the X-Forwarded-For header a client sent lets that client choose"
		warn "which address gets banned. The Cosmos jails are installed because you asked for it."
	else
		die "Cosmos is set up behind a reverse proxy (HTTPConfig.TrustedProxies has $PROXIES entr(ies)): bans made here would not stop visitors who come through it, and could be aimed at other addresses. Run again with --no-cosmos-jails (SSH only) or, knowing this, with --behind-proxy"
	fi
fi

# -------------------------------------------------------------- what to write ---

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

{
	say "# Written by fork/fail2ban/install.sh for this server: run it again to change it."
	say "# Read after cosmos-10-jails.local and before cosmos-90-panel.local (the panel's file)."
	say ""
	say "[DEFAULT]"
	say "ignoreip = $IGNORE"
	say "cosmos_security_log = $LOG"
	say ""
	say "[sshd]"
	if [ "$WITH_SSHD" = 1 ]; then say "enabled = true"; else say "enabled = false"; fi
	for jail in $COSMOS_JAILS; do
		say ""
		say "[$jail]"
		if [ "$WITH_COSMOS" = 1 ]; then say "enabled = true"; else say "enabled = false"; fi
	done
} > "$WORK/cosmos-50-host.local"

# source<TAB>destination, one per line
PLAN="$WORK/plan"
: > "$PLAN"
for name in $FILTERS; do
	printf '%s\t%s\n' "$HERE/filter.d/$name.conf" "$ETC/filter.d/$name.conf" >> "$PLAN"
done
printf '%s\t%s\n' "$HERE/jail.d/cosmos-10-jails.local" "$ETC/jail.d/cosmos-10-jails.local" >> "$PLAN"
printf '%s\t%s\n' "$WORK/cosmos-50-host.local" "$ETC/jail.d/cosmos-50-host.local" >> "$PLAN"
printf '%s\t%s\n' "$HERE/fail2ban.d/cosmos-fork.local" "$ETC/fail2ban.d/cosmos-fork.local" >> "$PLAN"

state_of() { # source destination
	if [ ! -e "$2" ]; then echo "new"
	elif cmp -s "$1" "$2"; then echo "unchanged"
	else echo "replace"
	fi
}

INSTALLED=0
if dpkg-query -W -f='${Status}' fail2ban 2>/dev/null | grep -q "install ok installed"; then INSTALLED=1; fi
HAVE_SYSTEMD=0
[ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 && HAVE_SYSTEMD=1

say "Fail2ban for the Cosmos fork: what this will do"
say ""
if [ "$INSTALLED" = 1 ]; then
	say "  package    fail2ban is installed ($(dpkg-query -W -f='${Version}' fail2ban)): left as it is"
else
	say "  package    apt-get install fail2ban ($(apt-cache policy fail2ban 2>/dev/null | sed -n 's/^ *Candidate: //p'), from this system's repositories)"
fi
say ""
say "  files"
while IFS="$(printf '\t')" read -r source destination; do
	say "    $(printf '%-9s' "$(state_of "$source" "$destination")") $destination"
done < "$PLAN"
if [ "$WITH_COSMOS" = 1 ]; then
	if [ -e "$LOG" ]; then say "    unchanged $LOG (Cosmos' security log)"
	else say "    new       $LOG (empty, mode 0600: Cosmos appends to it)"
	fi
fi
if [ -e "$ETC/jail.d/cosmos-90-panel.local" ]; then
	say "    kept      $ETC/jail.d/cosmos-90-panel.local (the settings made in Cosmos' panel; they win over the files above)"
fi
say "    backups of replaced files go to $BACKUP_ROOT/<time>/"
say ""
say "  jails"
if [ "$WITH_COSMOS" = 1 ]; then
	say "    cosmos-auth    5 failed logins, codes or tokens in 1 hour        -> banned 4 hours"
	say "    cosmos-probe   3 requests for scanner paths in 1 hour            -> banned 24 hours"
	say "    cosmos-scan    10 unknown host names or refused bots in 10 min   -> banned 4 hours"
	say "    cosmos-abuse   3 SmartShield strikes or bans in 24 hours         -> banned 4 hours"
else
	say "    (the jails that read Cosmos' log are left off)"
fi
if [ "$WITH_SSHD" = 1 ]; then
	say "    sshd           5 failed SSH logins in 10 minutes                 -> banned 1 hour"
else
	say "    (SSH is not watched)"
fi
say "    recidive       3 bans in 24 hours, by any jail                   -> banned 1 week"
say "    A ban drops everything from the address: Cosmos, SSH, file sharing and the"
say "    published ports of the containers (table inet f2b-cosmos, hook prerouting)."
say "    Thresholds can be changed afterwards in Cosmos' panel."
say ""
say "  never banned"
for network in $IGNORE; do say "    $network"; done
[ -z "$SESSION_NOTE" ] || say "    ($SESSION_NOTE)"
[ -z "$PANEL_NOTE" ] || warn "$PANEL_NOTE"
say "    (and this machine's own addresses)"
say ""
if [ "$HAVE_SYSTEMD" = 1 ]; then
	say "  service    fail2ban is enabled at boot and (re)loaded"
else
	say "  service    systemd is not running here: the service is not touched, start Fail2ban yourself"
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

# ------------------------------------------------------------------ do it ---

STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="$BACKUP_ROOT/$STAMP"

mkdir -p "$ETC/filter.d" "$ETC/jail.d" "$ETC/fail2ban.d"

while IFS="$(printf '\t')" read -r source destination; do
	case "$(state_of "$source" "$destination")" in
		unchanged) continue ;;
		replace)
			mkdir -p "$BACKUP"
			chmod 700 "$BACKUP_ROOT" "$BACKUP"
			cp -p -- "$destination" "$BACKUP/$(basename -- "$destination")"
			say "saved    $destination -> $BACKUP/"
			;;
	esac
	install -m 0644 -o root -g root -- "$source" "$destination.cosmos-new"
	mv -f -- "$destination.cosmos-new" "$destination"
	say "wrote    $destination"
done < "$PLAN"

if [ "$WITH_COSMOS" = 1 ] && [ ! -e "$LOG" ]; then
	( umask 077; : > "$LOG" )
	say "created  $LOG"
fi

if [ "$INSTALLED" != 1 ]; then
	say "installing the package fail2ban ..."
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends fail2ban
fi

command -v fail2ban-client >/dev/null 2>&1 || die "fail2ban-client was not found after the installation"

say "checking the configuration ..."
if ! fail2ban-client -t; then
	die "Fail2ban does not accept the configuration (see above). The service was not reloaded. Replaced files, if any, are in $BACKUP"
fi

if [ "$HAVE_SYSTEMD" = 1 ]; then
	systemctl enable fail2ban.service >/dev/null 2>&1 || warn "could not enable fail2ban.service"
	if systemctl is-active --quiet fail2ban.service; then
		# a restart, not a reload: jails that were turned off must stop, and
		# the ban action may have changed. Bans are put back from the database.
		systemctl restart fail2ban.service
	else
		systemctl start fail2ban.service
	fi
	tries=0
	until fail2ban-client ping >/dev/null 2>&1; do
		tries=$((tries + 1))
		[ "$tries" -lt 30 ] || die "Fail2ban did not answer after 30 seconds: see 'systemctl status fail2ban' and /var/log/fail2ban.log"
		sleep 1
	done
	say ""
	fail2ban-client status
	EXPECTED="recidive"
	[ "$WITH_SSHD" = 1 ] && EXPECTED="$EXPECTED sshd"
	[ "$WITH_COSMOS" = 1 ] && EXPECTED="$EXPECTED $COSMOS_JAILS"
	LOADED=$(fail2ban-client status | sed -n 's/.*Jail list:[[:space:]]*//p' | tr -d ',')
	for jail in $EXPECTED; do
		case " $LOADED " in
			*" $jail "*) ;;
			*) warn "the jail $jail is not running: see /var/log/fail2ban.log" ;;
		esac
	done
elif fail2ban-client ping >/dev/null 2>&1; then
	fail2ban-client reload >/dev/null
	say ""
	say "systemd is not running here; the Fail2ban that is running was told to reload."
	fail2ban-client status
else
	say ""
	say "systemd is not running here. Start Fail2ban yourself:  fail2ban-server -xf start"
fi

say ""
say "Done. Useful commands:"
say "  fail2ban-client status                 the jails"
say "  fail2ban-client status cosmos-auth     one jail: counts and banned addresses"
say "  fail2ban-client unban <address>        lift the ban of an address in every jail"
say "  fail2ban-client unban --all            lift every ban"
say "  systemctl stop fail2ban                stop Fail2ban: every ban is removed at once"
say "  nft list table inet f2b-cosmos         the bans as the kernel has them (the table exists once there is a ban)"
say "  sh $HERE/uninstall.sh                  undo this installation"
