#!/bin/bash
# shellcheck disable=SC2012,SC2013
# Fork addition: what the install scripts' test bed runs INSIDE a test
# machine (sourced by the steps of scripts/fork-install-e2e.sh). Never run
# on a real server: it changes the machine freely.
#
# Its first act: check that this is a test machine of the running test bed.
# Every machine is made with /.cosmos-e2e-machine (mounted read-only, holding
# the run's random id) and every step is given E2E_RUN_ID; anywhere else (the
# host, a container of anything else) this stops before anything runs.
if ! { [ -f /.cosmos-e2e-machine ] && [ -n "${E2E_RUN_ID:-}" ] && [ "$(cat /.cosmos-e2e-machine 2>/dev/null)" = "$E2E_RUN_ID" ]; }; then
	echo "REFUSED: this is not a test machine of this run (no /.cosmos-e2e-machine with the run's id): nothing is run" >&2
	exit 99
fi
set -euo pipefail
export LC_ALL=C

# release A's install.sh (the steps call it by this name)
install_a() { bash "/e2e/releases/v$VA/install.sh" "$@"; }

# the machine's start, for start_machine on the host
machine_state() { systemctl is-system-running 2>/dev/null || true; }
trust_test_ca() { if command -v update-ca-certificates >/dev/null; then update-ca-certificates >/dev/null 2>&1 || true; fi; }

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

# ---- Upstream's Cosmos and the migration (migrate.sh) ------------------------
migrate_a() { bash "/e2e/releases/v$VA/migrate.sh" "$@"; }
# the test mode: the repository's unstamped script with the test key, as
# migrate.sh, which may make a step fail on purpose (COSMOS_INSTALL_TEST_FAIL_AT)
migrate_test() {
	COSMOS_INSTALL_TEST_KEYS=$(cat /e2e/bin/test.pub) COSMOS_INSTALL_TEST_VERSION=$VA \
		COSMOS_INSTALL_TEST_REPOSITORY=example-owner/cosmos-fork COSMOS_INSTALL_TEST_BASE=0.24.0 \
		COSMOS_INSTALL_TEST_ROLE=migrate bash /e2e/bin/install-unstamped.sh --from "/e2e/releases/v$VA" "$@"
}
E2E_PASSWORD='E2e-Test-Passw0rd!x'

# upstream_install <version>: upstream's release zip as upstream's get.sh puts
# it in place (unpacked to /opt/cosmos, "cosmos service install" with the two
# ports, the unit started), its first-run wizard over the API, then a web app
# in a container and a URL of Cosmos to it
upstream_install() {
	local v=$1 d
	d=$(mktemp -d)
	unzip -q "/e2e/bin/upstream/cosmos-cloud-$v-amd64.zip" -d "$d"
	mkdir -p /opt/cosmos
	cp -a "$d/cosmos-cloud-$v/." /opt/cosmos/
	rm -rf "$d"
	chmod +x /opt/cosmos/cosmos /opt/cosmos/cosmos-launcher /opt/cosmos/start.sh
	(cd /opt/cosmos && env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin COSMOS_HTTP_PORT=80 COSMOS_HTTPS_PORT=443 ./cosmos service install >"$OUT/service-install.txt" 2>&1) || true # (it ends with 1 on success)
	check "upstream's own service install wrote the unit" test -f /etc/systemd/system/CosmosCloud.service
	systemctl daemon-reload
	systemctl start CosmosCloud
	wait_panel 80
	check "upstream's Cosmos $v runs" grep -q "\"version\": \"$v\"" /opt/cosmos/meta.json
	# upstream asks no setup code: the header the helper sends is ignored
	wizard_and_login upstream-asks-none
	check "upstream made no setup code" test ! -e /var/lib/cosmos/setup-code
	# the app: a container answering on 8099, and Cosmos's URL to it
	tar -cf - --files-from /dev/null | docker import - e2e-empty:1 >/dev/null
	docker run -d --name e2e-app -p 8099:8099 -v /e2e/bin:/b:ro e2e-empty:1 /b/helper serve 8099 >/dev/null
	systemctl stop CosmosCloud
	/e2e/bin/helper add-route /var/lib/cosmos/cosmos.config.json e2e-app app.srv-e2e.local http://127.0.0.1:8099
	systemctl start CosmosCloud
	wait_panel 80
	check "the app answers through upstream's Cosmos" sh -c "curl -s -m 10 -H 'Host: app.srv-e2e.local' http://127.0.0.1/ | grep -qx e2e-app"
}

# the snapshots of both folders that the unit's own hooks take when it stops
# and before it starts (the folders are still then): a rollback is proven
# when the folders at upstream's restart are those at the migration's stop
cosmos_snap() { # event
	local f
	f=$OUT/hook-$(date +%s%N)-$1
	{
		for d in /opt/cosmos /var/lib/cosmos; do
			echo "== $d"
			[ -d "$d" ] || continue
			(cd "$d" && find . -xdev -printf '%y %m %U:%G %p -> %l\n' | LC_ALL=C sort && find . -xdev -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum)
		done
		echo "== unit"
		sha256sum /etc/systemd/system/CosmosCloud.service 2>/dev/null || true
	} >"$f"
}
add_snapshot_hooks() {
	mkdir -p /etc/systemd/system/CosmosCloud.service.d
	cat >/etc/systemd/system/CosmosCloud.service.d/e2e-snapshots.conf <<'HOOKS'
[Service]
ExecStopPost=/bin/bash -c 'export E2E_RUN_ID="$(cat /.cosmos-e2e-machine)"; . /e2e/bin/machine-lib.sh; cosmos_snap stop'
ExecStartPre=/bin/bash -c 'export E2E_RUN_ID="$(cat /.cosmos-e2e-machine)"; . /e2e/bin/machine-lib.sh; cosmos_snap start'
HOOKS
	systemctl daemon-reload
	rm -f "$OUT"/hook-*
}
# rolled_back_exactly: the folders and the unit at upstream's last start are
# those at the first stop since the hooks were cleared, byte for byte
rolled_back_exactly() {
	local stop start
	stop=$(ls "$OUT"/hook-*-stop 2>/dev/null | head -n 1)
	start=$(ls "$OUT"/hook-*-start 2>/dev/null | tail -n 1)
	check "the unit's hooks saw the stop and the restart" sh -c "test -n '$stop' && test -n '$start' && test '$start' '>' '$stop'"
	if ! cmp -s "$stop" "$start"; then
		echo "FAIL: the folders or the unit differ from those at the stop:"
		diff "$stop" "$start" | head -n 20
		return 1
	fi
	echo "ok: /opt/cosmos, /var/lib/cosmos and the unit are byte for byte those of before ($(grep -c '^[0-9a-f]\{64\}  ' "$stop") files)"
	rm -f "$OUT"/hook-*
}
# upstream_back <version>: upstream's Cosmos runs again as before, nothing of the migration left
upstream_back() {
	PANEL_HOST=srv-e2e.local
	check "upstream's Cosmos $1 is in /opt/cosmos" grep -q "\"version\": \"$1\"" /opt/cosmos/meta.json
	check "it is active and enabled as before" sh -c "systemctl is-active --quiet CosmosCloud && [ \"\$(systemctl is-enabled CosmosCloud)\" = \"\$(cat $OUT/was-enabled)\" ]"
	wait_panel 80
	check "nothing of the migration is left" sh -c "test ! -e /opt/.cosmos-upstream-before-fork && ! ls -d /var/backups/cosmos-before-fork-* /opt/.cosmos-fork-tried.* /var/lib/.cosmos-migrate-replaced.* /opt/.cosmos-migrate-replaced.* >/dev/null 2>&1 && test ! -e /var/lib/cosmos-installer/state && test ! -e /var/lib/cosmos-installer/migration"
	local code
	rm -f "$JAR"
	code=$(api POST login "{\"nickname\":\"admin\",\"password\":\"$E2E_PASSWORD\"}")
	check "the administrator signs in (code $code)" test "$code" = 200
}
# upstream_unchanged <name>: what a refusal must leave as it was (the
# folder, the unit, the service, the packages; nothing of a migration)
upstream_state() { # name
	{
		(cd /opt/cosmos && find . -printf '%y %m %p\n' | sort)
		sha256sum /etc/systemd/system/CosmosCloud.service /var/lib/cosmos/cosmos.config.json
		systemctl is-enabled CosmosCloud || true
		systemctl is-active CosmosCloud || true
		dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n' | awk '$1=="ii"{print $2}' | sort
		ls -d /var/lib/cosmos-installer /var/backups/cosmos-before-fork-* /opt/.cosmos-* 2>/dev/null || true
	} >"$OUT/upstream-$1"
}
upstream_unchanged() { # name of the state taken before
	upstream_state "$1-after"
	if cmp -s "$OUT/upstream-$1" "$OUT/upstream-$1-after"; then echo "unchanged: $1"; else echo "CHANGED: $1"; diff "$OUT/upstream-$1" "$OUT/upstream-$1-after" | head -n 10; return 1; fi
}
# migrated_ok <version from>: the fork runs on upstream's data
migrated_ok() {
	PANEL_HOST=srv-e2e.local
	check "the fork $VA is in /opt/cosmos" grep -q "\"version\": \"$VA\"" /opt/cosmos/meta.json
	check "start.sh is upstream's (kept)" cmp -s /opt/cosmos/start.sh "$OUT/upstream-start.sh"
	check "the unit is the one of before" sh -c "sha256sum /etc/systemd/system/CosmosCloud.service | cut -d' ' -f1 | grep -qx \$(cat $OUT/unit.sha)"
	check "it is active and enabled as before" sh -c "systemctl is-active --quiet CosmosCloud && [ \"\$(systemctl is-enabled CosmosCloud)\" = \"\$(cat $OUT/was-enabled)\" ]"
	wait_panel 80
	check "no first-run setup code: no file" test ! -e /var/lib/cosmos/setup-code
	check "and none in the journal" sh -c "! journalctl -u CosmosCloud --no-pager | grep -q 'SETUP CODE'"
	local code
	# a session upstream made is not one the fork accepts: it is sent to the
	# sign-in page (everyone signs in once more after the migration)
	code=$(api GET me "")
	echo "with upstream's session cookie: $code"
	rm -f "$JAR"
	code=$(api POST login "{\"nickname\":\"admin\",\"password\":\"$E2E_PASSWORD\"}")
	check "the administrator of upstream signs in with the old password (code $code)" test "$code" = 200
	check "the app's URL still answers through Cosmos" sh -c "curl -s -m 10 -H 'Host: app.srv-e2e.local' http://127.0.0.1/ | grep -qx e2e-app"
	check "the container is the same one, still running" sh -c "[ \"\$(docker inspect -f '{{.Id}} {{.State.Status}}' e2e-app)\" = \"\$(cat $OUT/app.id) running\" ]"
	check "the settings kept the hostname and the URL" sh -c "grep -q '\"Hostname\": *\"srv-e2e.local\"' /var/lib/cosmos/cosmos.config.json && grep -q 'app.srv-e2e.local' /var/lib/cosmos/cosmos.config.json"
	local b
	b=$(sed -n 's/^backup //p' /var/lib/cosmos-installer/record)
	check "the record says migrated from $1, and where the backup is" sh -c "grep -qx 'migrated-from $1' /var/lib/cosmos-installer/record && test -n '$b'"
	check "the backup is private and complete" sh -c "[ \"\$(stat -c %a '$b')\" = 700 ] && test -f '$b/GOING-BACK.txt' && test -d '$b/opt-cosmos' && test -d '$b/var-lib-cosmos' && test -f '$b/MANIFEST.var-lib-cosmos'"
	check "the backup holds upstream's Cosmos $1" grep -q "\"version\": \"$1\"" "$b/opt-cosmos/meta.json"
	check "upstream's folder is not left aside" test ! -e /opt/.cosmos-upstream-before-fork
}
# note_upstream: what migrated_ok and upstream_back compare with
note_upstream() {
	cp /opt/cosmos/start.sh "$OUT/upstream-start.sh"
	sha256sum /etc/systemd/system/CosmosCloud.service | cut -d' ' -f1 >"$OUT/unit.sha"
	systemctl is-enabled CosmosCloud >"$OUT/was-enabled" || true
	docker inspect -f '{{.Id}}' e2e-app >"$OUT/app.id"
}
