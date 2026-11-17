#!/bin/bash
# Removes what install.sh installed, and nothing else.
#
#     sudo bash /opt/cosmos/fork/install/uninstall.sh            # asks first
#     sudo bash uninstall.sh --yes                               # no question
#
# It reads the list install.sh kept (/var/lib/cosmos-installer/record) and
# removes exactly what is on it, in this order: the service CosmosCloud
# (stopped, disabled, its unit file), the protection pieces (each with its
# own uninstall.sh), the installation folder /opt/cosmos, and the files
# install.sh wrote (Docker's apt source and key). By default it KEEPS:
#   - the data folder /var/lib/cosmos (settings, users, certificates): only
#     --delete-data (you then type the folder's name) or --yes-delete-data
#     (unattended) deletes it;
#   - the packages install.sh installed: only --remove-packages removes them,
#     and only those no other package of yours needs; Docker's packages stay
#     while any container exists (your apps), whatever the flags.
# Docker's images, containers and volumes are never touched.
#
# Options:
#   --yes               do not ask
#   --dry-run           show what would be removed; change nothing
#   --remove-packages   also remove the packages install.sh installed
#   --purge             with --remove-packages: their configuration files too
#   --delete-data       also delete the data folder (asks you to type its name)
#   --yes-delete-data   also delete the data folder, without asking
#   --help
#
# Exit codes: 0 done; 1 something could not be removed (said); 2 bad usage;
# 3 there is nothing install.sh installed here; 5 not root; 7 another run is going.

# Never from a pipe, and run again with bash only when $0 is this very
# script (see install.sh).
# COSMOS-FORK-UNINSTALL-SH
cosmos_self=""
case $0 in
*/*) cosmos_self=$0 ;;
*uninstall*.sh) cosmos_self=./$0 ;;
esac
if [ -z "$cosmos_self" ] || [ ! -f "$cosmos_self" ] || ! grep -q '^# COSMOS-FORK-UNINSTALL-SH$' "$cosmos_self" 2>/dev/null; then
	echo "uninstall.sh must be run from a file, not from a pipe:" >&2
	echo "    sudo bash /opt/cosmos/fork/install/uninstall.sh" >&2
	exit 2
fi
if [ -z "${BASH_VERSION:-}" ]; then
	exec bash "$cosmos_self" "$@"
fi

main() {
set -euo pipefail
shopt -s nullglob
umask 077
export LC_ALL=C LANG=C
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

INSTALL_DIR=/opt/cosmos
DATA_DIR=/var/lib/cosmos
STATE_DIR=/var/lib/cosmos-installer
STATE=$STATE_DIR/state
RECORD=$STATE_DIR/record
UNIT=CosmosCloud
LOCK=/run/cosmos-installer.lock
DOCKER_SET=" docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin docker-ce-rootless-extras "

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() {
	local code=$1
	shift
	printf 'ERROR: %s\n' "$*" >&2
	exit "$code"
}
usage() { sed -n '2,/^# 3 there is nothing/p' "$0" | sed 's/^# \{0,1\}//'; }

YES=0 DRY_RUN=0 REMOVE_PACKAGES=0 PURGE=0 DELETE_DATA=0 DELETE_DATA_YES=0
for a in "$@"; do
	case $a in
	--yes | -y) YES=1 ;;
	--dry-run) DRY_RUN=1 ;;
	--remove-packages) REMOVE_PACKAGES=1 ;;
	--purge) PURGE=1 ;;
	--delete-data) DELETE_DATA=1 ;;
	--yes-delete-data) DELETE_DATA_YES=1 ;;
	--help | -h) usage; exit 0 ;;
	*) die 2 "unknown option '$a' (see --help)" ;;
	esac
done
[ "$PURGE" = 0 ] || [ "$REMOVE_PACKAGES" = 1 ] || die 2 "--purge goes with --remove-packages"
[ "$(id -u)" = 0 ] || die 5 "run this as root: sudo bash $0"

# The script removes the folder it may be running from: run from a copy.
case $(readlink -f -- "$0") in
"$INSTALL_DIR"/*)
	copy=$(mktemp -d /var/tmp/cosmos-uninstall.XXXXXXXX)
	cp -- "$0" "$copy/uninstall.sh"
	exec bash "$copy/uninstall.sh" "$@"
	;;
esac
SELF_COPY=""
case $0 in /var/tmp/cosmos-uninstall.*/uninstall.sh) SELF_COPY=$(dirname -- "$0") ;; esac

exec 9>"$LOCK"
flock -n 9 || die 7 "another run of install.sh or uninstall.sh is going on"
WORK=$(mktemp -d /var/tmp/cosmos-uninstall-work.XXXXXXXX)
# shellcheck disable=SC2317,SC2329 # the EXIT trap
cleanup() {
	rm -rf -- "$WORK"
	if [ -n "$SELF_COPY" ]; then rm -rf -- "$SELF_COPY"; fi
	# the lock file stays (in /run): see install.sh
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

if [ ! -f "$RECORD" ] && [ ! -f "$STATE" ]; then
	if [ -e "$INSTALL_DIR" ] || [ -e /etc/systemd/system/$UNIT.service ]; then
		die 3 "the Cosmos in $INSTALL_DIR was not installed by install.sh (there is no $RECORD): this script removes only what install.sh installed. Nothing was changed."
	fi
	say "Nothing to remove: install.sh installed nothing here (or it was removed already)."
	[ ! -d "$DATA_DIR" ] || say "The data folder $DATA_DIR is there; it is never removed without --delete-data, and only while install.sh's record names it."
	exit 0
fi
[ -f "$RECORD" ] || : >"$WORK/empty"
RECORD_IN=$RECORD
[ -f "$RECORD" ] || RECORD_IN=$WORK/empty

recorded() { grep -qxF -- "$*" "$RECORD_IN"; }
lines_of() { sed -n "s/^$1 //p" "$RECORD_IN"; }
# the protection pieces the record names, from the allow-list only
recorded_pieces() {
	{ awk -F'\t' '$1 ~ /^piece / {sub(/^piece /, "", $1); print $1}' "$RECORD_IN"; lines_of piece-ran; } |
		grep -xE 'nftables|fail2ban|unattended-upgrades' | sort -u || true
}
ARCH=$(dpkg --print-architecture)
packages_now() { dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n' 2>/dev/null | awk '$1=="ii"{print $2}' | sed 's/:'"$ARCH"'$//' | sort -u; }

# The packages: those the record names (install.sh's own transactions and
# the pieces'), that are installed now. Nothing else.
{
	lines_of package
	awk -F'\t' '$1 ~ /^piece / && $2=="package" {print $3}' "$RECORD_IN"
} | grep -E '^[a-z0-9][a-z0-9.+-]{0,60}$' | sort -u | comm -12 - <(packages_now) >"$WORK/ours" || true
mapfile -t OURS <"$WORK/ours"

# Docker stays while any container exists. The daemon is asked only when it
# answers; when it cannot be asked, Docker stays (said), and the containers
# Docker keeps on disk count too.
KEEP_DOCKER=0 DOCKER_WHY=""
if command -v docker >/dev/null 2>&1; then
	if listed=$(timeout 30 docker ps -aq 2>/dev/null); then
		n=$(printf '%s' "$listed" | grep -c . || true)
		if [ "$n" -gt 0 ]; then KEEP_DOCKER=1; DOCKER_WHY="$n container(s) exist"; fi
	else
		KEEP_DOCKER=1
		DOCKER_WHY="the Docker daemon does not answer, so its containers cannot be counted"
	fi
fi
if [ "$KEEP_DOCKER" = 0 ] && [ -d /var/lib/docker/containers ] && [ -n "$(ls -A /var/lib/docker/containers 2>/dev/null)" ]; then
	KEEP_DOCKER=1
	DOCKER_WHY="/var/lib/docker/containers holds containers"
fi

data_named=0
if recorded "data $DATA_DIR" || [ -d "$DATA_DIR" ]; then data_named=1; fi

step "What will be removed"
if recorded "unit $UNIT.service"; then say "- the service $UNIT (stopped, disabled, /etc/systemd/system/$UNIT.service)"; fi
for p in $(recorded_pieces); do
	say "- the protection piece $p (its own uninstall.sh)"
done
if recorded "dir $INSTALL_DIR"; then say "- the installation folder $INSTALL_DIR"; fi
for f in $(lines_of file); do
	case $f in /etc/apt/keyrings/docker.asc | /etc/apt/sources.list.d/docker.sources) say "- the file $f" ;; esac
done
if [ "$DELETE_DATA" = 1 ] || [ "$DELETE_DATA_YES" = 1 ]; then
	say "- THE DATA FOLDER $DATA_DIR (settings, users, certificates: it cannot be undone)"
elif [ "$data_named" = 1 ]; then
	say "Kept: the data folder $DATA_DIR (add --delete-data to delete it)."
fi
if [ "${#OURS[@]}" -gt 0 ]; then
	if [ "$REMOVE_PACKAGES" = 1 ]; then
		say "- the packages install.sh installed, those nothing else needs: ${OURS[*]}"
		[ "$KEEP_DOCKER" = 0 ] || say "  except Docker's, which stay: $DOCKER_WHY."
	else
		say "Kept: the packages install.sh installed (${OURS[*]}); add --remove-packages to remove them."
	fi
fi
if [ "$DRY_RUN" = 1 ]; then say "Dry run: nothing was changed."; exit 0; fi

if [ "$YES" = 0 ]; then
	if ! { [ -r /dev/tty ] && [ -t 0 ]; }; then die 2 "no terminal to ask on: add --yes to remove without a question. Nothing was changed."; fi
	printf 'Remove this? [y/N] ' >/dev/tty
	read -r reply </dev/tty || reply=""
	case $reply in y | Y | yes | YES) ;; *) say "Nothing was changed."; exit 0 ;; esac
fi
if [ "$DELETE_DATA" = 1 ] && [ "$DELETE_DATA_YES" = 0 ]; then
	if ! { [ -r /dev/tty ] && [ -t 0 ]; }; then die 2 "--delete-data asks you to type the folder's name and there is no terminal: use --yes-delete-data. Nothing was changed."; fi
	printf 'To delete the data, type its folder (%s): ' "$DATA_DIR" >/dev/tty
	read -r reply </dev/tty || reply=""
	[ "$reply" = "$DATA_DIR" ] || { say "That is not $DATA_DIR: nothing was changed."; exit 0; }
	DELETE_DATA_YES=1
fi

FAILED=""
remove_line() { # drop a line from the record as soon as its item is gone
	[ -f "$RECORD" ] || return 0
	grep -vxF -- "$*" "$RECORD" >"$RECORD.new" || true
	mv -f "$RECORD.new" "$RECORD"
}

# 1. the service
if recorded "unit $UNIT.service"; then
	step "Stopping and removing the service $UNIT"
	systemctl disable --now "$UNIT.service" >/dev/null 2>&1 || true
	rm -f -- "/etc/systemd/system/$UNIT.service"
	systemctl daemon-reload
	systemctl reset-failed "$UNIT.service" >/dev/null 2>&1 || true
	remove_line "unit $UNIT.service"
fi

# 2. the protection pieces, from the release that installed them
for p in $(recorded_pieces); do
	case $p in nftables | fail2ban | unattended-upgrades) ;; *) continue ;; esac
	script="$INSTALL_DIR/fork/$p/uninstall.sh"
	[ -f "$script" ] || { warn "$script is missing: the piece $p was not removed"; FAILED="$FAILED $p"; continue; }
	step "Removing the protection piece $p"
	rc=0
	env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LANG=C.UTF-8 DEBIAN_FRONTEND=noninteractive sh "$script" --yes </dev/null || rc=$?
	if [ "$rc" = 0 ]; then
		remove_line "piece-ran $p"
		if [ -f "$RECORD" ]; then grep -v "^piece $p	" "$RECORD" >"$RECORD.new" || true; mv -f "$RECORD.new" "$RECORD"; fi
	else warn "$p: its uninstall.sh ended with $rc"; FAILED="$FAILED $p"; fi
done

# 3. the installation folder
if recorded "dir $INSTALL_DIR"; then
	step "Removing $INSTALL_DIR"
	rm -rf -- "$INSTALL_DIR"
	remove_line "dir $INSTALL_DIR"
fi
for f in /opt/.cosmos-install.*; do [ ! -d "$f" ] || rm -rf -- "$f"; done

# 4. the data
if [ "$DELETE_DATA_YES" = 1 ] && [ -d "$DATA_DIR" ]; then
	step "Deleting the data folder $DATA_DIR"
	rm -rf -- "$DATA_DIR"
	remove_line "data $DATA_DIR"
fi

# 5. the packages: all of them when nothing else needs any; otherwise,
# marked as installed automatically, apt itself says which of them nothing
# else needs, only those go, and the marks of the others are put back.
removed_docker=0
if [ "$REMOVE_PACKAGES" = 1 ] && [ "${#OURS[@]}" -gt 0 ]; then
	step "Removing the packages install.sh installed"
	candidates=()
	protected=()
	for p in "${OURS[@]}"; do
		if [ "$KEEP_DOCKER" = 1 ] && [[ $DOCKER_SET == *" $p "* ]]; then continue; fi
		# a package the system marks essential or protected (e2fsprogs on
		# Debian 13) is never removed: apt refuses, and the system needs it
		if dpkg-query -W -f='${Essential} ${Protected}' "$p" 2>/dev/null | grep -q yes; then protected+=("$p"); continue; fi
		candidates+=("$p")
	done
	# apt counts more packages as essential than dpkg marks (e2fsprogs on
	# Debian 13 is one): those it names in its warning stay too
	for _ in 1 2 3; do
		[ "${#candidates[@]}" -gt 0 ] || break
		essential=$(env DEBIAN_FRONTEND=noninteractive apt-get -s remove "${candidates[@]}" </dev/null 2>&1 |
			awk '/This should NOT be done/{on=1; next} on && /^  /{print; next} {on=0}' | sed 's/([^)]*)//g' |
			tr ' ' '\n' | grep -E '^[a-z0-9][a-z0-9.+:-]*$' | sed 's/:'"$ARCH"'$//' | sort -u || true)
		[ -n "$essential" ] || break
		mapfile -t protected < <(printf '%s\n' "${protected[@]}" "$essential" | sed '/^$/d' | sort -u)
		mapfile -t candidates < <(printf '%s\n' "${candidates[@]}" | sort -u | comm -23 - <(printf '%s\n' "$essential" | sort -u))
	done
	if [ "${#protected[@]}" -gt 0 ]; then
		say "Kept, because the system counts them as essential (they now belong to the system): ${protected[*]}"
		for p in "${protected[@]}"; do remove_line "package $p"; done
	fi
	sim_removed() { # packages: what apt would remove with them; fails when apt refuses
		local out
		out=$(env DEBIAN_FRONTEND=noninteractive apt-get -s remove "$@" </dev/null 2>/dev/null) || return 1
		printf '%s\n' "$out" | awk '$1=="Remv"{print $2}' | sed 's/:'"$ARCH"'$//' | sort -u
	}
	if [ "${#candidates[@]}" -gt 0 ] && extra=$(sim_removed "${candidates[@]}") &&
		[ -z "$(printf '%s\n' "$extra" | sed '/^$/d' | comm -23 - <(printf '%s\n' "${candidates[@]}" | sort -u))" ]; then
		# nothing else depends on any of them: all of them go
		printf '%s\n' "${candidates[@]}" | sort -u >"$WORK/removable"
		: >"$WORK/kept"
	elif [ "${#candidates[@]}" -gt 0 ]; then
		# some are needed by packages that stay: apt's own reckoning of what
		# nothing needs, on the candidates marked as installed automatically
		apt-mark showmanual 2>/dev/null | sed 's/:'"$ARCH"'$//' | sort -u | comm -12 - <(printf '%s\n' "${candidates[@]}" | sort -u) >"$WORK/manual" || true
		apt-mark auto "${candidates[@]}" >/dev/null
		env DEBIAN_FRONTEND=noninteractive apt-get -s autoremove --purge </dev/null 2>/dev/null | awk '$1=="Purg"||$1=="Remv"{print $2}' | sed 's/:'"$ARCH"'$//' | sort -u |
			comm -12 - <(printf '%s\n' "${candidates[@]}" | sort -u) >"$WORK/removable" || true
		comm -23 "$WORK/manual" "$WORK/removable" | xargs -r apt-mark manual >/dev/null
		comm -23 <(printf '%s\n' "${candidates[@]}" | sort -u) "$WORK/removable" >"$WORK/kept" || true
	fi
	if [ "${#candidates[@]}" -gt 0 ]; then
		mapfile -t removable <"$WORK/removable"
		if [ "${#removable[@]}" -gt 0 ]; then
			how=remove
			[ "$PURGE" = 0 ] || how="remove --purge"
			# shellcheck disable=SC2086 # how is one of two fixed words
			if env DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a apt-get -y -q -o DPkg::Lock::Timeout=600 $how "${removable[@]}" </dev/null >"$WORK/apt.log" 2>&1; then
				say "Removed: ${removable[*]}"
				for p in "${removable[@]}"; do
					remove_line "package $p"
					if [[ $DOCKER_SET == *" $p "* ]]; then removed_docker=1; fi
				done
			else
				tail -n 5 "$WORK/apt.log" >&2
				warn "apt could not remove ${removable[*]}"
				FAILED="$FAILED packages"
			fi
		fi
		if [ -s "$WORK/kept" ]; then
			say "Kept, because other packages need them or Docker stays: $(tr '\n' ' ' <"$WORK/kept")"
		fi
	fi
fi

# 6. the files install.sh wrote: Docker's source and key go with Docker
docker_left=0
if dpkg-query -W -f='${db:Status-Abbrev}' docker-ce 2>/dev/null | grep -q '^ii'; then docker_left=1; fi
for f in $(lines_of file); do
	case $f in
	/etc/apt/keyrings/docker.asc | /etc/apt/sources.list.d/docker.sources)
		if [ "$docker_left" = 1 ]; then say "Kept $f: Docker is still installed and is updated from there."; continue; fi ;;
	*) warn "the record names $f, which install.sh never writes: left alone"; continue ;;
	esac
	rm -f -- "$f"
	remove_line "file $f"
done
if recorded "dir /etc/apt/keyrings" && [ "$docker_left" = 0 ]; then
	if rmdir /etc/apt/keyrings 2>/dev/null; then remove_line "dir /etc/apt/keyrings"; fi
fi
if [ "$removed_docker" = 1 ] || ! [ -f /etc/apt/sources.list.d/docker.sources ]; then
	env DEBIAN_FRONTEND=noninteractive apt-get -q update >/dev/null 2>&1 || true
fi

# 7. the record itself, when nothing it names is left
left=$(grep -vE '^(package |piece )' "$RECORD" 2>/dev/null | grep -v "^data $DATA_DIR$" || true)
pk=$(comm -12 <(lines_of package | sort -u) <(packages_now) || true)
if [ -z "$left" ] && [ -z "$pk" ] && [ -z "$FAILED" ]; then
	rm -rf -- "$STATE_DIR"
	step "Done"
else
	rm -f -- "$STATE" "$STATE_DIR/packages-before"
	step "Done, with something left"
	[ -z "$pk" ] || say "Packages install.sh installed are still there (run again with --remove-packages): $(printf '%s' "$pk" | tr '\n' ' ')"
	say "What is left is listed in $RECORD."
fi
if [ -d "$DATA_DIR" ]; then
	say "The data folder $DATA_DIR is kept. install.sh --reuse-data installs Cosmos over it again."
fi
[ -z "$FAILED" ] || { warn "not removed:$FAILED"; exit 1; }
exit 0
}

main "$@"
