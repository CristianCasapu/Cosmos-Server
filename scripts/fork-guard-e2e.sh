#!/bin/sh
# Fork addition (plan 47, E3): the protection pieces end to end — the nftables
# host guard (fork/nftables), Fail2ban (fork/fail2ban) and the automatic
# security updates (fork/unattended-upgrades) — on systemd test machines
# (Debian 12, Debian 13, Ubuntu 24.04) with sshd and Docker, each between a
# "home" and an "outside" network with IPv6, inside a docker:29-dind of the
# test's own. What it proves is in scripts/fork-guard-e2e/inner.sh. Never the
# owner's server, its Docker or its firewall: every rule is loaded inside a
# test machine's own network namespace.
#
#   sh scripts/fork-guard-e2e.sh            shellcheck + every machine, every group
#   sh scripts/fork-guard-e2e.sh fast       shellcheck + Debian 13, the core groups
#   sh scripts/fork-guard-e2e.sh clean      remove what a kept run left
# Options: MACHINES="debian12-ce debian13-ce ubuntu2404-ce" (-ce: Docker 29 from
# download.docker.com; -distro: the distribution's docker.io), TEST_GROUPS (see inner.sh),
# PREFIX=fork-guard, SCRATCH (default /storage/dev/.cache/$PREFIX when that
# exists, else $TMPDIR), KEEP=1 (leave the Docker in Docker), BREAK (inner.sh),
# OLD_REF (the commit whose fork/fail2ban is "the current version" for the
# upgrade comparison; skipped when git does not have it),
# REGISTRY_MIRROR (default https://mirror.gcr.io).
# The Docker in Docker needs a way out: the images are built with apt.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
PREFIX=${PREFIX:-fork-guard}
if [ -z "${SCRATCH:-}" ]; then
	if [ -d /storage/dev/.cache ]; then SCRATCH=/storage/dev/.cache/$PREFIX; else SCRATCH=${TMPDIR:-/tmp}/$PREFIX; fi
fi
MIRROR=${REGISTRY_MIRROR:-https://mirror.gcr.io}
OLD_REF=${OLD_REF:-b6c9743a}
D="$PREFIX-dind"
MODE=${1:-all}

clean() {
	docker rm -f -v "$D" >/dev/null 2>&1 || true
	docker network rm "$PREFIX-net" >/dev/null 2>&1 || true
	# the Docker in Docker's files are root's: removed from a container
	if [ -d "$SCRATCH" ]; then
		docker run --rm --entrypoint rm -v "$(dirname "$SCRATCH"):/w" docker:29-dind -rf "/w/$(basename "$SCRATCH")" || true
	fi
}
case "$MODE" in
	clean) clean; exit 0 ;;
	fast) MACHINES=${MACHINES:-debian13-ce}; TEST_GROUPS=${TEST_GROUPS:-fast} ;;
	all) MACHINES=${MACHINES:-debian12-ce debian13-ce ubuntu2404-ce}; TEST_GROUPS=${TEST_GROUPS:-all} ;;
	*) echo "usage: sh scripts/fork-guard-e2e.sh [all|fast|clean]" >&2; exit 2 ;;
esac

start=$(date +%s)
mkdir -p "$SCRATCH/dind" "$SCRATCH/work"
docker network create "$PREFIX-net" >/dev/null 2>&1 || true
docker run -d --name "$D" --privileged -e DOCKER_TLS_CERTDIR= --network "$PREFIX-net" \
	--sysctl net.ipv6.conf.all.disable_ipv6=0 \
	-v "$SCRATCH/dind:/var/lib/docker" -v "$SCRATCH/work:/work" docker:29-dind --registry-mirror="$MIRROR" >/dev/null
for _ in $(seq 1 60); do docker exec "$D" docker info >/dev/null 2>&1 && break; sleep 1; done

# what the test machines get: the pieces, the test scripts, and the Fail2ban
# installer as it was before this work (the owner's server has that one)
docker exec "$D" sh -c 'rm -rf /work/fork /work/e2e /work/fail2ban-old'
docker cp "$repo/fork" "$D:/work/fork"
docker cp "$here/fork-guard-e2e" "$D:/work/e2e"
docker cp "$here/fork-guard-e2e.sh" "$D:/work/e2e/outer.sh"
if git -C "$repo" cat-file -e "$OLD_REF:fork/fail2ban/install.sh" 2>/dev/null; then
	mkdir -p "$SCRATCH/old"
	git -C "$repo" archive "$OLD_REF" fork/fail2ban | tar -x -C "$SCRATCH/old"
	docker cp "$SCRATCH/old/fork/fail2ban" "$D:/work/fail2ban-old"
	rm -rf "$SCRATCH/old"
else
	echo "note: $OLD_REF is not in this clone: the Fail2ban upgrade comparison uses this version as the old one"
	docker cp "$repo/fork/fail2ban" "$D:/work/fail2ban-old"
fi

docker exec "$D" sh -c '
	set -e
	cd /work/e2e
	docker build -q -t cosmos-guard-test-client -f client.Dockerfile . >/dev/null
	for m in '"$MACHINES"'; do
		case "${m%-*}" in
			debian12) base=debian:12 ;; debian13) base=debian:13 ;; ubuntu2404) base=ubuntu:24.04 ;;
			*) echo "unknown machine $m" >&2; exit 2 ;;
		esac
		case "$m" in *-ce) docker_flavour=ce ;; *-distro) docker_flavour=distro ;; *) echo "unknown machine $m (-ce or -distro)" >&2; exit 2 ;; esac
		docker build -q --build-arg BASE=$base --build-arg DOCKER=$docker_flavour -t cosmos-guard-test-machine:$m -f machine.Dockerfile . >/dev/null
	done
	docker pull -q busybox:1.36 >/dev/null'
echo "images ready after $(( $(date +%s) - start )) s"

status=0
echo "== shellcheck"
if docker exec "$D" docker run --rm -v /work:/work:ro cosmos-guard-test-client sh -c \
	'shellcheck -s sh /work/fork/nftables/install.sh /work/fork/nftables/uninstall.sh /work/fork/nftables/cosmos-host-guard /work/fork/fail2ban/install.sh /work/fork/fail2ban/uninstall.sh /work/fork/unattended-upgrades/install.sh /work/fork/unattended-upgrades/uninstall.sh && shellcheck -s sh -x -P /work/e2e /work/e2e/lib.sh /work/e2e/inner.sh /work/e2e/outer.sh'; then
	echo "shellcheck: clean"
else
	status=1
fi
for m in $MACHINES; do
	# shellcheck disable=SC2086
	docker exec -e PREFIX="$PREFIX-t" -e BREAK="${BREAK:-}" "$D" sh /work/e2e/inner.sh "$m" $TEST_GROUPS || status=1
done
echo "took $(( $(date +%s) - start )) s"
[ "${KEEP:-}" = 1 ] || clean
exit $status
