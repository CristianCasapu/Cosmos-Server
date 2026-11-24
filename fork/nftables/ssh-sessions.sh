#!/bin/sh
# The SSH servers that listen on this machine and the SSH connections open now,
# as the host guard's and Fail2ban's installers need them (fork/nftables/install.sh,
# fork/fail2ban/install.sh). Run as root; reads ss, /proc and the journal.
#
#   sh ssh-sessions.sh listeners   "<server> <pid> <address> <port>" per listening socket
#   sh ssh-sessions.sh sessions    "<address> <local port> <server> <signed-in>" per
#                                  connection accepted by an SSH server
#   sh ssh-sessions.sh explained <pid>
#                                  exit 0 when an SSH server's process is among the
#                                  ancestors of <pid> (its terminal comes from a session)
#
# <server> is openssh, dropbear or tinysshd; <signed-in> is yes, no (a stranger
# still signing in) or unsure. Only "yes" may ever be given an SSH allowance or be
# kept from bans; "unsure" refuses an unattended install.
#
# What is trusted is what a user signed in over SSH cannot make:
# - a LISTENER is a listening socket held by a root process whose executable, read
#   through /proc/<pid>/exe (never its name), is an SSH server's binary owned by
#   root and writable by root only: OpenSSH's daemon (its root-set title says
#   "[listener]": a root session's -R forward does not), dropbear or tinysshd whose
#   parent is not a process of the same server (a connection's -R forward listens
#   in such a child); however it was started (a unit, by hand, a supervisor). Or
#   systemd itself (pid 1) on a port of ssh.socket, sshd.socket, dropbear.socket or
#   tinysshd.socket. "*" listens on both families, 0.0.0.0 on IPv4, [::] on IPv6.
# - a CONNECTION counts when it was ACCEPTED: an established socket on such a
#   listener's family, address and port, held (found by the socket's inode in
#   /proc/<pid>/fd, never by parsing process names) by a process of that server's
#   binary that descends from the listener through processes of that binary (or,
#   its listener restarted or systemd's, whose parent is pid 1). Connections an
#   SSH server makes outwards (-L, -D, -W), visitors of a -R forward and processes
#   with a forged name or title are never on a listener's port.
#
# SIGNED IN rests on evidence that authentication SUCCEEDED for that very
# connection, never on a title (OpenSSH titles its monitor "<name> [priv]" as soon
# as a client NAMES a user, before any authentication):
# - the server's own line in the journal: OpenSSH "Accepted <method> for <user>
#   from <address> port <port>", dropbear "... auth succeeded for '<user>' ... from
#   <address>:<port>", with address and port equal to the socket's peer, and
#   accepted only from an entry whose _PID is the connection's own process (for
#   dropbear also its listener, which writes for its children when it logs to its
#   standard error under a unit), _UID 0 and _EXE that process's binary: journald
#   takes these fields from the kernel, a user's logger or systemd-cat line never
#   has them (the message text and the identifier are not looked at);
# - or the processes, which can only add certainty: OpenSSH's post-authentication
#   child (a child of the monitor running as the user: neither root nor the
#   privilege separation user, which runs the pre-authentication child), a
#   dropbear connection process that dropped to a user's uid (dropbear from 2024)
#   or started a child (the shell or the command). dropbear up to 2022 (Debian 12,
#   Ubuntu 24.04) keeps a signed-in tunnel as root without a child: there only the
#   journal tells it from a stranger.
# NOT signed in ("no"): an OpenSSH connection whose monitor has not been given a
# user name; one whose monitor was (a stranger at the password prompt) when the
# journal covers the connection's whole life (its oldest entry of this boot is
# older than the connection, and this server writes to it) and holds no such
# line; a dropbear connection whose own "Child connection from <peer>" line is in
# the journal without a line of success. Anything else is "unsure": no journal, a
# server that logs elsewhere, a line rotated or vacuumed away, root's own session
# without its line (its post-authentication child is root's too).

set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin

uid_of() { awk '/^Uid:/ {print $2; exit}' "/proc/$1/status" 2>/dev/null || true; }
ppid_of() { awk '/^PPid:/ {print $2; exit}' "/proc/$1/status" 2>/dev/null || echo 0; }
title_of() { tr '\0' ' ' < "/proc/$1/cmdline" 2>/dev/null | sed 's/ *$//' || true; }
exe_of() { e=$(readlink "/proc/$1/exe" 2>/dev/null || true); printf '%s\n' "${e% (deleted)}"; }
children_of() { cat /proc/"$1"/task/*/children 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' || true; }

# server_of <pid>: openssh, dropbear or tinysshd when the process's executable is
# that server's binary, owned by root and not writable by anyone else
server_of() {
	exe=$(exe_of "$1")
	case "$exe" in
		/usr/sbin/sshd|/sbin/sshd|/usr/local/sbin/sshd|/usr/lib/openssh/sshd-session|/usr/lib/openssh/sshd-auth|/usr/libexec/openssh/sshd-session|/usr/libexec/openssh/sshd-auth|/usr/local/libexec/sshd-session|/usr/local/libexec/sshd-auth) kind=openssh ;;
		/usr/sbin/dropbear|/usr/bin/dropbear|/sbin/dropbear|/usr/local/sbin/dropbear) kind=dropbear ;;
		/usr/sbin/tinysshd|/usr/bin/tinysshd|/sbin/tinysshd|/usr/local/sbin/tinysshd) kind=tinysshd ;;
		*) return 1 ;;
	esac
	# the binary itself (a deleted one too: /proc/<pid>/exe still reaches it)
	owner=$(stat -L -c '%u %a' "/proc/$1/exe" 2>/dev/null || true)
	case "$owner" in
		"0 "*) mode=${owner#0 } ;;
		*) return 1 ;;
	esac
	case "$mode" in *[2367]?|*[2367]) return 1 ;; esac
	echo "$kind"
}

# addr_of <ss's address:port>: the address without brackets or scope, an
# IPv4-mapped one as IPv4; "*" for any on both families, "*4" / "*6" for any on one
fam_of() { case "$1" in \[*) echo 6 ;; *) echo 4 ;; esac; }
addr_of() {
	a=${1%:*}
	a=${a#[}; a=${a%]}
	a=${a%%%*}
	a=${a%]}
	case "$a" in ::ffff:*.*) a=${a#::ffff:} ;; esac
	case "$a" in '*') a='*' ;; 0.0.0.0) a='*4' ;; ::) a='*6' ;; esac
	printf '%s\n' "$a"
}

# "<inode> <pid>" for every socket every process holds (one pass over /proc)
socket_index() {
	find /proc/[0-9]*/fd -maxdepth 1 -type l -lname 'socket:\[*' -printf '%l %h\n' 2>/dev/null |
		sed -n 's|^socket:\[\([0-9]*\)\] /proc/\([0-9]*\)/fd$|\1 \2|p' || true
}
holders() { printf '%s\n' "$INDEX" | awk -v i="$1" '$1 == i {print $2}' | sort -un; }
# ss -e lines: the inode
inode_of() { printf '%s\n' "$1" | tr ' ' '\n' | sed -n 's/^ino:\([0-9][0-9]*\)$/\1/p' | head -n 1; }

# the ports of the SSH servers' socket units: "<server> <port>"
unit_ports() {
	for unit in ssh.socket sshd.socket dropbear.socket tinysshd.socket; do
		case "$unit" in ssh*) kind=openssh ;; dropbear*) kind=dropbear ;; *) kind=tinysshd ;; esac
		systemctl show -p Listen --value "$unit" 2>/dev/null | tr ' ' '\n' | grep -E ':[0-9]{1,5}$' | sed "s/.*:/$kind /" || true
	done
}

listeners() {
	[ -n "${INDEX:-}" ] || INDEX=$(socket_index)
	uports=$(unit_ports)
	ss -Htlne 2>/dev/null | while read -r _ _ _ local _ ext; do
		port=${local##*:}
		case "$port" in ''|*[!0-9]*) continue ;; esac
		addr=$(addr_of "$local")
		ino=$(inode_of "$ext")
		[ -n "$ino" ] || continue
		for pid in $(holders "$ino"); do
			if [ "$pid" = 1 ]; then
				# systemd holds the socket of a socket unit
				printf '%s\n' "$uports" | awk -v p="$port" -v a="$addr" '$2 == p {print $1, 1, a, p}'
				continue
			fi
			[ "$(uid_of "$pid")" = 0 ] || continue
			kind=$(server_of "$pid") || continue
			case "$kind" in
				openssh) title_of "$pid" | grep -q '\[listener\]' || continue ;;
				*) [ "$(server_of "$(ppid_of "$pid")" || true)" != "$kind" ] || continue ;;
			esac
			printf '%s %s %s %s\n' "$kind" "$pid" "$addr" "$port"
		done
	done | sort -u
}

# accepted <holder pid> <server>: the holder descends from one of $LPIDS (the
# listeners of that socket) through processes of that server's binary, or is
# such a process whose parent is pid 1; prints the process just below the
# listener (the connection's own process: OpenSSH's monitor)
accepted() {
	p=$1; depth=0; below=$1
	while [ "$p" -gt 1 ] 2>/dev/null && [ "$depth" -lt 64 ]; do
		[ "$(server_of "$p" || true)" = "$2" ] || return 1
		case " $LPIDS " in *" $p "*) [ "$p" != "$1" ] || return 1; echo "$below"; return 0 ;; esac
		pp=$(ppid_of "$p")
		if [ "$pp" = 1 ]; then echo "$p"; return 0; fi
		below=$p; p=$pp; depth=$((depth + 1))
	done
	return 1
}

# jmessages <pid> <binary>: the messages root wrote from that process with that
# binary in this boot (journald's own fields; bounded)
jmessages() {
	command -v journalctl >/dev/null 2>&1 || return 0
	[ -n "$2" ] || return 0
	timeout 5 journalctl -q --no-pager -b -o cat _PID="$1" _UID=0 _EXE="$2" 2>/dev/null | head -n 5000 || true
}
# the peer's address and port, as the servers write them
peer_parts() { # ss's peer
	PA=${1%:*}; PP=${1##*:}
	PA=${PA#[}; PA=${PA%]}; PA=${PA%%%*}
	case "$PA" in ::ffff:*.*) PA=${PA#::ffff:} ;; esac
}
openssh_accepted() { # monitor pid: its "Accepted ... for U from A port P" for this peer
	jmessages "$1" "$(exe_of "$1")" | awk -v a="$PA" -v b="::ffff:$PA" -v p="$PP" '
		$1 == "Accepted" && $3 == "for" && $5 == "from" && ($6 == a || $6 == b) && $7 == "port" && $8 == p {found = 1}
		END {exit !found}'
}
dropbear_line() { # pattern connection-pid: its (or its listener's) line of that kind for this peer
	for lp in "$2" $LPIDS; do
		jmessages "$lp" "$(exe_of "$lp")" | awk -v re="$1" -v a="$PA:$PP" -v b="::ffff:$PA:$PP" '
			$0 ~ re && $(NF - 1) == "from" && ($NF == a || $NF == b) {found = 1}
			END {exit !found}' && return 0
	done
	return 1
}

# covers <pid>: the journal holds this boot from before the process started, and
# the process's binary writes to it (so a missing line of success means none)
covers() {
	command -v journalctl >/dev/null 2>&1 || return 1
	if [ -z "${OLDEST:-}" ]; then
		OLDEST=$(timeout 5 journalctl -b -q --no-pager -o short-unix 2>/dev/null | head -n 1 | cut -d. -f1 || true)
		case "$OLDEST" in ''|*[!0-9]*) OLDEST=none ;; esac
	fi
	[ "$OLDEST" != none ] || return 1
	ticks=$(awk '{print $22}' "/proc/$1/stat" 2>/dev/null || echo x)
	hz=$(getconf CLK_TCK 2>/dev/null || echo 100)
	boot=$(awk '/^btime/ {print $2}' /proc/stat)
	case "$ticks$hz$boot" in ''|*[!0-9]*) return 1 ;; esac
	[ "$OLDEST" -lt $((boot + ticks / hz)) ] || return 1
	timeout 5 journalctl -q --no-pager -b -n 1 -o cat _UID=0 _EXE="$(exe_of "$1")" 2>/dev/null | grep -q .
}

# the uid of OpenSSH's privilege separation user (it runs the pre-authentication child)
privsep_uid() { id -u sshd 2>/dev/null || echo none; }

# state <server> <connection's own process>: yes, no or unsure
state_of() {
	case "$1" in
		openssh)
			openssh_accepted "$2" && { echo yes; return; }
			for c in $(children_of "$2"); do
				u=$(uid_of "$c")
				case "$u" in ''|0|"$PRIVSEP") continue ;; esac
				[ "$(server_of "$c" || true)" = openssh ] || continue
				case "$(exe_of "$c")" in */sshd-auth) continue ;; esac
				case "$(title_of "$c")" in *"[net]"|*"[preauth]") continue ;; esac
				echo yes; return
			done
			# no user named yet ("[accepted]", "[net]"...): a stranger at the very start;
			# a name ("tester [priv]", root's own "root" or "root@pts/0"): from here only
			# the journal tells a stranger at the password prompt from a session
			if ! title_of "$2" | grep -qE '^sshd[a-z-]*: [^[ ]'; then echo no; return; fi
			if covers "$2"; then echo no; else echo unsure; fi ;;
		dropbear|tinysshd)
			if [ "$(uid_of "$2")" != 0 ] || [ -n "$(children_of "$2")" ]; then echo yes; return; fi
			if [ "$1" = dropbear ]; then
				dropbear_line ' auth succeeded for ' "$2" && { echo yes; return; }
				dropbear_line 'Child connection from ' "$2" && { echo no; return; }
			fi
			echo unsure ;;
	esac
}

# on_ports <port,...>: the lines of `ss state established` whose local port is one of them
on_ports() { awk -v ports=",$1," '{n = split($3, a, ":"); if (index(ports, "," a[n] ",")) print}'; }
# listeners_on <port> <address> <family>: the listeners ($LIST) of that local address and port
listeners_on() {
	printf '%s\n' "$LIST" | awk -v p="$1" -v a="$2" -v f="*$3" '$4 == p && ($3 == "*" || $3 == f || $3 == a)'
}

sessions() {
	INDEX=$(socket_index)
	LIST=$(listeners)
	[ -n "$LIST" ] || return 0
	PRIVSEP=$(privsep_uid)
	OLDEST=""
	ports=$(printf '%s\n' "$LIST" | awk '{print $4}' | sort -u | paste -sd, -)
	# only the connections on a listener's port go further (one pass over them all)
	ss -Htne state established 2>/dev/null | on_ports "$ports" |
	while read -r _ _ local peer ext; do
		lport=${local##*:}
		laddr=$(addr_of "$local")
		match=$(listeners_on "$lport" "$laddr" "$(fam_of "$local")")
		[ -n "$match" ] || continue
		kind=$(printf '%s\n' "$match" | awk '{print $1; exit}')
		LPIDS=$(printf '%s\n' "$match" | awk -v k="$kind" '$1 == k {print $2}' | tr '\n' ' ')
		ino=$(inode_of "$ext")
		[ -n "$ino" ] || continue
		peer_parts "$peer"
		state=""
		for pid in $(holders "$ino"); do
			conn=$(accepted "$pid" "$kind") || continue
			s=$(state_of "$kind" "$conn")
			case "$state:$s" in
				*:yes) state=yes; break ;;
				:*|no:unsure) state=$s ;;
			esac
		done
		[ -n "$state" ] || continue
		printf '%s %s %s %s\n' "$PA" "$lport" "$kind" "$state"
	done | sort -u
}

# explained <pid>: an SSH server's process is among its ancestors
explained() {
	p=$1; depth=0
	while [ "$p" -gt 1 ] 2>/dev/null && [ "$depth" -lt 64 ]; do
		server_of "$p" >/dev/null && return 0
		p=$(ppid_of "$p"); depth=$((depth + 1))
	done
	return 1
}

case "${1:-}" in
	listeners) listeners ;;
	sessions) sessions ;;
	explained) explained "${2:-0}" ;;
	*) echo "usage: sh ssh-sessions.sh listeners|sessions|explained <pid>" >&2; exit 2 ;;
esac
