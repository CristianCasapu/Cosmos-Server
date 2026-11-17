#!/bin/sh
# Installs the host guard of the Cosmos fork on a Debian or Ubuntu server.
#
# Run as root, from the unpacked release or a checkout of the fork:
#
#     sh fork/nftables/install.sh --dry-run    # shows what it would do, changes nothing
#     sh fork/nftables/install.sh              # shows it, asks, applies it with an automatic undo
#     sh fork/nftables/install.sh --yes        # unattended
#
# What the guard does: on the interfaces that carry a default route, a new
# connection from an address outside the home network reaches only Cosmos'
# public port (and the ports given with --port), for the server's own ports and
# for the containers' published ports. Over IPv6 always; over IPv4 only when
# this server has a public IPv4 address itself (behind a home router, IPv4 from
# the internet only arrives through the forwards asked of the router). The home
# network (private ranges, link-local, the prefixes on the outside interfaces),
# loopback, Docker's networks, VPN interfaces and established connections are
# never filtered; the ICMPv6 that IPv6 needs, the DHCP clients and the VPNs'
# own UDP ports (Tailscale, WireGuard) are let through.
#
# It is a table of its own (inet cosmos_host_guard), loaded by a unit of its own
# (cosmos-host-guard.service): it never flushes the ruleset, never uses
# nftables.service, and never touches Docker's, ufw's, firewalld's or
# Fail2ban's rules. A drop in it is final, an accept is not: it can only take
# away what other rules let in. Addresses and routes are followed as they
# change (cosmos-host-guard-watch.service, and a timer as a backstop).
#
# The SSH session that runs it is never cut: its address is allowed to reach
# the SSH port before the table goes live. In an interactive run the rules are
# applied with an automatic undo (a systemd timer): open a NEW SSH connection
# and confirm within the time, or the guard removes itself. An unattended run
# (--yes) from an SSH session that comes from outside the home network is
# refused unless --allow-ssh-from-outside keeps the SSH port open.
#
# Options:
#   --dry-run              show what would be done and stop (exit code as a real run)
#   --yes                  do not ask; no automatic undo (checked with nft -c and
#                          from the inside instead)
#   --record <file>        append one line per item installed to <file>
#   --print-packages       print the packages that would be installed, and stop
#   --cosmos-config <path> Cosmos' configuration, read for its ports
#                          (default /var/lib/cosmos/cosmos.config.json)
#   --https-port <n>       Cosmos' HTTPS port (instead of the configuration's)
#   --http-port <n>        Cosmos' HTTP port (instead of the configuration's)
#   --port <n>[/tcp|/udp]  a port that answers from outside; may be given several
#                          times, and then replaces what the configuration says
#   --home <network>       another network that counts as home (CIDR); repeatable
#   --ipv4 auto|yes|no     filter IPv4 too: auto (default) when the server has a
#                          public IPv4 address on an outside interface
#   --allow-ssh-from-outside
#                          the SSH port answers from outside too
#   --confirm-timeout <s>  seconds before the automatic undo (default 120, 30-900)
#   --accept-nftables-service
#                          go on although nftables.service is enabled
#   --help                 this text
#
# Exit codes: 0 done, 1 failed (nothing changed), 2 bad usage, 3 not applicable
# here, 4 refused for the SSH session's safety, 5 failed or not confirmed and
# undone, 6 failed and not completely undone, 7 answered no.
# Undo it with fork/nftables/uninstall.sh.

set -eu
umask 022

HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
PIECE=nftables
TNAME=cosmos_host_guard
TABLE="inet $TNAME"
ETC=/etc/cosmos-host-guard
SBIN=/usr/local/sbin/cosmos-host-guard
UNIT_DIR=/etc/systemd/system
UNITS="cosmos-host-guard.service cosmos-host-guard-watch.service cosmos-host-guard-refresh.service cosmos-host-guard-refresh.timer"
ENABLE="cosmos-host-guard.service cosmos-host-guard-watch.service cosmos-host-guard-refresh.timer"
STATE_DIR=/var/lib/cosmos-protection
STATE=$STATE_DIR/$PIECE.state
MANIFEST=$STATE_DIR/$PIECE.manifest
RUN_DIR=/run/cosmos-host-guard
BACKUP_ROOT=/var/backups/cosmos-host-guard
UNDO_UNIT=cosmos-host-guard-undo
COSMOS_CONFIG=/var/lib/cosmos/cosmos.config.json

DRY_RUN=0
YES=0
RECORD=""
PRINT_PACKAGES=0
HTTPS_PORT=""
HTTP_PORT=""
PORTS=""
HOMES=""
IPV4=auto
ALLOW_SSH=0
TIMEOUT=120
ACCEPT_NFT_SERVICE=0

say()  { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }

record() { # kind value
	[ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ] || return 0
	printf '%s\t%s\t%s\n' "$PIECE" "$1" "$(printf '%s' "$2" | tr -cd ' -~' | cut -c1-300)" >> "$RECORD"
}
fail() { # code message
	code=$1; shift
	printf 'ERROR: %s\n' "$*" >&2
	exit "$code"
}
not_applicable() {
	record status "not-applicable $1"
	printf 'Not applicable here: %s\n' "$1" >&2
	exit 3
}
usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

port_ok() { case "$1" in ''|*[!0-9]*) return 1 ;; esac; [ "$1" -ge 1 ] && [ "$1" -le 65535 ]; }

while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		--print-packages) PRINT_PACKAGES=1 ;;
		--record) [ $# -ge 2 ] || fail 2 "--record needs a file"; RECORD=$2; shift ;;
		--cosmos-config) [ $# -ge 2 ] || fail 2 "--cosmos-config needs a path"; COSMOS_CONFIG=$2; shift ;;
		--https-port) if [ $# -lt 2 ] || ! port_ok "$2"; then fail 2 "--https-port needs a port number"; fi; HTTPS_PORT=$2; shift ;;
		--http-port) if [ $# -lt 2 ] || ! port_ok "$2"; then fail 2 "--http-port needs a port number"; fi; HTTP_PORT=$2; shift ;;
		--port)
			[ $# -ge 2 ] || fail 2 "--port needs a port"
			case "$2" in
				*/tcp) p=${2%/tcp}; proto=tcp ;;
				*/udp) p=${2%/udp}; proto=udp ;;
				*) p=$2; proto=tcp ;;
			esac
			port_ok "$p" || fail 2 "--port: give a number from 1 to 65535, optionally with /tcp or /udp"
			PORTS="$PORTS $p/$proto"; shift ;;
		--home)
			[ $# -ge 2 ] || fail 2 "--home needs a network"
			case "$2" in
				*[!0-9A-Fa-f.:/]*|'') fail 2 "--home: give a network such as 192.168.1.0/24 or 2001:db8::/48" ;;
			esac
			HOMES="$HOMES $2"; shift ;;
		--ipv4)
			[ $# -ge 2 ] || fail 2 "--ipv4 needs auto, yes or no"
			case "$2" in auto|yes|no) IPV4=$2 ;; *) fail 2 "--ipv4 needs auto, yes or no" ;; esac
			shift ;;
		--allow-ssh-from-outside) ALLOW_SSH=1 ;;
		--confirm-timeout)
			if [ $# -lt 2 ] || ! port_ok "$2" || [ "$2" -lt 30 ] || [ "$2" -gt 900 ]; then fail 2 "--confirm-timeout needs 30 to 900 seconds"; fi
			TIMEOUT=$2; shift ;;
		--accept-nftables-service) ACCEPT_NFT_SERVICE=1 ;;
		--help|-h) usage; exit 0 ;;
		*) fail 2 "unknown option: $1 (see --help)" ;;
	esac
	shift
done

if [ "$PRINT_PACKAGES" = 1 ]; then
	command -v nft >/dev/null 2>&1 || say nftables
	exit 0
fi

if [ -n "$RECORD" ] && [ "$DRY_RUN" = 0 ]; then
	[ -e "$RECORD" ] || ( umask 077; : > "$RECORD" ) || fail 2 "--record: cannot write $RECORD"
fi

# ---------------------------------------------------------------- checks ---

[ "$(id -u)" = 0 ] || fail 1 "run this as root (sudo sh $0)"

for f in cosmos-host-guard cosmos-host-guard.nft.in $UNITS; do
	[ -f "$HERE/$f" ] || fail 1 "missing $HERE/$f: run the script from the unpacked release or a checkout of the fork"
done

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
{ [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; } || not_applicable "systemd is not running: the guard is loaded by a systemd unit"
command -v ip >/dev/null 2>&1 || not_applicable "the command ip (package iproute2) was not found"

# A guard of the same name that this installer did not make is not taken over.
if [ ! -f "$STATE" ]; then
	for unit in $UNITS; do
		[ ! -e "$UNIT_DIR/$unit" ] || not_applicable "$UNIT_DIR/$unit exists but was not made by this installer: remove it first"
	done
	[ ! -e "$ETC" ] || not_applicable "$ETC exists but was not made by this installer: remove it first"
	if command -v nft >/dev/null 2>&1 && nft list table inet "$TNAME" >/dev/null 2>&1; then
		not_applicable "the table $TABLE is loaded but was not made by this installer"
	fi
fi

# nftables.service runs /etc/nftables.conf, which starts with "flush ruleset",
# and empties the whole packet filter when it stops: the guard, Fail2ban's
# bans and Docker's rules would go with every start, reload or stop of it.
NFT_SERVICE_BEFORE=$(systemctl is-enabled nftables.service 2>/dev/null || true)
if [ "$NFT_SERVICE_BEFORE" = enabled ]; then
	if [ "$ACCEPT_NFT_SERVICE" = 1 ]; then
		warn "nftables.service is enabled: when it starts, reloads or stops it empties the whole packet filter, this guard included, until the guard is loaded again (systemctl reload cosmos-host-guard)"
	else
		not_applicable "nftables.service is enabled: whenever it starts, reloads or stops it empties the whole packet filter ('flush ruleset'), this guard, Fail2ban's bans and Docker's rules included. Disable it, or run again with --accept-nftables-service"
	fi
fi

# ufw and firewalld keep their own rules; the guard sits beside them.
OTHERS=""
if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'; then
	OTHERS="$OTHERS ufw"
fi
if [ "$(systemctl is-active firewalld.service 2>/dev/null || true)" = active ]; then
	OTHERS="$OTHERS firewalld"
fi

# ------------------------------------------------------------- the ports ---

config_value() { # key: the first string value of that key in Cosmos' configuration
	if command -v python3 >/dev/null 2>&1; then
		python3 -I -c '
import json, sys
try:
    http = json.load(open(sys.argv[1])).get("HTTPConfig") or {}
except Exception:
    sys.exit(0)
value = http.get(sys.argv[2])
if isinstance(value, str) and len(value) < 100 and value.isprintable():
    print(value)
' "$COSMOS_CONFIG" "$1"
	else
		sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$COSMOS_CONFIG" | head -n 1 | tr -cd 'A-Za-z0-9._-'
	fi
}

CONFIG_NOTE=""
CERT_MODE=""
DNS_PROVIDER=""
CONF_HTTPS=""
CONF_HTTP=""
if [ -r "$COSMOS_CONFIG" ]; then
	CONF_HTTPS=$(config_value HTTPSPort)
	CONF_HTTP=$(config_value HTTPPort)
	CERT_MODE=$(config_value HTTPSCertificateMode)
	DNS_PROVIDER=$(config_value DNSChallengeProvider)
	port_ok "$CONF_HTTPS" || CONF_HTTPS=""
	port_ok "$CONF_HTTP" || CONF_HTTP=""
	CONFIG_NOTE="read from $COSMOS_CONFIG"
fi
[ -n "$HTTPS_PORT" ] || HTTPS_PORT=$CONF_HTTPS
[ -n "$HTTP_PORT" ] || HTTP_PORT=$CONF_HTTP

PUBLIC_TCP=""
PUBLIC_UDP=""
PORT_REASONS=""
add_port() { # port proto reason
	case "$2" in
		tcp) case " $PUBLIC_TCP " in *" $1 "*) return 0 ;; esac; PUBLIC_TCP="$PUBLIC_TCP $1" ;;
		udp) case " $PUBLIC_UDP " in *" $1 "*) return 0 ;; esac; PUBLIC_UDP="$PUBLIC_UDP $1" ;;
	esac
	PORT_REASONS="$PORT_REASONS
    $1/$2  $3"
}
if [ -n "$PORTS" ]; then
	for spec in $PORTS; do add_port "${spec%/*}" "${spec#*/}" "given with --port"; done
else
	if [ "$CERT_MODE" = DISABLED ]; then
		[ -n "$HTTP_PORT" ] && add_port "$HTTP_PORT" tcp "Cosmos (HTTPS is off: its HTTP port is the only one)"
	else
		[ -n "$HTTPS_PORT" ] && add_port "$HTTPS_PORT" tcp "Cosmos' HTTPS port"
		if [ "$CERT_MODE" = LETSENCRYPT ] && [ -z "$DNS_PROVIDER" ] && [ -n "$HTTP_PORT" ]; then
			add_port "$HTTP_PORT" tcp "Cosmos' HTTP port: Let's Encrypt checks the certificate over HTTP (no DNS challenge is set)"
		fi
	fi
fi
[ -n "$PUBLIC_TCP$PUBLIC_UDP" ] || fail 2 "no public port: Cosmos' configuration ($COSMOS_CONFIG) could not be read; give --https-port or --port"

# --------------------------------------------- interfaces, home, session ---

DYNAMIC=$(COSMOS_HOST_GUARD_RUN=/nonexistent sh "$HERE/cosmos-host-guard" show)
dyn() { printf '%s\n' "$DYNAMIC" | sed -n "s/^$1=//p"; }
WAN=$(dyn wan_if)
ONLINK4=$(dyn onlink4)
ONLINK6=$(dyn onlink6)
VPN_UDP=$(dyn vpn_udp)

# in_net <address> <network>...: succeeds when one of the networks holds the address
in_net() {
	awk -v addr="$1" -v nets="$(shift; printf '%s ' "$@")" '
	function bits4(a,   p, i, s, v, b, k) {
		if (split(a, p, ".") != 4) return ""
		s = ""
		for (i = 1; i <= 4; i++) { v = p[i] + 0; b = ""; for (k = 0; k < 8; k++) { b = (v % 2) b; v = int(v / 2) }; s = s b }
		return s
	}
	function hexbits(h,   i, v, b, k, s) {
		h = tolower(h); while (length(h) < 4) h = "0" h
		s = ""
		for (i = 1; i <= 4; i++) { v = index("0123456789abcdef", substr(h, i, 1)) - 1; if (v < 0) return ""; b = ""; for (k = 0; k < 4; k++) { b = (v % 2) b; v = int(v / 2) }; s = s b }
		return s
	}
	function bits6(a,   at, head, tail, hp, tp, nh, nt, i, s, miss) {
		sub(/%.*/, "", a)
		at = index(a, "::")
		if (at) { head = substr(a, 1, at - 1); tail = substr(a, at + 2) } else { head = a; tail = "" }
		nh = (head == "") ? 0 : split(head, hp, ":")
		nt = (tail == "") ? 0 : split(tail, tp, ":")
		if (!at && nh != 8) return ""
		s = ""
		for (i = 1; i <= nh; i++) s = s hexbits(hp[i])
		for (miss = 8 - nh - nt; miss > 0; miss--) s = s "0000000000000000"
		for (i = 1; i <= nt; i++) s = s hexbits(tp[i])
		return length(s) == 128 ? s : ""
	}
	function bits(a) { return index(a, ":") ? bits6(a) : bits4(a) }
	BEGIN {
		a = bits(addr); if (a == "") exit 1
		n = split(nets, list, /[ ,]+/)
		for (i = 1; i <= n; i++) {
			if (list[i] == "") continue
			net = list[i]; len = -1
			if (index(net, "/")) { len = substr(net, index(net, "/") + 1) + 0; net = substr(net, 1, index(net, "/") - 1) }
			b = bits(net)
			if (b == "" || length(b) != length(a)) continue
			if (len < 0) len = length(b)
			if (substr(a, 1, len) == substr(b, 1, len)) exit 0
		}
		exit 1
	}'
}

HOME_STATIC="10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 fe80::/10 fc00::/7"
for net in $HOMES; do
	case "$net" in */*) ;; *) fail 2 "--home: give a network with its length, such as 192.168.1.0/24" ;; esac
	in_net "${net%/*}" "${net%/*}" || fail 2 "--home: not a network: $net"
done

is_public4() { # an IPv4 address that is not private, shared, link-local or loopback
	! in_net "$1" 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 127.0.0.0/8 0.0.0.0/8
}

PUBLIC4=""
old_ifs=$IFS; IFS=,
for dev in $WAN; do
	IFS=$old_ifs
	for a in $(ip -o -4 addr show dev "$dev" scope global 2>/dev/null | awk '{print $4}'); do
		is_public4 "${a%/*}" && PUBLIC4="$PUBLIC4 ${a%/*}"
	done
done
IFS=$old_ifs
case "$IPV4" in
	yes) GUARD4=1; IPV4_WHY="asked for with --ipv4 yes" ;;
	no) GUARD4=0; IPV4_WHY="left alone (--ipv4 no)" ;;
	*)
		if [ -n "$PUBLIC4" ]; then GUARD4=1; IPV4_WHY="this server has a public IPv4 address on an outside interface:$PUBLIC4"
		else GUARD4=0; IPV4_WHY="left alone: this server has no public IPv4 address, IPv4 from the internet only arrives through the router's forwards"
		fi ;;
esac

# The SSH session: SSH_CONNECTION is "client port server port"; under sudo it
# is in a parent process's environment.
SESSION_ADDR=""
SESSION_PORT=""
find_session() {
	conn=${SSH_CONNECTION:-}
	pid=$$
	depth=0
	while [ -z "$conn" ] && [ "$pid" -gt 1 ] 2>/dev/null && [ "$depth" -lt 32 ]; do
		conn=$(tr '\0' '\n' < "/proc/$pid/environ" 2>/dev/null | sed -n 's/^SSH_CONNECTION=//p' | head -n 1)
		pid=$(awk '/^PPid:/ {print $2}' "/proc/$pid/status" 2>/dev/null || echo 0)
		depth=$((depth + 1))
	done
	if [ -n "$conn" ]; then
		# shellcheck disable=SC2086
		set -- $conn
		SESSION_ADDR=${1:-}
		SESSION_PORT=${4:-}
	else
		SESSION_ADDR=$(who -m 2>/dev/null | sed -n 's/.*(\([0-9A-Fa-f.:]*\))[[:space:]]*$/\1/p' | head -n 1)
	fi
	case "$SESSION_ADDR" in
		::ffff:*.*) SESSION_ADDR=${SESSION_ADDR#::ffff:} ;;
	esac
	SESSION_ADDR=${SESSION_ADDR%%%*}
	if [ -n "$SESSION_ADDR" ] && ! in_net "$SESSION_ADDR" "$SESSION_ADDR"; then SESSION_ADDR=""; fi
	if ! port_ok "$SESSION_PORT"; then
		SESSION_PORT=$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2; exit}')
		port_ok "$SESSION_PORT" || SESSION_PORT=22
	fi
}
find_session

# Would the guard filter the session's address? Only if it arrives on an
# outside interface, from outside the home network, in a family that is filtered.
SESSION_FILTERED=0
SESSION_WHERE=""
if [ -n "$SESSION_ADDR" ]; then
	dev=$(ip route get "$SESSION_ADDR" 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "dev") {print $(i + 1); exit}}')
	case ",$WAN," in
		*",$dev,"*)
			family=6
			case "$SESSION_ADDR" in *:*) ;; *) family=4 ;; esac
			# shellcheck disable=SC2086,SC2046
			if [ "$family" = 4 ] && [ "$GUARD4" = 0 ]; then
				SESSION_WHERE="over IPv4, which the guard leaves alone"
			elif in_net "$SESSION_ADDR" $HOME_STATIC $HOMES $(printf '%s' "$ONLINK4,$ONLINK6" | tr ',' ' '); then
				SESSION_WHERE="from the home network"
			else
				SESSION_FILTERED=1
				SESSION_WHERE="from OUTSIDE the home network"
			fi ;;
		*) SESSION_WHERE="through ${dev:-an interface} the guard does not filter (loopback, a VPN or a bridge)" ;;
	esac
fi

SSH_OPEN=0
if [ -n "$SESSION_ADDR" ] && [ "$ALLOW_SSH" = 1 ]; then
	add_port "$SESSION_PORT" tcp "SSH (--allow-ssh-from-outside)"
	SSH_OPEN=1
elif [ "$ALLOW_SSH" = 1 ]; then
	p=$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2; exit}')
	port_ok "$p" || p=22
	add_port "$p" tcp "SSH (--allow-ssh-from-outside)"
	SSH_OPEN=1
fi

# ------------------------------------------------------- the files to write ---

WORK=$(mktemp -d)
# shellcheck disable=SC2317
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
mkdir -p "$WORK/etc"

if [ "$GUARD4" = 1 ]; then
	IPV4_LINE='# IPv4 is filtered too: this server has a public IPv4 address'
else
	IPV4_LINE='meta nfproto ipv4 accept comment "IPv4 reaches this server only through the router'"'"'s forwards"'
fi
sed "s|^\([[:space:]]*\)#@IPV4@\$|\1$IPV4_LINE|" "$HERE/cosmos-host-guard.nft.in" > "$WORK/etc/cosmos-host-guard.nft"
grep -q '#@IPV4@' "$WORK/etc/cosmos-host-guard.nft" && fail 1 "could not fill in the ruleset"

elements() { printf '%s' "$*" | sed 's/^ *//; s/ *$//; s/  */, /g'; }
{
	say "# Written by fork/nftables/install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ): run it again to change it."
	[ -z "$PUBLIC_TCP" ] || say "add element $TABLE public_tcp { $(elements "$PUBLIC_TCP") }"
	[ -z "$PUBLIC_UDP" ] || say "add element $TABLE public_udp { $(elements "$PUBLIC_UDP") }"
	h4=""; h6=""
	for net in $HOMES; do case "$net" in *:*) h6="$h6 $net" ;; *) h4="$h4 $net" ;; esac; done
	[ -z "$h4" ] || say "add element $TABLE home4 { $(elements "$h4") }"
	[ -z "$h6" ] || say "add element $TABLE home6 { $(elements "$h6") }"
	if [ -n "$SESSION_ADDR" ]; then
		say "# the SSH session that ran the installer"
		say "add element $TABLE ssh_port { $SESSION_PORT }"
		case "$SESSION_ADDR" in
			*:*) say "add element $TABLE ssh_allow6 { $SESSION_ADDR }" ;;
			*) say "add element $TABLE ssh_allow4 { $SESSION_ADDR }" ;;
		esac
	fi
} > "$WORK/etc/settings.nft"

cat > "$WORK/etc/README" <<EOF
The host guard of the Cosmos fork (fork/nftables/install.sh).

  nft list table inet cosmos_host_guard        what is loaded, with the counters
  cosmos-host-guard show                       the interfaces, prefixes and VPN ports it follows
  systemctl reload cosmos-host-guard           load it again (atomic, no gap)
  sh <the release>/fork/nftables/uninstall.sh  remove it

Do not edit the files here: run the installer again with other options.
EOF

cp "$HERE/cosmos-host-guard" "$WORK/cosmos-host-guard"

# the whole transaction as a load would send it, for nft -c
transaction() {
	COSMOS_HOST_GUARD_CONF="$WORK/etc" COSMOS_HOST_GUARD_RUN="$WORK/run" sh "$WORK/cosmos-host-guard" "$@"
}

PACKAGES=""
command -v nft >/dev/null 2>&1 || PACKAGES="nftables"

# ------------------------------------------------------------- the plan ---

REINSTALL=0
[ -f "$STATE" ] && REINSTALL=1
INTERACTIVE=1
[ "$YES" = 1 ] && INTERACTIVE=0

say "The host guard of the Cosmos fork: what this will do"
say ""
if [ -n "$PACKAGES" ]; then
	say "  package     apt-get install nftables (from this system's repositories; nftables.service is not enabled)"
else
	say "  package     nftables is installed: left as it is (nftables.service: ${NFT_SERVICE_BEFORE:-not found}, not used)"
fi
if [ "$REINSTALL" = 1 ]; then
	say "  guard       installed already: replaced in one step (no moment without rules)"
fi
say "  table       $TABLE (its own; nothing else is touched)"
say "  outside     ${WAN:-NO interface has a default route now: nothing is filtered until one has}"
say "  answers from outside:$PORT_REASONS"
[ -z "$CONFIG_NOTE" ] || [ -n "$PORTS" ] || say "              (Cosmos' ports $CONFIG_NOTE)"
say "  IPv6        filtered"
say "  IPv4        $( [ "$GUARD4" = 1 ] && echo filtered || echo 'not filtered' ): $IPV4_WHY"
say "  home        $HOME_STATIC${HOMES:+ $HOMES}"
say "              and the prefixes on the outside interfaces now: ${ONLINK4:+$ONLINK4 }${ONLINK6:-(none)}"
say "  always let through: established connections, the ICMPv6 IPv6 needs (errors, packet too"
say "              big, neighbour discovery, router adverts), DHCP and DHCPv6 replies, VPN"
say "              interfaces, VPN ports found now: ${VPN_UDP:-(none)}"
if [ -n "$SESSION_ADDR" ]; then
	say "  SSH         your session comes from $SESSION_ADDR $SESSION_WHERE;"
	say "              that address may reach the SSH port ($SESSION_PORT) from now on"
	[ "$SSH_OPEN" = 1 ] && say "              and the SSH port answers everyone from outside (--allow-ssh-from-outside)"
else
	say "  SSH         no SSH session found (a local console): nothing extra is allowed"
fi
[ -z "$OTHERS" ] || say "  beside      ${OTHERS# } is active and keeps its own rules: both filter, a connection must pass both"
say "  files       $ETC/, $SBIN,"
say "              $UNIT_DIR/: $UNITS"
say "              state in $STATE_DIR/"
if [ "$INTERACTIVE" = 1 ]; then
	say "  safety      the rules are applied with an automatic undo after $TIMEOUT s: open a NEW SSH"
	say "              connection and confirm here before then, or the guard removes itself"
else
	say "  safety      unattended: checked with nft -c first, then from the inside"
fi
say ""

if [ "$SESSION_FILTERED" = 1 ] && [ "$YES" = 1 ] && [ "$ALLOW_SSH" = 0 ]; then
	record status "refused ssh-session-from-outside"
	printf '%s\n' "Refused for your session's safety: this SSH session comes from $SESSION_ADDR, outside the home network, and an unattended run has nobody to confirm that a new connection still works. Install the guard from inside the home network (or a VPN), run it without --yes to confirm by hand, or add --allow-ssh-from-outside to keep the SSH port open." >&2
	exit 4
fi

# nft -c checks the whole transaction without changing anything (when nft is here)
if command -v nft >/dev/null 2>&1; then
	mkdir -p "$WORK/run"
	if ! transaction check 2>"$WORK/check.err"; then
		cat "$WORK/check.err" >&2
		fail 1 "nft does not accept the ruleset: nothing was changed"
	fi
fi

if [ "$DRY_RUN" = 1 ]; then
	say "Dry run: nothing was changed."
	exit 0
fi

if [ "$INTERACTIVE" = 1 ]; then
	printf 'Go on? [y/N] '
	read -r answer || answer=""
	case "$answer" in
		y|Y|yes|YES) ;;
		*) say "Nothing was changed."; record status "declined"; exit 7 ;;
	esac
fi

# ------------------------------------------------------------------ do it ---

if [ -n "$PACKAGES" ]; then
	say "installing the package nftables ..."
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables >/dev/null || fail 1 "apt-get could not install nftables: nothing else was changed"
	record package nftables
	if [ "$NFT_SERVICE_BEFORE" != enabled ] && [ "$(systemctl is-enabled nftables.service 2>/dev/null || true)" = enabled ]; then
		systemctl disable nftables.service >/dev/null 2>&1 || true
		say "disabled nftables.service again (the package had enabled it)"
	fi
	mkdir -p "$WORK/run"
	if ! transaction check 2>"$WORK/check.err"; then
		cat "$WORK/check.err" >&2
		fail 1 "nft does not accept the ruleset: only the package nftables was installed"
	fi
fi

mkdir -p "$RUN_DIR"
chmod 700 "$RUN_DIR"
rm -f "$RUN_DIR/undone" "$RUN_DIR/previous.nft"
if nft list table inet "$TNAME" > "$RUN_DIR/previous.nft" 2>/dev/null; then :; else rm -f "$RUN_DIR/previous.nft"; fi

# The undo puts the table back as it was before this run (or removes it).
# It runs from a systemd timer in an interactive run, so it fires even when
# this session is cut and the script with it.
cat > "$RUN_DIR/undo.sh" <<EOF
#!/bin/sh
# Written by fork/nftables/install.sh: puts $TABLE back as it was before the run.
if [ -s "$RUN_DIR/previous.nft" ]; then
	{ printf 'table $TABLE\ndelete table $TABLE\n'; cat "$RUN_DIR/previous.nft"; } | /usr/sbin/nft -f -
else
	/usr/sbin/nft list table inet "$TNAME" >/dev/null 2>&1 && /usr/sbin/nft delete table inet "$TNAME"
fi
rm -f "$RUN_DIR/dynamic"
: > "$RUN_DIR/undone"
[ ! -s "$RUN_DIR/installer.pid" ] || kill -TERM "\$(cat "$RUN_DIR/installer.pid")" 2>/dev/null
exit 0
EOF
chmod 700 "$RUN_DIR/undo.sh"
NFT=$(command -v nft)
[ "$NFT" = /usr/sbin/nft ] || sed -i "s|/usr/sbin/nft|$NFT|g" "$RUN_DIR/undo.sh"

undo_now() {
	systemctl stop "$UNDO_UNIT.timer" >/dev/null 2>&1 || true
	rm -f "$RUN_DIR/installer.pid"
	sh "$RUN_DIR/undo.sh" || return 1
}

if [ "$INTERACTIVE" = 1 ]; then
	systemctl stop "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
	systemctl reset-failed "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
	echo $$ > "$RUN_DIR/installer.pid"
	systemd-run --quiet --unit="$UNDO_UNIT" --on-active="${TIMEOUT}s" --timer-property=AccuracySec=1s \
		/bin/sh "$RUN_DIR/undo.sh" || fail 1 "could not arm the automatic undo (systemd-run): nothing was changed"
	UNDO_AT=$(date -d "+$TIMEOUT seconds" +%H:%M:%S 2>/dev/null || echo "in $TIMEOUT s")
	trap 'say ""; say "Not confirmed in time: the automatic undo took the guard away again. Nothing else was changed."; record status "undone not-confirmed"; exit 5' TERM
fi

# load it: the same transaction a boot would send, from the files staged here
if ! transaction load 2>"$WORK/load.err"; then
	cat "$WORK/load.err" >&2
	undo_now || fail 6 "loading failed and the table could not be put back: check with: nft list table $TABLE"
	record status "undone load-failed"
	fail 5 "nft could not load the guard: it was put back as it was"
fi

# checked from the inside: the table, its hooks, its counters, the public
# ports and the session's allowance are in the kernel
verify() {
	nft list table inet "$TNAME" >/dev/null 2>&1 || { echo "the table is not loaded"; return 1; }
	for chain in input forward; do
		nft list chain inet "$TNAME" $chain 2>/dev/null | grep -q "hook $chain priority" || { echo "the chain $chain is not hooked"; return 1; }
	done
	for counter in dropped_input dropped_forward; do
		nft list counter inet "$TNAME" $counter 2>/dev/null | grep -q 'packets [0-9]' || { echo "the counter $counter cannot be read"; return 1; }
	done
	for p in $PUBLIC_TCP; do nft get element inet "$TNAME" public_tcp "{ $p }" >/dev/null 2>&1 || { echo "port $p/tcp is not open"; return 1; }; done
	for p in $PUBLIC_UDP; do nft get element inet "$TNAME" public_udp "{ $p }" >/dev/null 2>&1 || { echo "port $p/udp is not open"; return 1; }; done
	if [ -n "$SESSION_ADDR" ]; then
		case "$SESSION_ADDR" in *:*) allow=ssh_allow6 ;; *) allow=ssh_allow4 ;; esac
		nft get element inet "$TNAME" $allow "{ $SESSION_ADDR }" >/dev/null 2>&1 || { echo "the SSH session's address is not allowed"; return 1; }
		nft get element inet "$TNAME" ssh_port "{ $SESSION_PORT }" >/dev/null 2>&1 || { echo "the SSH port is not allowed for the session"; return 1; }
	fi
	if [ -n "$WAN" ]; then
		first=${WAN%%,*}
		nft get element inet "$TNAME" wan_if "{ \"$first\" }" >/dev/null 2>&1 || { echo "the outside interface $first is not in the guard"; return 1; }
	fi
	return 0
}
if ! why=$(verify); then
	undo_now || fail 6 "the check from the inside failed ($why) and the table could not be put back"
	record status "undone check-failed"
	fail 5 "the check from the inside failed ($why): the guard was put back as it was"
fi
say "loaded      $TABLE: checked from the inside (hooks, counters, ports, your session's allowance)"

if [ "$INTERACTIVE" = 1 ]; then
	say ""
	say "The guard is live. Open a NEW SSH connection to this server now. If it works, type yes"
	say "here before $UNDO_AT. If it does not, do nothing: the guard removes itself then."
	printf 'Confirm? [yes/N] '
	answer=""
	read -r answer || answer=""
	trap - TERM
	if [ "$answer" = yes ] || [ "$answer" = y ]; then
		systemctl stop "$UNDO_UNIT.timer" >/dev/null 2>&1 || true
		rm -f "$RUN_DIR/installer.pid"
		if [ -e "$RUN_DIR/undone" ]; then
			record status "undone not-confirmed"
			fail 5 "too late: the automatic undo had already removed the guard. Nothing else was changed."
		fi
	else
		if [ -e "$RUN_DIR/undone" ]; then
			record status "undone not-confirmed"
			fail 5 "not confirmed in time: the automatic undo removed the guard. Nothing else was changed."
		fi
		undo_now || fail 6 "could not take the guard away: nft delete table $TABLE"
		record status "undone not-confirmed"
		fail 5 "not confirmed: the guard was taken away again. Nothing else was changed."
	fi
fi

# --------------------------------------------- make it stay (boot, changes) ---

STAMP=$(date -u +%Y%m%d-%H%M%S)
BACKUP=""
rollback() { # put the files and units back as they were, then the table
	for unit in $ENABLE; do systemctl disable --now "$unit" >/dev/null 2>&1 || true; done
	rm -rf "$ETC" "$SBIN"
	for unit in $UNITS; do rm -f "$UNIT_DIR/$unit"; done
	if [ -n "$BACKUP" ] && [ -d "$BACKUP" ]; then
		[ ! -d "$BACKUP/etc" ] || cp -a "$BACKUP/etc" "$ETC"
		[ ! -f "$BACKUP/cosmos-host-guard" ] || cp -a "$BACKUP/cosmos-host-guard" "$SBIN"
		for unit in $UNITS; do [ ! -f "$BACKUP/$unit" ] || cp -a "$BACKUP/$unit" "$UNIT_DIR/$unit"; done
	fi
	systemctl daemon-reload || true
	if [ "$REINSTALL" = 1 ]; then
		for unit in $ENABLE; do systemctl enable "$unit" >/dev/null 2>&1 || true; done
		systemctl start cosmos-host-guard.service cosmos-host-guard-watch.service cosmos-host-guard-refresh.timer >/dev/null 2>&1 || true
	fi
	sh "$RUN_DIR/undo.sh" >/dev/null 2>&1 || true
}
commit() {
	if [ "$REINSTALL" = 1 ]; then
		BACKUP="$BACKUP_ROOT/$STAMP"
		mkdir -p "$BACKUP"
		chmod 700 "$BACKUP_ROOT" "$BACKUP"
		[ ! -d "$ETC" ] || cp -a "$ETC" "$BACKUP/etc"
		[ ! -f "$SBIN" ] || cp -a "$SBIN" "$BACKUP/cosmos-host-guard"
		for unit in $UNITS; do [ ! -f "$UNIT_DIR/$unit" ] || cp -a "$UNIT_DIR/$unit" "$BACKUP/$unit"; done
	fi
	mkdir -p "$ETC"
	chmod 700 "$ETC"
	for f in cosmos-host-guard.nft settings.nft README; do
		install -m 0600 -o root -g root "$WORK/etc/$f" "$ETC/$f.new" && mv -f "$ETC/$f.new" "$ETC/$f" || return 1
	done
	install -m 0755 -o root -g root "$WORK/cosmos-host-guard" "$SBIN.new" && mv -f "$SBIN.new" "$SBIN" || return 1
	for unit in $UNITS; do
		install -m 0644 -o root -g root "$HERE/$unit" "$UNIT_DIR/$unit.new" && mv -f "$UNIT_DIR/$unit.new" "$UNIT_DIR/$unit" || return 1
	done
	systemctl daemon-reload || return 1
	for unit in $ENABLE; do systemctl enable "$unit" >/dev/null 2>&1 || return 1; done
	# start, or reload when it runs already: both send the table in one
	# transaction over the one loaded above, never a moment without it
	if systemctl is-active --quiet cosmos-host-guard.service; then
		systemctl reload cosmos-host-guard.service || return 1
	else
		systemctl start cosmos-host-guard.service || return 1
	fi
	systemctl restart cosmos-host-guard-watch.service || return 1
	systemctl restart cosmos-host-guard-refresh.timer || return 1
	return 0
}
if ! commit; then
	rollback
	if [ "$REINSTALL" = 0 ] && nft list table inet "$TNAME" >/dev/null 2>&1; then
		fail 6 "could not install the units, and the table is still loaded: nft delete table $TABLE"
	fi
	record status "undone units-failed"
	fail 5 "could not install or start the units: everything was put back as it was"
fi
if ! why=$(verify) || ! systemctl is-active --quiet cosmos-host-guard-watch.service; then
	rollback
	record status "undone check-failed"
	fail 5 "after starting the units the check from the inside failed (${why:-the watcher is not running}): everything was put back as it was"
fi
rm -f "$RUN_DIR/previous.nft" "$RUN_DIR/undo.sh" "$RUN_DIR/installer.pid" "$RUN_DIR/undone"

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
	say "public_tcp=$(elements "$PUBLIC_TCP" | tr -d ' ')"
	say "public_udp=$(elements "$PUBLIC_UDP" | tr -d ' ')"
	say "ipv4=$( [ "$GUARD4" = 1 ] && echo yes || echo no )"
	say "ssh_open=$( [ "$SSH_OPEN" = 1 ] && echo yes || echo no )"
} > "$STATE.new"
chmod 644 "$STATE.new"
mv -f "$STATE.new" "$STATE"
{
	if [ -z "$PACKAGES" ] && [ -f "$MANIFEST" ]; then grep '^package	' "$MANIFEST" || true; fi
	[ -z "$PACKAGES" ] || printf 'package\tnftables\n'
	printf 'table\t%s\n' "$TABLE"
	for f in cosmos-host-guard.nft settings.nft README; do printf 'file\t%s\n' "$ETC/$f"; done
	printf 'file\t%s\n' "$SBIN"
	for unit in $UNITS; do printf 'unit\t%s\n' "$unit"; done
	printf 'file\t%s\n' "$STATE"
} > "$MANIFEST.new"
chmod 600 "$MANIFEST.new"
mv -f "$MANIFEST.new" "$MANIFEST"

record table "$TABLE"
for f in cosmos-host-guard.nft settings.nft README; do record file "$ETC/$f"; done
record file "$SBIN"
for unit in $UNITS; do record unit "$unit"; done
record file "$STATE"
if [ "$REINSTALL" = 1 ]; then record status already-installed; else record status installed; fi

say ""
say "Done: the guard is installed, loaded and follows address changes."
dropped=$(nft list counter inet "$TNAME" dropped_input 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p')
say "  nft list table $TABLE      the rules and counters (dropped so far: ${dropped:-0})"
say "  systemctl status cosmos-host-guard"
say "  sh $HERE/uninstall.sh      remove it"
[ -z "$BACKUP" ] || say "  the files it replaced are in $BACKUP"
exit 0
