#!/bin/sh
# The end-to-end tests of the protection pieces (fork/nftables, fork/fail2ban,
# fork/unattended-upgrades), run by scripts/fork-guard-e2e.sh inside a Docker in
# Docker of the test's own: systemd test machines with sshd and Docker, home and
# outside clients and a router on networks of their own (see lib.sh).
#
#   sh inner.sh <machine image tag> [group...]
# Groups: guard ssh follow docker vpn pub4 ufw firewalld fail2ban updates (default: all);
# "fast" is guard and the unattended SSH cases.
# BREAK=open-port|no-ssh-allow|no-undo breaks the guard's installer on purpose:
# the tests must then fail.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

TAG=$1
shift
MACHINE=$TAG
IMAGE=cosmos-guard-test-machine:$TAG
GROUPS_WANTED=${*:-all}
[ "$GROUPS_WANTED" = all ] && GROUPS_WANTED="guard ssh follow docker vpn pub4 ufw firewalld fail2ban updates"
[ "$GROUPS_WANTED" = fast ] && GROUPS_WANTED="guard ssh-unattended"
BREAK=${BREAK:-}
G=/opt/cosmos/fork/nftables

fresh() { # nat|pub: a new machine on new networks
	topo_down
	topo_up "$1"
	machine_up "$IMAGE"
	if ! docker exec "$M" sh -c 'docker ps --format "{{.Names}}" | grep -q priv && docker ps --format "{{.Names}}" | grep -q pub' 2>/dev/null; then
		bad "the test machine $IMAGE did not start"
		topo_down
		log "$TAG: $PASS passed, $FAILED failed$FAILS"
		exit 1
	fi
	case "$BREAK" in
		open-port) msh "sed -i 's/^\[ -n \"\$PUBLIC_TCP\$PUBLIC_UDP\" \] ||/add_port 9999 tcp broken; &/' $G/install.sh" ;;
		no-ssh-allow) msh "sed -i -e '/ssh_allow6 { \$SESSION_ADDR }\" ;;/d' -e '/ssh_allow4 { \$SESSION_ADDR }\" ;;/d' -e 's/^\tif \[ -n \"\$SESSION_ADDR\" \]; then\$/\tif false; then/' $G/install.sh" ;;
		no-undo) msh "sed -i 's/^\tsystemd-run --quiet --unit=/\ttrue || systemd-run --quiet --unit=/' $G/install.sh" ;;
	esac
}

# install from a client through SSH (sudo, as an administrator would)
# guard_install <client> <args...>; sets RC and leaves the output in OUT
guard_install() {
	c=$1; shift
	OUT=$(docker exec "$c" sh -c "ssh $SSHO tester@$TM6 'sudo sh $G/install.sh $*; echo RC=\$?'" 2>&1)
	RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p' | tail -n 1)
}
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
	fresh nat
	before_rules=$(ruleset); before_files=$(files_snapshot); before_ipt=$(xtables)
	guard_uninstall
	expect_rc 0 "uninstall without an installation"
	check "uninstall without an installation says so" sh -c "printf '%s' \"\$1\" | grep -q 'not installed'" _ "$OUT"
	guard_install "$HC" --dry-run --port 2053 --port 8443
	expect_rc 0 "dry run"
	check "dry run: the ruleset is unchanged" test "$before_rules" = "$(ruleset)"
	check "dry run: no file changed" test "$before_files" = "$(files_snapshot)"
	check "dry run: nftables was not installed" sh -c "! docker exec $M sh -c 'command -v nft'"
	packages=$(m sh $G/install.sh --print-packages)
	check "--print-packages names nftables" test "$packages" = nftables
	guard_install "$HC" --yes --port 2053 --port 8443 --record /root/record
	expect_rc 0 "unattended install from home"
	check "the record names the table, the units and the status" sh -c "docker exec $M grep -q 'nftables	status	installed' /root/record && docker exec $M grep -q 'nftables	unit	cosmos-host-guard.service' /root/record && docker exec $M grep -q 'nftables	package	nftables' /root/record"
	check "nftables.service is not enabled" sh -c "[ \"\$(docker exec $M systemctl is-enabled nftables.service 2>/dev/null)\" != enabled ]"
	check "the units are active" m systemctl is-active --quiet cosmos-host-guard.service cosmos-host-guard-watch.service cosmos-host-guard-refresh.timer
	probe_matrix "NAT host"
	check "IPv4 is not filtered behind NAT: outside v4 reaches 9999 (the router forwards only what is asked)" tcp "$OC" "$TM4" 9999
	check "the drop counters count" sh -c "docker exec $M nft list counter inet cosmos_host_guard dropped_input | grep -qv 'packets 0 '"
	check "the forward counter counts (the container port 18080)" sh -c "docker exec $M nft list counter inet cosmos_host_guard dropped_forward | grep -qv 'packets 0 '"
	check "Docker's own rules are still there" sh -c "docker exec $M iptables -S DOCKER >/dev/null && docker exec $M ip6tables -S DOCKER >/dev/null"
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
	docker exec "$c" sh -c "rm -f /tmp/in /tmp/out; mkfifo /tmp/in; (sleep 900 > /tmp/in &) ; (ssh $SSHO tester@$TM6 'sh -c \"(while :; do echo TICK \\\$(date +%s); sleep 1; done) & t=\\\$!; sudo sh $G/install.sh $*; echo RC=\\\$?; sleep 2; kill \\\$t\"' < /tmp/in > /tmp/out 2>&1; echo SSH_RC=\$? >> /tmp/out) &"
}
session_wait() { # pattern seconds
	for _ in $(seq 1 "$2"); do
		docker exec "$c" grep -q "$1" /tmp/out && return 0
		sleep 1
	done
	return 1
}
session_say() { docker exec "$c" sh -c "echo '$1' > /tmp/in"; }
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
	fresh nat
	before_rules=$(ruleset); before_files=$(files_snapshot)
	session_install "$OC" --yes --port 2053
	session_result
	expect_rc 4 "unattended from outside without --allow-ssh-from-outside is refused"
	check "the refusal says why" sh -c "printf '%s' \"\$1\" | grep -q 'Refused for your session'" _ "$SSH_OUT"
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
	g_ssh_unattended
	log "== ssh: interactive runs from outside, under sudo ($TAG)"
	before_rules=$(ruleset)
	# confirmed in time
	session_install "$OC" --port 2053 --confirm-timeout 60
	session_wait 'Go on?' 30; session_say y
	if session_wait 'Confirm?' 30; then ok "the installer asks for a confirmation"; else bad "the installer did not ask for a confirmation"; fi
	check "the undo timer is armed" m systemctl is-active --quiet cosmos-host-guard-undo.timer
	check "while waiting: a NEW SSH connection from the session's address works" ssh_run "$OC" true
	check_not "while waiting: another outside address does not reach SSH" tcp "$OC2" "$TM6" 22
	session_say yes
	session_result
	expect_rc 0 "interactive from outside, confirmed"
	check "the session survived (ticks without a gap)" sh -c "[ '$SSH_RC' = 0 ] && printf '%s\n' \"\$1\" | awk '/^TICK / {t = \$2; if (last && t - last > 3) gap = 1; last = t; n++} END {exit (gap || n < 3)}'" _ "$SSH_OUT"
	check "the session's address was found under sudo (SSH_CONNECTION from the parent)" m grep -q "ssh_allow6 { $OC6 }" /etc/cosmos-host-guard/settings.nft
	check "the undo timer is gone after the confirmation" sh -c "! docker exec $M systemctl is-active --quiet cosmos-host-guard-undo.timer"
	check "the guard stays installed" guard_loaded
	sleep 3
	check "still loaded after the undo's time would have passed" guard_loaded
	check "the confirmed session's address still reaches SSH" ssh_run "$OC" true
	check_not "another outside address does not reach SSH" tcp "$OC2" "$TM6" 22
	guard_uninstall
	# nobody confirms: the undo fires, the session lives on
	session_install "$OC" --port 2053 --confirm-timeout 30
	session_wait 'Go on?' 30; session_say y
	session_wait 'Confirm?' 30
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
	session_wait 'Go on?' 30; session_say y
	session_wait 'Confirm?' 30
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
	session_wait 'Go on?' 30; session_say y
	session_wait 'Confirm?' 30
	docker exec "$OC" sh -c 'pkill -f "ssh .*tester@" ; true'
	sleep 34
	check "a cut session takes the guard away too" sh -c "! docker exec $M nft list table inet cosmos_host_guard"
	check "and nothing is left installed" sh -c "! docker exec $M test -e /etc/systemd/system/cosmos-host-guard.service"
	docker exec "$OC" sh -c 'pkill -f "sleep 900"; true'
}

g_follow() {
	log "== follow: address changes without a gap ($TAG)"
	fresh nat
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
		followed=0
		for _ in $(seq 1 20); do
			if docker exec "$HC" nc -z -w 1 -s 2001:db8:1:1::20 2001:db8:1:1::10 9999 2>/dev/null; then followed=1; break; fi
			sleep 0.2
		done
		t1=$(date +%s)
		if [ "$followed" = 1 ]; then ok "round $i: a new home prefix is followed within $((t1 - t0)) s"; else bad "round $i: the new home prefix was not followed"; fi
		m ip -6 addr del 2001:db8:1:1::10/64 dev eth0
		sleep 1
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
	guard_loaded || { fresh nat; guard_install "$HC" --yes --port 2053 --port 8443; }
	guard_install "$HC" --yes --port 2053 --port 8443
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
	check "after a reboot Docker's rules are there" sh -c "docker exec $M iptables -S DOCKER >/dev/null"
	probe_matrix "after a reboot"
	guard_uninstall
}

g_vpn() {
	log "== vpn: a VPN interface and a VPN's port ($TAG)"
	fresh nat
	# a VPN peer behind an interface without a default route (as tailscale0 or wg0)
	msh 'ip netns add vpnpeer && ip link add vpn0 type veth peer name vpn0p && ip link set vpn0p netns vpnpeer && ip addr add 100.100.1.1/24 dev vpn0 && ip -6 addr add fd7a:115c:a1e0::1/64 dev vpn0 && ip link set vpn0 up && ip netns exec vpnpeer sh -c "ip addr add 100.100.1.2/24 dev vpn0p; ip -6 addr add fd7a:115c:a1e0::2/64 dev vpn0p; ip link set vpn0p up; ip link set lo up"'
	# a VPN daemon listening for direct connections (named as Tailscale's)
	# shellcheck disable=SC2016
	msh 'cp "$(command -v nc)" /usr/local/bin/tailscaled; (setsid /usr/local/bin/tailscaled -6 -u -l -k 41641 > /tmp/vpn.udp 2>&1 &)'
	sleep 1
	guard_install "$HC" --yes --port 2053
	expect_rc 0 "install with a VPN"
	check "the VPN's port was found" m nft get element inet cosmos_host_guard vpn_udp '{ 41641 }'
	check_not "the VPN interface is not an outside interface" m nft get element inet cosmos_host_guard wan_if '{ "vpn0" }'
	check "a VPN peer reaches port 9999" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 9999
	check "a VPN peer reaches SSH" m ip netns exec vpnpeer nc -z -w 2 100.100.1.1 22
	docker exec "$OC" sh -c "echo hello | nc -u -w 1 $TM6 41641" >/dev/null 2>&1
	sleep 1
	check "outside reaches the VPN's UDP port" m grep -q hello /tmp/vpn.udp
	check_not "outside still does not reach 9999" tcp "$OC" "$TM6" 9999
	guard_uninstall
}

g_pub4() {
	log "== pub4: a host with a public IPv4 address ($TAG)"
	fresh pub
	guard_install "$HC" --yes --port 2053 --port 8443
	expect_rc 0 "install on a host with a public IPv4 address"
	check "the installer chose to filter IPv4" sh -c "printf '%s' \"\$1\" | grep -q 'IPv4        filtered'" _ "$OUT"
	check "IPv4 is filtered (no IPv4 accept rule)" sh -c "! docker exec $M nft list chain inet cosmos_host_guard input | grep -q 'meta nfproto ipv4 accept'"
	check "outside v4 reaches 2053" tcp "$OC" "$TM4" 2053
	check "outside v4 reaches the public container port 8443 (forward path, DNAT)" tcp "$OC" "$TM4" 8443
	check_not "outside v4 does not reach 9999" tcp "$OC" "$TM4" 9999
	check_not "outside v4 does not reach the container port 18080 (forward path, DNAT)" tcp "$OC" "$TM4" 18080
	check_not "outside v4 does not reach SSH" tcp "$OC" "$TM4" 22
	for p in 9999 18080 22; do check "home v4 (on-link public network) reaches $p" tcp "$HC" "$TM4" "$p"; done
	probe_matrix "public IPv4 host"
	guard_uninstall
	guard_install "$HC" --yes --port 2053 --ipv4 no
	check "--ipv4 no leaves IPv4 alone" tcp "$OC" "$TM4" 9999
	guard_uninstall
}

g_ufw() {
	log "== ufw active ($TAG)"
	fresh nat
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
	fresh nat
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

f2b_log() { # address count
	msh "for i in \$(seq 1 $2); do echo \"\$(date +%Y-%m-%dT%H:%M:%S%z) cosmos-security kind=auth.login.failed ip=$1 sev=medium n=1\" >> /var/lib/cosmos/cosmos.security.log; done"
}
banned() { m sh -c "fail2ban-client status cosmos-auth | grep -q -- '$1'"; }

g_fail2ban() {
	log "== fail2ban: the current version, then this one over it; bans ($TAG)"
	fresh nat
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
	fresh nat
	before_files=$(files_snapshot)
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --dry-run 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "Fail2ban dry run on a machine without python3"
	check "Fail2ban dry run: no file changed" test "$before_files" = "$(files_snapshot)"
	check "--print-packages names python3, nftables and fail2ban" sh -c "[ \"\$(docker exec $M sh /opt/cosmos/fork/fail2ban/install.sh --print-packages | tr '\n' ' ')\" = 'python3 nftables fail2ban ' ]"
	OUT=$(m sh /opt/cosmos/fork/fail2ban/install.sh --yes --record /root/f2b.rec 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "Fail2ban unattended on a bare machine"
	check "the record names the packages it installed" sh -c "docker exec $M grep -c 'fail2ban	package	' /root/f2b.rec | grep -q 3"
	check "the sshd jail is off by default" sh -c "! docker exec $M fail2ban-client status sshd"
}

uu_value() { m apt-config dump | grep -F "$1"; }
g_updates() {
	log "== automatic security updates ($TAG)"
	fresh nat
	m apt-mark hold docker.io >/dev/null
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
	check "Docker's hold is kept" sh -c "docker exec $M apt-mark showhold | grep -qx docker.io"
	check "the record says installed" m grep -q 'unattended-upgrades	status	installed' /root/uu.rec
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/install.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "install a second time"
	OUT=$(m sh /opt/cosmos/fork/unattended-upgrades/uninstall.sh --yes 2>&1; echo "RC=$?"); RC=$(printf '%s\n' "$OUT" | sed -n 's/^RC=//p')
	expect_rc 0 "uninstall"
	check_not "after uninstall apt no longer runs them" uu_value 'APT::Periodic::Unattended-Upgrade "1";'
	check "Docker's hold is still kept" sh -c "docker exec $M apt-mark showhold | grep -qx docker.io"
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

for g in $GROUPS_WANTED; do
	case "$g" in
		guard) g_guard ;;
		ssh) g_ssh ;;
		ssh-unattended) g_ssh_unattended ;;
		follow) g_follow ;;
		docker) g_docker ;;
		vpn) g_vpn ;;
		pub4) g_pub4 ;;
		ufw) g_ufw ;;
		firewalld) g_firewalld ;;
		fail2ban) g_fail2ban ;;
		updates) g_updates ;;
		*) bad "unknown group $g" ;;
	esac
done
topo_down
log "$TAG: $PASS passed, $FAILED failed$FAILS"
[ "$FAILED" = 0 ]
