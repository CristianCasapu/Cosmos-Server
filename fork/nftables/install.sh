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
# for the containers' published ports. Over IPv6 always; over IPv4 when this
# server has a public or carrier-grade (100.64/10) IPv4 address itself, or with
# --ipv4 yes / --exposed (behind a home router, IPv4 from the internet only
# arrives through the forwards asked of the router, unless the server is the
# router's exposed host). Loopback, Docker's networks, VPN interfaces and
# established connections are never filtered; the ICMPv6 that IPv6 needs, the
# DHCP clients and the VPN's own UDP port (tailscaled.service, kernel WireGuard)
# are let through.
#
# What counts as home: on an outside interface that is a home network behind a
# router (a private IPv4 address there and the default route through a private
# gateway), the prefixes on it and link-local sources; anywhere, what --home
# names. On an interface with a public address, without IPv4, or with --exposed
# the segment belongs to the provider: nothing there counts as home but --home.
# Forged private sources from outside are dropped.
#
# It is a table of its own (inet cosmos_host_guard), loaded by a unit of its own
# (cosmos-host-guard.service): it never flushes the ruleset, never uses
# nftables.service, and never touches Docker's, ufw's, firewalld's or
# Fail2ban's rules. A drop in it is final, an accept is not: it can only take
# away what other rules let in. Addresses and routes are followed as they
# change (cosmos-host-guard-watch.service, and a timer as a backstop).
#
# SSH sessions are never cut: before the table goes live it looks at sshd's open,
# signed-in sessions (the sockets, not the environment). In an interactive run
# each one from outside the home network may reach the SSH port for 24 hours
# (--ssh-allowance-hours), and the rules are applied with an automatic undo (a
# systemd timer): open a NEW SSH connection and confirm within the time, or the
# guard removes itself. An unattended run (--yes) while such a session is open is
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
#                          public or carrier-grade IPv4 address on an outside
#                          interface; yes when it is the router's exposed host (DMZ)
#   --exposed              a cloud server, or one whose outside interface is shared
#                          with strangers: IPv4 filtered, and nothing on the outside
#                          interfaces counts as home but --home
#   --ssh-allowance-hours <h>
#                          how long an outside SSH session's address may reach the
#                          SSH port (default 24, 1-720)
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
EXPOSED=0
ALLOW_HOURS=24
CHANGED_PKG=0

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
# failed before the rules were touched: 1 when nothing changed, 6 when a package
# was installed by this run (kept, and said)
fail_early() {
	if [ "$CHANGED_PKG" = 1 ]; then
		fail 6 "$* (the package nftables was installed by this run and is kept; nothing else changed)"
	fi
	fail 1 "$*: nothing was changed"
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
			HOMES="$HOMES $2"; shift ;;
		--exposed) EXPOSED=1 ;;
		--ssh-allowance-hours)
			if [ $# -lt 2 ] || ! port_ok "$2" || [ "$2" -gt 720 ]; then fail 2 "--ssh-allowance-hours needs 1 to 720"; fi
			ALLOW_HOURS=$2; shift ;;
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

# ------------------------------------------------ addresses, strictly ---

# valid_addr <address>: exactly one IPv4 or IPv6 address, nothing else
valid_addr() {
	printf '%s\n' "$1" | awk '
	function v4(a,   p, n, i) {
		n = split(a, p, ".")
		if (n != 4) return 0
		for (i = 1; i <= 4; i++) if (p[i] !~ /^(0|[1-9][0-9]?|1[0-9][0-9]|2[0-4][0-9]|25[0-5])$/) return 0
		return 1
	}
	function v6(a,   at, n, g, i, parts) {
		if (a !~ /^[0-9a-fA-F:]+$/ || length(a) > 39) return 0
		at = index(a, "::")
		if (at && index(substr(a, at + 2), "::")) return 0
		if (a ~ /:::/) return 0
		if (!at && (a ~ /^:/ || a ~ /:$/)) return 0
		n = split(a, parts, ":")
		g = 0
		for (i = 1; i <= n; i++) {
			if (parts[i] == "") continue
			if (length(parts[i]) > 4) return 0
			g++
		}
		return at ? g <= 7 : g == 8
	}
	NR == 1 { ok = index($0, ":") ? v6($0) : v4($0) }
	END { exit !(NR == 1 && ok) }'
}

# bits <address>: the address as a string of 0 and 1 (valid addresses only)
bits() {
	awk -v a="$1" '
	function hexbits(h,   i, v, b, k, s) {
		h = tolower(h); while (length(h) < 4) h = "0" h
		s = ""
		for (i = 1; i <= 4; i++) { v = index("0123456789abcdef", substr(h, i, 1)) - 1; b = ""; for (k = 0; k < 4; k++) { b = (v % 2) b; v = int(v / 2) }; s = s b }
		return s
	}
	BEGIN {
		if (!index(a, ":")) {
			split(a, p, "."); s = ""
			for (i = 1; i <= 4; i++) { v = p[i] + 0; b = ""; for (k = 0; k < 8; k++) { b = (v % 2) b; v = int(v / 2) }; s = s b }
			print s; exit
		}
		at = index(a, "::")
		if (at) { head = substr(a, 1, at - 1); tail = substr(a, at + 2) } else { head = a; tail = "" }
		nh = (head == "") ? 0 : split(head, hp, ":")
		nt = (tail == "") ? 0 : split(tail, tp, ":")
		s = ""
		for (i = 1; i <= nh; i++) s = s hexbits(hp[i])
		for (miss = 8 - nh - nt; miss > 0; miss--) s = s "0000000000000000"
		for (i = 1; i <= nt; i++) s = s hexbits(tp[i])
		print s
	}'
}

# in_net <address> <network>...: one of the networks holds the address
in_net() {
	a=$1; shift
	valid_addr "$a" || return 1
	ab=$(bits "$a")
	for net in "$@"; do
		[ -n "$net" ] || continue
		n=${net%/*}; len=${net#*/}
		[ "$len" = "$net" ] && len=${#ab}
		valid_addr "$n" || continue
		nb=$(bits "$n")
		[ ${#nb} = ${#ab} ] || continue
		[ "$(printf '%s' "$ab" | cut -c1-"$len")" = "$(printf '%s' "$nb" | cut -c1-"$len")" ] && return 0
	done
	return 1
}

# --home: a network with its length, aligned, not wider than /8 (IPv4) or /32 (IPv6)
for net in $HOMES; do
	case "$net" in */*) ;; *) fail 2 "--home: give a network with its length, such as 192.168.1.0/24" ;; esac
	n=${net%/*}; len=${net#*/}
	valid_addr "$n" || fail 2 "--home: not a network: $net"
	case "$len" in ''|*[!0-9]*) fail 2 "--home: not a network: $net" ;; esac
	nb=$(bits "$n")
	if [ ${#nb} = 32 ]; then min=8; else min=32; fi
	{ [ "$len" -ge "$min" ] && [ "$len" -le ${#nb} ]; } || fail 2 "--home: $net is wider than /$min or not a valid length"
	rest=$(printf '%s' "$nb" | cut -c$((len + 1))-)
	case "$rest" in *1*) fail 2 "--home: $net has bits set after /$len (write the network address)" ;; esac
done

# ------------------------------------------------ the network as it is ---

WORK=$(mktemp -d)
# shellcheck disable=SC2317
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
mkdir -p "$WORK/etc" "$WORK/run"
if [ "$EXPOSED" = 1 ]; then echo "exposed=yes" > "$WORK/etc/options"; else echo "exposed=no" > "$WORK/etc/options"; fi

DYNAMIC=$(COSMOS_HOST_GUARD_CONF="$WORK/etc" COSMOS_HOST_GUARD_RUN="$WORK/run" sh "$HERE/cosmos-host-guard" show)
dyn() { printf '%s\n' "$DYNAMIC" | sed -n "s/^$1=//p"; }
WAN=$(dyn wan_if)
LAN=$(dyn lan_if)
FAILCLOSED=$(dyn failclosed_if)
ONLINK4=$(dyn onlink4)
ONLINK6=$(dyn onlink6)
VPN_UDP=$(dyn vpn_udp)
in_csv() { case ",$2," in *",$1,"*) return 0 ;; esac; return 1; }
csv_words() { printf '%s' "$1" | tr ',' ' '; }

private4() { in_net "$1" 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16; }
cgnat4() { in_net "$1" 100.64.0.0/10; }

# IPv4: filtered when an outside interface holds a public or carrier-grade address
PUBLIC4=""
CGNAT4=""
for dev in $(csv_words "$WAN"); do
	for a in $(ip -o -4 addr show dev "$dev" scope global 2>/dev/null | awk '{sub(/\/.*/, "", $4); print $4}'); do
		if private4 "$a"; then :; elif cgnat4 "$a"; then CGNAT4="$CGNAT4 $a"; else PUBLIC4="$PUBLIC4 $a"; fi
	done
done
if [ "$EXPOSED" = 1 ]; then
	GUARD4=1; IPV4_REASON=exposed; IPV4_WHY="filtered: --exposed (a cloud server or a shared segment)"
elif [ "$IPV4" = yes ]; then
	GUARD4=1; IPV4_REASON=asked-yes; IPV4_WHY="filtered: --ipv4 yes (the router's exposed host)"
elif [ "$IPV4" = no ]; then
	GUARD4=0; IPV4_REASON=asked-no; IPV4_WHY="NOT filtered: --ipv4 no"
elif [ -n "$PUBLIC4" ]; then
	GUARD4=1; IPV4_REASON=public-address; IPV4_WHY="filtered: this server has a public IPv4 address:$PUBLIC4"
elif [ -n "$CGNAT4" ]; then
	GUARD4=1; IPV4_REASON=cgnat-address; IPV4_WHY="filtered: this server has a carrier-grade address:$CGNAT4 (the carrier's other customers share that network)"
else
	GUARD4=0; IPV4_REASON=behind-router
	IPV4_WHY="NOT filtered by the guard: the server has a private IPv4 address. That is safe only if
              the router forwards just the ports you chose. If this server is the router's
              exposed host (DMZ), run again with --ipv4 yes; if it is a cloud server with 1:1 NAT,
              with --exposed"
fi

# what each outside interface is, in words
IF_NOTES=""
for dev in $(csv_words "$WAN"); do
	a4=$(ip -o -4 addr show dev "$dev" scope global 2>/dev/null | awk '{sub(/\/.*/, "", $4); printf "%s ", $4}')
	p6=$(ip -o -6 route show dev "$dev" 2>/dev/null | awk '$1 !~ /^(default|fe80|ff|multicast|local)/ && !/ via / && $1 ~ /:/ {printf "%s ", $1}')
	if in_csv "$dev" "$LAN"; then
		gw=$(ip -o -4 route show table all default dev "$dev" 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "via") {print $(i + 1); exit}}')
		note="$dev: a home network behind a router (private address ${a4% }, router ${gw:-?}):
              its prefixes (${p6:-no IPv6 prefix}) and link-local count as home.
              If this server is a cloud server or the router's exposed host, use --exposed / --ipv4 yes."
	elif [ "$EXPOSED" = 1 ]; then
		note="$dev: --exposed: nothing on it counts as home but --home"
	elif [ -n "$a4" ] && { [ -n "$PUBLIC4" ] || [ -n "$CGNAT4" ]; }; then
		note="$dev: it holds a public or carrier-grade address (${a4% }): the segment is the provider's;
              its prefixes and link-local sources do NOT count as home (only --home networks do)"
	else
		note="$dev: could not tell whether this is your home network (${a4:+no private router found for ${a4% }}${a4:-no IPv4 address on it}):
              strict, its prefixes do NOT count as home. If it is your home network, add
              ${p6:+--home }${p6:-the --home networks of your home}"
	fi
	IF_NOTES="$IF_NOTES
  outside     $note"
done

# ------------------------------------------------ the SSH sessions open now ---

# sshd's listening ports: its configuration and the socket unit that starts it
SSH_PORTS=$({
	sshd -T 2>/dev/null | awk '$1 == "port" {print $2}'
	systemctl show -p Listen --value ssh.socket sshd.socket 2>/dev/null | tr ' ' '\n' | grep -E ':[0-9]+$' | sed 's/.*://'
} | grep -E '^[0-9]{1,5}$' | sort -un | tr '\n' ' ')
[ -n "$SSH_PORTS" ] || SSH_PORTS=22

# The signed-in SSH sessions, from the sockets: a connection to an SSH port held
# by a process whose title is "sshd...: <user>@pts/N" or "...@notty" (a
# connection that is still signing in, a stranger's, does not count).
live_peers() {
	ss -Htnp state established 2>/dev/null | while read -r _ _ local peer procs; do
		lport=${local##*:}
		case " $SSH_PORTS " in *" $lport "*) ;; *) continue ;; esac
		signed=0
		for pid in $(printf '%s' "$procs" | grep -oE 'pid=[0-9]+' | cut -d= -f2); do
			if tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -qE '^sshd[^ ]*: [^ ]+@(pts/[0-9]+|notty)'; then signed=1; fi
		done
		[ "$signed" = 1 ] || continue
		addr=${peer%:*}
		addr=${addr#[}; addr=${addr%]}
		case "$addr" in ::ffff:*.*) addr=${addr#::ffff:} ;; esac
		printf '%s\n' "$addr"
	done | sort -u
}
PEERS=$(live_peers)

# the environment's session is only a hint: under sudo it is in a parent's
# environment, in tmux or screen it may be stale
HINT=${SSH_CONNECTION:-}
pid=$$
depth=0
while [ -z "$HINT" ] && [ "$pid" -gt 1 ] 2>/dev/null && [ "$depth" -lt 32 ]; do
	HINT=$(tr '\0' '\n' < "/proc/$pid/environ" 2>/dev/null | sed -n 's/^SSH_CONNECTION=//p' | head -n 1)
	pid=$(awk '/^PPid:/ {print $2}' "/proc/$pid/status" 2>/dev/null || echo 0)
	depth=$((depth + 1))
done
HINT_ADDR=${HINT%% *}
case "$HINT_ADDR" in ::ffff:*.*) HINT_ADDR=${HINT_ADDR#::ffff:} ;; esac
HINT_NOTE=""
if [ -n "$HINT_ADDR" ]; then
	if printf '%s\n' "$PEERS" | grep -qxF -- "${HINT_ADDR%%%*}"; then
		HINT_NOTE="this run's own session comes from ${HINT_ADDR%%%*}"
	else
		HINT_NOTE="the environment names an SSH session from ${HINT_ADDR%%%*}, but none is open from there now: ignored (stale, from tmux/screen or a closed session)"
	fi
fi

# where_from <address>: why the guard leaves it alone, or "outside"
where_from() {
	a=${1%%%*}
	zone=""
	case "$1" in *%*) zone=${1#*%} ;; esac
	valid_addr "$a" || { echo "outside"; return; }
	dev=$zone
	[ -n "$dev" ] || dev=$(ip route get "$a" 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "dev") {print $(i + 1); exit}}')
	if ! in_csv "$dev" "$WAN"; then echo "through ${dev:-an interface} the guard does not filter (loopback, a VPN, a bridge)"; return; fi
	case "$a" in *:*) fam=6 ;; *) fam=4 ;; esac
	if [ "$fam" = 4 ] && [ "$GUARD4" = 0 ]; then echo "over IPv4, which the guard leaves alone here"; return; fi
	# shellcheck disable=SC2086
	if in_net "$a" $HOMES; then echo "from a --home network"; return; fi
	if in_csv "$dev" "$LAN"; then
		# shellcheck disable=SC2046
		if in_net "$a" fe80::/10 169.254.0.0/16 $(csv_words "$ONLINK4,$ONLINK6"); then echo "from the home network on $dev"; return; fi
	fi
	echo "outside"
}

OUTSIDE_PEERS=""
PEER_NOTES=""
for p in $PEERS; do
	w=$(where_from "$p")
	if [ "$w" = outside ]; then
		OUTSIDE_PEERS="$OUTSIDE_PEERS ${p%%%*}"
		PEER_NOTES="$PEER_NOTES
              ${p%%%*}: from OUTSIDE the home network"
	else
		PEER_NOTES="$PEER_NOTES
              ${p%%%*}: $w"
	fi
done

SSH_OPEN=0
if [ "$ALLOW_SSH" = 1 ]; then
	for p in $SSH_PORTS; do add_port "$p" tcp "SSH (--allow-ssh-from-outside)"; done
	SSH_OPEN=1
fi
ALLOW_UNTIL=$(( $(date +%s) + ALLOW_HOURS * 3600 ))
ALLOW_UNTIL_TEXT=$(date -d "@$ALLOW_UNTIL" '+%Y-%m-%d %H:%M %Z' 2>/dev/null || echo "in $ALLOW_HOURS hours")

# other tables that filter what reaches this server (never touched)
FOREIGN=""
if command -v nft >/dev/null 2>&1; then
	FOREIGN=$(COSMOS_HOST_GUARD_CONF="$WORK/etc" COSMOS_HOST_GUARD_RUN="$WORK/run" sh "$HERE/cosmos-host-guard" others | paste -sd ';' -)
fi
OTHERS_TEXT=""
if [ -n "$FOREIGN$OTHERS" ]; then
	OTHERS_TEXT=$(printf '%s' "$FOREIGN" | sed 's/;/, /g')
	[ -z "$OTHERS" ] || OTHERS_TEXT="${OTHERS_TEXT:+$OTHERS_TEXT; }active:$OTHERS"
fi

# ------------------------------------------------------- the files to write ---

if [ "$GUARD4" = 1 ]; then
	IPV4_LINE='# IPv4 is filtered too'
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
	say "add element $TABLE ssh_port { $(elements "$SSH_PORTS") }"
} > "$WORK/etc/settings.nft"
# the allowances: "<address> <until>", kept after a reinstall while still valid
: > "$WORK/etc/allow"
if [ -f "$ETC/allow" ]; then
	now=$(date +%s)
	while read -r a u; do
		case "$u" in ''|*[!0-9]*) continue ;; esac
		valid_addr "$a" && [ "$u" -gt "$now" ] && printf '%s %s\n' "$a" "$u" >> "$WORK/etc/allow"
	done < "$ETC/allow"
fi
if [ "$SSH_OPEN" = 0 ]; then
	for a in $OUTSIDE_PEERS; do
		grep -v "^$a " "$WORK/etc/allow" > "$WORK/allow.tmp" || true
		printf '%s %s\n' "$a" "$ALLOW_UNTIL" >> "$WORK/allow.tmp"
		mv "$WORK/allow.tmp" "$WORK/etc/allow"
	done
fi

cat > "$WORK/etc/README" <<EOF
The host guard of the Cosmos fork (fork/nftables/install.sh).

  nft list table inet cosmos_host_guard        what is loaded, with the counters
  cosmos-host-guard show                       the interfaces, prefixes and VPN port it follows
  systemctl reload cosmos-host-guard           load it again (atomic, no gap)
  sh <the release>/fork/nftables/uninstall.sh  remove it

  allow     SSH allowances: "<address> <until, seconds since 1970>"; run the
            installer again from that address to renew one
Do not edit the files here: run the installer again with other options.
EOF

cp "$HERE/cosmos-host-guard" "$WORK/cosmos-host-guard"

# the whole transaction as a load would send it
transaction() {
	COSMOS_HOST_GUARD_CONF="$WORK/etc" COSMOS_HOST_GUARD_RUN="$WORK/run" COSMOS_HOST_GUARD_STATE=/nonexistent sh "$WORK/cosmos-host-guard" "$@"
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
[ "$REINSTALL" = 0 ] || say "  guard       installed already: replaced in one step (no moment without rules)"
say "  table       $TABLE (its own; nothing else is touched)"
if [ -n "$FAILCLOSED" ]; then
	say "  outside     NO default route was found: IPv6 is filtered on every interface that is not"
	say "              loopback, Docker's or a VPN ($FAILCLOSED) until one appears"
elif [ -z "$WAN" ]; then
	say "  outside     NO interface to filter was found: the guard filters nothing until one appears"
fi
[ -z "$IF_NOTES" ] || say "${IF_NOTES#?}"
[ -z "$HOMES" ] || say "  home        also, everywhere:$HOMES"
say "  answers from outside:$PORT_REASONS"
[ -z "$CONFIG_NOTE" ] || [ -n "$PORTS" ] || say "              (Cosmos' ports $CONFIG_NOTE)"
say "  IPv6        filtered"
say "  IPv4        $IPV4_WHY"
say "  always let through: established connections; from anywhere the ICMPv6 errors IPv6 needs"
say "              (packet too big, unreachable...) and echo; neighbour discovery and router"
say "              adverts with hop limit 255; DHCP replies from the link; VPN interfaces;"
say "              the VPN port found now: ${VPN_UDP:-(none)}"
say "  dropped     sources from outside that claim a private, unique-local or link-local"
say "              address, or that would not be answered through that interface"
if [ -n "$PEERS" ]; then
	say "  SSH         signed-in sessions now (sshd port $SSH_PORTS):$PEER_NOTES"
else
	say "  SSH         no signed-in SSH session is open now"
fi
[ -z "$HINT_NOTE" ] || say "              ($HINT_NOTE)"
if [ "$SSH_OPEN" = 1 ]; then
	say "              the SSH port answers everyone from outside (--allow-ssh-from-outside)"
elif [ -n "$OUTSIDE_PEERS" ]; then
	say "              ONLY$OUTSIDE_PEERS may reach SSH from outside, until $ALLOW_UNTIL_TEXT;"
	say "              from any other outside address SSH is refused. Renew: run this installer"
	say "              again from there; keep SSH open on purpose: --allow-ssh-from-outside;"
	say "              better: come in through a VPN (its interface is never filtered)"
fi
if [ -n "$OTHERS_TEXT" ]; then
	say "  beside      other rules also filter here: $OTHERS_TEXT."
	say "              Both filter; a drop in either is final. This installer never removes them."
	case ";$FOREIGN;" in *";inet cosmos_guard;"*)
		say "              inet cosmos_guard looks like a guard made by hand: once this one works,"
		say "              it can be removed (systemctl disable --now cosmos-guard, if that is its unit)" ;;
	esac
fi
say "  not covered mosh's UDP ports are not opened; virtual machines bridged on an outside"
say "              bridge are filtered like containers; on a server that is itself the router,"
say "              a LAN host's port numbered like a public port answers from outside; an"
say "              interface with a public address but no default route is not filtered"
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

if [ -n "$OUTSIDE_PEERS" ] && [ "$YES" = 1 ] && [ "$ALLOW_SSH" = 0 ]; then
	record status "refused ssh-session-from-outside"
	printf '%s\n' "Refused for the SSH sessions' safety: signed-in SSH sessions are open from outside the home network (${OUTSIDE_PEERS# }), and an unattended run has nobody to confirm that a new connection still works. Install the guard from inside the home network (or a VPN), run it without --yes to confirm by hand, or add --allow-ssh-from-outside to keep the SSH port open." >&2
	exit 4
fi

# nft -c checks the whole transaction without changing anything (when nft is here)
if command -v nft >/dev/null 2>&1; then
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
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables >/dev/null || fail 1 "apt-get could not install nftables: nothing was changed"
	CHANGED_PKG=1
	record package nftables
	if [ "$NFT_SERVICE_BEFORE" != enabled ] && [ "$(systemctl is-enabled nftables.service 2>/dev/null || true)" = enabled ]; then
		systemctl disable nftables.service >/dev/null 2>&1 || true
		say "disabled nftables.service again (the package had enabled it)"
	fi
	if ! transaction check 2>"$WORK/check.err"; then
		cat "$WORK/check.err" >&2
		fail_early "nft does not accept the ruleset"
	fi
fi

mkdir -p "$RUN_DIR"
chmod 700 "$RUN_DIR"
rm -f "$RUN_DIR/undone" "$RUN_DIR/confirmed" "$RUN_DIR/previous.nft"
if nft list table inet "$TNAME" > "$RUN_DIR/previous.nft" 2>/dev/null; then :; else rm -f "$RUN_DIR/previous.nft"; fi

# The undo puts the table back as it was before this run (or removes it). It
# runs from a systemd timer in an interactive run, so it fires even when this
# session is cut and the script with it. Confirming and undoing are one
# decision, taken under one lock: whichever comes first wins, the other does
# nothing.
NFT=$(command -v nft)
FLOCK=$(command -v flock || true)
cat > "$RUN_DIR/undo.sh" <<EOF
#!/bin/sh
# Written by fork/nftables/install.sh: puts $TABLE back as it was before the run.
[ -z "$FLOCK" ] || { exec 8>"$RUN_DIR/decision"; $FLOCK 8; }
[ ! -e "$RUN_DIR/confirmed" ] || exit 0
[ ! -e "$RUN_DIR/undone" ] || exit 0
if [ -s "$RUN_DIR/previous.nft" ]; then
	{ printf 'table $TABLE\ndelete table $TABLE\n'; cat "$RUN_DIR/previous.nft"; } | $NFT -f -
else
	$NFT list table $TABLE >/dev/null 2>&1 && $NFT delete table $TABLE
fi
rm -f "$RUN_DIR/dynamic"
: > "$RUN_DIR/undone"
[ ! -s "$RUN_DIR/installer.pid" ] || kill -TERM "\$(cat "$RUN_DIR/installer.pid")" 2>/dev/null
exit 0
EOF
chmod 700 "$RUN_DIR/undo.sh"

undo_now() {
	rm -f "$RUN_DIR/installer.pid"
	systemctl stop "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
	sh "$RUN_DIR/undo.sh" || return 1
}
# is the machine back as it was before this run (the table, at least)?
as_before() {
	if [ -s "$RUN_DIR/previous.nft" ]; then nft list table inet "$TNAME" >/dev/null 2>&1; return; fi
	if nft list table inet "$TNAME" >/dev/null 2>&1; then return 1; fi
	return 0
}

if [ "$INTERACTIVE" = 1 ]; then
	systemctl stop "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
	systemctl reset-failed "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
	echo $$ > "$RUN_DIR/installer.pid"
	systemd-run --quiet --unit="$UNDO_UNIT" --on-active="${TIMEOUT}s" --timer-property=AccuracySec=1s \
		/bin/sh "$RUN_DIR/undo.sh" || fail_early "could not arm the automatic undo (systemd-run)"
	UNDO_AT=$(date -d "+$TIMEOUT seconds" +%H:%M:%S 2>/dev/null || echo "in $TIMEOUT s")
	trap 'say ""; say "Not confirmed in time: the automatic undo took the guard away again. Nothing else was changed."; record status "undone not-confirmed"; exit 5' TERM
fi

# load it: the same transaction a boot would send, from the files staged here
if ! transaction load 2>"$WORK/load.err"; then
	cat "$WORK/load.err" >&2
	undo_now || true
	as_before || fail 6 "loading failed and the table could not be put back: check with: nft list table $TABLE"
	record status "undone load-failed"
	fail 5 "nft could not load the guard: it was put back as it was"
fi

# checked from the inside: the table, its hooks, its rules, its counters, the
# public ports and the outside sessions' allowances are in the kernel
verify() {
	nft list table inet "$TNAME" >/dev/null 2>&1 || { echo "the table is not loaded"; return 1; }
	for chain in input forward; do
		nft list chain inet "$TNAME" $chain 2>/dev/null | grep -q "hook $chain priority" || { echo "the chain $chain is not hooked"; return 1; }
	done
	nft list chain inet "$TNAME" input 2>/dev/null | grep -q 'counter name "dropped_input" drop' || { echo "the input chain has lost its rules"; return 1; }
	for counter in dropped_input dropped_forward dropped_spoofed; do
		nft list counter inet "$TNAME" $counter 2>/dev/null | grep -q 'packets [0-9]' || { echo "the counter $counter cannot be read"; return 1; }
	done
	for p in $PUBLIC_TCP; do nft get element inet "$TNAME" public_tcp "{ $p }" >/dev/null 2>&1 || { echo "port $p/tcp is not open"; return 1; }; done
	for p in $PUBLIC_UDP; do nft get element inet "$TNAME" public_udp "{ $p }" >/dev/null 2>&1 || { echo "port $p/udp is not open"; return 1; }; done
	if [ "$SSH_OPEN" = 0 ]; then
		for a in $OUTSIDE_PEERS; do
			case "$a" in *:*) allow=ssh_allow6 ;; *) allow=ssh_allow4 ;; esac
			nft get element inet "$TNAME" $allow "{ $a }" >/dev/null 2>&1 || { echo "the SSH session from $a is not allowed"; return 1; }
		done
		for p in $SSH_PORTS; do
			nft get element inet "$TNAME" ssh_port "{ $p }" >/dev/null 2>&1 || { echo "the SSH port $p is not in the allowance"; return 1; }
		done
	fi
	if [ -n "$WAN" ]; then
		first=${WAN%%,*}
		nft get element inet "$TNAME" wan_if "{ \"$first\" }" >/dev/null 2>&1 || { echo "the outside interface $first is not in the guard"; return 1; }
	fi
	return 0
}
if ! why=$(verify); then
	undo_now || true
	as_before || fail 6 "the check from the inside failed ($why) and the table could not be put back"
	record status "undone check-failed"
	fail 5 "the check from the inside failed ($why): the guard was put back as it was"
fi
say "loaded      $TABLE: checked from the inside (hooks, rules, counters, ports, SSH allowances)"

if [ "$INTERACTIVE" = 1 ]; then
	say ""
	say "The guard is live. Open a NEW SSH connection to this server now. If it works, type yes"
	say "here before $UNDO_AT. If it does not, do nothing: the guard removes itself then."
	printf 'Confirm? [yes/N] '
	answer=""
	read -r answer || answer=""
	if [ "$answer" = yes ] || [ "$answer" = y ]; then
		# one decision with the undo: under its lock, either it already ran or it never will
		decided=0
		if [ -n "$FLOCK" ]; then
			exec 8>"$RUN_DIR/decision"
			$FLOCK 8
		fi
		if [ -e "$RUN_DIR/undone" ]; then decided=undone; else : > "$RUN_DIR/confirmed"; decided=confirmed; fi
		[ -z "$FLOCK" ] || exec 8>&-
		trap - TERM
		rm -f "$RUN_DIR/installer.pid"
		systemctl stop "$UNDO_UNIT.timer" "$UNDO_UNIT.service" >/dev/null 2>&1 || true
		if [ "$decided" = undone ]; then
			record status "undone not-confirmed"
			fail 5 "too late: the automatic undo had already removed the guard. Nothing else was changed."
		fi
	else
		trap - TERM
		if [ -e "$RUN_DIR/undone" ]; then
			record status "undone not-confirmed"
			fail 5 "not confirmed in time: the automatic undo removed the guard. Nothing else was changed."
		fi
		undo_now || true
		as_before || fail 6 "could not take the guard away: nft delete table $TABLE"
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
	rm -f "$RUN_DIR/confirmed"
	sh "$RUN_DIR/undo.sh" >/dev/null 2>&1 || true
}
# after a rollback: the table and the units as before this run?
rolled_back() {
	as_before || return 1
	if [ "$REINSTALL" = 1 ]; then
		systemctl is-active --quiet cosmos-host-guard.service || return 1
	else
		for unit in $UNITS; do [ ! -e "$UNIT_DIR/$unit" ] || return 1; done
		[ ! -e "$ETC" ] || return 1
	fi
	return 0
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
	for f in cosmos-host-guard.nft settings.nft options allow README; do
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
fail_rolled() { # message
	if rolled_back; then
		record status "undone $2"
		fail 5 "$1: everything was put back as it was"
	fi
	fail 6 "$1, and putting it back did not fully work: check nft list table $TABLE and systemctl status cosmos-host-guard"
}
if ! commit; then
	rollback
	fail_rolled "could not install or start the units" units-failed
fi
if ! why=$(verify) || ! systemctl is-active --quiet cosmos-host-guard-watch.service; then
	rollback
	fail_rolled "after starting the units the check from the inside failed (${why:-the watcher is not running})" check-failed
fi
rm -f "$RUN_DIR/previous.nft" "$RUN_DIR/undo.sh" "$RUN_DIR/installer.pid" "$RUN_DIR/undone" "$RUN_DIR/confirmed" "$RUN_DIR/decision"

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
	say "ipv4_reason=$IPV4_REASON"
	say "exposed=$( [ "$EXPOSED" = 1 ] && echo yes || echo no )"
	say "ssh_open=$( [ "$SSH_OPEN" = 1 ] && echo yes || echo no )"
	say "other_filters=$FOREIGN"
} > "$STATE.new"
chmod 644 "$STATE.new"
mv -f "$STATE.new" "$STATE"
{
	if [ -z "$PACKAGES" ] && [ -f "$MANIFEST" ]; then grep '^package	' "$MANIFEST" || true; fi
	[ -z "$PACKAGES" ] || printf 'package\tnftables\n'
	printf 'table\t%s\n' "$TABLE"
	for f in cosmos-host-guard.nft settings.nft options allow README; do printf 'file\t%s\n' "$ETC/$f"; done
	printf 'file\t%s\n' "$SBIN"
	for unit in $UNITS; do printf 'unit\t%s\n' "$unit"; done
	printf 'file\t%s\n' "$STATE"
} > "$MANIFEST.new"
chmod 600 "$MANIFEST.new"
mv -f "$MANIFEST.new" "$MANIFEST"

record table "$TABLE"
for f in cosmos-host-guard.nft settings.nft options allow README; do record file "$ETC/$f"; done
record file "$SBIN"
for unit in $UNITS; do record unit "$unit"; done
record file "$STATE"
if [ "$REINSTALL" = 1 ]; then record status already-installed; else record status installed; fi

say ""
say "Done: the guard is installed, loaded and follows address changes."
[ -z "$OUTSIDE_PEERS" ] || [ "$SSH_OPEN" = 1 ] || say "  SSH from outside: only$OUTSIDE_PEERS, until $ALLOW_UNTIL_TEXT"
dropped=$(nft list counter inet "$TNAME" dropped_input 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p')
say "  nft list table $TABLE      the rules and counters (dropped so far: ${dropped:-0})"
say "  systemctl status cosmos-host-guard"
say "  sh $HERE/uninstall.sh      remove it"
[ -z "$BACKUP" ] || say "  the files it replaced are in $BACKUP"
exit 0
