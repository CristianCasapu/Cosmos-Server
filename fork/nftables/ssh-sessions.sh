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
# <server> is openssh, dropbear or tinysshd; <signed-in> is yes, no (OpenSSH only: a
# stranger still signing in) or unsure. Only "yes" may ever be given an SSH allowance or be
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
# NOT signed in ("no") needs positive evidence too, never the journal's silence (a
# server at LogLevel ERROR, a rate-limited or vacuumed journal would make a silence
# lie, and a wrong "no" cuts the owner off). Only OpenSSH shows it: the connection
# is still before authentication by its processes (its monitor has no user yet,
# "[accepted]", or the monitor's pre-authentication child runs now, as the
# privilege separation user, "<name> [net]"; its binary is sshd-auth from OpenSSH
# 9.8). Measured on 9.2, 9.6 and 10.0: a stranger at the password prompt has that
# child, any signed-in connection (root's included) has it no more.
# Other servers (dropbear, tinysshd) have no "no": no process shows a connection
# still signing in, and the journal cannot prove a missing line. Their connections
# are "yes" on positive evidence, "unsure" otherwise.
# Anything not decided is "unsure": root's own session without its line, a server
# that logs elsewhere or nothing, a line rotated or vacuumed away, and whatever the
# bounded scan (SCAN_BOUND) did not reach.
# Cost: the processes decide first; the journal is read once, for all the
# connections they leave undecided.

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

# start_of <pid>: when the process started, in seconds since 1970
start_of() {
	ticks=$(awk '{print $22}' "/proc/$1/stat" 2>/dev/null || echo x)
	hz=$(getconf CLK_TCK 2>/dev/null || echo 100)
	boot=$(awk '/^btime/ {print $2}' /proc/stat)
	case "$ticks$hz$boot" in ''|*[!0-9]*) return 1 ;; esac
	echo $((boot + ticks / hz))
}
# the peer's address and port, as the servers write them
peer_parts() { # ss's peer
	PA=${1%:*}; PP=${1##*:}
	PA=${PA#[}; PA=${PA%]}; PA=${PA%%%*}
	case "$PA" in ::ffff:*.*) PA=${PA#::ffff:} ;; esac
}

# the uid of OpenSSH's privilege separation user (it runs the pre-authentication child)
privsep_uid() { id -u sshd 2>/dev/null || echo none; }

# process_state <server> <connection's own process>: "yes" or "no" when the
# processes tell, nothing when they do not (the journal is read for those, once)
process_state() {
	case "$1" in
		openssh)
			# signed in: a child of the monitor running as the user (neither root nor
			# the privilege separation user)
			for c in $(children_of "$2"); do
				u=$(uid_of "$c")
				case "$u" in ''|0|"$PRIVSEP") continue ;; esac
				[ "$(server_of "$c" || true)" = openssh ] || continue
				case "$(exe_of "$c")" in */sshd-auth) continue ;; esac
				case "$(title_of "$c")" in *"[net]"|*"[preauth]") continue ;; esac
				echo yes; return
			done
			# still before authentication, shown by the processes themselves (OpenSSH 9.2
			# to 10.0, measured): the monitor has no user yet ("[accepted]"), or its
			# pre-authentication child runs now (the privilege separation user,
			# "<name> [net]"; sshd-auth from OpenSSH 9.8). Once signed in that child is
			# gone. The journal's silence is never taken as "no".
			if ! title_of "$2" | grep -qE '^sshd[a-z-]*: [^[ ]'; then echo no; return; fi
			for c in $(children_of "$2"); do
				[ "$(uid_of "$c")" = "$PRIVSEP" ] || continue
				[ "$(server_of "$c" || true)" = openssh ] || continue
				case "$(title_of "$c")" in *"[net]"|*"[preauth]") echo no; return ;; esac
			done ;;
		*)
			# another server (dropbear, tinysshd): its process dropped to a user's uid,
			# or started a child (the shell or the command). There is no "no" for these
			# servers: no process shows a connection still signing in.
			if [ "$(uid_of "$2")" != 0 ] || [ -n "$(children_of "$2")" ]; then echo yes; fi ;;
	esac
}

# journal_yes <rows file> <pids file>: of the undecided connections ("<row> <server>
# <address> <port> <pid,...>"), those whose server wrote its line of success for
# that peer: one bounded read of the journal for all of them (root's lines of
# those processes since the oldest of them began), each line taken only when its
# _EXE is the binary of the process named by its _PID ("<pid> <binary>"); prints
# the rows signed in
journal_yes() {
	command -v journalctl >/dev/null 2>&1 || return 0
	since=$(awk '{print $2}' "$2" | sort -n | head -n 1)
	case "$since" in ''|*[!0-9]*) return 0 ;; esac
	left=$((DEADLINE - $(date +%s)))
	[ "$left" -gt 0 ] || return 0
	# shellcheck disable=SC2046
	timeout "$left" journalctl -q --no-pager --system -b --since "@$((since - 1))" -n 50000 -o json \
		--output-fields=_PID,_EXE,MESSAGE _UID=0 $(awk '{print "_PID=" $1}' "$2") 2>/dev/null |
	awk -v rows="$1" -v pids="$2" '
		function field(name,   re, v) {
			re = "\"" name "\":\"([^\"\\\\]|\\\\.)*\""
			if (!match($0, re)) return ""
			v = substr($0, RSTART + length(name) + 4, RLENGTH - length(name) - 5)
			gsub(/\\"/, "\"", v); gsub(/\\\\/, "\\", v)
			return v
		}
		BEGIN {
			while ((getline line < pids) > 0) { split(line, f, " "); exe[f[1]] = f[3] }
			while ((getline line < rows) > 0) {
				split(line, f, " "); kind[f[1]] = f[2]; addr[f[1]] = f[3]; port[f[1]] = f[4]
				n = split(f[5], ps, ","); for (i = 1; i <= n; i++) of[ps[i]] = of[ps[i]] " " f[1]
			}
		}
		{
			pid = field("_PID"); bin = field("_EXE"); msg = field("MESSAGE")
			if (pid == "" || !(pid in exe) || bin != exe[pid] || msg == "") next
			m = split(msg, w, " ")
			k = split(of[pid], rs, " ")
			for (i = 1; i <= k; i++) {
				r = rs[i]; a = addr[r]; p = port[r]
				if (kind[r] == "openssh") {
					if (w[1] == "Accepted" && w[3] == "for" && w[5] == "from" && (w[6] == a || w[6] == "::ffff:" a) && w[7] == "port" && w[8] == p) yes[r] = 1
				} else if (index(msg, " auth succeeded for ") && w[m - 1] == "from" && (w[m] == a ":" p || w[m] == "::ffff:" a ":" p)) {
					yes[r] = 1
				}
			}
		}
		END { for (r in yes) print r }'
}

# on_ports <port,...>: the lines of `ss state established` whose local port is one of them
on_ports() { awk -v ports=",$1," '{n = split($3, a, ":"); if (index(ports, "," a[n] ",")) print}'; }
# listeners_on <port> <address> <family>: the listeners ($LIST) of that local address and port
listeners_on() {
	printf '%s\n' "$LIST" | awk -v p="$1" -v a="$2" -v f="*$3" '$4 == p && ($3 == "*" || $3 == f || $3 == a)'
}

# The whole scan is bounded (SCAN_BOUND seconds): a connection not decided by then
# is "unsure". The processes decide first; the journal is read once, for the
# connections they leave undecided.
SCAN_BOUND=60
sessions() {
	DEADLINE=$(($(date +%s) + SCAN_BOUND))
	INDEX=$(socket_index)
	LIST=$(listeners)
	[ -n "$LIST" ] || return 0
	PRIVSEP=$(privsep_uid)
	WORK=$(mktemp -d)
	ports=$(printf '%s\n' "$LIST" | awk '{print $4}' | sort -u | paste -sd, -)
	# only the connections on a listener's port go further (one pass over them all)
	ss -Htne state established 2>/dev/null | on_ports "$ports" > "$WORK/conns"
	row=0
	while read -r _ _ local peer ext; do
		row=$((row + 1))
		lport=${local##*:}
		laddr=$(addr_of "$local")
		match=$(listeners_on "$lport" "$laddr" "$(fam_of "$local")")
		[ -n "$match" ] || continue
		kind=$(printf '%s\n' "$match" | awk '{print $1; exit}')
		LPIDS=$(printf '%s\n' "$match" | awk -v k="$kind" '$1 == k {print $2}' | tr '\n' ' ')
		ino=$(inode_of "$ext")
		[ -n "$ino" ] || continue
		peer_parts "$peer"
		state=""; conn=""
		for pid in $(holders "$ino"); do
			c=$(accepted "$pid" "$kind") || continue
			conn=$c
			[ "$(date +%s)" -lt "$DEADLINE" ] || { state=unsure; break; }
			s=$(process_state "$kind" "$c")
			case "$state:$s" in
				*:yes) state=yes; break ;;
				:no) state=no ;;
			esac
		done
		[ -n "$conn" ] || continue
		if [ -z "$state" ] && [ "$kind" != tinysshd ] && start=$(start_of "$conn"); then
			# undecided: its own line of success may be in the journal (dropbear also
			# writes its children's lines under its listener when it logs to its
			# standard error under a unit)
			jp=$conn
			[ "$kind" != dropbear ] || jp="$jp $LPIDS"
			for p in $jp; do printf '%s %s %s\n' "$p" "$start" "$(exe_of "$p")" >> "$WORK/pids"; done
			printf '%s %s %s %s %s\n' "$row" "$kind" "$PA" "$PP" "$(printf '%s' "$jp" | tr ' ' ',')" >> "$WORK/rows"
		fi
		printf '%s %s %s %s %s\n' "$row" "$PA" "$lport" "$kind" "${state:-unsure}" >> "$WORK/states"
	done < "$WORK/conns"
	if [ -s "$WORK/rows" ]; then
		sort -u -k1,1n "$WORK/pids" -o "$WORK/pids"
		journal_yes "$WORK/rows" "$WORK/pids" > "$WORK/yes" || true
	fi
	touch "$WORK/states" "$WORK/yes"
	awk 'FILENAME == ARGV[1] {yes[$1] = 1; next} {print $2, $3, $4, (($1 in yes) ? "yes" : $5)}' "$WORK/yes" "$WORK/states" | sort -u
	rm -rf "$WORK"
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
