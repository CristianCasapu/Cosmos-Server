#!/bin/sh
# Installs Fail2ban with the jails of the Cosmos fork on a Debian or Ubuntu server.
#
# Run as root, from a checkout of the fork:
#
#     sh fork/fail2ban/install.sh --dry-run     # shows what it would do, changes nothing
#     sh fork/fail2ban/install.sh               # shows it, asks, then does it
#     sh fork/fail2ban/install.sh --yes         # unattended
#
# What it does, in this order:
#   1. works out the addresses that are never banned (this machine, the
#      private networks, the IPv6 networks of the local link, the address of
#      your SSH session, whatever you add with --ignore, and Cloudflare's
#      address ranges when Cosmos serves an address through Cloudflare);
#   2. writes the fork's filter and jail files under /etc/fail2ban, BEFORE
#      the package is installed: Debian's package starts an sshd jail at
#      once, and these files are what keeps your own network out of it;
#   3. creates the empty file Cosmos writes its security log to, if Cosmos
#      has not made it yet (a jail does not start without its log);
#   4. installs the package "fail2ban" with apt-get from the repositories
#      this system already uses (and, first of all, python3 and nftables when
#      they are missing: the script needs them to work out the list above,
#      and the bans are enforced with nft);
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
#   --record <file>      append one line per item installed to <file>
#   --print-packages     print the packages that would be installed, and stop
#   --ignore <address>   an address or network (CIDR) that is never banned;
#                        may be given several times
#   --sshd               watch SSH too (off by default: the guard and key-only
#                        sign-in protect it better, and a ban of your own
#                        address would lock you out)
#   --no-sshd            do not watch SSH (Debian's own sshd jail is turned off too).
#                        Without either, a machine that has the fork's jails keeps
#                        the choice made before; a new one does not watch SSH
#   --no-cosmos-jails    only watch SSH: leave the jails that read Cosmos' log off
#   --log <path>         Cosmos' security log
#                        (default /var/lib/cosmos/cosmos.security.log)
#   --cosmos-config <path>
#                        Cosmos' configuration, read to see whether addresses
#                        are served through Cloudflare or a reverse proxy
#                        (default /var/lib/cosmos/cosmos.config.json)
#   --behind-proxy       no longer needed, accepted and ignored: Cosmos does
#                        not write visitors a proxy reported to its security
#                        log any more, so the jails are safe behind one
#   --accept-nftables-service
#                        go on although nftables.service is enabled
#   --help               this text
#
# Bans go to a table of their own (inet f2b-cosmos, hook prerouting), whatever
# else filters here (ufw, firewalld, iptables, the fork's guard): they are
# enforced by nftables in the kernel beside the others.
#
# Exit codes: 0 done, 1 failed (nothing changed), 2 bad usage, 3 not applicable
# here, 5 failed and undone, 6 failed and not completely undone, 7 answered no.

set -eu

HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
ETC=/etc/fail2ban
LOG=/var/lib/cosmos/cosmos.security.log
COSMOS_CONFIG=/var/lib/cosmos/cosmos.config.json
BACKUP_ROOT=/var/backups/cosmos-fail2ban

# Never banned unless a later file says otherwise: this machine and the
# networks at home (the same list as jail.d/cosmos-10-jails.local).
BASE_IGNORE="127.0.0.0/8 ::1 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 fc00::/7 fe80::/10"

# Cloudflare's published address ranges (https://www.cloudflare.com/ips/,
# last changed 2023-09-28): the same list as cloudflareBuiltinV4 and
# cloudflareBuiltinV6 in src/utils/fork_cloudflare.go, which a test compares
# with this line. Used when Cosmos serves an address through Cloudflare and
# its configuration holds no newer list.
CLOUDFLARE_RANGES="173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22 2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32"

FILTERS="cosmos-auth cosmos-probe cosmos-scan cosmos-abuse"
COSMOS_JAILS="cosmos-auth cosmos-probe cosmos-scan cosmos-abuse"

DRY_RUN=0
YES=0
WITH_SSHD=""
WITH_COSMOS=1
ACCEPT_NFTABLES_SERVICE=0
EXTRA_IGNORE=""
RECORD=""
PRINT_PACKAGES=0
PIECE=fail2ban
STATE_DIR=/var/lib/cosmos-protection
STATE=$STATE_DIR/$PIECE.state
MANIFEST=$STATE_DIR/$PIECE.manifest

say()  { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
bad_usage() { printf 'ERROR: %s\n' "$*" >&2; exit 2; }
record() { # kind value
	[ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ] || return 0
	printf '%s\t%s\t%s\n' "$PIECE" "$1" "$(printf '%s' "$2" | tr -cd ' -~' | cut -c1-300)" >> "$RECORD"
}
not_applicable() {
	record status "not-applicable $1"
	printf 'Not applicable here: %s\n' "$1" >&2
	exit 3
}
installed() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"; }

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		--sshd) WITH_SSHD=1 ;;
		--no-sshd) WITH_SSHD=0 ;;
		--record) [ $# -ge 2 ] || bad_usage "--record needs a file"; RECORD=$2; shift ;;
		--print-packages) PRINT_PACKAGES=1 ;;
		--no-cosmos-jails) WITH_COSMOS=0 ;;
		--behind-proxy) ;; # no longer needed, see --help
		--accept-nftables-service) ACCEPT_NFTABLES_SERVICE=1 ;;
		--ignore) [ $# -ge 2 ] || bad_usage "--ignore needs an address or a network"; EXTRA_IGNORE="$EXTRA_IGNORE
$2"; shift ;;
		--log) [ $# -ge 2 ] || bad_usage "--log needs a path"; LOG=$2; shift ;;
		--cosmos-config) [ $# -ge 2 ] || bad_usage "--cosmos-config needs a path"; COSMOS_CONFIG=$2; shift ;;
		--help|-h) usage; exit 0 ;;
		*) bad_usage "unknown option: $1 (see --help)" ;;
	esac
	shift
done

# what this script needs before it can work out anything, and what it installs
PREREQ=""
command -v python3 >/dev/null 2>&1 || PREREQ="$PREREQ python3"
command -v nft >/dev/null 2>&1 || PREREQ="$PREREQ nftables"
if [ "$PRINT_PACKAGES" = 1 ]; then
	for p in $PREREQ; do say "$p"; done
	installed fail2ban || say fail2ban
	exit 0
fi
if [ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ]; then
	[ -e "$RECORD" ] || ( umask 077; : > "$RECORD" ) || bad_usage "--record: cannot write $RECORD"
fi

# ---------------------------------------------------------------- checks ---

[ "$(id -u)" = 0 ] || die "run this as root"
OS_ID=""
OS_LIKE=""
if [ -r /etc/os-release ]; then
	OS_ID=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
	OS_LIKE=$(sed -n 's/^ID_LIKE=//p' /etc/os-release | tr -d '"')
fi
case " $OS_ID $OS_LIKE " in
	*" debian "*|*" ubuntu "*) ;;
	*) not_applicable "this is for Debian and Ubuntu (this system says ${OS_ID:-nothing})" ;;
esac
command -v apt-get >/dev/null 2>&1 || not_applicable "apt-get was not found"

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
		not_applicable "nftables.service is enabled: whenever it starts, reloads or stops it empties the whole packet filter ('flush ruleset'), the bans and Docker's rules included. Disable it, or run again with --accept-nftables-service"
	fi
fi

# python3 works out the addresses below, nft enforces the bans: installed
# first when they are missing (a dry run stops here then).
if [ -n "$PREREQ" ]; then
	if [ "$DRY_RUN" = 1 ]; then
		say "Fail2ban for the Cosmos fork: first of all this would install$PREREQ (apt-get),"
		say "which it needs to work out the rest; run the dry run again once they are installed"
		say "to see the whole plan (or install them: apt-get install$PREREQ)."
		say ""
		say "Dry run: nothing was changed."
		exit 0
	fi
	if [ "$YES" != 1 ]; then
		printf 'This needs%s first: install it now? [y/N] ' "$PREREQ"
		read -r answer || answer=""
		case "$answer" in
			y|Y|yes|YES) ;;
			*) say "Nothing was changed."; exit 7 ;;
		esac
	fi
	say "installing$PREREQ ..."
	# shellcheck disable=SC2086
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $PREREQ >/dev/null || die "apt-get could not install$PREREQ: nothing else was changed"
	for p in $PREREQ; do record package "$p"; done
fi
nft list tables >/dev/null 2>&1 || die "nft cannot read the packet filter here: bans could not be enforced"

# SSH: what was chosen before stays, unless an option says otherwise
if [ -z "$WITH_SSHD" ]; then
	WITH_SSHD=0
	if [ -f "$ETC/jail.d/cosmos-50-host.local" ] && awk '
		/^\[/ { in_sshd = ($0 ~ /^\[sshd\][[:space:]]*$/); next }
		in_sshd && $1 == "enabled" && $3 == "true" { found = 1 }
		END { exit !found }' "$ETC/jail.d/cosmos-50-host.local"; then
		WITH_SSHD=1
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

# -------------------------------------------- Cloudflare and reverse proxies ---

# What Cosmos' configuration says about who stands in front of it. Only
# counts and networks are taken from the file, each parsed by Python.
SHIELDED=0
PROXIES=0
CF_RANGES=""
CF_SOURCE=""
if [ -r "$COSMOS_CONFIG" ]; then
	FRONT=$(py '
import ipaddress, json, sys
try:
    http = json.load(open(sys.argv[1])).get("HTTPConfig") or {}
except Exception:
    print("unknown"); sys.exit(0)
cloudflare = http.get("Cloudflare") or {}
shielded = [name for name in (cloudflare.get("Shielded") or []) if isinstance(name, str) and name.strip()]
proxies = [entry for entry in (http.get("TrustedProxies") or []) if str(entry).strip()]

def usable(entries, version):
    # what a list of Cloudflare ranges can be: public networks, not too wide, not too many
    if not isinstance(entries, list) or not 1 <= len(entries) <= 200:
        return None
    networks = []
    for entry in entries:
        try:
            network = ipaddress.ip_network(str(entry).strip(), strict=True)
        except ValueError:
            return None
        if network.version != version or not network.is_global or network.prefixlen < (8 if version == 4 else 16):
            return None
        networks.append(str(network))
    return networks

v4 = usable(cloudflare.get("RangesV4"), 4)
v6 = usable(cloudflare.get("RangesV6"), 6)
stored = " ".join(v4 + v6) if v4 and v6 else ""
print(len(shielded), len(proxies), stored)
' "$COSMOS_CONFIG")
	if [ "$FRONT" != unknown ]; then
		SHIELDED=${FRONT%% *}
		rest=${FRONT#* }
		PROXIES=${rest%% *}
		case "$rest" in
			*" "*) CF_RANGES=${rest#* } ;;
		esac
	fi
fi
if [ "$SHIELDED" != 0 ] && [ "$WITH_COSMOS" = 1 ]; then
	if [ -n "$CF_RANGES" ]; then
		CF_SOURCE="the list Cosmos fetched from Cloudflare"
	else
		CF_RANGES=$CLOUDFLARE_RANGES
		CF_SOURCE="Cloudflare's published list of 2023-09-28, built into this script"
	fi
	# shellcheck disable=SC2086
	CF_RANGES=$(canonical $CF_RANGES | tr '\n' ' ' | sed 's/ $//') || die "Cloudflare's address ranges could not be read"
	IGNORE="$IGNORE $CF_RANGES"
else
	CF_RANGES=""
fi

# The settings file of Cosmos' panel wins over this script's files for the
# fork's jails. Cosmos puts Cloudflare's ranges into it by itself; until it
# has, say so.
CF_PANEL_NOTE=""
if [ -n "$CF_RANGES" ] && [ -f "$PANEL_FILE" ]; then
	status=0
	# shellcheck disable=SC2086
	py '
import ipaddress, sys
wanted = {ipaddress.ip_network(value) for value in sys.argv[2:]}
lists = [line.split("=", 1)[1].split() for line in open(sys.argv[1]) if line.startswith("ignoreip")]
for entries in lists:
    held = set()
    for entry in entries:
        try:
            held.add(ipaddress.ip_network(entry, strict=False))
        except ValueError:
            continue
    if not wanted <= held:
        sys.exit(1)
sys.exit(0 if lists else 1)
' "$PANEL_FILE" $CF_RANGES || status=$?
	if [ "$status" != 0 ]; then
		CF_PANEL_NOTE="the settings file of Cosmos' panel ($PANEL_FILE) is read after this script's files and does not hold Cloudflare's ranges yet. Cosmos adds them by itself within a minute of running (the version of the fork this script came with, or a later one). Check afterwards with: fail2ban-client get cosmos-auth ignoreip"
	fi
fi

# one canonical list, no duplicates, order kept
# shellcheck disable=SC2086
LIST=$(canonical $IGNORE) || die "could not work out the addresses that are never banned"
IGNORE=$(printf '%s\n' "$LIST" | awk '!seen[$0]++' | tr '\n' ' ' | sed 's/ $//')
[ -n "$IGNORE" ] || die "the list of addresses that are never banned came out empty"

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
{
	for name in $FILTERS; do
		printf '%s\t%s\n' "$HERE/filter.d/$name.conf" "$ETC/filter.d/$name.conf"
	done
	printf '%s\t%s\n' "$HERE/jail.d/cosmos-10-jails.local" "$ETC/jail.d/cosmos-10-jails.local"
	printf '%s\t%s\n' "$WORK/cosmos-50-host.local" "$ETC/jail.d/cosmos-50-host.local"
	printf '%s\t%s\n' "$HERE/fail2ban.d/cosmos-fork.local" "$ETC/fail2ban.d/cosmos-fork.local"
} >> "$PLAN"

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
for network in $IGNORE; do
	case " $CF_RANGES " in
		*" $network "*) ;; # listed below, as Cloudflare's
		*) say "    $network" ;;
	esac
done
[ -z "$SESSION_NOTE" ] || say "    ($SESSION_NOTE)"
[ -z "$PANEL_NOTE" ] || warn "$PANEL_NOTE"
say "    (and this machine's own addresses)"
if [ -n "$CF_RANGES" ]; then
	say ""
	say "  never banned: Cloudflare ($SHIELDED address(es) of Cosmos are served through Cloudflare)"
	for network in $CF_RANGES; do say "    $network"; done
	say "    ($CF_SOURCE)"
	say "    Every visitor of an address served through Cloudflare reaches this server from"
	say "    these ranges: a ban of one of them would cut all of them off. Cosmos keeps this"
	say "    list current afterwards, in the settings file it writes for Fail2ban"
	say "    ($ETC/jail.d/cosmos-90-panel.local), and takes it off again when nothing is"
	say "    served through Cloudflare any more."
	[ -z "$CF_PANEL_NOTE" ] || say "    Note: $CF_PANEL_NOTE"
fi
say ""
if [ "$WITH_COSMOS" = 1 ] && { [ "$SHIELDED" != 0 ] || [ "$PROXIES" != 0 ]; }; then
	say "  what a ban does when visitors come through Cloudflare or a reverse proxy"
	if [ "$SHIELDED" != 0 ]; then
		say "    Addresses served directly: a ban cuts the visitor off at the firewall, as always."
		say "    Addresses served through Cloudflare: the firewall never sees those visitors, only"
		say "    Cloudflare. Cosmos does not write them to the log the jails read, so nothing they"
		say "    do leads to a ban here, and nobody can get somebody else's address banned by"
		say "    claiming it. SmartShield still blocks them inside Cosmos, by the address"
		say "    Cloudflare reports; to stop them earlier, block them at Cloudflare."
	fi
	if [ "$PROXIES" != 0 ]; then
		say "    Cosmos trusts $PROXIES reverse proxy address(es) (HTTPConfig.TrustedProxies): visitors"
		say "    who come through them are not written to that log either, for the same reason."
		say "    Whoever connects to this server directly is still banned as usual."
	fi
	say ""
fi
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
		*) say "Nothing was changed."; record status declined; exit 7 ;;
	esac
fi

# ------------------------------------------------------------------ do it ---

STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="$BACKUP_ROOT/$STAMP"

mkdir -p "$ETC/filter.d" "$ETC/jail.d" "$ETC/fail2ban.d"

REINSTALL=0
if [ -f "$STATE" ] || [ -f "$ETC/jail.d/cosmos-10-jails.local" ]; then REINSTALL=1; fi
WROTE="$WORK/wrote"
: > "$WROTE"
: > "$WORK/files"
while IFS="$(printf '\t')" read -r source destination; do
	printf '%s\n' "$destination" >> "$WORK/files"
	case "$(state_of "$source" "$destination")" in
		unchanged) continue ;;
		new) printf 'new\t%s\n' "$destination" >> "$WROTE" ;;
		replace)
			mkdir -p "$BACKUP"
			chmod 700 "$BACKUP_ROOT" "$BACKUP"
			cp -p -- "$destination" "$BACKUP/$(basename -- "$destination")"
			printf 'replaced\t%s\n' "$destination" >> "$WROTE"
			say "saved    $destination -> $BACKUP/"
			;;
	esac
	install -m 0644 -o root -g root -- "$source" "$destination.cosmos-new"
	mv -f -- "$destination.cosmos-new" "$destination"
	say "wrote    $destination"
done < "$PLAN"

# puts the files back as they were before this run
undo_files() {
	while IFS="$(printf '\t')" read -r how file; do
		case "$how" in
			new) rm -f -- "$file" ;;
			replaced) cp -p -- "$BACKUP/$(basename -- "$file")" "$file" ;;
		esac
	done < "$WROTE"
}

LOG_CREATED=0
if [ "$WITH_COSMOS" = 1 ] && [ ! -e "$LOG" ]; then
	( umask 077; : > "$LOG" )
	LOG_CREATED=1
	say "created  $LOG"
fi

if [ "$INSTALLED" != 1 ]; then
	say "installing the package fail2ban ..."
	if ! DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends fail2ban; then
		undo_files
		[ "$LOG_CREATED" = 0 ] || rm -f -- "$LOG"
		record status "undone package-failed"
		printf 'ERROR: %s\n' "apt-get could not install fail2ban: the files written were put back as they were" >&2
		exit 5
	fi
	record package fail2ban
fi

command -v fail2ban-client >/dev/null 2>&1 || { printf 'ERROR: %s\n' "fail2ban-client was not found after the installation" >&2; exit 6; }

say "checking the configuration ..."
if ! fail2ban-client -t; then
	undo_files
	record status "undone config-rejected"
	printf 'ERROR: %s\n' "Fail2ban does not accept the configuration (see above). The service was not reloaded, and the files were put back as they were." >&2
	exit 5
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
		if [ "$tries" -ge 30 ]; then
			printf 'ERROR: %s\n' "Fail2ban did not answer after 30 seconds: see 'systemctl status fail2ban' and /var/log/fail2ban.log (the files are installed)" >&2
			exit 6
		fi
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

# what this piece made, for the panel and for uninstall.sh
mkdir -p "$STATE_DIR"
chmod 755 "$STATE_DIR"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
INSTALLED_AT=$NOW
if [ -f "$STATE" ]; then
	old=$(sed -n 's/^installed_at=//p' "$STATE" | head -n 1)
	case "$old" in [0-9][0-9][0-9][0-9]-*Z) INSTALLED_AT=$old ;; esac
elif [ "$REINSTALL" = 1 ]; then
	# installed by a version of this script that kept no state: since its jail file
	old=$(date -u -r "$ETC/jail.d/cosmos-10-jails.local" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)
	[ -z "$old" ] || INSTALLED_AT=$old
fi
{
	say "piece=$PIECE"
	say "format=1"
	say "installed_at=$INSTALLED_AT"
	say "updated_at=$NOW"
	if [ "$WITH_SSHD" = 1 ]; then say "sshd=on"; else say "sshd=off"; fi
	if [ "$WITH_COSMOS" = 1 ]; then say "cosmos_jails=on"; else say "cosmos_jails=off"; fi
} > "$STATE.new"
chmod 644 "$STATE.new"
mv -f "$STATE.new" "$STATE"
{
	if [ -f "$MANIFEST" ]; then grep '^package	' "$MANIFEST" || true; fi
	for p in $PREREQ; do printf 'package\t%s\n' "$p"; done
	[ "$INSTALLED" = 1 ] || printf 'package\tfail2ban\n'
	while read -r f; do printf 'file\t%s\n' "$f"; done < "$WORK/files"
	printf 'file\t%s\n' "$STATE"
} | awk '!seen[$0]++' > "$MANIFEST.new"
chmod 600 "$MANIFEST.new"
mv -f "$MANIFEST.new" "$MANIFEST"
while IFS="$(printf '\t')" read -r how file; do record file "$file"; done < "$WROTE"
record file "$STATE"
if [ "$REINSTALL" = 1 ]; then record status already-installed; else record status installed; fi

say ""
say "Done. Useful commands:"
say "  fail2ban-client status                 the jails"
say "  fail2ban-client status cosmos-auth     one jail: counts and banned addresses"
say "  fail2ban-client unban <address>        lift the ban of an address in every jail"
say "  fail2ban-client unban --all            lift every ban"
say "  systemctl stop fail2ban                stop Fail2ban: every ban is removed at once"
say "  nft list table inet f2b-cosmos         the bans as the kernel has them (the table exists once there is a ban)"
say "  sh $HERE/uninstall.sh                  undo this installation"
