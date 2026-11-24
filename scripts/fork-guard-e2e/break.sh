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
		for h in \$(printf '%s' "\$procs" | grep -oE 'pid=[0-9]+' | cut -d= -f2); do if $cond; then a=\${peer%:*}; a=\${a#[}; a=\${a%]}; echo "\$a \${local##*:} openssh yes"; continue 2; fi; done
EOF
		sed -i -e 's/| on_ports "$ports" |/|/' -e '/^\twhile read -r _ _ local peer procs; do$/r /tmp/brk' "$S"
		;;
	tunnel-title)
		# only a title with a terminal or a command (user@pts/N, user@notty) is signed in
		sed -i '/ \\\[priv\\\]\$'"'"' \&\& return 0$/d' "$S"
		;;
	any-socket)
		# any connection held below a signed-in sshd counts, whatever its local port
		sed -i -e 's/| on_ports "$ports" |/|/' \
			-e 's/^\tprintf .%s\\n. "\$LIST" | awk -v p="\$1" -v a="\$2" .*$/\tprintf "%s\\n" "$LIST" | awk "\\$1 == \\"openssh\\"" | head -n 1/' "$S"
		;;
	multipath-last)
		sed -i 's/devs = devs " " \$(j + 1) "=" via/devs = " " $(j + 1) "=" via/' "$G"
		;;
	def1-ignored)
		sed -i 's/if (\$i != "default" \&\& !wide(\$i)) next/if ($i != "default") next/' "$G"
		;;
	transient-narrow)
		# home stays home only with positive evidence, and its absence is written down
		sed -i -e 's/elif in_csv "\$dev" "\$homes" \&\& ! against_home "\$dev"; then/elif in_csv "$dev" "$homes" \&\& home_evidence "$dev"; then/' \
			-e '/^\t\t\tagainst_home "\$dev" || continue$/d' "$G"
		;;
	exitnode-outside)
		sed -i 's/^\t\tfor dev in \$wan; do .* vpnhome="\$vpnhome \$dev"; done$/\t\t:/' "$G"
		;;
	*) echo "unknown break $1" >&2; exit 2 ;;
esac
