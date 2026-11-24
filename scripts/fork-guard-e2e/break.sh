#!/bin/sh
# shellcheck disable=SC2016 # the sed scripts quote the pieces' own code
# Breaks of the protection pieces on purpose, run inside a test machine by
# inner.sh (BREAK=<name>): each undoes one rule of what an SSH session is or of
# what is outside, and the group named in inner.sh's header must then fail.
set -eu
F=/opt/cosmos/fork/nftables
S=$F/ssh-sessions.sh
G=$F/cosmos-host-guard
case "$1" in
	dropbear-any|forged-ok)
		# every established connection is looked at; one held by a process named
		# dropbear (dropbear-any), or by one with an OpenSSH session's title
		# (forged-ok), counts as a signed-in session
		if [ "$1" = dropbear-any ]; then
			cond='[ "$(cat /proc/$h/comm 2>/dev/null)" = dropbear ]'
		else
			cond='title_of "$h" | grep -qE "^sshd[^ ]*: [^ ]+@(pts/[0-9]+|notty)"'
		fi
		cat > /tmp/brk <<EOF
		for h in \$(holders "\$(inode_of "\$ext")"); do if $cond; then a=\${peer%:*}; a=\${a#[}; a=\${a%]}; echo "\$a \${local##*:} openssh yes"; continue 2; fi; done
EOF
		sed -i -e 's/| on_ports "$ports" |/|/' -e '/^\twhile read -r _ _ local peer ext; do$/r /tmp/brk' "$S"
		;;
	priv-title)
		# the monitor's "<name> [priv]" title is taken as signed in (it is set as soon
		# as a client names a user)
		sed -i 's/^\t\t\topenssh_accepted "\$2" \&\& { echo yes; return; }$/\t\t\ttitle_of "$2" | grep -q "\\[priv\\]$" \&\& { echo yes; return; }; openssh_accepted "$2" \&\& { echo yes; return; }/' "$S"
		;;
	no-journal)
		# the servers' own journal lines are not looked at
		sed -i 's/^\ttimeout 5 journalctl -q --no-pager -b -o cat _PID=.*$/\ttrue/' "$S"
		;;
	any-socket)
		# any connection held below a signed-in sshd counts, whatever its local port
		sed -i -e 's/| on_ports "$ports" |/|/' \
			-e 's/^\tprintf .%s\\n. "\$LIST" | awk -v p="\$1" -v a="\$2" -v f=.*$/\tprintf "%s\\n" "$LIST" | awk "\\$1 == \\"openssh\\"" | head -n 1/' "$S"
		;;
	accept-any)
		# accepted() takes any holder, without descent from the listener
		sed -i 's/^\tp=\$1; depth=0; below=\$1$/\techo "$1"; return 0/' "$S"
		;;
	multipath-last)
		sed -i 's/devs = devs " " \$(j + 1) "=" via/devs = " " $(j + 1) "=" via/' "$G"
		;;
	def1-ignored)
		sed -i 's/if (\$i != "default" \&\& !wide(\$i)) next/if ($i != "default") next/' "$G"
		;;
	multicast-uplink)
		# any IPv4 prefix shorter than /8 counts, multicast included
		sed -i 's/return len < 8 \&\& o\[1\] + 0 < 224 }/return len < 8 }/' "$G"
		;;
	transient-narrow)
		# home stays home only with positive evidence, and its absence is written down
		sed -i -e 's/elif in_csv "\$dev" "\$homes" \&\& ! against_home "\$dev"; then/elif in_csv "$dev" "$homes" \&\& home_evidence "$dev"; then/' \
			-e '/^\t\t\tagainst_home "\$dev" || continue$/d' "$G"
		;;
	moved-home)
		# any private network on a home interface counts as home, and IPv4 passes there
		sed -i -e 's/ \&\& on_recorded4 "\$dev" \&\& pass4=/ \&\& pass4=/' \
			-e 's/^\t\t\tcase " \$net4 " in \*" \$p "\*) on4="\$on4 \$p" ;; \*) pend4="\$pend4 \$p" ;; esac$/\t\t\ton4="$on4 $p"/' "$G"
		;;
	stale-cache)
		# a refresh no longer rewrites the fast reload's cache (the reviewer's rv4-stale-cache)
		awk '/^\treload_base "\$now"$/ {c++; if (c == 2) next} {print}' "$G" > /tmp/g.new && cat /tmp/g.new > "$G"
		;;
	exitnode-outside)
		sed -i 's/^\t\tfor dev in \$wan; do .* vpnhome="\$vpnhome \$dev"; done$/\t\t:/' "$G"
		;;
	exitnode-anyone)
		# through an exit node every tailnet-range source counts as home
		sed -i -e 's/ip saddr @vpnpeer4 accept/ip saddr 100.64.0.0\/10 accept/' -e 's/ip6 saddr @vpnpeer6 accept/ip6 saddr fd7a:115c:a1e0::\/48 accept/' "$F/cosmos-host-guard.nft.in"
		;;
	unload-reloaded)
		# the watcher ignores the mark of a removal on purpose
		sed -i 's/^\t\[ ! -e "\$RUN\/unloaded" \] || return 0$/\t:/' "$G"
		;;
	*) echo "unknown break $1" >&2; exit 2 ;;
esac
