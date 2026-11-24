#!/bin/sh
# The end-to-end tests of the protection pieces (fork/nftables, fork/fail2ban,
# fork/unattended-upgrades), run by scripts/fork-guard-e2e.sh inside a Docker in
# Docker of the test's own: systemd test machines with sshd and Docker, home and
# outside clients and a router on networks of their own (see lib.sh).
#
#   sh inner.sh <machine image tag> [group...]
# Groups: guard ssh live follow routes nftsvc docker docker-noipv6 docker-nft vpn pub4
# spoof foreign race validate uplink failclosed ipv4later v4gap v4boot homewiden moved
# sshservers sessions strangers nohome churn flushstorm vpnports vpnexit cloud allowfile
# confirmwindow ufw firewalld fail2ban f2bsessions updates (default: all);
# "fast" is guard, the unattended SSH cases, the live-session cases and what a session is.
# BREAK=open-port|no-ssh-allow|no-undo|ssh-allow-all|rv-fc-fwd|rv-ll-all breaks the guard on purpose:
# the tests must then fail. So do the breaks of what an SSH session is and of
# what is outside (each with the group that catches it; break.sh applies them):
#   dropbear-any      any process named dropbear holding a connection is a session (sessions)
#   forged-ok         an OpenSSH process title alone makes a session (sessions)
#   priv-title        OpenSSH's "<name> [priv]" title means signed in (strangers, f2bsessions)
#   no-journal        the servers' own records of a sign-in are not read (strangers)
#   silence-no        OpenSSH: no line of success means "no" (strangers)
#   dropbear-silence-no  dropbear: "no" without proof that its successes are logged (strangers)
#   no-port           OpenSSH's line of success not matched on the port (strangers)
#   no-exe            the writer's binary not matched (no probe can tell: see the plan)
#   no-since          the journal read from the boot's oldest 5000 lines (not observable: see the plan)
#   peers-none        Tailscale's devices never read (vpnexit)
#   pending-closed    a new home network gets no SSH until confirmed (moved)
#   any-socket        any connection a signed-in sshd holds is a session (sessions, f2bsessions)
#   accept-any        a holder need not descend from the listener (sessions)
#   multipath-last    of a multipath default route only the last nexthop counts (uplink)
#   def1-ignored      routes that cover the internet without "default" do not count (uplink)
#   multicast-uplink  a multicast route makes an uplink (uplink)
#   transient-narrow  home narrows on missing evidence (v4gap, v4boot)
#   moved-home        any private network on a home interface is home (moved)
#   stale-cache       a refresh does not renew the fast reload's cache (flushstorm)
#   unload-reloaded   the watcher loads again a table taken away on purpose (flushstorm)
#   exitnode-outside  Tailscale's own peers are refused once it carries a default route (vpnexit)
#   exitnode-anyone   through an exit node every tailnet-range source is home (vpnexit)

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

TAG=$1
shift
MACHINE=$TAG
IMAGE=cosmos-guard-test-machine:$TAG
GROUPS_WANTED=${*:-all}
[ "$GROUPS_WANTED" = all ] && GROUPS_WANTED="guard ssh live follow routes nftsvc docker docker-noipv6 docker-nft vpn pub4 spoof foreign race validate uplink failclosed ipv4later v4gap v4boot homewiden moved sshservers sessions strangers nohome churn flushstorm vpnports vpnexit cloud allowfile confirmwindow ufw firewalld fail2ban f2bsessions updates"
[ "$GROUPS_WANTED" = fast ] && GROUPS_WANTED="guard ssh-unattended live sessions strangers uplink failclosed"
BREAK=${BREAK:-}
G=/opt/cosmos/fork/nftables
# The test machines are containers: to the guard a virtual machine that may be a
# VPS. They stand for a machine at home, so the installer is told so (the cloud
# and VPS cases are tested without it).
GV=--home-vm

fresh() { # nat|pub: a new machine on new networks (DOCKER_MODE: ipv6, noipv6, nft)
	topo_down
	topo_up "$1"
	machine_up "$IMAGE"
	if ! docker exec "$M" sh -c 'docker ps --format "{{.Names}}" | grep -q priv && docker ps --format "{{.Names}}" | grep -q pub' 2>/dev/null; then
		# counted as a failure of this group; the next group tries a new machine
		bad "group $GROUP: the test machine $IMAGE did not start, the group did not run ($(docker exec "$M" journalctl -u docker --no-pager 2>/dev/null | grep -o 'failed to start daemon: .*' | tail -n 1 | cut -c1-200))"
		topo_down
		return 1
	fi
	pieces_sum() { m sh -c 'cat /opt/cosmos/fork/nftables/* /opt/cosmos/fork/fail2ban/install.sh | md5sum'; }
	[ -z "$BREAK" ] || unbroken=$(pieces_sum)
	case "$BREAK" in
		dropbear-any|forged-ok|priv-title|no-journal|silence-no|dropbear-silence-no|no-port|no-exe|no-since|peers-none|pending-closed|any-socket|accept-any|multipath-last|def1-ignored|multicast-uplink|transient-narrow|moved-home|stale-cache|exitnode-outside|exitnode-anyone|unload-reloaded)
			docker exec -i "$M" sh -s "$BREAK" < "$HERE/break.sh" ;;
		open-port) msh "sed -i 's/^\[ -n \"\$PUBLIC_TCP\$PUBLIC_UDP\" \] ||/add_port 9999 tcp broken; &/' $G/install.sh" ;;
		no-ssh-allow) msh "sed -i -e 's/^\tfor a in \$OUTSIDE_PEERS; do\$/\tfor a in ; do/' -e 's/^\t\tfor a in \$OUTSIDE_PEERS; do\$/\t\tfor a in ; do/' $G/install.sh" ;;
		no-undo) msh "sed -i 's/^\tsystemd-run --quiet --unit=/\ttrue || systemd-run --quiet --unit=/' $G/install.sh" ;;
		ssh-allow-all) msh "sed -i 's/ tcp dport @ssh_port accept/ accept/' $G/cosmos-host-guard.nft.in" ;;
		rv-fc-fwd) msh "sed -i '/chain forward {/,/policy accept;/ s/policy accept;/policy accept;\\n\\t\\tiifname @failclosed_if accept/' $G/cosmos-host-guard.nft.in" ;;
		rv-ll-all) msh "sed -i '0,/ct state established,related accept/ s/ct state established,related accept/ct state established,related accept\\n\\t\\tip6 saddr fe80::\\/10 accept/' $G/cosmos-host-guard.nft.in" ;;
	esac
	# a break that does not apply would prove nothing
	if [ -n "$BREAK" ] && [ "$unbroken" = "$(pieces_sum)" ]; then bad "BREAK=$BREAK did not change the pieces"; fi
}

# install from a client through SSH (sudo, as an administrator would)
# guard_install <client> <args...>; sets RC and leaves the output in OUT
guard_install() {
	c=$1; shift
	OUT=$(docker exec "$c" sh -c "ssh $SSHO tester@$TM6 'sudo sh $G/install.sh $GV $*; echo RC=\$?'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
}
# a table of the test's own, after the guard (priority 10): counts UDP to 9998 that passed it
probe_table() {
	docker exec -i "$M" nft -f - <<'EOF'
table inet cg_probe {
	counter passed { }
	chain c { type filter hook input priority 10; policy accept; udp dport 9998 counter name passed; }
}
EOF
}

# install from the machine's console (no SSH session)
guard_install_local() {
	OUT=$(m sh $G/install.sh $GV "$@" 2>&1; echo "RC=$?")
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
}
says() { printf '%s' "$OUT" | grep -q -- "$1"; }
guard_uninstall() {
	OUT=$(m sh $G/uninstall.sh --yes 2>&1; echo "RC=$?")
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
}

expect_rc() { # code description
	if [ "${RC:-x}" = "$1" ]; then ok "$2 (exit $1)"; else bad "$2: exit ${RC:-none}, wanted $1"; printf '%s\n' "$OUT" | tail -n 15; fi
}

# what outside and home see over IPv6 (and IPv4 when asked)
probe_matrix() { # label
	check "$1: outside v6 reaches Cosmos' HTTPS port 2053" tcp "$OC" "$TM6" 2053
	check "$1: outside v6 reaches the public container port 8443 (forward path)" tcp "$OC" "$TM6" 8443
	check_not "$1: outside v6 does not reach port 9999 of the host" tcp "$OC" "$TM6" 9999
	check_not "$1: outside v6 does not reach the container port 18080 (forward path)" tcp "$OC" "$TM6" 18080
	check_not "$1: outside v6 does not reach SSH" tcp "$OC" "$TM6" 22
	check_not "$1: outside v6 does not reach the HTTP port 2052 (no HTTP challenge)" tcp "$OC" "$TM6" 2052
	for p in 2053 8443 9999 18080 22 2052; do
		check "$1: home v6 reaches port $p" tcp "$HC" "$TM6" "$p"
	done
}

g_guard() {
	log "== guard: dry run, install, probes, twice, uninstall ($TAG)"
	fresh nat || return 1
	before_rules=$(ruleset); before_files=$(files_snapshot); before_ipt=$(xtables)
	had_nft=$(m sh -c 'command -v nft' || true)
	guard_uninstall
	expect_rc 0 "uninstall without an installation"
	check "uninstall without an installation says so" sh -c "printf '%s' \"\$1\" | grep -q 'not installed'" _ "$OUT"
	guard_install "$HC" --dry-run --port 2053 --port 8443
	expect_rc 0 "dry run"
	check "dry run: the ruleset is unchanged" test "$before_rules" = "$(ruleset)"
	check "dry run: no file changed" test "$before_files" = "$(files_snapshot)"
	if [ -z "$had_nft" ]; then
		check "dry run: nftables was not installed" sh -c "! docker exec $M sh -c 'command -v nft'"
		packages=$(m sh $G/install.sh --print-packages)
		check "--print-packages names nftables" test "$packages" = nftables
	else
		packages=$(m sh $G/install.sh --print-packages)
		check "--print-packages names nothing (nftables came with Docker here)" test -z "$packages"
	fi
	guard_install "$HC" --yes --port 2053 --port 8443 --record /root/record
	expect_rc 0 "unattended install from home"
	check "the record names the table, the units and the status" sh -c "docker exec $M grep -q 'nftables	status	installed' /root/record && docker exec $M grep -q 'nftables	unit	cosmos-host-guard.service' /root/record"
	[ -n "$had_nft" ] || check "the record names the package it installed" m grep -q 'nftables	package	nftables' /root/record
	check "nftables.service is not enabled" sh -c "[ \"\$(docker exec $M systemctl is-enabled nftables.service 2>/dev/null)\" != enabled ]"
	check "the units are active" m systemctl is-active --quiet cosmos-host-guard.service cosmos-host-guard-watch.service cosmos-host-guard-refresh.timer
	probe_matrix "NAT host"
	check "IPv4 is not filtered behind NAT: outside v4 reaches 9999 (the router forwards only what is asked)" tcp "$OC" "$TM4" 9999
	check "the drop counters count" sh -c "docker exec $M nft list counter inet cosmos_host_guard dropped_input | grep -qv 'packets 0 '"
	check "the forward counter counts (the container port 18080)" sh -c "docker exec $M nft list counter inet cosmos_host_guard dropped_forward | grep -qv 'packets 0 '"
	check "Docker's own rules are still there" docker_rules
	guard_install "$HC" --yes --port 2053 --port 8443 --record /root/record2
	expect_rc 0 "install a second time"
	check "the second install says already-installed" m grep -q 'status	already-installed' /root/record2
	check "only one table of the guard" sh -c "[ \$(docker exec $M nft list tables | grep -c cosmos_host_guard) = 1 ]"
	probe_matrix "after the second install"
	# the ports from Cosmos' configuration (HTTPS only), then with Let's Encrypt's HTTP challenge
	guard_install "$HC" --yes
	expect_rc 0 "install with the ports of Cosmos' configuration"
	check "Cosmos' HTTPS port is public" tcp "$OC" "$TM6" 2053
	check_not "the HTTP port is not public with a self-signed certificate" tcp "$OC" "$TM6" 2052
	msh 'sed -i "s/\"SELFSIGNED\"/\"LETSENCRYPT\"/" /var/lib/cosmos/cosmos.config.json'
	guard_install "$HC" --yes
	check "the HTTP port is public with Let's Encrypt's HTTP challenge" tcp "$OC" "$TM6" 2052
	check "the installer says why" sh -c "printf '%s' \"\$1\" | grep -q 'HTTP challenge\\|checks the certificate over HTTP'" _ "$OUT"
	msh 'sed -i "s/\"LETSENCRYPT\"/\"SELFSIGNED\"/" /var/lib/cosmos/cosmos.config.json'
	guard_uninstall
	expect_rc 0 "uninstall"
	check "after uninstall Docker's rules are as before the install (iptables-save)" test "$before_ipt" = "$(xtables)"
	check "after uninstall no table of the guard is left" sh -c "! docker exec $M nft list tables | grep -q cosmos_host_guard"
	before_rules=$(ruleset)
	guard_install "$HC" --yes --port 2053
	guard_uninstall
	check "install and uninstall again: the whole ruleset is as before (nft -s list ruleset)" test "$before_rules" = "$(ruleset)"
	check "after uninstall no unit or file of the guard is left" sh -c "! docker exec $M sh -c 'ls /etc/systemd/system/cosmos-host-guard* /usr/local/sbin/cosmos-host-guard /etc/cosmos-host-guard /var/lib/cosmos-protection/nftables.state 2>/dev/null | grep .'"
	check "after uninstall outside reaches 9999 again" tcp "$OC" "$TM6" 9999
	guard_uninstall
	expect_rc 0 "uninstall a second time"
}

# an SSH session that ticks every second while the installer runs through it;
# SSH_OUT holds what it printed, SSH_RC the ssh client's exit code
session_install() { # client args...
	c=$1; shift
	docker exec "$c" sh -c "rm -f /tmp/in /tmp/out; mkfifo /tmp/in; (sleep 900 > /tmp/in &) ; (ssh $SSHO tester@$TM6 'sh -c \"(while :; do echo TICK \\\$(date +%s); sleep 1; done) & t=\\\$!; sudo sh $G/install.sh $GV $*; echo RC=\\\$?; sleep 2; kill \\\$t\"' < /tmp/in > /tmp/out 2>&1; echo SSH_RC=\$? >> /tmp/out) &"
}
session_wait() { # pattern seconds
	for _ in $(seq 1 "$2"); do
		docker exec "$c" grep -q "$1" /tmp/out && return 0
		sleep 1
	done
	return 1
}
# a write to the session's input that cannot hang the run when the session died
session_say() { docker exec "$c" timeout 5 sh -c "echo '$1' > /tmp/in" || bad "the session did not take the answer '$1' (it ended)"; }
session_end() { docker exec "$c" sh -c 'pkill -f "ssh .*tester@"; pkill -f "sleep 900"; true'; }
session_result() {
	session_wait 'SSH_RC=' 60 || true
	SSH_OUT=$(docker exec "$c" cat /tmp/out)
	SSH_RC=$(printf '%s\n' "$SSH_OUT" | sed -n 's/^SSH_RC=//p')
	RC=$(printf '%s\n' "$SSH_OUT" | sed -n 's/^RC=//p' | tail -n 1)
	OUT=$SSH_OUT
	docker exec "$c" sh -c 'pkill -f "sleep 900" 2>/dev/null; true'
}
# the ticks came without a gap longer than 3 s, and the session ended by itself
no_gap() {
	printf '%s\n' "$SSH_OUT" | awk '/^TICK / {t = $2; if (last && t - last > 3) gap = 1; last = t; n++} END {exit (gap || n < 3)}'
}

g_ssh_unattended() {
	log "== ssh: unattended runs from outside ($TAG)"
	fresh nat || return 1
	before_rules=$(ruleset); before_files=$(files_snapshot)
	session_install "$OC" --yes --port 2053
	session_result
	expect_rc 4 "unattended from outside without --allow-ssh-from-outside is refused"
	check "the refusal says why and names the address" sh -c "printf '%s' \"\$1\" | grep -q 'Refused for the SSH sessions.*$OC6'" _ "$SSH_OUT"
	check "refused: the ruleset is unchanged" test "$before_rules" = "$(ruleset)"
	check "refused: no file changed" test "$before_files" = "$(files_snapshot)"
	session_install "$OC" --yes --port 2053 --allow-ssh-from-outside
	session_result
	expect_rc 0 "unattended from outside with --allow-ssh-from-outside"
	check "the session survived the install (ticks without a gap, ssh ended normally)" sh -c "[ '$SSH_RC' = 0 ] && printf '%s\n' \"\$1\" | awk '/^TICK / {t = \$2; if (last && t - last > 3) gap = 1; last = t; n++} END {exit (gap || n < 3)}'" _ "$SSH_OUT"
	check "a new SSH connection from the same address works" ssh_run "$OC" true
	check "SSH answers another outside address (the port was kept open)" ssh_run "$OC2" true
	check_not "another outside address does not reach 9999" tcp "$OC2" "$TM6" 9999
	guard_uninstall
	session_install "$HC" --yes --port 2053
	session_result
	expect_rc 0 "unattended from home"
	check "the home session survived the install" sh -c "[ '$SSH_RC' = 0 ]"
	check_not "SSH does not answer from outside after a home install" tcp "$OC2" "$TM6" 22
	guard_uninstall
}

g_ssh() {
	g_ssh_unattended || return 1
	log "== ssh: interactive runs from outside, under sudo ($TAG)"
	before_rules=$(ruleset)
	# confirmed in time
	session_install "$OC" --port 2053 --confirm-timeout 60
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	if session_wait 'Confirm?' 30; then ok "the installer asks for a confirmation"; else bad "the installer did not ask for a confirmation"; session_result; session_end; return; fi
	check "the undo timer is armed" m systemctl is-active --quiet cosmos-host-guard-undo.timer
	check "while waiting: a NEW SSH connection from the session's address works" ssh_run "$OC" true
	check_not "while waiting: another outside address does not reach SSH" tcp "$OC2" "$TM6" 22
	session_say yes
	session_result
	expect_rc 0 "interactive from outside, confirmed"
	check "the session survived (ticks without a gap)" sh -c "[ '$SSH_RC' = 0 ] && printf '%s\n' \"\$1\" | awk '/^TICK / {t = \$2; if (last && t - last > 3) gap = 1; last = t; n++} END {exit (gap || n < 3)}'" _ "$SSH_OUT"
	check "the session's address was found from sshd's sockets and allowed" m grep -q "^$OC6 " /etc/cosmos-host-guard/allow
	check "the allowance runs out in 24 hours (said, and in the kernel with an expiry)" sh -c "printf '%s' \"\$1\" | grep -q 'ONLY.*$OC6 may reach SSH from outside, until' && docker exec $M nft list set inet cosmos_host_guard ssh_allow6 | grep -q 'expires'" _ "$SSH_OUT"
	# shellcheck disable=SC2016
	until=$(m awk -v a="$OC6" '$1 == a {print $2}' /etc/cosmos-host-guard/allow)
	check "the allowance's end is 24 h from now" sh -c "n=\$(date +%s); [ \$(( $until - n )) -gt 86000 ] && [ \$(( $until - n )) -le 86400 ]"
	check_not "the allowed address reaches ONLY the SSH port (not 9999)" tcp "$OC" "$TM6" 9999
	check_not "the allowed address reaches ONLY the SSH port (not the container port 18080)" tcp "$OC" "$TM6" 18080
	check "the undo timer is gone after the confirmation" sh -c "! docker exec $M systemctl is-active --quiet cosmos-host-guard-undo.timer"
	check "the guard stays installed" guard_loaded
	sleep 3
	check "still loaded after the undo's time would have passed" guard_loaded
	check "the confirmed session's address still reaches SSH" ssh_run "$OC" true
	check_not "another outside address does not reach SSH" tcp "$OC2" "$TM6" 22
	guard_uninstall
	# nobody confirms: the undo fires, the session lives on
	session_install "$OC" --port 2053 --confirm-timeout 30
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
	check "loaded while waiting" guard_loaded
	sleep 34
	session_result
	expect_rc 5 "not confirmed: undone"
	check "not confirmed: the table is gone" sh -c "! docker exec $M nft list table inet cosmos_host_guard"
	check "not confirmed: the ruleset is as before" test "$before_rules" = "$(ruleset)"
	check "not confirmed: no unit was installed" sh -c "! docker exec $M test -e /etc/systemd/system/cosmos-host-guard.service"
	check "not confirmed: the session survived" sh -c "[ '$SSH_RC' = 0 ]"
	# the installer dies while waiting: the undo still fires (a systemd timer, not this shell)
	session_install "$OC" --port 2053 --confirm-timeout 30
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
	# the installer dies without a word (as with a power cut of the client
	# and a kill of the shell), then the session goes
	# shellcheck disable=SC2016
	msh 'kill -9 "$(cat /run/cosmos-host-guard/installer.pid)"'
	docker exec "$OC" sh -c 'pkill -f "ssh .*tester@" ; true'
	check "the session is gone" sh -c "! docker exec $OC pgrep -f 'ssh .*tester@'"
	check "loaded after the installer and the session died" guard_loaded
	sleep 34
	check "the undo fired without the session" sh -c "! docker exec $M nft list table inet cosmos_host_guard"
	check "after the undo the ruleset is as before" test "$before_rules" = "$(ruleset)"
	check "after the undo SSH answers from outside again" ssh_run "$OC2" true
	docker exec "$OC" sh -c 'pkill -f "sleep 900"; true'
	# only the session is cut (the installer gets its hang-up): taken away as well
	session_install "$OC" --port 2053 --confirm-timeout 30
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
	docker exec "$OC" sh -c 'pkill -f "ssh .*tester@" ; true'
	sleep 34
	check "a cut session takes the guard away too" sh -c "! docker exec $M nft list table inet cosmos_host_guard"
	check "and nothing is left installed" sh -c "! docker exec $M test -e /etc/systemd/system/cosmos-host-guard.service"
	docker exec "$OC" sh -c 'pkill -f "sleep 900"; true'
}

g_follow() {
	log "== follow: address changes without a gap ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install"
	# a client of the new prefix at home
	docker exec "$HC" ip -6 addr add 2001:db8:1:1::20/64 dev eth0
	# probes every 0.2 s from home (must always answer) and from outside (must never)
	docker exec -d "$HC" sh -c 'rm -f /tmp/p; while [ ! -e /tmp/stop ]; do if nc -z -w 1 2001:db8:1::10 9999; then echo ok; else echo FAIL; fi >> /tmp/p; sleep 0.2; done'
	docker exec -d "$OC" sh -c 'rm -f /tmp/p; while [ ! -e /tmp/stop ]; do if nc -z -w 1 2001:db8:1::10 9999; then echo OPEN; else echo closed; fi >> /tmp/p; sleep 0.2; done'
	for i in 1 2 3 4 5; do
		m ip -6 addr add 2001:db8:1:1::10/64 dev eth0
		t0=$(date +%s)
		if [ "$i" = 1 ]; then
			# a new global prefix is not home by itself: it waits for confirmation;
			# once confirmed it is home whenever it comes back
			waiting=0
			for _ in $(seq 1 30); do in_set pending6 2001:db8:1:1::/64 && { waiting=1; break; }; sleep 0.5; done
			check "round $i: the new global prefix is seen and waits for confirmation" test "$waiting" = 1
			check_not "round $i: and does not count as home before" docker exec "$HC" nc -z -w 1 -s 2001:db8:1:1::20 2001:db8:1:1::10 9999
			m /usr/local/sbin/cosmos-host-guard confirm >/dev/null 2>&1
		fi
		followed=0
		# the watcher refreshes once per burst: after 2 s of quiet, at most every 10 s
		for _ in $(seq 1 60); do
			if docker exec "$HC" nc -z -w 1 -s 2001:db8:1:1::20 2001:db8:1:1::10 9999 2>/dev/null; then followed=1; break; fi
			sleep 0.2
		done
		t1=$(date +%s)
		if [ "$followed" = 1 ]; then ok "round $i: a new home prefix is followed within $((t1 - t0)) s"; else bad "round $i: the new home prefix was not followed"; fi
		m ip -6 addr del 2001:db8:1:1::10/64 dev eth0
		gone=0
		for _ in $(seq 1 30); do in_set onlink6 2001:db8:1:1::/64 || { gone=1; break; }; sleep 0.5; done
		check "round $i: the old prefix is gone from the guard within 15 s" test "$gone" = 1
		check_not "round $i: the old prefix is gone from the guard" m nft get element inet cosmos_host_guard onlink6 '{ 2001:db8:1:1::/64 }'
		m systemctl reload cosmos-host-guard.service
		m /usr/local/sbin/cosmos-host-guard refresh force
		m systemctl start cosmos-host-guard-refresh.service
	done
	docker exec "$HC" touch /tmp/stop; docker exec "$OC" touch /tmp/stop
	sleep 2
	home=$(docker exec "$HC" sh -c 'grep -c . /tmp/p; grep -c FAIL /tmp/p' | tr '\n' ' ')
	out=$(docker exec "$OC" sh -c 'grep -c . /tmp/p; grep -c OPEN /tmp/p' | tr '\n' ' ')
	check "home: every probe answered during the changes and reloads ($home: probes, failures)" sh -c "set -- $home; [ \$1 -gt 10 ] && [ \$2 = 0 ]"
	check "outside: no probe got through during the changes and reloads ($out: probes, open)" sh -c "set -- $out; [ \$1 -gt 10 ] && [ \$2 = 0 ]"
	docker exec "$HC" rm -f /tmp/stop; docker exec "$OC" rm -f /tmp/stop
}

g_docker() {
	log "== docker: restart, recreated container, reboot ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053 --port 8443
	expect_rc 0 "install"
	m systemctl restart docker
	for _ in $(seq 1 30); do tcp "$HC" "$TM6" 8443 && break; sleep 1; done
	check "Docker restarted: the guard is still loaded" guard_loaded
	probe_matrix "after Docker restarted"
	msh 'docker rm -f pub >/dev/null && docker run -d --restart=always --name pub -p 8443:80 busybox:1.36 httpd -f -p 80 >/dev/null'
	for _ in $(seq 1 30); do tcp "$HC" "$TM6" 8443 && break; sleep 1; done
	probe_matrix "after the container was recreated"
	reboot_machine
	check "after a reboot the guard is loaded" guard_loaded
	check "after a reboot the units are active" m systemctl is-active --quiet cosmos-host-guard.service cosmos-host-guard-watch.service cosmos-host-guard-refresh.timer
	check "after a reboot the outside interface is in the guard" m nft get element inet cosmos_host_guard wan_if '{ "eth0" }'
	check "after a reboot Docker's rules are there" docker_rules
	probe_matrix "after a reboot"
	guard_uninstall
}

g_vpn() {
	log "== vpn: a VPN interface and a VPN's port ($TAG)"
	fresh nat || return 1
	# a VPN peer behind an interface without a default route (as tailscale0 or wg0)
	msh 'ip netns add vpnpeer && ip link add vpn0 type veth peer name vpn0p && ip link set vpn0p netns vpnpeer && ip addr add 100.100.1.1/24 dev vpn0 && ip -6 addr add fd7a:115c:a1e0::1/64 dev vpn0 && ip link set vpn0 up && ip netns exec vpnpeer sh -c "ip addr add 100.100.1.2/24 dev vpn0p; ip -6 addr add fd7a:115c:a1e0::2/64 dev vpn0p; ip link set vpn0p up; ip link set lo up"'
	# a VPN daemon listening for direct connections: tailscaled.service, root,
	# the packaged path (a stand-in binary), its default port 41641
	# shellcheck disable=SC2016
	msh 'cp "$(command -v nc)" /usr/sbin/tailscaled; printf "[Service]\nExecStart=/bin/sh -c \"exec /usr/sbin/tailscaled -6 -u -l -k 41641 > /tmp/vpn.udp 2>&1\"\n" > /etc/systemd/system/tailscaled.service; systemctl daemon-reload; systemctl start tailscaled'
	# impostors: a normal user's process named tailscaled, and root's outside the unit
	# shellcheck disable=SC2016
	msh 'cp "$(command -v nc)" /tmp/tailscaled; chmod 755 /tmp/tailscaled; su tester -s /bin/sh -c "setsid /tmp/tailscaled -6 -u -l -k 5555 > /tmp/imp.udp 2>&1 &"; (setsid /usr/sbin/tailscaled -6 -u -l -k 33696 > /tmp/imp2.udp 2>&1 &)'
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install with a VPN"
	check "the VPN's port was found (tailscaled.service, root, the packaged path)" m nft get element inet cosmos_host_guard vpn_udp '{ 41641 }'
	check_not "a normal user's process named tailscaled gets no port" in_set vpn_udp 5555
	check_not "root's tailscaled outside the unit (another socket) gets no port" in_set vpn_udp 33696
	docker exec "$OC" sh -c "echo intruder | nc -u -w 1 $TM6 5555" >/dev/null 2>&1
	sleep 1
	check_not "outside does not reach the impostor's port" m grep -q intruder /tmp/imp.udp
	check_not "the VPN interface is not an outside interface" m nft get element inet cosmos_host_guard wan_if '{ "vpn0" }'
	check "a VPN peer reaches port 9999" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 9999
	check "a VPN peer reaches SSH" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 22
	docker exec "$OC" sh -c "echo hello | nc -u -w 1 $TM6 41641" >/dev/null 2>&1
	sleep 1
	check "outside reaches the VPN's UDP port" m grep -q hello /tmp/vpn.udp
	check_not "outside still does not reach 9999" tcp "$OC" "$TM6" 9999
	guard_uninstall
	# an SSH session through the VPN is never filtered: an unattended run goes on
	msh 'mkdir -p /root/.ssh && [ -e /root/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N "" -f /root/.ssh/id_ed25519; cat /root/.ssh/id_ed25519.pub >> /home/tester/.ssh/authorized_keys'
	OUT=$(m ip netns exec vpnpeer ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o LogLevel=ERROR -i /root/.ssh/id_ed25519 tester@100.100.1.1 "sudo sh $G/install.sh $GV --yes --port 2053; echo RC=\$?" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 0 "unattended from an SSH session through the VPN (100.64/10 on vpn0)"
	check "the installer says the VPN session is not filtered" says "100.100.1.2: through vpn0 the guard does not filter"
	guard_uninstall
}

g_pub4() {
	log "== pub4: a host with a public IPv4 address; the provider's segment is not home ($TAG)"
	fresh pub || return 1
	guard_install_local --yes --port 2053 --port 8443 --port 9998/udp
	expect_rc 4 "no home network and no VPN: an unattended run needs a choice for SSH"
	check "it says why and what to choose" says "Choose with --ssh open"
	guard_install_local --yes --port 2053 --port 8443 --port 9998/udp --ssh closed-i-have-a-console
	expect_rc 0 "install on a host with a public IPv4 address (from the console, --ssh closed-i-have-a-console)"
	check "the installer chose to filter IPv4" says "IPv4 on eth0 is filtered"
	check "the installer says the segment is the provider's" says "eth0: it holds a public or carrier-grade address"
	check_not "eth0 is not a home network" in_set lan_if '"eth0"'
	check_not "IPv4 is filtered (eth0 does not pass IPv4)" in_set pass4_if '"eth0"'
	check "outside v4 reaches 2053" tcp "$OC" "$TM4" 2053
	check "outside v4 reaches the public container port 8443 (forward path, DNAT)" tcp "$OC" "$TM4" 8443
	check_not "outside v4 does not reach 9999" tcp "$OC" "$TM4" 9999
	check_not "outside v4 does not reach the container port 18080 (forward path, DNAT)" tcp "$OC" "$TM4" 18080
	check_not "outside v4 does not reach SSH" tcp "$OC" "$TM4" 22
	# a neighbour in the provider's segment: global v4, global v6, link-local, 169.254
	ll=$(tm_ll)
	docker exec "$HC" ip addr add 169.254.7.20/16 dev eth0
	m ip addr add 169.254.7.10/16 dev eth0
	for p in 9999 22 18080; do
		check_not "a provider neighbour (v4, on-link) does not reach $p" tcp "$HC" "$TM4" "$p"
		check_not "a provider neighbour (v6, on-link) does not reach $p" tcp "$HC" "$TM6" "$p"
	done
	for p in 9999 22; do
		check_not "a provider neighbour over link-local does not reach $p" tcp "$HC" "$ll%eth0" "$p"
		check_not "a provider neighbour over 169.254 does not reach $p" tcp "$HC" 169.254.7.10 "$p"
	done
	check "a provider neighbour still reaches the public port 2053" tcp "$HC" "$TM6" 2053
	check "IPv6 still works with the neighbour (neighbour discovery: ping)" docker exec "$HC" busybox ping -6 -c 1 -W 2 "$TM6"
	# forged private sources from outside: dropped, the real source comes through
	# (counted after the guard by a probe table of the test's own)
	probe_table
	docker exec "$OC" sh -c 'ip addr add 10.66.0.5/32 dev eth0; ip -6 addr add fd00:6666::5/128 dev eth0 nodad'
	udp_sent "$OC" "$OC4" "$TM4" 9998
	udp_sent "$OC" "$OC6" "$TM6" 9998
	check "control: datagrams from the outside client's own addresses pass the guard" m sh -c "! nft list counter inet cg_probe passed | grep -q 'packets 0 '"
	m nft reset counter inet cg_probe passed >/dev/null
	udp_sent "$OC" 10.66.0.5 "$TM4" 9998
	udp_sent "$OC" fd00:6666::5 "$TM6" 9998
	check "forged RFC 1918 and ULA sources from outside do not pass the guard" m sh -c "nft list counter inet cg_probe passed | grep -q 'packets 0 '"
	m nft delete table inet cg_probe
	check "the forged ones are counted" sh -c "docker exec $M nft list counter inet cosmos_host_guard dropped_spoofed | grep -qv 'packets 0 '"
	guard_uninstall
	# --home names the neighbour: then it is home
	guard_install_local --yes --port 2053 --home 203.0.113.20/32 --home 2001:db8:1::20/128
	expect_rc 0 "install with --home for the neighbour"
	check "a --home neighbour reaches 9999 (v4)" tcp "$HC" "$TM4" 9999
	check "a --home neighbour reaches 9999 (v6)" tcp "$HC" "$TM6" 9999
	check_not "link-local still does not count as home there" tcp "$HC" "$ll%eth0" 9999
	guard_uninstall
	guard_install_local --yes --port 2053 --ipv4 no --ssh closed-i-have-a-console
	check "--ipv4 no leaves IPv4 alone" tcp "$OC" "$TM4" 9999
	check "and says it is not filtered" says "IPv4 on eth0 is NOT filtered"
	guard_uninstall
}

# the home behind NAT: on-link and link-local are home; forged sources from outside are not
g_spoof() {
	log "== spoof: home behind a router, link-local home, forged sources from outside ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053 --port 9998/udp
	expect_rc 0 "install"
	check "eth0 is a home network (private address, private router)" in_set lan_if '"eth0"'
	check "the installer says so and how to change it" says "eth0: a home network behind a router"
	check "it says that a forged home-prefix source is the router's job" says "dropping those is the router's job"
	ll=$(tm_ll)
	check "home over link-local reaches 9999" tcp "$HC" "$ll%eth0" 9999
	probe_table
	docker exec "$OC" ip -6 addr add fd00:6666::5/128 dev eth0 nodad
	udp_sent "$OC" "$OC6" "$TM6" 9998
	check "control: datagrams from the outside client's own address pass the guard" m sh -c "! nft list counter inet cg_probe passed | grep -q 'packets 0 '"
	m nft reset counter inet cg_probe passed >/dev/null
	udp_sent "$OC" fd00:6666::5 "$TM6" 9998
	check "a forged ULA source from outside does not pass the guard" m sh -c "nft list counter inet cg_probe passed | grep -q 'packets 0 '"
	m nft delete table inet cg_probe
	guard_uninstall
	# a carrier-grade address (100.64/10) on the outside interface: filtered as public, not home
	m ip addr add 100.70.0.10/24 dev eth0
	guard_install_local --yes --port 2053 --dry-run --ssh closed-i-have-a-console
	check "a carrier-grade address on the outside interface is filtered as public" says "IPv4 on eth0 is filtered"
	check "and the interface is then not a home network" says "eth0: it holds a public or carrier-grade address"
	m ip addr del 100.70.0.10/24 dev eth0
	# --exposed: nothing on the outside interface is home
	guard_install_local --yes --port 2053 --exposed --ssh closed-i-have-a-console
	expect_rc 0 "install with --exposed"
	check_not "with --exposed eth0 is not a home network" in_set lan_if '"eth0"'
	check_not "with --exposed a neighbour does not reach 9999 (v6)" tcp "$HC" "$TM6" 9999
	check_not "with --exposed IPv4 is filtered: a neighbour does not reach 9999 (v4)" tcp "$HC" "$TM4" 9999
	guard_uninstall
}

g_ufw() {
	log "== ufw active ($TAG)"
	fresh nat || return 1
	msh 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ufw nftables >/dev/null 2>&1; ufw allow 22/tcp >/dev/null; ufw allow 2053/tcp >/dev/null; ufw allow 8443/tcp >/dev/null; ufw --force enable >/dev/null'
	check "ufw is active" sh -c "docker exec $M ufw status | grep -q 'Status: active'"
	before_rules=$(ruleset)
	guard_install "$HC" --yes --port 2053 --port 8443
	expect_rc 0 "install beside ufw"
	check "the installer says that ufw keeps its rules" sh -c "printf '%s' \"\$1\" | grep -q 'ufw is active and keeps its own rules'" _ "$OUT"
	check "outside v6 reaches 2053 (ufw and the guard allow it)" tcp "$OC" "$TM6" 2053
	check_not "outside v6 does not reach 9999" tcp "$OC" "$TM6" 9999
	check_not "home v6 does not reach 9999 either: ufw drops it (both filter)" tcp "$HC" "$TM6" 9999
	check "home v6 reaches SSH (allowed by ufw)" tcp "$HC" "$TM6" 22
	m ufw reload >/dev/null
	check "after ufw reload the guard is loaded" guard_loaded
	msh 'ufw --force disable >/dev/null; ufw --force enable >/dev/null'
	check "after ufw disable/enable the guard is loaded" guard_loaded
	check "ufw is still active" sh -c "docker exec $M ufw status | grep -q 'Status: active'"
	guard_uninstall
	check "after uninstall ufw's rules are as before" test "$before_rules" = "$(ruleset)"
}

g_firewalld() {
	log "== firewalld active ($TAG)"
	fresh nat || return 1
	msh 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends firewalld >/dev/null 2>&1; systemctl reload dbus; systemctl enable --now firewalld >/dev/null 2>&1; firewall-cmd -q --add-port=2053/tcp --add-port=9999/tcp; firewall-cmd -q --runtime-to-permanent'
	check "firewalld is active" m systemctl is-active --quiet firewalld
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install beside firewalld"
	check "the installer says that firewalld keeps its rules" sh -c "printf '%s' \"\$1\" | grep -q 'firewalld is active and keeps its own rules'" _ "$OUT"
	check "outside v6 reaches 2053" tcp "$OC" "$TM6" 2053
	check_not "outside v6 does not reach 9999 (firewalld allows it, the guard does not)" tcp "$OC" "$TM6" 9999
	check "home v6 reaches 9999 (both allow it)" tcp "$HC" "$TM6" 9999
	m firewall-cmd -q --reload
	check "after firewall-cmd --reload the guard is loaded" guard_loaded
	m systemctl restart firewalld
	check "after a restart of firewalld the guard is loaded" guard_loaded
	check_not "after a restart of firewalld outside still does not reach 9999" tcp "$OC" "$TM6" 9999
	reboot_machine
	check "after a reboot with firewalld the guard is loaded" guard_loaded
	check_not "after a reboot with firewalld outside does not reach 9999" tcp "$OC" "$TM6" 9999
	before_rules=$(ruleset)
	guard_uninstall
	check "after uninstall only the guard's table is gone" sh -c "[ \"\$(printf '%s\n' \"\$1\" | grep -c '^table')\" = \"\$((\$(printf '%s\n' \"\$2\" | grep -c '^table') + 1))\" ]" _ "$before_rules" "$(ruleset)"
	check "firewalld's table is still there" sh -c "docker exec $M nft list tables | grep -q firewalld"
}

# Docker's own rules: iptables (iptables-nft or legacy) or its nftables backend
docker_rules() {
	if docker exec "$M" sh -c 'grep -q nftables /etc/docker/daemon.json'; then
		docker exec "$M" sh -c 'nft list tables | grep -q docker'
	else
		docker exec "$M" iptables -S DOCKER >/dev/null 2>&1
	fi
}

# an outside session runs the installer unattended in ways that lose or fake
# SSH_CONNECTION: every one must be refused, because sshd's sockets show it
outside_run() { # description command (run in an SSH session from OC as tester)
	OUT=$(docker exec "$OC" sh -c "ssh $SSHO tester@$TM6 '$2'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 4 "$1"
}
g_live() {
	log "== live: the SSH sessions are found from sshd's sockets, not the environment ($TAG)"
	fresh nat || return 1
	before_rules=$(ruleset)
	outside_run "systemd-run from an outside session (no SSH_CONNECTION) is refused" \
		"sudo systemd-run --quiet --wait --pipe sh $G/install.sh $GV --yes --port 2053; echo RC=\$?"
	outside_run "a stale home SSH_CONNECTION in an outside session is refused" \
		"sudo env SSH_CONNECTION=\"$HC6 50000 $TM6 22\" sh $G/install.sh $GV --yes --port 2053; echo RC=\$?"
	check "it says the environment's session is stale" says "none is open from there now: ignored"
	outside_run "su from an outside session is refused" \
		"sudo su -c \"sh $G/install.sh $GV --yes --port 2053\" root; echo RC=\$?"
	outside_run "tmux from an outside session is refused" \
		"tmux new-session -d \"sudo sh $G/install.sh $GV --yes --port 2053 > /tmp/t.out 2>&1; echo RC=\\\$? >> /tmp/t.out\"; sleep 10; cat /tmp/t.out"
	outside_run "screen from an outside session is refused" \
		"screen -dmS g sh -c \"sudo sh $G/install.sh $GV --yes --port 2053 > /tmp/s.out 2>&1; echo RC=\\\$? >> /tmp/s.out\"; sleep 10; cat /tmp/s.out"
	check "refused runs changed nothing" test "$before_rules" = "$(ruleset)"
	# a stranger still signing in (a TCP connection to sshd, no session) does not
	# make an unattended home run refuse, and gets no allowance
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 22 > /dev/null"
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "unattended from home with a stranger's unauthenticated connection open"
	check_not "the stranger got no allowance" m grep -q "$OC26" /etc/cosmos-host-guard/allow
	check_not "the stranger cannot open SSH" tcp "$OC2" "$TM6" 22
	docker exec "$OC2" sh -c 'pkill -f "nc .*22"; true'
	guard_uninstall
	# two signed-in outside sessions: an interactive run allows both
	docker exec -d "$OC2" sh -c "ssh $SSHO tester@$TM6 'sleep 120'"
	sleep 3
	session_install "$OC" --port 2053 --confirm-timeout 60
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
	check "the first session's address is in the allowance" m sh -c "nft list set inet cosmos_host_guard ssh_allow6 | grep -q '$OC6'"
	check "the second session's address is in the allowance" m sh -c "nft list set inet cosmos_host_guard ssh_allow6 | grep -q '$OC26'"
	session_say yes
	session_result
	expect_rc 0 "interactive with two outside sessions, confirmed"
	check "the second session's address can open SSH again" ssh_run "$OC2" true
	docker exec "$OC2" sh -c 'pkill -f "ssh .*tester@"; true'
	guard_uninstall
}

g_routes() {
	log "== routes: default routes in another table, none at all, the watcher gone ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053 --port 8443
	expect_rc 0 "install"
	# the default routes only in a policy routing table
	msh "ip route del default; ip -6 route del default; ip route add default via $RT4H table 100; ip -6 route add default via $RT6H table 100; ip rule add pref 100 lookup 100; ip -6 rule add pref 100 lookup 100"
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check "a default route only in table 100: eth0 is still outside" in_set wan_if '"eth0"'
	check_not "and not in fail-closed mode" in_set failclosed_if '"eth0"'
	check "outside still reaches 2053 (answered through table 100)" tcp "$OC" "$TM6" 2053
	check_not "outside does not reach 9999" tcp "$OC" "$TM6" 9999
	# no default route at all: every interface but loopback, Docker's and VPNs is filtered
	msh "ip route del default table 100; ip -6 route del default table 100; ip rule del pref 100; ip -6 rule del pref 100"
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check "no default route: fail closed on eth0" in_set failclosed_if '"eth0"'
	check "no default route: eth0 is filtered" in_set wan_if '"eth0"'
	check_not "no default route: Docker's bridge is not filtered" in_set wan_if '"docker0"'
	# the guard drops it; with no route back, as a source that cannot be answered
	before=$(m nft list counters table inet cosmos_host_guard | awk '/dropped_input|dropped_spoofed/ {c = 1} c && /packets/ {s += $2; c = 0} END {print s + 0}')
	tcp "$OC" "$TM6" 9999 >/dev/null 2>&1
	after=$(m nft list counters table inet cosmos_host_guard | awk '/dropped_input|dropped_spoofed/ {c = 1} c && /packets/ {s += $2; c = 0} END {print s + 0}')
	check "no default route: a connection from outside to 9999 is dropped by the guard ($before -> $after)" test "$after" -gt "$before"
	msh "ip route replace default via $RT4H; ip -6 route replace default via $RT6H"
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check_not "the default route back: fail-closed mode ends" in_set failclosed_if '"eth0"'
	probe_matrix "after the default route came back"
	# the watcher killed again and again comes back (no start limit)
	for i in 1 2 3 4 5 6; do m systemctl kill -s KILL cosmos-host-guard-watch.service >/dev/null 2>&1; sleep 1; done
	sleep 6
	check "the watcher comes back after being killed six times" m systemctl is-active --quiet cosmos-host-guard-watch.service
	# the watcher stopped: the timer follows a new prefix within its minute
	m systemctl stop cosmos-host-guard-watch.service
	m ip -6 addr add 2001:db8:1:5::10/64 dev eth0
	found=0
	for _ in $(seq 1 40); do in_set pending6 2001:db8:1:5::/64 && { found=1; break; }; sleep 2; done
	check "without the watcher, the timer notices a new prefix within 80 s (waiting for confirmation)" test "$found" = 1
	m ip -6 addr del 2001:db8:1:5::10/64 dev eth0
	m systemctl start cosmos-host-guard-watch.service
	guard_uninstall
}

# back_within <seconds>: how long until the guard's table is loaded again ("never")
back_within() {
	start=$(date +%s)
	end=$((start + $1))
	while [ "$(date +%s)" -le "$end" ]; do
		if guard_loaded; then echo "$(( $(date +%s) - start ))"; return 0; fi
		sleep 0.3
	done
	echo never
}

g_nftsvc() {
	log "== nftsvc: nftables.service empties the ruleset; the guard comes back ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install"
	# right after the install the watcher has just started: the moment it could miss it
	m systemctl start nftables.service
	t=$(back_within 10)
	check "after nftables.service started (flush ruleset) the guard is loaded again within 5 s (took $t)" test "$t" != never -a "${t%.*}" -le 5
	check "the journal says so" sh -c "docker exec $M journalctl -u cosmos-host-guard-watch --no-pager | grep -q 'loading it again'"
	check_not "outside does not reach 9999 after it" tcp "$OC" "$TM6" 9999
	m systemctl restart nftables.service
	t=$(back_within 10)
	check "after a restart of nftables.service the guard is loaded again within 5 s (took $t)" test "$t" != never -a "${t%.*}" -le 5
	# the watcher restarting at that very moment
	m systemctl restart cosmos-host-guard-watch.service
	m nft flush ruleset
	t=$(back_within 10)
	check "a flush while the watcher starts: loaded again within 5 s (took $t)" test "$t" != never -a "${t%.*}" -le 5
	m systemctl restart docker
	# the package upgraded while the service is enabled (its postinst restarts it)
	m systemctl enable nftables.service >/dev/null 2>&1
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --reinstall --no-install-recommends nftables >/dev/null 2>&1'
	t=$(back_within 10)
	check "after the nftables package was reinstalled with the service enabled the guard is back within 5 s (took $t)" test "$t" != never -a "${t%.*}" -le 5
	# the watcher stopped too: the timer puts it back within its minute
	m systemctl stop cosmos-host-guard-watch.service
	m nft delete table inet cosmos_host_guard
	back=0
	for _ in $(seq 1 40); do guard_loaded && { back=1; break; }; sleep 2; done
	check "without the watcher, the timer loads a deleted table again within 80 s" test "$back" = 1
	m systemctl disable --now nftables.service >/dev/null 2>&1
	m systemctl restart docker
	m systemctl start cosmos-host-guard-watch.service
	guard_uninstall
}

g_docker_mode() { # mode label
	DOCKER_MODE=$1
	export DOCKER_MODE
	log "== docker: $2 ($TAG)"
	if [ "$1" = nft ] && ! docker run --rm "$IMAGE" grep -q docker-ce /etc/test-docker-package 2>/dev/null; then
		log "  (skipped: the distribution's Docker has no nftables backend here)"
		DOCKER_MODE=ipv6
		return
	fi
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053 --port 8443
	expect_rc 0 "install"
	probe_matrix "$2"
	check "Docker's own rules are there" docker_rules
	m systemctl restart docker
	for _ in $(seq 1 30); do tcp "$HC" "$TM6" 8443 2>/dev/null && break; sleep 1; done
	probe_matrix "$2, after Docker restarted"
	guard_uninstall
	DOCKER_MODE=ipv6
}

g_foreign() {
	log "== foreign: another filtering table (a guard made by hand) is named, never removed ($TAG)"
	fresh nat || return 1
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables >/dev/null 2>&1'
	docker exec -i "$M" nft -f - <<'EOF'
table inet cosmos_guard {
	chain input { type filter hook input priority filter - 5; policy accept; tcp dport 7777 drop; }
}
EOF
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install beside a guard made by hand"
	check "the installer names it" says "other rules also filter here: inet cosmos_guard"
	check "and says that a drop in either is final" says "a drop in either is final"
	check "the state file names it for the panel" m grep -q '^other_filters=inet cosmos_guard' /var/lib/cosmos-protection/nftables.state
	guard_uninstall
	check "uninstall leaves it" m nft list table inet cosmos_guard
	m nft delete table inet cosmos_guard
}

g_race() {
	log "== race: confirming at the moment the undo fires ($TAG)"
	fresh nat || return 1
	for i in 1 2 3; do
		session_install "$OC" --port 2053 --confirm-timeout 30
		session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
		session_say y
		session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
		docker exec -d "$M" systemctl start cosmos-host-guard-undo.service
		session_say yes
		session_result
		loaded=0; guard_loaded && loaded=1
		unit=0; m test -e /etc/systemd/system/cosmos-host-guard.service && unit=1
		if { [ "$RC" = 0 ] && [ "$loaded" = 1 ] && [ "$unit" = 1 ]; } || { [ "$RC" = 5 ] && [ "$loaded" = 0 ] && [ "$unit" = 0 ]; }; then
			ok "round $i: one decision (exit $RC, table $loaded, units $unit)"
		else
			bad "round $i: confirm and undo disagree (exit ${RC:-none}, table $loaded, units $unit)"
		fi
		guard_uninstall
	done
}

g_validate() {
	log "== validate: strict input, a table left behind ($TAG)"
	fresh nat || return 1
	for bad_home in 0.0.0.0/0 ::/0 192.168.1.1/24 2001:db8::/16 '4;x/8' 300.1.1.0/24 192.168.1.0; do
		guard_install_local --dry-run --port 2053 --home "$bad_home"
		expect_rc 2 "--home $bad_home is refused"
	done
	guard_install_local --dry-run --port 2053 --home 2001:db8:1::/64 --home 192.168.7.0/24
	expect_rc 0 "--home with real networks"
	guard_install_local --dry-run --port 70000
	expect_rc 2 "--port 70000 is refused"
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables >/dev/null 2>&1'
	m nft add table inet cosmos_host_guard
	guard_install_local --yes --port 2053
	expect_rc 3 "a table of the guard's name without its state is not taken over"
	guard_uninstall
	expect_rc 0 "uninstall removes a table left behind"
	check_not "the table left behind is gone" guard_loaded
}

# --- third round: what decides "outside", home kept and never widened, SSH servers ---

g_uplink() {
	log "== uplink: an uplink is outside whatever its name or kind ($TAG)"
	fresh nat || return 1
	# the uplink renamed br-wan (a home router's or a hypervisor's naming)
	msh "sysctl -q -w net.ipv6.conf.eth0.keep_addr_on_down=1 && ip link set eth0 down && ip link set eth0 name br-wan && ip link set br-wan up && ip route replace default via $RT4H dev br-wan && ip -6 route replace default via $RT6H dev br-wan"
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install with the uplink named br-wan"
	check "br-wan is an outside interface" in_set wan_if '"br-wan"'
	check_not "outside v6 does not reach 9999 through br-wan" tcp "$OC" "$TM6" 9999
	check_not "outside v6 does not reach the container port 18080 through br-wan" tcp "$OC" "$TM6" 18080
	check_not "outside v6 does not reach SSH through br-wan" tcp "$OC" "$TM6" 22
	check "outside v6 reaches 2053" tcp "$OC" "$TM6" 2053
	# a 6in4 tunnel (sit) carrying the IPv6 default route is outside too
	msh "ip tunnel add sit1 mode sit remote 192.0.2.1 local $TM4 ttl 64 && ip link set sit1 up && ip -6 route add default dev sit1 metric 2000"
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "a sit tunnel with a default route is an outside interface" in_set wan_if '"sit1"'
	check_not "with two uplinks the strict reverse-path check is off" in_set rpf_if '"br-wan"'
	guard_install "$HC" --yes --port 2053 --trust-interface sit1
	expect_rc 0 "install trusting sit1"
	check "a trusted uplink is not filtered" in_set trusted_if '"sit1"'
	check_not "and is not in the filtered interfaces" in_set wan_if '"sit1"'
	check "the installer says nothing through it is filtered" says "NOTHING that"
	m ip -6 route del default dev sit1
	m ip tunnel del sit1
	guard_uninstall
	msh "ip link set br-wan down && ip link set br-wan name eth0 && ip link set eth0 up && ip route replace default via $RT4H dev eth0 && ip -6 route replace default via $RT6H dev eth0"
	sleep 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install with eth0 again"
	# a multipath default route (two uplinks in one route, IPv4 and IPv6): every nexthop is outside
	msh "ip link add d1 type dummy && ip addr add 10.99.0.1/24 dev d1 && ip link set d1 up && sleep 1 && ip route replace default nexthop via $RT4H dev eth0 weight 200 nexthop via 10.99.0.2 dev d1 weight 1 && ip -6 route del default && ip -6 route add default nexthop via $RT6H dev eth0 weight 200 nexthop via fe80::99 dev d1 weight 1"
	sleep 3
	log "  (routes: $(m sh -c 'ip -o route show default; ip -o -6 route show default' | tr '\n' ';' | cut -c1-300))"
	check "multipath default: eth0 (the first nexthop) is outside" in_set wan_if '"eth0"'
	check "multipath default: d1 (the last nexthop) is outside" in_set wan_if '"d1"'
	open=0
	for _ in 1 2 3 4 5; do tcp "$OC" "$TM6" 9999 2>/dev/null && open=$((open + 1)); done
	check "multipath default: outside v6 does not reach 9999 through eth0 (open $open of 5)" test "$open" = 0
	# a default route through a nexthop group (nexthop objects)
	if msh "ip nexthop add id 11 via $RT4H dev eth0 && ip nexthop add id 12 via 10.99.0.2 dev d1 && ip nexthop add id 13 group 11/12 && ip route replace default nhid 13" 2>/dev/null; then
		sleep 3
		log "  (routes: $(m sh -c 'ip -o route show default' | tr '\n' ';' | cut -c1-200))"
		check "a nexthop group's default route: eth0 is outside" in_set wan_if '"eth0"'
		check "a nexthop group's default route: d1 is outside" in_set wan_if '"d1"'
		msh "ip route replace default via $RT4H dev eth0; ip nexthop flush" 2>/dev/null
	else
		log "  (nexthop objects not available here: that part is skipped)"
	fi
	msh "ip -6 route del default 2>/dev/null; ip -6 route del default 2>/dev/null; ip -6 route add default via $RT6H dev eth0; ip route replace default via $RT4H dev eth0; ip nexthop flush 2>/dev/null; ip link del d1"
	m /usr/local/sbin/cosmos-host-guard refresh force
	# a full tunnel without a default route (OpenVPN's def1: 0.0.0.0/1 + 128.0.0.0/1
	# through tun9, a provider's forwarded port): outside, with traffic from the
	# internet through it
	msh 'ip netns add vpnp && ip link add tun9 type veth peer name tun9p && ip link set tun9p netns vpnp && ip addr add 10.8.0.2/24 dev tun9 && ip -6 addr add fd00:8::2/64 dev tun9 nodad && ip link set tun9 up && ip netns exec vpnp sh -c "ip addr add 10.8.0.1/24 dev tun9p; ip addr add 203.0.113.99/32 dev tun9p; ip -6 addr add fd00:8::1/64 dev tun9p nodad; ip -6 addr add 2001:db8:99::1/128 dev tun9p nodad; ip link set tun9p up; ip link set lo up; ip route add default via 10.8.0.2; ip -6 route add default via fd00:8::2"'
	m ip route add 203.0.113.99/32 via 10.8.0.1 dev tun9
	sleep 3
	check "a VPN with routes to its own network only is not outside" sh -c "! docker exec $M nft get element inet cosmos_host_guard wan_if '{ \"tun9\" }' >/dev/null 2>&1"
	check "before the full tunnel: an internet host through it reaches 9999 (not filtered)" m ip netns exec vpnp nc -z -w 2 -s 203.0.113.99 10.8.0.2 9999
	# a refresh just ran (a link change, then quiet): a new uplink must not wait for the spacing
	m ip link add dz type dummy; sleep 3; m ip link del dz; sleep 2
	m ip route add 0.0.0.0/1 via 10.8.0.1 dev tun9
	m ip route add 128.0.0.0/1 via 10.8.0.1 dev tun9
	n=0; while [ $n -lt 8 ] && ! in_set wan_if '"tun9"'; do sleep 1; n=$((n + 1)); done
	check "def1 (0.0.0.0/1 + 128.0.0.0/1): tun9 is outside within 3 s, without a forced refresh ($n s)" test "$n" -le 3
	check "def1: tun9 is outside" in_set wan_if '"tun9"'
	check_not "def1: an internet host through the full tunnel does not reach 9999" m ip netns exec vpnp nc -z -w 2 -s 203.0.113.99 10.8.0.2 9999
	check "def1: it reaches the public port 2053 (the provider's forwarded port)" m ip netns exec vpnp nc -z -w 2 -s 203.0.113.99 10.8.0.2 2053
	m ip route del 0.0.0.0/1 via 10.8.0.1 dev tun9
	m ip route del 128.0.0.0/1 via 10.8.0.1 dev tun9
	# (the full tunnel gone, tun9 leaves the outside interfaces first: otherwise the
	# next check would see it outside from before, and race the refresh that takes it out)
	n=0; while [ $n -lt 8 ] && in_set wan_if '"tun9"'; do sleep 1; n=$((n + 1)); done
	check_not "def1 removed: tun9 is no longer outside ($n s)" in_set wan_if '"tun9"'
	# an IPv6 full tunnel: global unicast (2000::/3) through it
	m ip -6 route add 2000::/3 via fd00:8::1 dev tun9
	n=0; while [ $n -lt 8 ] && ! in_set wan_if '"tun9"'; do sleep 1; n=$((n + 1)); done
	check "2000::/3 through tun9: tun9 is outside" in_set wan_if '"tun9"'
	check_not "2000::/3: an internet host through the tunnel does not reach 9999" m ip netns exec vpnp nc -z -w 2 -s 2001:db8:99::1 fd00:8::2 9999
	m ip -6 route del 2000::/3 via fd00:8::1 dev tun9
	# a VPN's routes to private, carrier-grade, unique-local and narrow networks do not make it an uplink
	msh 'ip route add 10.0.0.0/8 via 10.8.0.1 dev tun9; ip route add 100.64.0.0/10 via 10.8.0.1 dev tun9; ip -6 route add fd00::/8 via fd00:8::1 dev tun9; ip -6 route add 2001:db8:5::/48 via fd00:8::1 dev tun9'
	m /usr/local/sbin/cosmos-host-guard refresh force
	check_not "routes to 10/8, 100.64/10, fd00::/8 and a /48 do not make tun9 outside" in_set wan_if '"tun9"'
	m ip netns del vpnp
	m ip link del tun9 2>/dev/null
	# a second LAN with the multicast route of IPTV or a multicast proxy (224.0.0.0/4),
	# and routes that carry nothing (unreachable, blackhole, prohibit, throw)
	msh 'ip netns add lan2p && ip link add lan2 type veth peer name lan2q && ip link set lan2q netns lan2p && ip addr add 10.50.0.1/24 dev lan2 && ip link set lan2 up && ip netns exec lan2p sh -c "ip addr add 10.50.0.2/24 dev lan2q; ip link set lan2q up; ip link set lo up"'
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "a second LAN: a host on it reaches SSH" m ip netns exec lan2p nc -z -w 2 10.50.0.1 22
	m ip route add 224.0.0.0/4 dev lan2
	m ip route add 240.0.0.0/4 dev lan2
	sleep 4
	check_not "a multicast route (224.0.0.0/4) or class E (240.0.0.0/4) does not make the second LAN an uplink" in_set wan_if '"lan2"'
	check "a host on the second LAN still reaches SSH" m ip netns exec lan2p nc -z -w 2 10.50.0.1 22
	m ip route del 224.0.0.0/4 dev lan2
	m ip route del 240.0.0.0/4 dev lan2
	msh 'ip route add unreachable default table 200; ip route add blackhole 0.0.0.0/1 table 201; ip route add prohibit default table 202; ip route add throw default table 203; ip -6 route add unreachable default table 200'
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "unreachable, blackhole, prohibit and throw routes make no uplink (wan_if=eth0)" sh -c "docker exec $M /usr/local/sbin/cosmos-host-guard show | grep -qx 'wan_if=eth0'"
	msh 'ip route flush table 200; ip route flush table 201; ip route flush table 202; ip route flush table 203; ip -6 route flush table 200'
	m ip netns del lan2p
	guard_uninstall
}

g_failclosed() {
	log "== failclosed: no default route: outside dropped (input and forward), home still in ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053 --port 8443
	expect_rc 0 "install"
	ll=$(tm_ll)
	msh "ip route del default; ip -6 route del default"
	# one refresh per burst: after 2 s of quiet, at most every 10 s
	for _ in $(seq 1 30); do in_set failclosed_if '"eth0"' && break; sleep 1; done
	check "no default route: fail closed on eth0" in_set failclosed_if '"eth0"'
	check "home kept: eth0 is still home" in_set lan_if '"eth0"'
	check "home v6 (on-link) reaches SSH during the outage" tcp "$HC" "$TM6" 22
	check "home v6 (on-link) reaches the panel's port 2053 during the outage" tcp "$HC" "$TM6" 2053
	check "home link-local reaches SSH during the outage" tcp "$HC" "$ll%eth0" 22
	check "home v4 reaches SSH during the outage" tcp "$HC" "$TM4" 22
	# forward: a container port from outside is dropped by the guard (counted)
	before=$(m sh -c "nft list counter inet cosmos_host_guard dropped_forward" | sed -n 's/.*packets \([0-9]*\).*/\1/p')
	tcp "$OC" "$TM6" 18080 >/dev/null 2>&1
	after=$(m sh -c "nft list counter inet cosmos_host_guard dropped_forward" | sed -n 's/.*packets \([0-9]*\).*/\1/p')
	check "fail-closed covers the forward chain: outside to 18080 is dropped there ($before -> $after)" test "$after" -gt "$before"
	msh "ip route replace default via $RT4H; ip -6 route replace default via $RT6H"
	# one refresh per burst, at most every 10 s
	ended=0
	for _ in $(seq 1 30); do in_set failclosed_if '"eth0"' || { ended=1; break; }; sleep 1; done
	check "the default route back: fail-closed ends within 30 s" test "$ended" = 1
	# an interface with no address at all is filtered in fail-closed, never home
	m ip link add dz0 type dummy
	m ip link set dz0 up
	msh "ip route del default; ip -6 route del default"
	for _ in $(seq 1 30); do in_set failclosed_if '"dz0"' && break; sleep 1; done
	check "fail-closed: an interface without addresses is filtered" in_set failclosed_if '"dz0"'
	check_not "fail-closed: an interface without addresses is not home" in_set lan_if '"dz0"'
	msh "ip route replace default via $RT4H; ip -6 route replace default via $RT6H"
	m ip link del dz0
	guard_uninstall
	# with IPv4 filtered (the router's exposed host), the LAN keeps reaching the server
	guard_install "$HC" --yes --port 2053 --ipv4 yes
	expect_rc 0 "install with --ipv4 yes"
	msh "ip route del default; ip -6 route del default"
	for _ in $(seq 1 30); do in_set failclosed_if '"eth0"' && break; sleep 1; done
	check "with --ipv4 yes, home v4 reaches SSH during the outage" tcp "$HC" "$TM4" 22
	check "with --ipv4 yes, home v6 reaches SSH during the outage" tcp "$HC" "$TM6" 22
	msh "ip route replace default via $RT4H; ip -6 route replace default via $RT6H"
	guard_uninstall
	# an isolated LAN: no default route and only private and link-local addresses
	# (installed from the console)
	msh "ip route del default; ip -6 route del default; ip -6 addr del $TM6/64 dev eth0"
	guard_install_local --yes --port 2053
	expect_rc 0 "install on an isolated LAN (no default route, private addresses only)"
	check "isolated LAN: eth0 is home" in_set lan_if '"eth0"'
	check "isolated LAN: home v4 reaches SSH" tcp "$HC" "$TM4" 22
	check "isolated LAN: home link-local reaches SSH" tcp "$HC" "$ll%eth0" 22
	msh "ip -6 addr add $TM6/64 dev eth0 nodad; ip route replace default via $RT4H; ip -6 route replace default via $RT6H"
	guard_uninstall
}

g_ipv4later() {
	log "== ipv4later: a public address appears later: IPv4 starts being filtered, and stays so ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install behind a router"
	check "IPv4 is not filtered on eth0" in_set pass4_if '"eth0"'
	check "outside v4 reaches 9999 (not filtered behind the router)" tcp "$OC" "$TM4" 9999
	m ip addr add 203.0.113.50/24 dev eth0
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check_not "a public address appeared: IPv4 is filtered on eth0" in_set pass4_if '"eth0"'
	check_not "outside v4 no longer reaches 9999" tcp "$OC" "$TM4" 9999
	m ip addr del 203.0.113.50/24 dev eth0
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check_not "the public address gone again: IPv4 stays filtered (only an install or confirm stops it)" in_set pass4_if '"eth0"'
	check_not "outside v4 still does not reach 9999" tcp "$OC" "$TM4" 9999
	m /usr/local/sbin/cosmos-host-guard confirm
	check "after confirm, IPv4 is not filtered on eth0 again" in_set pass4_if '"eth0"'
	guard_uninstall
}

g_homewiden() {
	log "== homewiden: what is home never widens by itself ($TAG)"
	fresh pub || return 1
	guard_install_local --yes --port 2053 --ssh closed-i-have-a-console
	expect_rc 0 "install on a provider segment"
	check_not "eth0 is not home" in_set lan_if '"eth0"'
	# a bridge-mode modem hands out a private address and gateway (or a rogue DHCP)
	msh "ip addr del $TM4/24 dev eth0; ip addr add 192.168.100.10/24 dev eth0; ip route replace default via 192.168.100.1 dev eth0 onlink"
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check_not "a private address and gateway later do not make eth0 home" in_set lan_if '"eth0"'
	ll=$(tm_ll)
	check_not "a neighbour over link-local still does not reach SSH" tcp "$HC" "$ll%eth0" 22
	guard_uninstall
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	# a router advert with an on-link prefix shorter than /64 (most of the internet)
	m ip -6 route add 2000::/3 dev eth0
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check_not "an on-link 2000::/3 does not count as home" in_set onlink6 2000::/3
	check_not "outside (inside 2000::/3) still does not reach 9999" tcp "$OC" "$TM6" 9999
	m ip -6 route del 2000::/3 dev eth0
	# the provider renumbers: a new global /64 waits for confirmation
	m ip -6 addr add 2001:db8:1:7::10/64 dev eth0
	sleep 1; m /usr/local/sbin/cosmos-host-guard refresh >/dev/null 2>&1
	check "a new global prefix is waiting for confirmation" in_set pending6 2001:db8:1:7::/64
	check_not "and is not home yet" in_set onlink6 2001:db8:1:7::/64
	m /usr/local/sbin/cosmos-host-guard confirm
	check "after confirm it is home" in_set onlink6 2001:db8:1:7::/64
	check_not "and no longer waiting" in_set pending6 2001:db8:1:7::/64
	m ip -6 addr del 2001:db8:1:7::10/64 dev eth0
	guard_uninstall
}

g_sshservers() {
	log "== sshservers: SSH on other ports and other servers, terminals no session explains ($TAG)"
	fresh nat || return 1
	# OpenSSH also on 2222 through ListenAddress
	# (Ubuntu 24.04 starts sshd through ssh.socket, whose ports its generator takes
	# from the configuration: a daemon-reload and a restart of the socket apply them)
	msh 'printf "ListenAddress 0.0.0.0:22\nListenAddress [::]:22\nListenAddress 0.0.0.0:2222\nListenAddress [::]:2222\n" > /etc/ssh/sshd_config.d/listen.conf; /usr/local/bin/ssh_restart'
	for _ in $(seq 1 20); do m sh -c 'ss -Htln | grep -q ":2222 "' && break; sleep 1; done
	check "sshd listens on 2222 (the test's own setup)" m sh -c 'ss -Htln | grep -q ":2222 "'
	OUT=$(docker exec "$OC" sh -c "ssh -p 2222 $SSHO tester@$TM6 'sudo sh $G/install.sh $GV --yes --port 2053; echo RC=\$?'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 4 "an outside session on port 2222 (ListenAddress) is seen: refused"
	m rm -f /etc/ssh/sshd_config.d/listen.conf
	m /usr/local/bin/ssh_restart
	# socket-activated sshd (Ubuntu): a port only the socket unit knows (systemd holds
	# the listener) is still seen, from the session's own process
	if m systemctl is-enabled --quiet ssh.socket 2>/dev/null; then
		msh 'mkdir -p /etc/systemd/system/ssh.socket.d && printf "[Socket]\nListenStream=2223\n" > /etc/systemd/system/ssh.socket.d/extra.conf && /usr/local/bin/ssh_restart'
		for _ in $(seq 1 20); do m sh -c 'ss -Htln | grep -q ":2223 "' && break; sleep 1; done
		OUT=$(docker exec "$OC" sh -c "ssh -p 2223 $SSHO tester@$TM6 'sudo sh $G/install.sh $GV --yes --port 2053; echo RC=\$?'" 2>&1)
		RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
		expect_rc 4 "an outside session on a port only ssh.socket knows (systemd holds the listener) is seen: refused"
		m rm -rf /etc/systemd/system/ssh.socket.d
		m /usr/local/bin/ssh_restart
	fi
	# dropbear on 2022
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends dropbear-bin >/dev/null 2>&1; mkdir -p /etc/dropbear; dropbearkey -t ed25519 -f /etc/dropbear/k >/dev/null 2>&1; dropbear -r /etc/dropbear/k -p 2022 -E 2>/dev/null'
	sleep 1
	OUT=$(docker exec "$OC" sh -c "ssh -p 2022 $SSHO tester@$TM6 'sudo sh $G/install.sh $GV --yes --port 2053; echo RC=\$?'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 4 "an outside session through dropbear is seen: refused"
	m pkill dropbear
	# a terminal that no SSH session explains (tmux started from a home session)
	OUT=$(docker exec "$HC" sh -c "ssh $SSHO tester@$TM6 'tmux new-session -d \"sudo sh $G/install.sh $GV --yes --port 2053 > /tmp/u.out 2>&1; echo RC=\\\$? >> /tmp/u.out\"; sleep 10; cat /tmp/u.out'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 4 "an unattended run in a terminal no session explains (tmux) is refused"
	check "it says why" says "belongs to no SSH session"
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "the same from a plain home session goes on"
	guard_uninstall
}

g_nohome() {
	log "== nohome: no home network and no VPN: SSH needs a choice, and the choice holds ($TAG)"
	fresh pub || return 1
	guard_install_local --yes --port 2053 --ssh "from" "$OC6"
	expect_rc 0 "--ssh from one address"
	check "that address reaches SSH" tcp "$OC" "$TM6" 22
	check_not "another outside address does not" tcp "$OC2" "$TM6" 22
	check_not "the allowance from --ssh from has no end" m sh -c "nft list set inet cosmos_host_guard ssh_allow6 | grep -q expires"
	check_not "and opens only SSH (not 9999)" tcp "$OC" "$TM6" 9999
	guard_uninstall
	guard_install_local --yes --port 2053 --ssh open
	expect_rc 0 "--ssh open"
	check "SSH answers everyone" tcp "$OC2" "$TM6" 22
	guard_uninstall
	guard_install_local --yes --port 2053 --ssh closed-i-have-a-console
	expect_rc 0 "--ssh closed-i-have-a-console"
	check_not "SSH answers nobody from outside" tcp "$OC" "$TM6" 22
	guard_uninstall
	guard_install_local --yes --port 2053 --ssh "from" "not-an-address"
	expect_rc 2 "--ssh from with a bad address is refused"
}

g_churn() {
	log "== churn: a burst of link changes costs the watcher little ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install"
	cpu0=$(m systemctl show -p CPUUsageNSec --value cosmos-host-guard-watch.service)
	# shellcheck disable=SC2016
	msh 'for i in $(seq 1 150); do ip link add d$i type dummy; ip link set d$i up; ip link del d$i; done'
	sleep 15
	cpu1=$(m systemctl show -p CPUUsageNSec --value cosmos-host-guard-watch.service)
	case "$cpu0$cpu1" in *[!0-9]*|'') bad "the watcher's CPU time cannot be read ($cpu0 / $cpu1)" ;;
		*) used=$(( (cpu1 - cpu0) / 1000000 )); check "150 link changes cost the watcher ${used} ms of CPU (bound: 5000 ms)" test "$used" -lt 5000 ;; esac
	check "the guard still follows: 9999 closed from outside" sh -c "! docker exec $OC nc -z -w 2 $TM6 9999"
	check "the watcher marks itself alive" m sh -c 'find /run/cosmos-host-guard/alive -newermt "-15 seconds" | grep -q .'
	guard_uninstall
}

g_vpnports() {
	log "== vpnports: WireGuard without the wg tool, a VPN in a container ($TAG)"
	fresh nat || return 1
	if m ip link add wg9 type wireguard 2>/dev/null; then
		m ip link set wg9 up
		guard_install "$HC" --yes --port 2053 --dry-run
		check "WireGuard without wg: its port is said to be unknown" says "wireguard wg9 (its port is NOT known"
		msh 'mkdir -p /etc/systemd/network; printf "[NetDev]\nName=wg9\nKind=wireguard\n\n[WireGuard]\nListenPort=51821\n" > /etc/systemd/network/90-wg9.netdev'
		guard_install "$HC" --yes --port 2053
		expect_rc 0 "install with networkd's WireGuard"
		check "the port from networkd's .netdev is opened" in_set vpn_udp 51821
		m rm -f /etc/systemd/network/90-wg9.netdev
		m ip link del wg9
		guard_uninstall
	else
		log "  (no wireguard module in this kernel: the WireGuard part is skipped)"
	fi
	m docker run -d --name wgc -p 51820:51820/udp busybox:1.36 sh -c 'nc -u -l -p 51820' >/dev/null 2>&1
	sleep 2
	guard_install "$HC" --yes --port 2053
	check "a VPN in a container: its UDP port is said not to answer from outside" says "NOT answering from outside: .*51820"
	m docker rm -f wgc >/dev/null 2>&1
	guard_uninstall
}

g_cloud() {
	log "== cloud: a cloud server and a VM without evidence of home count as exposed ($TAG)"
	fresh nat || return 1
	msh 'mkdir -p /tmp/dmi && echo "Amazon EC2" > /tmp/dmi/sys_vendor && echo "m5.large" > /tmp/dmi/product_name'
	OUT=$(m env COSMOS_HOST_GUARD_DMI=/tmp/dmi sh $G/install.sh --yes --port 2053 --ssh closed-i-have-a-console --dry-run 2>&1; echo "RC=$?")
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 0 "a cloud server (DMI says Amazon EC2)"
	check "it says a cloud server was detected" says "a cloud server was detected (Amazon EC2)"
	check "and filters IPv4 on eth0" says "IPv4 on eth0 is filtered"
	OUT=$(m env COSMOS_HOST_GUARD_DMI=/tmp/dmi sh $G/install.sh --yes --port 2053 --not-exposed --dry-run 2>&1; echo "RC=$?")
	check "--not-exposed overrides it" says "eth0: a home network behind a router"
	# a VM (this test machine is a container) with no evidence of a home router
	OUT=$(m sh $G/install.sh --yes --port 2053 --ssh closed-i-have-a-console --dry-run 2>&1; echo "RC=$?")
	check "a virtual machine without evidence of home: exposed, said" says "nothing shows a home router"
	msh 'echo "{\"router\": {\"answers\": true, \"name\": \"Test router\"}, \"network\": {\"gateway\": \"192.168.1.254\"}}" > /tmp/fd.json'
	OUT=$(m sh $G/install.sh --yes --port 2053 --fork-detect /tmp/fd.json --dry-run 2>&1; echo "RC=$?")
	check "with cosmos fork-detect showing a home router it is home" says "eth0: a home network behind a router"
	# Oracle Cloud, named only by the chassis asset tag (bare metal without cloud-init)
	msh 'mkdir -p /tmp/dmi2 && echo "Oracle Corporation" > /tmp/dmi2/sys_vendor && echo "BM.Standard" > /tmp/dmi2/product_name && echo "OracleCloud.com" > /tmp/dmi2/chassis_asset_tag'
	OUT=$(m env COSMOS_HOST_GUARD_DMI=/tmp/dmi2 sh $G/install.sh --yes --port 2053 --ssh closed-i-have-a-console --dry-run 2>&1; echo "RC=$?")
	check "Oracle Cloud by its asset tag" says "a cloud server was detected (Oracle Cloud)"
}

g_allowfile() {
	log "== allowfile: a bad line in the allowances never stops the load ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	t=$(( $(date +%s) + 3600 ))
	msh "printf '999.1.1.1 $t\n:::: $t\n$OC6 $t\n' > /etc/cosmos-host-guard/allow"
	m systemctl reload cosmos-host-guard.service
	check "the load went through" guard_loaded
	check "the good line is loaded" m nft get element inet cosmos_host_guard ssh_allow6 "{ $OC6 }"
	check "the bad lines are said in the journal" sh -c "docker exec $M journalctl -u cosmos-host-guard --no-pager | grep -q 'skipped a bad'"
	# the home record too: bad lines are skipped, never loaded (also in fail-closed,
	# where its networks go into the sets as they are)
	msh "printf 'net4 999.1.1.0/24\nnet6 ::/0\nnet4 192.168.1.0/8\nif bad;name\npriv4 1.0.0.0/8\n' >> /etc/cosmos-host-guard/home"
	m systemctl reload cosmos-host-guard.service
	check "a bad home record: the load went through" guard_loaded
	msh "ip route del default; ip -6 route del default"
	for _ in $(seq 1 15); do in_set failclosed_if '"eth0"' && break; sleep 1; done
	check "a bad home record: fail-closed still loads" in_set failclosed_if '"eth0"'
	check "a bad home record: the good lines still count" in_set onlink6 2001:db8:1::1
	check "a bad home record: said in the journal" sh -c "docker exec $M journalctl -u cosmos-host-guard -u cosmos-host-guard-watch --no-pager | grep -q 'skipped a bad line of /etc/cosmos-host-guard/home'"
	msh "ip route replace default via $RT4H; ip -6 route replace default via $RT6H"
	guard_uninstall
}

g_confirmwindow() {
	log "== confirmwindow: an unconfirmed reinstall puts the old guard back; a reboot inside the window ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053 --ssh open
	expect_rc 0 "first install from home (SSH open, so the reinstall can come from outside)"
	session_install "$OC" --port 2053 --port 9999 --ssh open --confirm-timeout 30
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
	check "the new rules are live while waiting (9999 public)" tcp "$OC2" "$TM6" 9999
	check_not "the old watcher rests during the window" m systemctl is-active --quiet cosmos-host-guard-watch.service
	sleep 34
	session_result
	expect_rc 5 "not confirmed: undone"
	check_not "the old guard is back (9999 closed again)" tcp "$OC2" "$TM6" 9999
	check "the old watcher runs again" m systemctl is-active --quiet cosmos-host-guard-watch.service
	guard_uninstall
	# a reboot inside the window of a first install: nothing comes up
	session_install "$OC" --port 2053 --confirm-timeout 120
	session_wait 'Go on?' 30 || { bad "no question"; session_result; printf "%s\n" "$OUT" | tail -n 15; session_end; return; }
	session_say y
	session_wait 'Confirm?' 30 || { bad "no confirmation prompt"; session_result; session_end; return; }
	session_end
	reboot_machine
	check_not "after a reboot inside the window the guard is not loaded" guard_loaded
	check_not "and no unit was installed" m test -e /etc/systemd/system/cosmos-host-guard.service
}

f2b_log() { # address count
	msh "for i in \$(seq 1 $2); do echo \"\$(date +%Y-%m-%dT%H:%M:%S%z) cosmos-security kind=auth.login.failed ip=$1 sev=medium n=1\" >> /var/lib/cosmos/cosmos.security.log; done"
}
banned() { m sh -c "fail2ban-client status cosmos-auth | grep -q -- '$1'"; }

g_fail2ban() {
	log "== fail2ban: the current version, then this one over it; bans ($TAG)"
	fresh nat || return 1
	# the version on custom before this work, as the owner's server has it
	docker cp /work/fail2ban-old "$M:/opt/fail2ban-old"
	msh 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 nftables >/dev/null 2>&1'
	OUT=$(m sh /opt/fail2ban-old/install.sh --yes --no-sshd 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "the current version installs (--no-sshd, as on the owner's server)"
	old=$(msh 'cd /etc/fail2ban && md5sum jail.d/* filter.d/cosmos-* fail2ban.d/cosmos-*')
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --yes --record /root/f2b.rec 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "the new version over it, without options"
	new=$(msh 'cd /etc/fail2ban && md5sum jail.d/* filter.d/cosmos-* fail2ban.d/cosmos-*')
	check "the jails and filters came out identical" test "$old" = "$new"
	check "it reports already-installed" m grep -q 'fail2ban	status	already-installed' /root/f2b.rec
	check "a state file for the panel" m grep -q '^sshd=off' /var/lib/cosmos-protection/fail2ban.state
	# an earlier choice of watching SSH is kept
	m sh /opt/fail2ban-old/install.sh --yes >/dev/null 2>&1
	old=$(msh 'cd /etc/fail2ban && md5sum jail.d/* filter.d/cosmos-*')
	m sh /opt/cosmos/fork/fail2ban/install.sh --yes >/dev/null 2>&1
	new=$(msh 'cd /etc/fail2ban && md5sum jail.d/* filter.d/cosmos-*')
	check "with the sshd jail on before, it stays on and the files are identical" test "$old" = "$new"
	m sh /opt/cosmos/fork/fail2ban/install.sh --yes --no-sshd >/dev/null 2>&1
	check "--no-sshd turns it off" sh -c "docker exec $M grep -A1 '^\[sshd\]' /etc/fail2ban/jail.d/cosmos-50-host.local | grep -q 'enabled = false'"
	# bans
	for _ in $(seq 1 20); do m fail2ban-client ping >/dev/null 2>&1 && break; sleep 1; done
	f2b_log "$OC6" 6; f2b_log "192.168.1.20" 6; f2b_log "100.100.1.2" 6; f2b_log "fd7a:115c:a1e0::2" 6
	for _ in $(seq 1 20); do banned "$OC6" && break; sleep 1; done
	check "an outside address is banned after failed sign-ins (without the guard)" banned "$OC6"
	check "the ban is in the kernel" m nft list table inet f2b-cosmos
	check_not "the banned address no longer reaches Cosmos' port" tcp "$OC" "$TM6" 2053
	check_not "a home address is never banned" banned 192.168.1.20
	check_not "a VPN address (100.64/10) is never banned" banned 100.100.1.2
	check_not "a VPN address (fd7a::/48, inside fc00::/7) is never banned" banned fd7a:115c:a1e0::2
	m fail2ban-client unban --all >/dev/null
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "the guard beside Fail2ban"
	f2b_log "$OC26" 6
	for _ in $(seq 1 20); do banned "$OC26" && break; sleep 1; done
	check "with the guard: an outside address is banned" banned "$OC26"
	check_not "with the guard: the banned address no longer reaches Cosmos' port" tcp "$OC2" "$TM6" 2053
	check "with the guard: another outside address still reaches Cosmos' port" tcp "$OC" "$TM6" 2053
	check "with the guard: both tables are loaded" sh -c "docker exec $M nft list tables | grep -q f2b-cosmos && docker exec $M nft list tables | grep -q cosmos_host_guard"
	guard_uninstall
	check "removing the guard keeps Fail2ban's table" m nft list table inet f2b-cosmos
	OUT=$(m sh /opt/cosmos/fork/fail2ban/uninstall.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "Fail2ban uninstall"
	check "uninstall removes the state file" sh -c "! docker exec $M test -e /var/lib/cosmos-protection/fail2ban.state"
	OUT=$(m sh /opt/cosmos/fork/fail2ban/uninstall.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "Fail2ban uninstall a second time"
	# from nothing: python3 and the package installed by the script itself
	fresh nat || return 1
	before_files=$(files_snapshot)
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --dry-run 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "Fail2ban dry run on a machine without python3"
	check "Fail2ban dry run: no file changed" test "$before_files" = "$(files_snapshot)"
	# nftables comes with Docker 29's packages; the distributions' Docker does not bring it
	if m sh -c 'command -v nft' >/dev/null 2>&1; then want_pkgs='python3 fail2ban '; else want_pkgs='python3 nftables fail2ban '; fi
	check "--print-packages names what is missing: $want_pkgs" sh -c "[ \"\$(docker exec $M sh /opt/cosmos/fork/fail2ban/install.sh --print-packages | tr '\n' ' ')\" = '$want_pkgs' ]"
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --yes --record /root/f2b.rec 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "Fail2ban unattended on a bare machine"
	check "the record names the packages it installed" sh -c "[ \$(docker exec $M grep -c 'fail2ban	package	' /root/f2b.rec) = $(printf '%s' "$want_pkgs" | wc -w) ]"
	check "the sshd jail is off by default" sh -c "! docker exec $M fail2ban-client status sshd"
	check "and it says so" says "off by default, --sshd to watch it"
	check "it says what 100.64.0.0/10 means" says "carrier-grade NAT"
	# run from an outside session under plain sudo (no SSH_CONNECTION): that session is never banned
	OUT=$(docker exec "$OC" sh -c "ssh $SSHO tester@$TM6 'sudo sh /opt/cosmos/fork/fail2ban/install.sh --yes; echo RC=\$?'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
	expect_rc 0 "Fail2ban from an outside session under sudo"
	check "the outside session's address is never banned (from sshd's sockets)" sh -c "docker exec $M grep '^ignoreip' /etc/fail2ban/jail.d/cosmos-50-host.local | grep -q '$OC6'"
}

uu_value() { m apt-config dump | grep -F "$1"; }
g_updates() {
	log "== automatic security updates ($TAG)"
	fresh nat || return 1
	dpkg_docker=$(m cat /etc/test-docker-package)
	m apt-mark hold "$dpkg_docker" >/dev/null
	before_files=$(files_snapshot)
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/install.sh --dry-run 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "dry run"
	check "dry run: no file changed" test "$before_files" = "$(files_snapshot)"
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/install.sh --yes --record /root/uu.rec 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "install on a machine without a configuration"
	check "apt reads: updates every day" uu_value 'APT::Periodic::Unattended-Upgrade "1";'
	check "apt reads: never a restart" uu_value 'Unattended-Upgrade::Automatic-Reboot "false";'
	origins=$(m apt-config dump | grep -E '^Unattended-Upgrade::(Origins-Pattern|Allowed-Origins)::')
	check "apt takes updates from somewhere" test -n "$origins"
	check "only from the security archive: $(printf '%s' "$origins" | tr '\n' ' ')" sh -c "! printf '%s\n' \"\$1\" | grep -v -i security" _ "$origins"
	check "unattended-upgrade runs with it (dry run)" m unattended-upgrade --dry-run
	check "Docker's hold is kept" sh -c "docker exec $M apt-mark showhold | grep -qx $dpkg_docker"
	check "the record says installed" m grep -q 'unattended-upgrades	status	installed' /root/uu.rec
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/install.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "install a second time"
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/uninstall.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "uninstall"
	check_not "after uninstall apt no longer runs them" uu_value 'APT::Periodic::Unattended-Upgrade "1";'
	check "Docker's hold is still kept" sh -c "docker exec $M apt-mark showhold | grep -qx $dpkg_docker"
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/uninstall.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "uninstall a second time"
	# configured by the administrator: left alone
	msh 'printf "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n" > /etc/apt/apt.conf.d/20auto-upgrades'
	before_files=$(files_snapshot)
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/install.sh --yes --record /root/uu2.rec 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "install where the administrator configured it"
	check "it says already configured" m grep -q 'unattended-upgrades	status	already-configured' /root/uu2.rec
	check "nothing was changed" test "$before_files" = "$(files_snapshot)"
}

# --- after the third review: what an SSH session is, every uplink, transient states ---

# forge <client> <script>: runs a script as the user tester, inside an SSH session
# from that client (what any user signed in over SSH can do)
forge() {
	printf '%s\n' "$2" | docker exec -i "$M" sh -c 'cat > /tmp/forge.sh; chmod 755 /tmp/forge.sh'
	docker exec -d "$1" sh -c "ssh $SSHO tester@$TM6 'sh /tmp/forge.sh'"
	sleep 4
}
# dry_run_from <client>: an interactive run's plan (dry run), from an SSH session
dry_run_from() {
	OUT=$(docker exec "$1" sh -c "ssh $SSHO tester@$TM6 'sudo sh $G/install.sh $GV --port 2053 --dry-run < /dev/null; echo RC=\$?'" 2>&1)
}

g_sessions() {
	log "== sessions: an SSH session is an accepted, signed-in connection: tunnels count, forwards and forged titles do not ($TAG)"
	fresh nat || return 1
	# a tunnel without a terminal (ssh -N: "sshd-session: tester", no @pts / @notty)
	docker exec -d "$OC" sh -c "ssh $SSHO -N tester@$TM6"
	sleep 3
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "an outside ssh -N tunnel is a session: the unattended run is refused"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	dry_run_from "$HC"
	check "an interactive run allows the tunnel's address" says "ONLY $OC6 may reach SSH"
	docker exec "$OC" sh -c 'pkill -f "ssh .*-N"; true'
	# stdio forwarding (-W: what ProxyJump does) from outside
	docker exec -d "$OC" sh -c "sleep 60 | ssh $SSHO -W [$TM6]:2053 tester@$TM6 > /dev/null"
	sleep 3
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "an outside -W (ProxyJump) session is a session: the unattended run is refused"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	docker exec "$OC" sh -c 'pkill -f "ssh .*-W"; true'
	sleep 1
	check "after the tunnels ended, outside can open SSH (nothing was installed)" tcp "$OC" "$TM6" 22

	# a home session's -L forward to an outside host: sshd's connection outwards
	docker exec -d "$OC2" sh -c 'nc -6 -lk 7000 > /dev/null'
	sleep 1
	docker exec -d "$HC" sh -c "ssh $SSHO -tt -L 7001:[$OC26]:7000 tester@$TM6 'sleep 90'"
	sleep 3
	docker exec -d "$HC" sh -c 'sleep 80 | nc 127.0.0.1 7001'
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "a home session's -L forward to an outside host is not a session: the unattended home run goes on"
	check_not "the forward's destination got no allowance" m grep -q "$OC26" /etc/cosmos-host-guard/allow
	check "ssh_port holds the listener's port only (no port of the forward)" sh -c "docker exec $M nft list set inet cosmos_host_guard ssh_port | tr -d ' \n\t' | grep -q 'elements={22}'"
	guard_uninstall
	dry_run_from "$HC"
	check_not "an interactive run offers the forward's destination no allowance" says "ONLY.*$OC26"
	check "it lists the listener's port only" says "sessions now (ports 22 )"
	docker exec "$HC" sh -c 'pkill -f "ssh .*7001"; pkill -f "nc 127.0.0.1 7001"; true'

	# a normal user's copy of nc named sshd, with an sshd title, connecting out
	forge "$HC" "mkdir -p /tmp/f && cp \"\$(command -v nc)\" /tmp/f/sshd
sleep 80 | bash -c \"exec -a 'sshd: root@pts/9' /tmp/f/sshd $OC26 7000\" &
sleep 85"
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "a user's forged sshd (name and title) connecting out is not a session"
	guard_uninstall
	# ... and listening, with an outside visitor
	forge "$HC" "mkdir -p /tmp/f && cp \"\$(command -v nc)\" /tmp/f/sshd
bash -c \"exec -a 'sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups' /tmp/f/sshd -lk 2200 > /dev/null\" &
sleep 85"
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 2200"
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "a user's forged sshd listener with an outside visitor is not a session"
	check_not "and its port is not an SSH port" sh -c "docker exec $M nft list set inet cosmos_host_guard ssh_port | grep -q 2200"
	guard_uninstall
	# a normal user's copy of nc named dropbear, connecting out
	forge "$HC" "mkdir -p /tmp/f && cp \"\$(command -v nc)\" /tmp/f/dropbear
sleep 80 | /tmp/f/dropbear $OC26 7000 &
sleep 85"
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "a user's process named dropbear connecting out is not a session"
	guard_uninstall
	m pkill -f /tmp/f/ 2>/dev/null
	docker exec "$HC" sh -c 'pkill -f "ssh .*forge"; true'

	# a visitor of a -R forward (GatewayPorts): not a session
	msh 'printf "GatewayPorts yes\n" > /etc/ssh/sshd_config.d/gw.conf; /usr/local/bin/ssh_restart'
	sleep 2
	docker exec -d "$HC" sh -c 'nc -lk 7100 > /dev/null'
	docker exec -d "$HC" sh -c "ssh $SSHO -tt -R '*:7200:127.0.0.1:7100' tester@$TM6 'sleep 90'"
	sleep 3
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 7200"
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "a visitor of a -R forward (GatewayPorts) is not a session"
	guard_uninstall
	docker exec "$HC" sh -c 'pkill -f "ssh .*7200"; true'
	m rm -f /etc/ssh/sshd_config.d/gw.conf
	# an outside session whose listener was restarted (its monitor now a child of pid 1)
	docker exec -d "$OC" sh -c "ssh $SSHO -N tester@$TM6"
	sleep 3
	m /usr/local/bin/ssh_restart
	sleep 2
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "an outside session from before sshd restarted is still a session: refused"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	docker exec "$OC" sh -c 'pkill -f "ssh .*-N"; true'
	docker exec "$OC2" sh -c 'pkill -f "nc .*7000"; true'

	# a root sshd started by hand (a rescue, a supervisor): its sessions count, its port is an SSH port
	m sh -c 'nohup sh -c "/usr/sbin/sshd -D -p 2222" > /dev/null 2>&1 &'
	sleep 2
	docker exec -d "$OC" sh -c "ssh -p 2222 $SSHO -N tester@$TM6"
	sleep 3
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "an outside session on a hand-started root sshd (port 2222) refuses the unattended run"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	guard_install "$HC" --yes --port 2053 --ssh open
	check "a hand-started root sshd's port 2222 is among the SSH ports" sh -c "docker exec $M nft list set inet cosmos_host_guard ssh_port | grep -q 2222"
	guard_uninstall
	docker exec "$OC" sh -c 'pkill -f "ssh -p 2222"; true'
	m pkill -f 'sshd -D -p 2222'
	# a -R forward on the other address family than the SSH port (sshd on 0.0.0.0:2222
	# only; a user's forward on [::]:2222): its visitors are no session
	msh 'printf "ListenAddress 0.0.0.0:22\nListenAddress [::]:22\nListenAddress 0.0.0.0:2222\nGatewayPorts clientspecified\n" > /etc/ssh/sshd_config.d/v4only.conf; /usr/local/bin/ssh_restart'
	for _ in $(seq 1 20); do m sh -c 'ss -Htln | grep -q "0.0.0.0:2222 "' && break; sleep 1; done
	docker exec -d "$HC" sh -c 'nc -lk 7100 > /dev/null'
	docker exec -d "$HC" sh -c "ssh $SSHO -tt -R '[::]:2222:127.0.0.1:7100' tester@$TM6 'sleep 90'"
	sleep 3
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 2222"
	sleep 2
	log "  (the -R visitor: $(m sh -c "sh $G/ssh-sessions.sh sessions" | tr '\n' ';'))"
	check_not "a visitor of a -R forward on the other family than the SSH port is not a session" sh -c "docker exec $M sh $G/ssh-sessions.sh sessions | grep -q '^$OC26 2222 '"
	docker exec "$HC" sh -c 'pkill -f "ssh .*2222"; true'
	docker exec "$OC2" sh -c 'pkill -f "nc .*2222"; true'
	m rm -f /etc/ssh/sshd_config.d/v4only.conf
	m /usr/local/bin/ssh_restart
	sleep 2
	# a socket on the SSH port held by a process that does not descend from the
	# listener (its descriptor taken over by another root process): no connection of sshd's
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 >/dev/null 2>&1'
	docker exec -d "$OC" sh -c "ssh $SSHO -N tester@$TM6"
	sleep 3
	# shellcheck disable=SC2016
	msh 'set -- $(ss -Htnp state established | awk -v p="'"$OC6"'" "index(\$4, p) && \$3 ~ /:22\$/ {print \$5}" | grep -oE "pid=[0-9]+,fd=[0-9]+" | head -n 1 | tr -c "0-9\n" " "); printf "%s\n" "import ctypes, os, sys, time" "libc = ctypes.CDLL(None, use_errno=True)" "pfd = libc.syscall(434, int(sys.argv[1]), 0)" "fd = libc.syscall(438, pfd, int(sys.argv[2]), 0)" "time.sleep(120)" > /tmp/take.py; setsid python3 /tmp/take.py "$1" "$2" > /dev/null 2>&1 & sleep 2; pkill -9 -f "sshd.*tester" || true'
	sleep 2
	log "  (taken over: $(m sh -c "ss -Htnp state established | grep ':22 '" | tr '\n' ';' | cut -c1-300))"
	check_not "a socket on port 22 held by a process that does not descend from sshd's listener is no connection of sshd's" sh -c "docker exec $M sh $G/ssh-sessions.sh sessions | grep -q '^$OC6 22 '"
	m pkill -f /tmp/take.py
	docker exec "$OC" sh -c 'pkill -f "ssh .*-N"; true'
}

# sessions_of <address>: the rows ssh-sessions.sh gives for that peer
sessions_of() { m sh "$G/ssh-sessions.sh" sessions | awk -v a="$1" '$1 == a {print $3, $4}' | sort -u | tr '\n' ';'; }
# askpass_scripts: on the outside clients, SSH_ASKPASS answers: wait (a stranger who
# never answers), right (the password), second (a wrong password, then the right one)
askpass_scripts() {
	for c in "$OC" "$OC2"; do
		docker exec "$c" sh -c 'rm -f /tmp/second.done
printf "#!/bin/sh\nsleep 300\n" > /tmp/wait.sh
printf "#!/bin/sh\necho Pw-0f-tester\n" > /tmp/right.sh
printf "#!/bin/sh\nif [ -e /tmp/second.done ]; then echo Pw-0f-tester; else : > /tmp/second.done; echo wrong-one; fi\n" > /tmp/second.sh
chmod 755 /tmp/wait.sh /tmp/right.sh /tmp/second.sh'
	done
}
# by_password <client> <askpass> <method> <user> <port> <ssh arguments...>: an SSH client that answers with the askpass script
by_password() {
	c=$1; a=$2; meth=$3; u=$4; p=$5; shift 5
	docker exec -d "$c" sh -c "SSH_ASKPASS=/tmp/$a.sh SSH_ASKPASS_REQUIRE=force DISPLAY=:0 setsid ssh -p $p -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o PreferredAuthentications=$meth -o PubkeyAuthentication=no -o NumberOfPasswordPrompts=2 $* $u@$TM6 < /dev/null > /dev/null 2>&1"
}
# jforge <pid> <count> <message>: root writes a line into the journal under another
# process's pid (SCM_CREDENTIALS: journald then takes that pid's _EXE and _UID)
jforge() {
	m sh -c 'test -e /tmp/jforge.py || printf "%s\n" "import socket, struct, sys" "pid, n, msg = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3]" "s = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)" "data = (\"MESSAGE=\" + msg + \"\\n\").encode()" "for i in range(n):" "    s.sendmsg([data], [(socket.SOL_SOCKET, socket.SCM_CREDENTIALS, struct.pack(\"3i\", pid, 0, 0))], 0, \"/run/systemd/journal/socket\")" > /tmp/jforge.py'
	m python3 /tmp/jforge.py "$1" "$2" "$3"
}
# peer_port <client address> <server port>: the client's port of its connection
peer_port() { m sh -c "ss -Htn state established | awk '\$3 ~ /:$2\$/ && index(\$4, \"$1\") {n = split(\$4, a, \":\"); print a[n]; exit}'"; }
# holder_of <client address> <server port>: a root process holding that connection (OpenSSH's monitor, dropbear's process)
# shellcheck disable=SC2016
holder_of() { m sh -c "ss -Htnp state established | awk '\$3 ~ /:$2\$/ && index(\$4, \"$1\")' | grep -oE 'pid=[0-9]+' | cut -d= -f2" | while read -r p; do [ "$(m awk '/^Uid:/ {print $2}' "/proc/$p/status")" = 0 ] && { echo "$p"; break; }; done; }
stop_clients() { for c in "$OC" "$OC2"; do docker exec "$c" sh -c 'pkill -f "ssh "; pkill -f "nc "; true'; done; sleep 1; }

g_strangers() {
	log "== strangers: signed in only on the server's own record of a sign-in; a stranger at the password prompt is nobody's session ($TAG)"
	fresh nat || return 1
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 nftables >/dev/null 2>&1'
	msh 'echo tester:Pw-0f-tester | chpasswd; mkdir -p /root/.ssh && cp /home/tester/.ssh/authorized_keys /root/.ssh/ && chmod 600 /root/.ssh/authorized_keys; printf "KbdInteractiveAuthentication yes\n" > /etc/ssh/sshd_config.d/kbd.conf; /usr/local/bin/ssh_restart'
	sleep 2
	askpass_scripts
	for u in tester nosuchuser; do
		by_password "$OC2" wait password "$u" 22
		sleep 3
		log "  (a stranger named $u: $(sessions_of "$OC26"))"
		check "OpenSSH, a stranger named $u at the password prompt: not signed in (no)" test "$(sessions_of "$OC26")" = "openssh no;"
		guard_install "$HC" --yes --port 2053
		expect_rc 0 "OpenSSH, a stranger named $u at the password prompt does not refuse the unattended home run"
		check_not "and gets no allowance" m grep -q "$OC26" /etc/cosmos-host-guard/allow
		guard_uninstall
		dry_run_from "$HC"
		check_not "an interactive run offers the stranger named $u no allowance" says "ONLY.*$OC26"
		OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --dry-run 2>&1)
		check_not "Fail2ban does not keep the stranger named $u from bans" says "$OC26"
		stop_clients
	done
	# a user writes sshd's line of success for a waiting stranger into the journal
	by_password "$OC2" wait password tester 22
	sleep 3
	sport=$(m sh -c "ss -Htn state established | awk '\$3 ~ /:22\$/ && index(\$4, \"$OC26\") {n = split(\$4, a, \":\"); print a[n]; exit}'")
	m su tester -s /bin/sh -c "logger -t sshd 'Accepted password for tester from $OC26 port $sport ssh2'; echo 'Accepted password for tester from $OC26 port $sport ssh2' | systemd-cat -t sshd"
	sleep 1
	check "a forged 'Accepted' line (logger, systemd-cat) makes nobody signed in" test "$(sessions_of "$OC26")" = "openssh no;"
	stop_clients
	# signed in: a password with a command, a failed then a right password on one
	# connection, keyboard-interactive, a public key as root
	by_password "$OC" right password tester 22 -N
	sleep 3
	check "OpenSSH, password, a tunnel: signed in" test "$(sessions_of "$OC6")" = "openssh yes;"
	stop_clients
	by_password "$OC2" second password tester 22 -N
	# (a wrong password costs PAM's delay of about 2 s before the second try: wait for it)
	n=0; while [ $n -lt 15 ] && [ "$(sessions_of "$OC26")" != "openssh yes;" ]; do sleep 1; n=$((n + 1)); done
	log "  (a wrong then the right password: $(sessions_of "$OC26") after $n s)"
	check "OpenSSH, a wrong then the right password on one connection: signed in" test "$(sessions_of "$OC26")" = "openssh yes;"
	stop_clients
	by_password "$OC" right keyboard-interactive tester 22 -N
	sleep 3
	check "OpenSSH, keyboard-interactive, a tunnel: signed in" test "$(sessions_of "$OC6")" = "openssh yes;"
	stop_clients
	docker exec -d "$OC" sh -c "ssh $SSHO -N root@$TM6"
	sleep 3
	check "OpenSSH, root's own tunnel by public key: signed in (its record in the journal)" test "$(sessions_of "$OC6")" = "openssh yes;"
	# the journal vacuumed: the user's tunnel keeps its post-sign-in process (signed
	# in); root's has none (unsure: refused, never an allowance)
	by_password "$OC2" right password tester 22 -N
	sleep 3
	msh 'journalctl --rotate >/dev/null 2>&1; sleep 2; journalctl --vacuum-time=1s >/dev/null 2>&1'
	log "  (after the vacuum: $OC6 $(sessions_of "$OC6") $OC26 $(sessions_of "$OC26"))"
	check "journal vacuumed: a user's tunnel is still signed in (its process)" test "$(sessions_of "$OC26")" = "openssh yes;"
	check "journal vacuumed: root's tunnel is unsure" test "$(sessions_of "$OC6")" = "openssh unsure;"
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "journal vacuumed: an unsure outside session refuses the unattended run"
	check "it says why" says "the server's record of a sign-in is not in the journal"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	dry_run_from "$HC"
	check_not "journal vacuumed: no allowance for the unsure session" says "ONLY.*$OC6"
	check "and the advice is said" says "run this again with --ssh from"
	stop_clients
	# the system journal trimmed while a user journal still holds an early entry
	# (a line of tester's written just before): root's tunnel stays unsure
	m su tester -s /bin/sh -c 'logger "an early line of a user journal"'
	sleep 2
	docker exec -d "$OC" sh -c "ssh $SSHO -N root@$TM6"
	sleep 3
	msh 'journalctl --rotate >/dev/null 2>&1; sleep 1; rm -f /var/log/journal/*/system@*.journal'
	log "  (journal files: $(m sh -c 'ls /var/log/journal/*/' | tr '\n' ' '))"
	check_not "system journal trimmed, a user journal older: root's tunnel is not called no" test "$(sessions_of "$OC6")" = "openssh no;"
	stop_clients
	# sshd that does not log its successes (LogLevel ERROR): root's own tunnel is never
	# called "no" (it is unsure: refused, not cut)
	msh 'printf "LogLevel ERROR\n" > /etc/ssh/sshd_config.d/00-quiet.conf; /usr/local/bin/ssh_restart'
	sleep 2
	docker exec -d "$OC" sh -c "ssh $SSHO -N root@$TM6"
	sleep 3
	log "  (root at LogLevel ERROR: $(sessions_of "$OC6"))"
	check "LogLevel ERROR: root's tunnel is unsure" test "$(sessions_of "$OC6")" = "openssh unsure;"
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "LogLevel ERROR: root's tunnel from outside refuses the unattended run"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	stop_clients
	check "after the tunnel ended, outside can still open SSH" tcp "$OC" "$TM6" 22
	by_password "$OC2" wait password tester 22
	sleep 3
	check "LogLevel ERROR: a stranger at the password prompt is still no (its pre-authentication process)" test "$(sessions_of "$OC26")" = "openssh no;"
	stop_clients
	m rm -f /etc/ssh/sshd_config.d/00-quiet.conf
	m /usr/local/bin/ssh_restart
	sleep 2
	# root forges sshd's line of success under the stranger's own monitor (its pid, so
	# its _EXE and _UID): for another port it does not count
	by_password "$OC2" wait password tester 22
	sleep 3
	sport=$(peer_port "$OC26" 22); mon=$(holder_of "$OC26" 22)
	jforge "$mon" 1 "Accepted password for tester from $OC26 port $((sport + 1)) ssh2"
	sleep 1
	check "a root-forged line under the monitor's pid for another port: still no" test "$(sessions_of "$OC26")" = "openssh no;"
	jforge 1 1 "Accepted password for tester from $OC26 port $sport ssh2"
	sleep 1
	check "a root-forged line under another root process's pid (systemd's): still no" test "$(sessions_of "$OC26")" = "openssh no;"
	stop_clients

	# dropbear as its package runs it (a unit, logging to the journal)
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends dropbear-bin >/dev/null 2>&1; mkdir -p /etc/dropbear; [ -e /etc/dropbear/k ] || dropbearkey -t ed25519 -f /etc/dropbear/k >/dev/null 2>&1; systemd-run --quiet --unit=test-dropbear /usr/sbin/dropbear -EF -r /etc/dropbear/k -p 2022'
	sleep 1
	# before this dropbear has written any line of success, nothing proves its
	# successes reach the journal: a stranger is unsure
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 2022 > /dev/null"
	sleep 2
	check "dropbear, no success seen yet: a stranger is unsure" test "$(sessions_of "$OC26")" = "dropbear unsure;"
	stop_clients
	# a long journal: 6000 lines of the listener before the owner's tunnel (dropbear
	# writes every connection's lines under its listener's pid in this mode)
	lpid=$(m systemctl show -p MainPID --value test-dropbear)
	jforge "$lpid" 6000 "Exit before auth from <192.0.2.1:1>: Exited normally"
	docker exec -d "$OC" sh -c "ssh -p 2022 $SSHO -N tester@$TM6"
	sleep 3
	check "dropbear, past 6000 lines of its listener: the owner's tunnel is signed in" test "$(sessions_of "$OC6")" = "dropbear yes;"
	docker exec "$OC" sh -c 'pkill -f "ssh "; true'
	for u in tester nosuchuser; do
		by_password "$OC2" wait password "$u" 2022
		sleep 3
		check "dropbear, a stranger named $u at the password prompt: not signed in (no)" test "$(sessions_of "$OC26")" = "dropbear no;"
		# root forges dropbear's success under its listener for another port of that address
		if [ "$u" = tester ]; then
			sport=$(peer_port "$OC26" 2022)
			jforge "$lpid" 1 "[1] Nov 24 00:00:00 Password auth succeeded for 'tester' from $OC26:$((sport + 1))"
			sleep 1
			check "dropbear, a root-forged success for another port: still no" test "$(sessions_of "$OC26")" = "dropbear no;"
		fi
		docker exec "$OC2" sh -c 'pkill -f "ssh "; true'
		sleep 1
	done
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 2022 > /dev/null"
	sleep 2
	check "dropbear, a stranger who sent nothing: not signed in (no)" test "$(sessions_of "$OC26")" = "dropbear no;"
	sport=$(m sh -c "ss -Htn state established | awk '\$3 ~ /:2022\$/ && index(\$4, \"$OC26\") {n = split(\$4, a, \":\"); print a[n]; exit}'")
	m su tester -s /bin/sh -c "logger -t dropbear \"Pubkey auth succeeded for 'tester' with ssh-ed25519 key SHA256:x from $OC26:$sport\""
	check "dropbear, a forged line of success: still not signed in" test "$(sessions_of "$OC26")" = "dropbear no;"
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "dropbear, a stranger does not refuse the unattended home run (its record shows no sign-in)"
	guard_uninstall
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --dry-run 2>&1)
	check_not "Fail2ban does not keep the dropbear stranger from bans" says "$OC26"
	stop_clients
	docker exec -d "$OC" sh -c "ssh -p 2022 $SSHO -N tester@$TM6"
	sleep 3
	check "dropbear, a signed-in tunnel: signed in (its record in the journal)" test "$(sessions_of "$OC6")" = "dropbear yes;"
	# journald dropped lines while the connection lived (its "Suppressed" line): unsure
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 2022 > /dev/null"
	sleep 2
	jpid=$(m systemctl show -p MainPID --value systemd-journald)
	jforge "$jpid" 1 "Suppressed 1234 messages from test-dropbear.service"
	sleep 1
	check "dropbear, journald suppressed lines meanwhile: a stranger is unsure" test "$(sessions_of "$OC26")" = "dropbear unsure;"
	docker exec "$OC2" sh -c 'pkill -f "nc "; true'
	by_password "$OC2" right password tester 2022 -N
	sleep 3
	check "dropbear, password, a tunnel: signed in" sh -c "printf '%s' '$(sessions_of "$OC26")' | grep -q 'dropbear yes;'"
	stop_clients
	m systemctl stop test-dropbear
	# dropbear logging nowhere the journal sees: a stranger is unsure; a signed-in
	# tunnel too before 2024 (it stays root without a child), signed in from 2024 on
	m sh -c 'dropbear -r /etc/dropbear/k -p 2022 -E 2>/dev/null'
	sleep 1
	docker exec -d "$OC2" sh -c "sleep 60 | nc $TM6 2022 > /dev/null"
	docker exec -d "$OC" sh -c "ssh -p 2022 $SSHO -N tester@$TM6"
	sleep 3
	check "dropbear without a journal: a stranger is unsure" test "$(sessions_of "$OC26")" = "dropbear unsure;"
	guard_install "$HC" --yes --port 2053
	expect_rc 4 "dropbear without a journal: an unsure connection from outside refuses the unattended run"
	guard_uninstall # (when a broken guard installed itself, the next cases start from nothing again)
	year=$(m sh -c 'dropbear -V 2>&1 | grep -o "v20[0-9][0-9]" | tr -d v' | head -n 1)
	dry_run_from "$HC"
	if [ "${year:-0}" -ge 2024 ]; then
		check "dropbear $year without a journal: a signed-in tunnel (the user's uid) gets the allowance" says "ONLY $OC6 may reach SSH"
	else
		check_not "dropbear ${year:-?} without a journal: a signed-in tunnel cannot be told from a stranger: no allowance" says "ONLY $OC6 may reach SSH"
		check "dropbear ${year:-?} without a journal: the advice is said" says "run this again with --ssh from"
	fi
	stop_clients
	m pkill dropbear
	m rm -f /etc/ssh/sshd_config.d/kbd.conf
}

g_moved() {
	log "== moved: another private network on a recorded home interface is not home ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install behind a router"
	check "IPv4 passes on eth0 at home" in_set pass4_if '"eth0"'
	msh "ip addr del $TM4/24 dev eth0; ip addr add 192.168.77.10/24 dev eth0; ip route replace default via 192.168.77.1 dev eth0 onlink"
	sleep 5
	log "  (moved: $(m /usr/local/sbin/cosmos-host-guard show 2>/dev/null | grep -E '^(lan_if|pass4_if|onlink4|pending4)=' | tr '\n' ' '))"
	check_not "another private network on the recorded interface is not home" in_set onlink4 192.168.77.0/24
	check "it waits for confirmation (shown)" in_set pending4 192.168.77.0/24
	check_not "and IPv4 is not left unfiltered there" in_set pass4_if '"eth0"'
	check "the journal says so" sh -c "docker exec $M journalctl -u cosmos-host-guard-watch --no-pager | grep -q '192.168.77.0/24 is on a home interface but is not the home network'"
	# the owner at home behind the new router (its new subnet): SSH answers, nothing else
	docker exec "$HC" ip addr add 192.168.77.20/24 dev eth0
	check "the owner on the new home network reaches SSH over IPv4" tcp "$HC" 192.168.77.10 22
	check_not "but not 9999" tcp "$HC" 192.168.77.10 9999
	check_not "nor a container port (18080)" tcp "$HC" 192.168.77.10 18080
	# a new IPv6 prefix as well (a new router often brings both)
	msh 'ip -6 addr add 2001:db8:1:77::10/64 dev eth0 nodad'
	docker exec "$HC" ip -6 addr add 2001:db8:1:77::20/64 dev eth0 nodad
	sleep 5
	check "a new IPv6 prefix waits too" in_set pending6 2001:db8:1:77::/64
	check "the owner reaches SSH over the new IPv6 prefix" tcp "$HC" 2001:db8:1:77::10 22
	check_not "but not 9999 there" tcp "$HC" 2001:db8:1:77::10 9999
	# fail-closed (no default route) keeps the same
	msh "ip route del default; ip -6 route del default"
	for _ in $(seq 1 15); do in_set failclosed_if '"eth0"' && break; sleep 1; done
	check "fail-closed: the owner on the new home network still reaches SSH" tcp "$HC" 192.168.77.10 22
	check_not "fail-closed: but not 9999" tcp "$HC" 192.168.77.10 9999
	msh "ip route replace default via 192.168.77.1 dev eth0 onlink; ip -6 route replace default via $RT6H"
	msh 'ip -6 addr del 2001:db8:1:77::10/64 dev eth0'
	docker exec "$HC" sh -c 'ip addr del 192.168.77.20/24 dev eth0; ip -6 addr del 2001:db8:1:77::20/64 dev eth0' 
	check_not "nothing is written down (no nopass4)" m grep -q nopass4 /etc/cosmos-host-guard/home
	msh "ip addr del 192.168.77.10/24 dev eth0; ip addr add $TM4/24 dev eth0; ip route replace default via $RT4H"
	sleep 5
	check "back home: IPv4 passes on eth0 again" in_set pass4_if '"eth0"'
	check "back home: home v4 reaches SSH" tcp "$HC" "$TM4" 22
	guard_uninstall
}

g_v4gap() {
	log "== v4gap: a moment without IPv4 while IPv6 is up changes nothing for good ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install behind a router"
	check "IPv4 passes on eth0" in_set pass4_if '"eth0"'
	ll=$(tm_ll)
	msh "ip route del default; ip addr del $TM4/24 dev eth0"
	sleep 5
	check "while IPv4 is missing, eth0 is still home" in_set lan_if '"eth0"'
	check "while IPv4 is missing, home v6 reaches SSH" tcp "$HC" "$TM6" 22
	check "while IPv4 is missing, home link-local reaches SSH" tcp "$HC" "$ll%eth0" 22
	check_not "nothing was written down meanwhile (no nopass4 in the record)" m grep -q nopass4 /etc/cosmos-host-guard/home
	msh "ip addr add $TM4/24 dev eth0; ip route replace default via $RT4H"
	sleep 5
	check "IPv4 back: it passes on eth0 again" in_set pass4_if '"eth0"'
	check "IPv4 back: outside v4 reaches 9999 (the router's forward)" tcp "$OC" "$TM4" 9999
	check "IPv4 back: outside v4 reaches SSH (the router's forward of 22)" tcp "$OC" "$TM4" 22
	check "home v4 reaches SSH" tcp "$HC" "$TM4" 22
	# evidence against home still narrows: a public IPv4 gateway
	msh "ip route replace default via 198.51.100.254 dev eth0 onlink"
	sleep 5
	check_not "a public gateway is evidence against home: eth0 is no longer home" in_set lan_if '"eth0"'
	check "and IPv4 is filtered on it for good (nopass4 written)" m grep -q 'nopass4 eth0' /etc/cosmos-host-guard/home
	msh "ip route replace default via $RT4H"
	guard_uninstall
}

g_v4boot() {
	log "== v4boot: at boot IPv6's default route comes seconds before IPv4's address ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install"
	check "IPv4 passes on eth0 after the install" in_set pass4_if '"eth0"'
	check "outside v4 reaches SSH (the router forwards 22) after the install" tcp "$OC" "$TM4" 22
	msh "mkdir -p /etc/systemd/system/test-routes.service.d && cat > /etc/systemd/system/test-routes.service.d/late4.conf <<EOF
[Service]
ExecStart=
ExecStart=/bin/sh -c 'ip addr del $TM4/24 dev eth0 2>/dev/null; ip -6 route replace default via $RT6H; setsid sh -c \"sleep 6; ip addr add $TM4/24 dev eth0; ip route replace default via $RT4H\" > /dev/null 2>&1 &'
EOF
systemctl daemon-reload"
	reboot_machine
	sleep 20
	log "  (home record: $(m cat /etc/cosmos-host-guard/home | tr '\n' ';'))"
	check "after that boot IPv4 still passes on eth0" in_set pass4_if '"eth0"'
	check_not "and nothing was written down (no nopass4)" m grep -q nopass4 /etc/cosmos-host-guard/home
	check "after that boot outside v4 reaches SSH through the router's forward" tcp "$OC" "$TM4" 22
	check "home v4 reaches SSH" tcp "$HC" "$TM4" 22
	m rm -rf /etc/systemd/system/test-routes.service.d
	m systemctl daemon-reload
	guard_uninstall
}

g_vpnexit() {
	log "== vpnexit: Tailscale switched to an exit node after the install ($TAG)"
	fresh nat || return 1
	# Tailscale's interface (a veth into a peer's namespace stands in for it) and
	# tailscaled.service (root, the packaged path: a stand-in binary)
	msh 'ip netns add vpnpeer && ip link add tailscale0 type veth peer name ts0p && ip link set ts0p netns vpnpeer && ip addr add 100.100.1.1/24 dev tailscale0 && ip -6 addr add fd7a:115c:a1e0::1/64 dev tailscale0 && ip link set tailscale0 up && ip netns exec vpnpeer sh -c "ip addr add 100.100.1.2/24 dev ts0p; ip -6 addr add fd7a:115c:a1e0::2/64 dev ts0p; ip addr add 203.0.113.77/32 dev ts0p; ip link set ts0p up; ip link set lo up; ip route add default via 100.100.1.1"'
	# shellcheck disable=SC2016
	msh 'cp "$(command -v nc)" /usr/sbin/tailscaled; printf "[Service]\nExecStart=/bin/sh -c \"exec /usr/sbin/tailscaled -6 -u -l -k 41641 > /dev/null 2>&1\"\n" > /etc/systemd/system/tailscaled.service; systemctl daemon-reload; systemctl start tailscaled'
	# the Tailscale client's peer list (a stand-in): the peer 100.100.1.2, and the
	# exit node 100.100.1.9; 100.100.1.50 is held by no peer
	# the Tailscale client's peer list (a stand-in printing ts-status.json: the real
	# layout of `tailscale status --json`, two-space indentation; the peer 100.100.1.2,
	# and the exit node 100.100.1.9; 100.100.1.50 is held by no peer); it fails while
	# /etc/ts-fail exists
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 >/dev/null 2>&1'
	docker cp "$HERE/ts-status.json" "$M:/etc/ts-status.json"
	msh 'printf "%s\n" "#!/bin/sh" "[ ! -e /etc/ts-fail ] || exit 1" "cat /etc/ts-status.json" > /usr/bin/tailscale; chmod 755 /usr/bin/tailscale; ip netns exec vpnpeer sh -c "ip addr add 100.100.1.9/32 dev ts0p; ip addr add 100.100.1.50/32 dev ts0p"'
	sleep 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install with Tailscale"
	check "before: a VPN peer reaches SSH" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 22
	# the exit node: a default route in Tailscale's table 52; an internet host's
	# replies go back through it
	m ip route add default dev tailscale0 table 52
	m ip route add 203.0.113.77/32 dev tailscale0
	n=0; while [ $n -lt 5 ] && ! in_set wan_if '"tailscale0"'; do sleep 1; n=$((n + 1)); done
	check "exit node on: tailscale0 is outside within 5 s ($n s)" in_set wan_if '"tailscale0"'
	check "exit node on: a VPN peer still reaches SSH" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 22
	check "exit node on: a VPN peer over IPv6 still reaches SSH" m ip netns exec vpnpeer nc -z -w 2 fd7a:115c:a1e0::1 22
	check_not "exit node on: an internet host through the exit node does not reach 9999" m ip netns exec vpnpeer nc -z -w 2 -s 203.0.113.77 100.100.1.1 9999
	check_not "exit node on: a tailnet address no peer holds does not reach SSH" m ip netns exec vpnpeer nc -z -w 2 -s 100.100.1.50 100.100.1.1 22
	check_not "exit node on: the exit node's own address does not reach SSH" m ip netns exec vpnpeer nc -z -w 2 -s 100.100.1.9 100.100.1.1 22
	check "the real layout: the peer is listed, the exit node is not" sh -c "docker exec $M /usr/local/sbin/cosmos-host-guard show | grep -qx 'vpnpeer4=100.100.1.2'"
	# the compact layout (one line) too
	msh 'python3 -c "import json; d = json.load(open(\"/etc/ts-status.json\")); open(\"/etc/ts-status.json\", \"w\").write(json.dumps(d, separators=(\",\", \":\")))"'
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "the compact layout: the peer is listed" sh -c "docker exec $M /usr/local/sbin/cosmos-host-guard show | grep -qx 'vpnpeer4=100.100.1.2'"
	# tailscaled not answering for a moment: the last good list is kept
	m touch /etc/ts-fail
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "the client not answering: the last good list is kept" sh -c "docker exec $M /usr/local/sbin/cosmos-host-guard show | grep -qx 'vpnpeer4=100.100.1.2'"
	check "the client not answering: the peer still reaches SSH" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 22
	# ... for longer than the bound (15 minutes): none count, said
	m touch -d '-20 minutes' /run/cosmos-host-guard/vpnpeers
	said=$(m /usr/local/sbin/cosmos-host-guard refresh force 2>&1)
	check "past the bound: no peer counts" sh -c "docker exec $M /usr/local/sbin/cosmos-host-guard show | grep -qx 'vpnpeer4='"
	check "past the bound: the state says so for the panel" m grep -qx 'tailnet_peers=unreadable' /var/lib/cosmos-protection/nftables.state
	# (said on the refresh's standard error: the journal's, when the units run it)
	check "past the bound: it is said" sh -c "printf '%s' \"\$1\" | grep -q \"Tailscale's devices cannot be listed\"" _ "$said"
	m rm -f /etc/ts-fail
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "the client back: the peer is listed again" sh -c "docker exec $M /usr/local/sbin/cosmos-host-guard show | grep -qx 'vpnpeer4=100.100.1.2'"
	check "the client back: the state says ok" m grep -qx 'tailnet_peers=ok' /var/lib/cosmos-protection/nftables.state
	# without python3 the list cannot be read
	# shellcheck disable=SC2016
	msh 'p=$(command -v python3); mv "$p" "$p.off"; rm -f /run/cosmos-host-guard/vpnpeers; /usr/local/sbin/cosmos-host-guard refresh force; mv "$p.off" "$p"'
	check "without python3: unreadable, said" m grep -qx 'tailnet_peers=unreadable' /var/lib/cosmos-protection/nftables.state
	m /usr/local/sbin/cosmos-host-guard refresh force
	check "the journal says tailscale0 became outside" sh -c "docker exec $M journalctl -u cosmos-host-guard-watch -u cosmos-host-guard-refresh --no-pager | grep -q 'tailscale0 is an outside interface now'"
	check "the state names the interfaces outside at install (for the panel)" m grep -qx 'outside_at_install=eth0' /var/lib/cosmos-protection/nftables.state
	m ip route del default dev tailscale0 table 52
	m ip route del 203.0.113.77/32 dev tailscale0
	m systemctl stop tailscaled
	m rm -f /usr/bin/tailscale /etc/ts-status.json
	m ip netns del vpnpeer
	guard_uninstall
}

g_flushstorm() {
	log "== flushstorm: another program flushes the ruleset every second ($TAG)"
	fresh nat || return 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install"
	sleep 3
	cpu0=$(m systemctl show -p CPUUsageNSec --value cosmos-host-guard-watch.service)
	docker exec -d "$OC" sh -c "rm -f /tmp/q; i=0; while [ \$i -lt 110 ]; do if nc -z -w 1 $TM6 9999; then echo OPEN; else echo closed; fi >> /tmp/q; sleep 0.2; i=\$((i+1)); done"
	docker exec -d "$M" sh -c 'for i in $(seq 1 20); do nft flush ruleset; sleep 1; done'
	miss=0; n=0
	for _ in $(seq 1 100); do n=$((n + 1)); guard_loaded || miss=$((miss + 1)); sleep 0.2; done
	sleep 8
	cpu1=$(m systemctl show -p CPUUsageNSec --value cosmos-host-guard-watch.service)
	q=$(docker exec "$OC" sh -c 'grep -c . /tmp/q; grep -c OPEN /tmp/q' | tr '\n' ' ')
	used=$(( (cpu1 - cpu0) / 1000000 ))
	log "  (samples without the table: $miss of $n; outside probes (n, open): $q; watcher CPU $used ms)"
	check "the watcher's cost is bounded: $used ms of CPU for 20 flushes in 20 s (bound: 4000 ms)" test "$used" -lt 4000
	# (the samples without the table are said, not bounded: a gap per flush is the
	# time nft takes to report the deletion and load the table again, and the old
	# code's 12-15 of 100 cannot be told from the new one's 3-9; the CPU bound can)
	check "the table is back after the storm" guard_loaded
	check "the journal says it loudly" sh -c "docker exec $M journalctl -u cosmos-host-guard-watch --no-pager | grep -q 'WARNING: something removed the guard'"
	check "the state says it for the panel (removals in the last hour)" sh -c "docker exec $M sed -n 's/^table_removed_hour=//p' /var/lib/cosmos-protection/nftables.state | awk '\$1 >= 10 {ok = 1} END {exit !ok}'"
	m systemctl restart docker
	sleep 3
	# a full refresh follows a fast reload (the network may have changed meanwhile)
	m touch -d '-1 hour' /run/cosmos-host-guard/last-refresh
	m nft flush ruleset
	sleep 15
	check "a full refresh follows a reload within 15 s" m find /run/cosmos-host-guard/last-refresh -newermt '-20 seconds'
	m systemctl restart docker
	sleep 3
	# the fast reload's cache follows a narrowing: a public gateway filters IPv4,
	# and a flush afterwards does not bring the older sets back
	check "IPv4 passes on eth0 before" in_set pass4_if '"eth0"'
	msh "ip route replace default via 198.51.100.254 dev eth0 onlink"
	sleep 5
	check_not "a public gateway: IPv4 no longer passes" in_set pass4_if '"eth0"'
	msh "ip route replace default via $RT4H"
	sleep 5
	m nft flush ruleset
	sleep 2
	check "after a flush the table is back" guard_loaded
	check_not "and IPv4 still does not pass on eth0 (the cache is not older than the narrowing)" in_set pass4_if '"eth0"'
	check_not "outside v4 does not reach 9999" tcp "$OC" "$TM4" 9999
	m systemctl restart docker
	# taken away on purpose: not loaded again, not counted
	before=$(m sh -c "sed -n 's/^table_removed_hour=//p' /var/lib/cosmos-protection/nftables.state")
	m /usr/local/sbin/cosmos-host-guard unload
	sleep 4
	check_not "a hand-typed unload is not undone by the watcher" guard_loaded
	check "and not counted as a removal" test "$before" = "$(m sh -c "sed -n 's/^table_removed_hour=//p' /var/lib/cosmos-protection/nftables.state")"
	m systemctl reload cosmos-host-guard
	check "a reload loads it again" guard_loaded
	guard_uninstall
}

g_f2bsessions() {
	log "== f2bsessions: Fail2ban's never-banned list takes real sessions only ($TAG)"
	fresh nat || return 1
	m sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 nftables >/dev/null 2>&1'
	docker exec -d "$OC2" sh -c 'nc -6 -lk 7000 > /dev/null'
	sleep 1
	docker exec -d "$HC" sh -c "ssh $SSHO -tt -L 7001:[$OC26]:7000 tester@$TM6 'sleep 90'"
	sleep 3
	docker exec -d "$HC" sh -c 'sleep 80 | nc 127.0.0.1 7001'
	docker exec -d "$OC" sh -c "ssh $SSHO -N tester@$TM6"
	sleep 3
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --dry-run 2>&1)
	check_not "a -L forward's destination is not put among the never-banned" says "$OC26"
	check "a real outside session (ssh -N) is never banned" says "signed-in SSH sessions open now from outside:.*$OC6"
	docker exec "$HC" sh -c 'pkill -f "ssh .*7001"; true'
	docker exec "$OC" sh -c 'pkill -f "ssh .*-N"; true'
}

for g in $GROUPS_WANTED; do
	GROUP=$g
	case "$g" in
		guard) g_guard ;;
		ssh) g_ssh ;;
		ssh-unattended) g_ssh_unattended ;;
		live) g_live ;;
		follow) g_follow ;;
		routes) g_routes ;;
		nftsvc) g_nftsvc ;;
		docker) g_docker ;;
		docker-noipv6) g_docker_mode noipv6 "Docker without IPv6 networks (docker-proxy, the production shape)" ;;
		docker-nft) g_docker_mode nft "Docker's nftables backend" ;;
		vpn) g_vpn ;;
		pub4) g_pub4 ;;
		spoof) g_spoof ;;
		foreign) g_foreign ;;
		race) g_race ;;
		validate) g_validate ;;
		uplink) g_uplink ;;
		failclosed) g_failclosed ;;
		ipv4later) g_ipv4later ;;
		homewiden) g_homewiden ;;
		sshservers) g_sshservers ;;
		nohome) g_nohome ;;
		churn) g_churn ;;
		vpnports) g_vpnports ;;
		cloud) g_cloud ;;
		allowfile) g_allowfile ;;
		confirmwindow) g_confirmwindow ;;
		ufw) g_ufw ;;
		firewalld) g_firewalld ;;
		fail2ban) g_fail2ban ;;
		updates) g_updates ;;
		sessions) g_sessions ;;
		strangers) g_strangers ;;
		moved) g_moved ;;
		v4gap) g_v4gap ;;
		v4boot) g_v4boot ;;
		vpnexit) g_vpnexit ;;
		flushstorm) g_flushstorm ;;
		f2bsessions) g_f2bsessions ;;
		*) bad "unknown group $g" ;;
	esac
done
topo_down
log "$TAG: $PASS passed, $FAILED failed$FAILS"
[ "$FAILED" = 0 ]
