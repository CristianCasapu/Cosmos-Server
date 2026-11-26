#!/bin/sh
# Helpers of scripts/fork-guard-e2e/inner.sh (sourced; runs inside the test's own
# Docker in Docker, never on a real server).
#
# The networks (documentation and private ranges only):
#   home  2001:db8:1::/64 + 192.168.1.0/24 (a home behind NAT) or 203.0.113.0/24
#         (a server with a public IPv4 address): the test machine (.10, ::10),
#         a home client (.20, ::20) and the router (.254, ::fe)
#   out   2001:db8:2::/64 + 198.51.100.0/24: two outside clients (.20/.21) and
#         the router (.254, ::fe), which routes between the two
# The test machine's and the outside clients' default routes go through the
# router, as at home.

P=${PREFIX:-cosmos-guard-test}
M="$P-m"
RT="$P-rt"
HC="$P-hc"
OC="$P-oc"
OC2="$P-oc2"
IMG_C=${IMG_C:-cosmos-guard-test-client}
TM6=2001:db8:1::10
HC6=2001:db8:1::20
RT6H=2001:db8:1::fe
OC6=2001:db8:2::20
OC26=2001:db8:2::21
RT6O=2001:db8:2::fe
OC4=198.51.100.20
OC24=198.51.100.21
RT4O=198.51.100.254
SSHO="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=4 -o ServerAliveInterval=2 -o ServerAliveCountMax=3 -o BatchMode=yes -o LogLevel=ERROR -i /root/.ssh/id_ed25519"

PASS=0
FAILED=0
FAILS=""
log() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }
ok() { PASS=$((PASS + 1)); log "  ok    $*"; }
bad() { FAILED=$((FAILED + 1)); FAILS="$FAILS
  [$MACHINE] $*"; log "  FAIL  $*"
	# STOP_ON_FAIL=1: keep the machines as they are at the first failure, to look at them
	if [ "${STOP_ON_FAIL:-}" = 1 ]; then log "stopped at the first failure (STOP_ON_FAIL=1): the machines are kept"; exit 99; fi; }
check() { # description command...
	d=$1; shift
	if "$@" >/dev/null 2>&1; then ok "$d"; else bad "$d"; fi
}
check_not() {
	d=$1; shift
	if "$@" >/dev/null 2>&1; then bad "$d"; else ok "$d"; fi
}

m() { docker exec "$M" "$@"; }
msh() { docker exec "$M" sh -c "$1"; }

# tcp <client> <address> <port>: a connection opens within 2 s
tcp() { docker exec "$1" nc -z -w 2 "$2" "$3"; }

topo_up() { # nat|pub
	TOPO=$1
	if [ "$TOPO" = pub ]; then NET4=203.0.113; else NET4=192.168.1; fi
	TM4=$NET4.10
	HC4=$NET4.20
	RT4H=$NET4.254
	docker network create --ipv6 --subnet 2001:db8:1::/64 --subnet "$NET4.0/24" "$P-home" >/dev/null
	docker network create --ipv6 --subnet 2001:db8:2::/64 --subnet 198.51.100.0/24 "$P-out" >/dev/null
	# The router container routes between the two networks. When br_netfilter is
	# loaded in the kernel (by anything on the host), bridged frames pass this
	# Docker in Docker's own ip(6)tables, whose raw rules drop packets for a
	# container address that arrive on another bridge: the routed traffic. Bridged
	# frames are not filtered here (this Docker in Docker's own network namespace).
	sysctl -q -w net.bridge.bridge-nf-call-iptables=0 net.bridge.bridge-nf-call-ip6tables=0 2>/dev/null || true
	docker run -d --name "$RT" --cap-add NET_ADMIN --network "$P-home" --ip "$RT4H" --ip6 "$RT6H" \
		--sysctl net.ipv4.ip_forward=1 --sysctl net.ipv6.conf.all.forwarding=1 \
		--sysctl net.ipv4.conf.all.rp_filter=0 --sysctl net.ipv4.conf.default.rp_filter=0 "$IMG_C" sleep infinity >/dev/null
	docker network connect --ip "$RT4O" --ip6 "$RT6O" "$P-out" "$RT"
	for c in "$HC:$HC4:$HC6:home" "$OC:$OC4:$OC6:out" "$OC2:$OC24:$OC26:out"; do
		name=${c%%:*}; rest=${c#*:}; a4=${rest%%:*}; rest=${rest#*:}; net=${rest##*:}; a6=${rest%:*}
		docker run -d --name "$name" --cap-add NET_ADMIN --network "$P-$net" --ip "$a4" --ip6 "$a6" "$IMG_C" sleep infinity >/dev/null
	done
	for c in "$OC" "$OC2"; do
		docker exec "$c" sh -c "ip route replace default via $RT4O && ip -6 route replace default via $RT6O"
	done
	docker exec "$HC" sh -c "ip route replace default via $RT4H && ip -6 route replace default via $RT6H"
	# one key for every client
	docker exec "$OC" sh -c 'mkdir -p /root/.ssh && ssh-keygen -q -t ed25519 -N "" -f /root/.ssh/id_ed25519'
	KEY=$(docker exec "$OC" cat /root/.ssh/id_ed25519)
	PUB=$(docker exec "$OC" cat /root/.ssh/id_ed25519.pub)
	for c in "$OC2" "$HC"; do
		printf '%s\n' "$KEY" | docker exec -i "$c" sh -c 'mkdir -p /root/.ssh && cat > /root/.ssh/id_ed25519 && chmod 600 /root/.ssh/id_ed25519'
	done
}

machine_up() { # image
	docker volume create "$M-docker" >/dev/null
	docker volume create "$M-containerd" >/dev/null
	docker run -d --name "$M" --hostname server --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock \
		-v "$M-docker:/var/lib/docker" -v "$M-containerd:/var/lib/containerd" --network "$P-home" --ip "$TM4" --ip6 "$TM6" "$1" >/dev/null
	wait_boot
	docker cp "${FORK_DIR:-/work/fork}" "$M:/opt/cosmos-fork"
	msh 'rm -rf /opt/cosmos && mkdir -p /opt/cosmos && mv /opt/cosmos-fork /opt/cosmos/fork'
	printf '%s\n' "$PUB" | docker exec -i "$M" sh -c 'cat > /home/tester/.ssh/authorized_keys && chown tester:tester /home/tester/.ssh/authorized_keys && chmod 600 /home/tester/.ssh/authorized_keys'
	# what DHCP and router adverts would do at home: the default routes through the router
	msh "cat > /etc/systemd/system/test-routes.service <<EOF
[Unit]
Description=test: default routes through the router
DefaultDependencies=no
After=local-fs.target
Before=network-pre.target
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'ip route replace default via $RT4H; ip -6 route replace default via $RT6H'
[Install]
WantedBy=sysinit.target
EOF
cat > /etc/systemd/system/stub-http@.service <<'EOF'
[Unit]
Description=test: a web server on port %i (stands in for Cosmos and other services)
[Service]
ExecStart=/bin/busybox httpd -f -p [::]:%i -h /srv
Restart=always
[Install]
WantedBy=multi-user.target
EOF
mkdir -p /srv && echo stub > /srv/index.html
printf '%s\\n' '#!/bin/sh' '# test helper: apply a change of sshd configuration, socket-activated (Ubuntu) or not' 'systemctl daemon-reload' 'if systemctl is-enabled --quiet ssh.socket 2>/dev/null; then systemctl stop ssh.service 2>/dev/null; systemctl restart ssh.socket; else systemctl restart ssh.service; fi' > /usr/local/bin/ssh_restart
chmod 755 /usr/local/bin/ssh_restart
mkdir -p /var/lib/cosmos /etc/docker
cat > /var/lib/cosmos/cosmos.config.json <<'EOF'
{
  \"HTTPConfig\": {
    \"HTTPSCertificateMode\": \"SELFSIGNED\",
    \"DNSChallengeProvider\": \"\",
    \"HTTPPort\": \"2052\",
    \"HTTPSPort\": \"2053\",
    \"UPnP\": { \"ExternalHTTPPort\": \"80\", \"ExternalHTTPSPort\": \"443\" }
  }
}
EOF
case '${DOCKER_MODE:-ipv6}' in
	noipv6) echo '{}' > /etc/docker/daemon.json ;;
	nft) echo '{ \"ipv6\": true, \"fixed-cidr-v6\": \"fd00:dead:beef::/64\", \"firewall-backend\": \"nftables\" }' > /etc/docker/daemon.json
	     # Docker's nftables backend does not switch forwarding on by itself (documented)
	     printf 'net.ipv4.ip_forward = 1\nnet.ipv6.conf.all.forwarding = 1\nnet.ipv6.conf.default.forwarding = 1\n' > /etc/sysctl.d/90-test-forwarding.conf
	     sysctl -q -p /etc/sysctl.d/90-test-forwarding.conf ;;
	*) echo '{ \"ipv6\": true, \"fixed-cidr-v6\": \"fd00:dead:beef::/64\", \"ip6tables\": true, \"experimental\": true }' > /etc/docker/daemon.json ;;
esac
systemctl daemon-reload
systemctl enable --now test-routes.service stub-http@2052 stub-http@2053 stub-http@9999 >/dev/null 2>&1
systemctl restart docker"
	docker save busybox:1.36 | docker exec -i "$M" docker load >/dev/null
	# a container whose published port is public (given with --port 8443) and one whose port is not
	msh 'docker run -d --restart=always --name pub -p 8443:80 busybox:1.36 httpd -f -p 80 >/dev/null
docker run -d --restart=always --name priv -p 18080:80 busybox:1.36 httpd -f -p 80 >/dev/null'
	for _ in $(seq 1 30); do tcp "$HC" "$TM6" 8443 2>/dev/null && tcp "$HC" "$TM6" 2053 2>/dev/null && break; sleep 1; done
}

wait_boot() {
	for _ in $(seq 1 90); do
		s=$(docker exec "$M" systemctl is-system-running 2>/dev/null || true)
		case "$s" in running|degraded) break ;; esac
		sleep 1
	done
	# sshd answers (socket activated on Ubuntu)
	for _ in $(seq 1 30); do docker exec "$M" sh -c 'ss -Htln | grep -q ":22 "' && break; sleep 1; done
}

reboot_machine() {
	docker restart -t 20 "$M" >/dev/null
	wait_boot
	for _ in $(seq 1 60); do tcp "$HC" "$TM6" 8443 2>/dev/null && tcp "$HC" "$TM6" 2053 2>/dev/null && break; sleep 1; done
}

topo_down() {
	for c in "$M" "$RT" "$HC" "$OC" "$OC2"; do docker rm -f -v "$c" >/dev/null 2>&1 || true; done
	# a systemd machine can take a while to go: wait until its name is free
	for _ in $(seq 1 60); do
		docker container inspect "$M" >/dev/null 2>&1 || break
		docker rm -f -v "$M" >/dev/null 2>&1 || true
		sleep 1
	done
	docker volume rm "$M-docker" "$M-containerd" >/dev/null 2>&1 || true
	docker network rm "$P-home" "$P-out" >/dev/null 2>&1 || true
}

# ssh_run <client> <command>: as tester, through a new connection
ssh_run() { docker exec "$1" sh -c "ssh $SSHO tester@$TM6 '$2'"; }

ruleset() { m nft -s list ruleset 2>/dev/null || true; }
# the files the pieces may touch, with their contents' checksums
files_snapshot() {
	# shellcheck disable=SC2016
	msh 'find /etc /usr/local /var/lib/cosmos-protection /var/backups -xdev \( -type f -o -type l \) 2>/dev/null | grep -v -E "^/etc/(ld.so.cache|machine-id|resolv.conf|hostname|hosts|mtab)$" | sort | while read -r f; do printf "%s " "$f"; if [ -L "$f" ]; then readlink "$f"; else md5sum < "$f" | cut -c1-32; fi; done'
}

# Docker's rules as iptables shows them, without counters
xtables() { msh 'iptables-save; ip6tables-save' 2>/dev/null | grep -v '^#' | sed 's/\[[0-9]*:[0-9]*\]//'; }

guard_loaded() { m nft list table inet cosmos_host_guard >/dev/null 2>&1; }
in_set() { m nft get element inet cosmos_host_guard "$1" "{ $2 }" >/dev/null 2>&1; }
# the test machine's link-local address on its home/outside interface
tm_ll() { m ip -o -6 addr show dev eth0 scope link | awk '{sub(/\/.*/, "", $4); print $4; exit}'; }
# udp_sent <client> <source> <address> <port>: three datagrams from that source
udp_sent() { docker exec "$1" sh -c "for i in 1 2 3; do echo probe | nc -u -w 1 -s $2 $3 $4; done" >/dev/null 2>&1 || true; }
