#!/bin/sh
# The SSH servers that listen on this machine and the SSH sessions open now, as
# the host guard's and Fail2ban's installers need them (fork/nftables/install.sh,
# fork/fail2ban/install.sh). Run as root; reads only ss and /proc.
#
#   sh ssh-sessions.sh listeners   "<server> <pid> <address> <port>" per listening socket
#   sh ssh-sessions.sh sessions    "<address> <local port> <server> <signed-in>" per session
#   sh ssh-sessions.sh explained <pid>
#                                  exit 0 when an SSH server's process is among the
#                                  ancestors of <pid> (its terminal comes from a session)
#
# <server> is openssh, dropbear or tinysshd; <signed-in> is yes, or unsure.
#
# What is trusted is what a user signed in over SSH cannot make:
# - a listener is a listening socket held by a root process whose executable,
#   read through /proc/<pid>/exe (never its name or title), is an SSH server's
#   binary owned by root and writable by root only, started by the service
#   manager (parent 1) or the main process of the server's unit; or held by
#   systemd itself (pid 1) on a port of ssh.socket, sshd.socket, dropbear.socket
#   or tinysshd.socket (socket activation);
# - a session is an ACCEPTED connection: an established socket whose local
#   address and port are those of such a listener, held by a process with an SSH
#   server's executable that descends from the listener (or, its listener gone or
#   systemd's, by a process of the same server whose parent is pid 1).
# Connections an SSH server makes outwards (-L, -D, -W forwards), visitors of a
# -R forward and processes with a forged name or title are not on a listener's
# port, so they never count.
#
# Signed in: OpenSSH's root monitor carries the user's name ("sshd...: user
# [priv]", or "user@pts/N" / "user@notty" for root's own logins); only a root
# process can set that title, and it exists for every signed-in connection,
# tunnels without a terminal (ssh -N, -W, ProxyJump) included. A connection
# whose monitor has no user yet is a stranger still signing in: not a session.
# dropbear and tinysshd do not say it in a title. A connection is signed in when
# its process has dropped to a user's uid (dropbear from 2024 on), has started a
# child (the shell or the command), or, for dropbear, when the journal holds its
# own line "... auth succeeded for '<user>' ... from <this peer's address:port>"
# written by root's dropbear binary (journald's _UID, _EXE and _PID come from the
# kernel: no user can forge them); dropbear up to 2022 (Debian 12, Ubuntu 24.04)
# keeps a signed-in tunnel (-N) as root without a child, so only that line tells
# it from a stranger still signing in. Any other connection is "unsure": a
# stranger still signing in and root's own tunnel cannot be told apart (also when
# dropbear logs nowhere the journal sees, e.g. with -E to a file).

set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin

uid_of() { awk '/^Uid:/ {print $2; exit}' "/proc/$1/status" 2>/dev/null || true; }
ppid_of() { awk '/^PPid:/ {print $2; exit}' "/proc/$1/status" 2>/dev/null || echo 0; }
title_of() { tr '\0' ' ' < "/proc/$1/cmdline" 2>/dev/null | sed 's/ *$//' || true; }

# server_of <pid>: openssh, dropbear or tinysshd when the process's executable is
# that server's binary, owned by root and not writable by anyone else
server_of() {
	exe=$(readlink "/proc/$1/exe" 2>/dev/null || true)
	exe=${exe% (deleted)}
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

# the listening address without brackets, scope or the IPv4-mapped prefix; "*" for any
addr_of() { # ss's "address:port"
	a=${1%:*}
	a=${a#[}; a=${a%]}
	a=${a%%%*}
	a=${a%]}
	case "$a" in ::ffff:*.*) a=${a#::ffff:} ;; esac
	case "$a" in '*'|0.0.0.0|::) a='*' ;; esac
	printf '%s\n' "$a"
}

main_pids() {
	systemctl show -p MainPID --value ssh.service sshd.service dropbear.service tinysshd.service 2>/dev/null | grep -E '^[1-9][0-9]*$' | tr '\n' ' ' || true
}
# the ports of the SSH servers' socket units: "<server> <port>"
unit_ports() {
	for unit in ssh.socket sshd.socket dropbear.socket tinysshd.socket; do
		case "$unit" in ssh*) kind=openssh ;; dropbear*) kind=dropbear ;; *) kind=tinysshd ;; esac
		systemctl show -p Listen --value "$unit" 2>/dev/null | tr ' ' '\n' | grep -E ':[0-9]{1,5}$' | sed "s/.*:/$kind /" || true
	done
}

listeners() {
	mains=" $(main_pids) "
	uports=$(unit_ports)
	ss -Htlnp 2>/dev/null | while read -r _ _ _ local _ procs; do
		port=${local##*:}
		case "$port" in ''|*[!0-9]*) continue ;; esac
		addr=$(addr_of "$local")
		for pid in $(printf '%s' "$procs" | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u); do
			if [ "$pid" = 1 ]; then
				# systemd holds the socket of a socket unit
				printf '%s\n' "$uports" | awk -v p="$port" -v a="$addr" '$2 == p {print $1, 1, a, p}'
				continue
			fi
			[ "$(uid_of "$pid")" = 0 ] || continue
			kind=$(server_of "$pid") || continue
			case "$mains" in *" $pid "*) ;; *) [ "$(ppid_of "$pid")" = 1 ] || continue ;; esac
			printf '%s %s %s %s\n' "$kind" "$pid" "$addr" "$port"
		done
	done | sort -u
}

signed_in_openssh() { # pid: yes when a root OpenSSH process from it up carries the user's name
	p=$1; depth=0
	while [ "$p" -gt 1 ] 2>/dev/null && [ "$depth" -lt 64 ]; do
		case " $LPIDS " in *" $p "*) break ;; esac
		if [ "$(uid_of "$p")" = 0 ] && [ "$(server_of "$p" || true)" = openssh ]; then
			t=$(title_of "$p")
			# the monitor of a signed-in connection (any: with a terminal, a command or none)
			printf '%s\n' "$t" | grep -qE '^sshd[a-z-]*: [^ []+ \[priv\]$' && return 0
			# root's own session, whose process is root's too
			printf '%s\n' "$t" | grep -qE '^sshd[a-z-]*: [^ []+@(pts/[0-9]+|notty)$' && return 0
		fi
		p=$(ppid_of "$p"); depth=$((depth + 1))
	done
	return 1
}

# dropbear_logged <connection pid> <peer as ss prints it>: root's dropbear wrote,
# from that process (or from its listener, when dropbear logs to its standard
# error under systemd), that this peer's address and port signed in
dropbear_logged() {
	command -v journalctl >/dev/null 2>&1 || return 1
	pa=${2%:*}; pp=${2##*:}
	pa=${pa#[}; pa=${pa%]}; pa=${pa%%%*}
	case "$pp" in ''|*[!0-9]*) return 1 ;; esac
	for lp in "$1" $LPIDS; do
		exe=$(readlink "/proc/$lp/exe" 2>/dev/null || true)
		exe=${exe% (deleted)}
		[ -n "$exe" ] || continue
		if timeout 5 journalctl -q --no-pager -b -o cat _PID="$lp" _UID=0 _EXE="$exe" 2>/dev/null |
			awk -v a="$pa:$pp" -v b="::ffff:$pa:$pp" '/ auth succeeded for / && $(NF - 1) == "from" && ($NF == a || $NF == b) {found = 1} END {exit !found}'; then
			return 0
		fi
	done
	return 1
}

# accepted <holder pid> <server>: the holder descends from one of $LPIDS (the
# listeners of that socket), or is that server's process whose parent is pid 1;
# prints the process just below the listener (the connection's own process)
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

# on_ports <port,...>: the lines of `ss state established` whose local port is one of them
on_ports() { awk -v ports=",$1," '{n = split($3, a, ":"); if (index(ports, "," a[n] ",")) print}'; }
# listeners_on <port> <address>: the listeners ($LIST) of that local address and port
listeners_on() {
	printf '%s\n' "$LIST" | awk -v p="$1" -v a="$2" '$4 == p && ($3 == "*" || $3 == a)'
}

sessions() {
	LIST=$(listeners)
	[ -n "$LIST" ] || return 0
	ports=$(printf '%s\n' "$LIST" | awk '{print $4}' | sort -u | paste -sd, -)
	# only the connections on a listener's port go further (one pass over them all)
	ss -Htnp state established 2>/dev/null | on_ports "$ports" |
	while read -r _ _ local peer procs; do
		lport=${local##*:}
		laddr=$(addr_of "$local")
		# the listeners of this local address and port
		match=$(listeners_on "$lport" "$laddr")
		[ -n "$match" ] || continue
		kind=$(printf '%s\n' "$match" | awk '{print $1; exit}')
		LPIDS=$(printf '%s\n' "$match" | awk -v k="$kind" '$1 == k {print $2}' | tr '\n' ' ')
		state=""
		for pid in $(printf '%s' "$procs" | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u); do
			conn=$(accepted "$pid" "$kind") || continue
			case "$kind" in
				openssh) if signed_in_openssh "$pid"; then state=yes; break; fi ;;
				*)
					if [ "$(uid_of "$conn")" != 0 ] || [ -n "$(cat /proc/"$conn"/task/*/children 2>/dev/null | tr -d ' \n')" ]; then
						state=yes; break
					fi
					if [ "$kind" = dropbear ] && dropbear_logged "$conn" "$peer"; then state=yes; break; fi
					state=unsure ;;
			esac
		done
		[ -n "$state" ] || continue
		addr=${peer%:*}
		addr=${addr#[}; addr=${addr%]}
		case "$addr" in ::ffff:*.*) addr=${addr#::ffff:} ;; esac
		printf '%s %s %s %s\n' "$addr" "$lport" "$kind" "$state"
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
