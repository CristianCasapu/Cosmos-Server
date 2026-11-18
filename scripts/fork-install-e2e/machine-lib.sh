#!/bin/bash
# shellcheck disable=SC2012,SC2013
# Fork addition: what the install scripts' test bed runs INSIDE a test
# machine (sourced by the steps of scripts/fork-install-e2e.sh). Never run
# on a real server: it changes the machine freely.
set -euo pipefail
export LC_ALL=C

OUT=/e2e/out
mkdir -p "$OUT"

# What the comparisons leave out: kernel and run-time folders, logs, apt's
# caches and lists (the scripts run apt-get update), /var/backups (the
# system's own daily dpkg-db-backup timer writes there at any time), dpkg's
# own database
# (the package list is compared on its own), systemd's and Docker's state,
# and /etc/ssl, where the test CA's links (made for the test bed, owned by
# no package) keep the folder after ca-certificates is purged.
SKIP='^/(proc|sys|dev|run|tmp|e2e|var/log|var/cache|var/lib/apt/lists|var/backups|var/lib/dpkg|var/lib/systemd|var/lib/docker|var/lib/containerd|usr/local/share/ca-certificates|etc/ssl|root/\.cache)(/|$)'

snap() { # name
	local n=$1
	dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n' | awk '$1=="ii"{print $2}' | sort >"$OUT/$n.pkgs"
	find / -xdev -printf '%p %y %m\n' 2>/dev/null | awk -v re="$SKIP" '$1 !~ re' | sort >"$OUT/$n.files" || true
	find /etc /opt /usr/local /var/lib -xdev -type f 2>/dev/null | grep -vE "$SKIP" | sort | xargs -r md5sum 2>/dev/null >"$OUT/$n.sums" || true
	{ systemctl is-enabled CosmosCloud 2>/dev/null || true; systemctl is-active CosmosCloud 2>/dev/null || true; } >"$OUT/$n.unit"
}

# differ <a> <b>: prints the differences; status 1 when there are any
differ() {
	local a=$1 b=$2 rc=0 kind
	for kind in pkgs files sums unit; do
		if ! cmp -s "$OUT/$a.$kind" "$OUT/$b.$kind"; then
			rc=1
			echo "--- $kind differ ($a -> $b):"
			diff "$OUT/$a.$kind" "$OUT/$b.$kind" | grep '^[<>]' | head -n 40 || true
		fi
	done
	return $rc
}

same() { # a b: takes the snapshot b now, compares; fails with the differences
	snap "$2"
	if differ "$1" "$2"; then echo "unchanged: $1 = $2"; else echo "CHANGED: $1 != $2"; return 1; fi
}

expect_exit() { # code, command...
	local want=$1 rc=0
	shift
	"$@" || rc=$?
	if [ "$rc" != "$want" ]; then
		echo "FAIL: '$*' ended with $rc, expected $want"
		return 1
	fi
	echo "ok: '$*' ended with $rc as expected"
}

# After the wizard the server answers only its hostname: PANEL_HOST.
PANEL_HOST=127.0.0.1
panel_code() { curl -s -o /dev/null -m 5 --resolve "srv-e2e.local:${1:-80}:127.0.0.1" -w '%{http_code}' "http://$PANEL_HOST:${1:-80}/cosmos-ui/" || true; }

wait_panel() { # port, seconds
	local i
	for i in $(seq 1 "${2:-180}"); do
		[ "$(panel_code "$1")" = 200 ] && return 0
		sleep 1
	done
	echo "FAIL: the panel did not answer on port $1"
	journalctl -u CosmosCloud -n 40 --no-pager || true
	return 1
}

installed_ok() { # port
	systemctl is-active --quiet CosmosCloud || { echo "FAIL: CosmosCloud is not active"; return 1; }
	systemctl is-enabled --quiet CosmosCloud || { echo "FAIL: CosmosCloud is not enabled"; return 1; }
	[ "$(panel_code "${1:-80}")" = 200 ] || { echo "FAIL: the panel does not answer"; return 1; }
	echo "ok: the unit is active and enabled, the panel answers"
}

# The record names exactly what was added: its packages are the packages
# added since the snapshot, and every file, dir and unit it names exists.
record_matches() { # snapshot before
	local before=$1 r=/var/lib/cosmos-installer/record
	[ -f "$r" ] || { echo "FAIL: no record"; return 1; }
	dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n' | awk '$1=="ii"{print $2}' | sort >"$OUT/now.pkgs"
	comm -13 "$OUT/$before.pkgs" "$OUT/now.pkgs" | sed 's/:amd64$//;s/:arm64$//' | sort -u >"$OUT/added.pkgs"
	sed -n 's/^package //p' "$r" | sort -u >"$OUT/recorded.pkgs"
	if ! cmp -s "$OUT/added.pkgs" "$OUT/recorded.pkgs"; then
		echo "FAIL: the record's packages are not those added:"
		diff "$OUT/added.pkgs" "$OUT/recorded.pkgs" | head -n 20
		return 1
	fi
	local p
	for p in $(sed -n 's/^file //p;s/^dir //p;s/^data //p' "$r"); do [ -e "$p" ] || { echo "FAIL: the record names $p, which is missing"; return 1; }; done
	if grep -q '^unit CosmosCloud.service$' "$r"; then [ -f /etc/systemd/system/CosmosCloud.service ] || { echo "FAIL: unit file missing"; return 1; }; fi
	echo "ok: the record lists the $(wc -l <"$OUT/recorded.pkgs") packages added and what was written"
}

# The first-run wizard over the API, then a session with sudo, as the
# browser would (no browser headers: the same-origin check lets it through).
JAR=$OUT/cookies
SETUP_CODE=${SETUP_CODE:-}
api() { # method path json (with the setup code when one is set)
	curl -s -m 30 -c "$JAR" -b "$JAR" -H "X-Cosmos-Setup-Code: $SETUP_CODE" --resolve "srv-e2e.local:${PORT:-80}:127.0.0.1" -H 'Content-Type: application/json' -X "$1" --data "${3:-}" -o "$OUT/api.out" -w '%{http_code}' "http://$PANEL_HOST:${PORT:-80}/cosmos/api/$2" || true
}
# wizard_and_login [code]: the code is the one install.sh printed, else the file's
wizard_and_login() {
	local code
	SETUP_CODE=${1:-$(cat /var/lib/cosmos/setup-code)}
	code=$(api POST newInstall '{"step":"-1"}'); echo "wizard -1 (clean install, the default): $code"
	code=$(api POST newInstall '{"step":"2"}'); echo "wizard 2: $code"
	code=$(api POST newInstall '{"step":"3","httpsCertificateMode":"DISABLED","hostname":"srv-e2e.local"}'); echo "wizard 3: $code"
	code=$(api POST newInstall '{"step":"4","nickname":"admin","password":"E2e-Test-Passw0rd!x","email":"admin@example.org"}'); echo "wizard 4: $code"
	code=$(api POST newInstall '{"step":"5"}'); echo "wizard 5: $code (the server restarts)"
	PANEL_HOST=srv-e2e.local
	sleep 5
	wait_panel "${PORT:-80}" 120
	local i
	for i in $(seq 1 30); do
		code=$(api POST login '{"nickname":"admin","password":"E2e-Test-Passw0rd!x"}')
		[ "$code" = 200 ] && break
		sleep 2
	done
	echo "login: $code"
	[ "$code" = 200 ] || { cat "$OUT/api.out"; return 1; }
	code=$(api POST sudo '{"password":"E2e-Test-Passw0rd!x"}'); echo "sudo: $code"
	[ "$code" = 200 ]
}

version_installed() { sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' /opt/cosmos/meta.json | head -n 1; }

# update_to <version>: the server's own updater, through the panel's
# command, then the launcher's trial until the new version proved itself.
update_to() {
	local want=$1 code i
	code=$(api POST force-server-update '')
	echo "force-server-update: $code $(head -c 300 "$OUT/api.out")"
	for i in $(seq 1 240); do
		if [ "$(version_installed)" = "$want" ] && [ ! -e /opt/cosmos/cosmos-update.trial ] && [ "$(panel_code "${PORT:-80}")" = 200 ]; then
			echo "ok: $want is installed and proved itself after $((i * 5)) s"
			return 0
		fi
		sleep 5
	done
	echo "FAIL: $want did not install and prove itself (installed: $(version_installed))"
	ls -la /opt/cosmos | head -n 40
	journalctl -u CosmosCloud -n 60 --no-pager || true
	return 1
}

# rolls_back_from <bad version> <good version>
rolls_back_from() {
	local bad=$1 good=$2 code i seen=0
	code=$(api POST force-server-update '')
	echo "force-server-update: $code"
	for i in $(seq 1 240); do
		if [ "$(version_installed)" = "$bad" ]; then seen=1; fi
		if [ "$seen" = 1 ] && [ "$(version_installed)" = "$good" ] && [ "$(panel_code "${PORT:-80}")" = 200 ]; then
			echo "ok: $bad was installed, did not start, and the launcher went back to $good after $((i * 5)) s"
			return 0
		fi
		sleep 5
	done
	echo "FAIL: no rollback from $bad to $good (seen $bad: $seen; installed: $(version_installed))"
	journalctl -u CosmosCloud -n 80 --no-pager || true
	return 1
}

fail() { echo "FAIL: $*"; exit 1; }
# check <message> <command...>: the command must succeed
check() {
	local msg=$1
	shift
	if "$@"; then echo "ok: $msg"; else fail "$msg"; fi
}

# packages_back <before> <after>: the package list is the one of before, but
# for packages apt counts as essential and never removes (e2fsprogs on
# Debian 13: absent from the slim test image only, present on every real one)
packages_back() {
	local extra
	local left essential
	left=$(comm -13 "$OUT/$1.pkgs" "$OUT/$2.pkgs" | sed 's/:amd64$//;s/:arm64$//')
	essential=""
	if [ -n "$left" ]; then
		# shellcheck disable=SC2086
		essential=$(apt-get -s remove $left </dev/null 2>&1 | awk '/This should NOT be done/{on=1; next} on && /^  /{print; next} {on=0}' | sed 's/([^)]*)//g' | tr ' ' '\n' | grep -E '^[a-z0-9]' | sort -u || true)
	fi
	extra=$(printf '%s\n' "$left" | sed '/^$/d' | sort -u | comm -23 - <(printf '%s\n' "$essential" | sort -u))
	if ! comm -23 "$OUT/$1.pkgs" "$OUT/$2.pkgs" | grep -q . && [ -z "$extra" ]; then
		echo "ok: the package list is the one of before$(comm -13 "$OUT/$1.pkgs" "$OUT/$2.pkgs" | tr '\n' ' ' | sed 's/^./ (but for the protected: &/; s/ $/)/')"
	else
		echo "FAIL: the package list is not the one of before:"
		diff "$OUT/$1.pkgs" "$OUT/$2.pkgs" | grep '^[<>]' | head -n 30
		exit 1
	fi
}

pkg_installed() { dpkg-query -W -f='${db:Status-Abbrev}' "$1" 2>/dev/null | grep -q '^ii'; }
