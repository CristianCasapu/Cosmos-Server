# shellcheck shell=bash
# shellcheck disable=SC2154,SC2016,SC2086,SC2046,SC2034,SC2164,SC2024 # VO VA VB VC VR and the loops' values come from the host (M passes them by name); the steps were written as strings for bash -c and are kept as they ran
# Fork addition: what the test bed (scripts/fork-install-e2e.sh) runs INSIDE
# its test machines, one function per step of a case (m_<case>_<n>). The
# host-side script holds no command that installs, removes or changes a
# system: it only names these functions (M <machine> <function> <variables>).
# This file and machine-lib.sh run nothing before the check that the machine
# is one of this run's test machines (see machine-lib.sh): sourced anywhere
# else, they stop.
if ! { [ -f /.cosmos-e2e-machine ] && [ -n "${E2E_RUN_ID:-}" ] && [ "$(cat /.cosmos-e2e-machine 2>/dev/null)" = "$E2E_RUN_ID" ]; }; then
	echo "REFUSED: this is not a test machine of this run (no /.cosmos-e2e-machine with the run's id): nothing is run" >&2
	exit 99
fi

m_fresh_1() {
	snap before; install_a --yes --print-setup-code </dev/null | tee /e2e/out/install.txt; installed_ok 80; record_matches before
		check "the state says completed" grep -q "^completed=yes" /var/lib/cosmos-installer/state
		# the one-time setup code: printed, in a root-only file, asked by every setup route
		printed=$(sed -n "s/^ *Setup code: //p" /e2e/out/install.txt)
		check "install.sh printed the setup code" test -n "$printed"
		check "it is the code of the file" test "$printed" = "$(cat /var/lib/cosmos/setup-code)"
		check "the file is root's, 0600" test "$(stat -c %a:%U /var/lib/cosmos/setup-code)" = 600:root
		check "a local user cannot read it" sh -c "! setpriv --reuid=65534 --regid=65534 --clear-groups cat /var/lib/cosmos/setup-code 2>/dev/null"
		check "the journal has it" sh -c "journalctl -u CosmosCloud --no-pager | grep -A1 \"SETUP CODE\" | grep -qF \"$printed\""
		check "cosmos.log does not" sh -c "! grep -qF \"$printed\" /var/lib/cosmos/cosmos.log"
		for route in setup setup-join newInstall; do
			code=$(curl -s -o /e2e/out/r.json -w "%{http_code}" -X POST -H "Content-Type: application/json" --data "{\"step\":\"4\",\"nickname\":\"x\",\"password\":\"Passw0rd-x!yz\"}" http://127.0.0.1/cosmos/api/$route)
			check "POST /cosmos/api/$route without the code is refused" sh -c "test $code = 403 && grep -q SC001 /e2e/out/r.json"
			code=$(setpriv --reuid=65534 --regid=65534 --clear-groups curl -s -o /e2e/out/r.json -w "%{http_code}" -X POST -H "Content-Type: application/json" -H "X-Cosmos-Setup-Code: AAAA-AAAA-AAAA-AAAA-AAAA" --data "{}" http://127.0.0.1/cosmos/api/$route)
			check "POST /cosmos/api/$route by a local user with a wrong code is refused" sh -c "test $code = 403 && grep -q SC002 /e2e/out/r.json"
		done
		check "the status stays open for the first page" test "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1/cosmos/api/status)" = 200
		# placement not given (unknown): the wizard answers on 127.0.0.1 only, through a tunnel
		check "the loopback marker is root's, 0600" test "$(stat -c %a:%U /var/lib/cosmos/setup-loopback)" = 600:root
		check "the wizard does not answer on the network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1)/cosmos-ui/" || true)" = 000
		check "install.sh printed the tunnel" grep -q "ssh -L 8080:127.0.0.1:80 " /e2e/out/install.txt
		check "and why" grep -q "placement is unknown, not home" /e2e/out/install.txt
		check "the server says where the wizard answers" sh -c "journalctl -u CosmosCloud --no-pager | grep -q \"Setup: the first-run wizard answers on 127.0.0.1 only\""
		check "port 80 listens on 127.0.0.1 only" sh -c "awk '\$4 == \"0A\" && \$2 ~ /:0050\$/ {print \$2}' /proc/net/tcp /proc/net/tcp6 | sort -u | grep -vx 0100007F:0050 | grep -c . | grep -qx 0"
		echo "$printed" >/e2e/out/printed-code
		check "nobody else can read the data folder (it is $(stat -c %a /var/lib/cosmos): 700 when made, 711 once Cosmos ran)" sh -c "stat -c %a /var/lib/cosmos | grep -qE \"^7[01][01]\$\""
		mkdir -p /root/up && cp /opt/cosmos/cosmos /root/up/ && printf "#!/bin/sh\n" >/root/up/start.sh && chmod 755 /root/up/start.sh
		cp /etc/systemd/system/CosmosCloud.service /e2e/out/ours.service
		(env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin COSMOS_HTTP_PORT=80 COSMOS_HTTPS_PORT=443 /root/up/cosmos service install >/dev/null 2>&1 || true)
		sed "s|/root/up|/opt/cosmos|g" /etc/systemd/system/CosmosCloud.service >/e2e/out/upstream.service
		cp /e2e/out/ours.service /etc/systemd/system/CosmosCloud.service; rm -rf /root/up; systemctl daemon-reload
		check "the unit is byte for byte what cosmos service install writes" cmp /e2e/out/ours.service /e2e/out/upstream.service
		snap installed
		expect_exit 10 install_a --yes </dev/null; same installed after-second
		out=$(install_a --yes </dev/null 2>&1 || true); echo "$out" | grep -q "from the" || fail "the refusal does not say updates come from the panel"
		systemctl stop CosmosCloud; snap stopped
		expect_exit 10 install_a --yes </dev/null; same stopped after-stopped
		systemctl disable CosmosCloud 2>/dev/null; snap disabled
		expect_exit 10 install_a --yes </dev/null; same disabled after-disabled
		systemctl enable --now CosmosCloud 2>/dev/null; wait_panel 80
		mv /var/lib/cosmos-installer /root/ci-aside; snap byhand
		expect_exit 10 install_a --yes </dev/null; same byhand after-byhand
		mv /etc/systemd/system/CosmosCloud.service /root/unit-aside; snap nounit
		expect_exit 10 install_a --yes </dev/null; same nounit after-nounit
		mv /root/unit-aside /etc/systemd/system/CosmosCloud.service; mv /opt/cosmos /root/opt-aside; snap nofolder
		expect_exit 12 install_a --yes </dev/null; same nofolder after-nofolder
		mv /root/opt-aside /opt/cosmos; mv /root/ci-aside /var/lib/cosmos-installer; systemctl daemon-reload; wait_panel 80
		RCLONE_CONFIG=/nonexistent /opt/cosmos/cosmos fork-detect --no-outside --timeout 5 >/e2e/out/detect.json 2>/dev/null
		check "fork-detect printed its JSON" grep -q "\"schema\": 1" /e2e/out/detect.json
		check "fork-detect asked no one outside" grep -q "\"asked\": false" /e2e/out/detect.json
}

m_fresh_2() { # igd from the host
	old=$(ip -4 route show default | head -n1); ip route replace default via $igd
		RCLONE_CONFIG=/nonexistent timeout 15 /opt/cosmos/cosmos fork-detect --timeout 10 >/e2e/out/detect-igd.json 2>/dev/null || true
		ip route replace $old
		cat /e2e/out/detect-igd.json
		check "fork-detect: the router answers" grep -q "\"answers\": true" /e2e/out/detect-igd.json
		check "fork-detect: its address" grep -q "\"externalIP\": \"203.0.113.7\"" /e2e/out/detect-igd.json
		check "fork-detect: from the router" grep -q "\"publicIPSource\": \"router\"" /e2e/out/detect-igd.json
		check "fork-detect: not a shared address" grep -q "\"sharedAddress\": false" /e2e/out/detect-igd.json
		check "fork-detect: no outside service asked" grep -q "\"asked\": false" /e2e/out/detect-igd.json
}

m_fresh_3() { # VB from the host
	wizard_and_login "$(cat /e2e/out/printed-code)" && update_to $VB
		check "the setup code file is gone once set up" test ! -e /var/lib/cosmos/setup-code
		check "so is the loopback marker" test ! -e /var/lib/cosmos/setup-loopback
		check "set up, the server answers on the network" sh -c "test \"\$(curl -s -o /dev/null -m 5 -w %{http_code} http://\$(hostname -I | cut -d\" \" -f1)/cosmos-ui/)\" != 000"
		code=$(curl -s -o /e2e/out/r.json -w "%{http_code}" --resolve srv-e2e.local:80:127.0.0.1 -X POST -H "Content-Type: application/json" -H "X-Cosmos-Setup-Code: $(cat /e2e/out/printed-code)" --data "{\"step\":\"4\"}" http://srv-e2e.local/cosmos/api/newInstall)
		check "the wizard is closed once set up, code or not" test "$code" = 403
}

m_fresh_4() { # VB VC from the host
	PANEL_HOST=srv-e2e.local; api POST login "{\"nickname\":\"admin\",\"password\":\"E2e-Test-Passw0rd!x\"}" >/dev/null; api POST sudo "{\"password\":\"E2e-Test-Passw0rd!x\"}" >/dev/null; rolls_back_from $VC $VB
}

m_fresh_5() {
	bash /opt/cosmos/fork/install/uninstall.sh --yes </dev/null
		check "the folder is gone" test ! -e /opt/cosmos
		check "the data is kept" test -d /var/lib/cosmos
		check "the unit is gone" test ! -e /etc/systemd/system/CosmosCloud.service
		expect_exit 13 install_a --yes </dev/null
}

m_fresh_6() { # VA from the host
	PANEL_HOST=srv-e2e.local; check "no network" sh -c "! getent hosts github.com"
		expect_exit 0 install_a --yes --reuse-data --from /e2e/releases/v$VA/cosmos-cloud-$VA-amd64.zip </dev/null; installed_ok 80
}

m_fresh_7() {
	bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null
		check "everything removed" test ! -e /var/lib/cosmos-installer -a ! -e /var/lib/cosmos -a ! -e /opt/cosmos
		snap removed; differ before removed > /e2e/out/left.txt || true
		packages_back before removed
		echo "what is left:"; grep "^[<>]" /e2e/out/left.txt | head -n 80 || true
}

m_refusals_1() { # VA from the host
	snap before
		mkdir -p /opt/cosmos && printf "{\n  \"version\": \"0.24.0\"\n}\n" >/opt/cosmos/meta.json && printf "#!/bin/sh\n" >/opt/cosmos/cosmos && chmod 755 /opt/cosmos/cosmos
		printf "[Service]\nWorkingDirectory=/opt/cosmos\nExecStart=/opt/cosmos/start.sh\n" >/etc/systemd/system/CosmosCloud.service
		snap upstream; expect_exit 11 install_a --yes </dev/null; same upstream after-upstream
		out=$(install_a --yes </dev/null 2>&1 || true); echo "$out" | grep -q migrate.sh || fail "upstream: no word of migrate.sh"
		rm -rf /opt/cosmos; mkdir -p /opt/cosmos && printf "{\n  \"version\": \"0.24.0-fork.900\"\n}\n" >/opt/cosmos/meta.json && : >/opt/cosmos/FORK-LICENCE.md
		snap byhand; expect_exit 10 install_a --yes </dev/null; same byhand after-byhand
		rm -f /opt/cosmos/meta.json /opt/cosmos/FORK-LICENCE.md /etc/systemd/system/CosmosCloud.service; printf "x cosmos-fork-release-v1 x" >/opt/cosmos/cosmos
		snap binonly; expect_exit 10 install_a --yes </dev/null; same binonly after-binonly
		rm -rf /opt/cosmos; printf "[Service]\nWorkingDirectory=/opt/cosmos\n" >/etc/systemd/system/CosmosCloud.service
		snap unitonly; expect_exit 12 install_a --yes </dev/null; same unitonly after-unitonly
		rm -f /etc/systemd/system/CosmosCloud.service
		mkdir -p /var/lib/cosmos && echo "{}" >/var/lib/cosmos/cosmos.config.json
		snap data; expect_exit 13 install_a --yes </dev/null; same data after-data
		rm -rf /var/lib/cosmos
		snap end; same before end
		expect_exit 2 install_a --nonsense </dev/null
		expect_exit 2 install_a --http-port 99999 </dev/null
		printf "nonsense=1\n" >/root/bad-answers
		expect_exit 2 install_a --answers /root/bad-answers </dev/null
		expect_exit 5 setpriv --reuid=65534 --regid=65534 --clear-groups bash /e2e/releases/v$VA/install.sh </dev/null
		real=$(readlink -f /etc/os-release); cp -a "$real" /root/os-release; sed -i "s/^VERSION_ID=.*/VERSION_ID=\"99\"/" "$real"
		expect_exit 3 install_a --yes </dev/null; cp -a /root/os-release "$real"
		rm -f /root/bad-answers /root/os-release
		snap end2; same before end2
}

m_tampered_1() {
	snap before
}

m_tampered_2() { # v from the host
	echo "== $v"; expect_exit 4 install_a --yes --from /e2e/variants/$v </dev/null; snap after-$v; same before after-$v
			check "nothing planted" test ! -e /etc/cosmos-e2e-planted
			check "nothing unpacked" sh -c "! ls -d /opt/cosmos /opt/.cosmos-install.* 2>/dev/null"
}

m_tampered_3() {
	echo "== no signature on the download path"; expect_exit 4 install_a --yes </dev/null; same before after-nosigdl
}

m_tampered_4() {
	echo "== another release without its scripts signature"; expect_exit 4 install_a --yes --version 0.24.0-fork.950 </dev/null; same before after-noscriptsig
}

m_dryrun_1() {
	printf "APT::Update::Post-Invoke { \"touch /e2e-hook-ran\"; };\nAPT::Update::Pre-Invoke { \"touch /e2e-hook-ran\"; };\nAPT::Update::Post-Invoke-Success { \"touch /e2e-hook-ran\"; };\nDPkg::Pre-Invoke { \"touch /e2e-hook-ran\"; };\n" >/etc/apt/apt.conf.d/50e2e-probe
		# and in the main file, which apt reads after the folder
		printf "APT::Update::Post-Invoke { \"touch /e2e-main-hook-ran\"; };\nAPT::Update::Pre-Invoke { \"touch /e2e-main-hook-ran\"; };\n" >/etc/apt/apt.conf
		snap before; expect_exit 0 install_a --dry-run </dev/null; snap after; same before after
		check "the simulation ran none of the apt hooks" test ! -e /e2e-hook-ran
		check "not even those of /etc/apt/apt.conf" test ! -e /e2e-main-hook-ran
		rm /etc/apt/apt.conf.d/50e2e-probe /etc/apt/apt.conf
}

m_dryrun_2() {
	snap before; expect_exit 0 install_a --dry-run --from /e2e/variants/good </dev/null; snap after; same before after
		expect_exit 0 install_a --dry-run </dev/null; snap after2; same before after2
}

m_unavailable_1() {
	printf "Package: snapraid\nPin: release *\nPin-Priority: -1\n" >/etc/apt/preferences.d/e2e-no-snapraid; snap before
		expect_exit 6 install_a --yes </dev/null; snap after; same before after
}

m_locked_1() {
	(setsid /e2e/bin/helper lock-dpkg 45 >/e2e/out/lock.txt 2>&1 &); sleep 2; cat /e2e/out/lock.txt
		rc=0; install_a --yes --wizard-on-lan </dev/null >/e2e/out/install.txt 2>&1 || rc=$?; tail -n 5 /e2e/out/install.txt
		check "it waited for the lock" grep -qE "waiting for it|Waiting for cache lock" /e2e/out/install.txt
		check "then installed" test $rc = 0
		installed_ok 80
		check "--wizard-on-lan: the wizard answers on the network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1)/cosmos-ui/")" = 200
}

m_aptdaily_1() {
	DEBIAN_FRONTEND=noninteractive apt-get -q update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends unattended-upgrades </dev/null >/dev/null
		check "the system has apt-daily and unattended-upgrades" sh -c "systemctl cat apt-daily.service apt-daily-upgrade.service >/dev/null && command -v unattended-upgrade"
		# the container image turns the daily runs off (a server does not): on again
		rm -f /etc/apt/apt.conf.d/docker-disable-periodic-update
		# the real units, made to hold apt longer, through their own configuration only:
		# apt-daily (apt-get update) holds the lists for 4 minutes, then apt-daily-upgrade
		# (unattended-upgrade) holds dpkg 60 s longer whenever it has an upgrade to install
		printf "APT::Periodic::RandomSleep \"0\";\nAPT::Update::Pre-Invoke { \"sleep 240\"; };\nDPkg::Pre-Invoke { \"sleep 60\"; };\n" >/etc/apt/e2e-daily.conf
		for u in apt-daily apt-daily-upgrade; do
			mkdir -p /etc/systemd/system/$u.service.d
			printf "[Service]\nEnvironment=APT_CONFIG=/etc/apt/e2e-daily.conf\n" >/etc/systemd/system/$u.service.d/e2e.conf
		done
		systemctl daemon-reload
		snap before
		systemctl start --no-block apt-daily.service apt-daily-upgrade.service
		for _ in $(seq 1 60); do locks=$(lslocks -n -r -o PATH); case $locks in */var/lib/apt/lists/lock*) break ;; esac; sleep 1; done
		check "apt-daily.service holds apt's lists lock" sh -c "lslocks -n -r -o COMMAND,PID,PATH | grep /var/lib/apt/lists/lock"
		rc=0; install_a --yes </dev/null >/e2e/out/install.txt 2>&1 || rc=$?; tail -n 8 /e2e/out/install.txt
		grep -E "Another program|Waiting for cache lock" /e2e/out/install.txt || true
		check "it said what it waited for" grep -q "Another program is using apt: .*apt-daily.service" /e2e/out/install.txt
		if grep -q "unattended-upgr" /e2e/out/install.txt; then echo "(it also met unattended-upgrade holding dpkg)"; else echo "(unattended-upgrade had nothing to install: it did not hold dpkg for long)"; fi
		check "then installed" test $rc = 0
		check "apt-daily.service ran to its end" sh -c "! systemctl is-failed --quiet apt-daily.service"
		for _ in $(seq 1 300); do [ "$(systemctl is-active apt-daily-upgrade.service)" = inactive ] && break; sleep 1; done
		installed_ok 80
}

m_netcut_1() {
	snap before
}

m_netcut_2() {
	expect_exit 1 install_a --yes </dev/null; snap after; same before after
}

m_netcut_3() {
	expect_exit 0 install_a --yes --placement home </dev/null >/e2e/out/home.txt; installed_ok 80
		check "at home the wizard answers on the home network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1)/cosmos-ui/")" = 200
		check "no loopback marker at home" test ! -e /var/lib/cosmos/setup-loopback
		check "it says the wizard is in clear on that network" grep -q "anyone on that network could read" /e2e/out/home.txt
		check "and offers the tunnel" grep -q "ssh -L 8080:127.0.0.1:80 " /e2e/out/home.txt
}

m_interrupt_1() {
	snap before
}

m_interrupt_2() { # point from the host
	(setsid bash -c "echo \$\$ >/e2e/out/pgid; exec bash /e2e/releases/v$VA/install.sh --yes </dev/null >/e2e/out/run-$point.txt 2>&1" &)
			killed=0
			for i in $(seq 1 3000); do
				s=$(sed -n "s/^step=//p" /var/lib/cosmos-installer/state 2>/dev/null | tail -n1 || true)
				if [ "$s" = $point ]; then
					if [ $point = packages ]; then grep -qsx dpkg /proc/[0-9]*/comm || { sleep 0.2; continue; }; sleep 2; fi
					kill -9 -- -$(cat /e2e/out/pgid); echo "killed at $point"; killed=1
					# SIGKILL lands when a process leaves the kernel (a busy disk can
					# take seconds): the next run starts once the killed one is gone
					# and its lock is free; a lock still held after that is a leak
					for _ in $(seq 1 300); do flock -n /run/cosmos-installer.lock true && break; sleep 0.2; done
					if ! flock -n /run/cosmos-installer.lock true; then
						lslocks -n -o COMMAND,PID,PATH | grep cosmos-installer.lock || true
						echo "FAIL: 60 s after the kill at $point, something still holds install.sh's lock"
					fi
					break
				fi
				sleep 0.2
			done
			check "the run reached $point" test $killed = 1
}

m_interrupt_3() {
	DEBIAN_FRONTEND=noninteractive apt-get install -y -q -o DPkg::Lock::Timeout=600 less </dev/null >/dev/null && echo "the admin installed less"
}

m_interrupt_4() { # VA from the host
	(DEBIAN_FRONTEND=noninteractive setsid apt-get install -y -q -o DPkg::Lock::Timeout=600 hello </dev/null >/e2e/out/admin.txt 2>&1 &)
		expect_exit 0 install_a --yes </dev/null >/e2e/out/run-final.txt; installed_ok 80
		for _ in $(seq 1 120); do pkg_installed hello && break; sleep 1; done
		check "the admin's packages are installed" sh -c "dpkg-query -W less hello >/dev/null"
		check "the record names neither of the admin's packages" sh -c "! grep -qE \"^package (less|hello)\$\" /var/lib/cosmos-installer/record"
		check "with --yes the code is not printed, its file is named" sh -c "! grep -q \"Setup code:\" /e2e/out/run-final.txt && grep -q \"sudo cat /var/lib/cosmos/setup-code\" /e2e/out/run-final.txt"
		expect_exit 10 install_a --yes </dev/null
		# lines in the record that are not install.sh's own are left alone
		printf "file /\npackage -rf\npiece ../x\ndir /etc\nunit ../../etc/passwd\n" >>/var/lib/cosmos-installer/record
		# a container exists and the Docker daemon is stopped: Docker stays
		tar -cf - --files-from /dev/null | docker import - e2e-empty:1 >/dev/null
		docker create --name e2e-app e2e-empty:1 /none >/dev/null
		systemctl stop docker.socket docker.service
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages </dev/null | tee /e2e/out/uninstall-1.txt
		check "Docker is kept while its daemon cannot be asked" pkg_installed docker-ce
		check "and the uninstaller says why" grep -q "does not answer" /e2e/out/uninstall-1.txt
		check "the hostile lines did nothing" sh -c "test -d /etc && test -f /etc/passwd"
		check "the admin's packages stayed" sh -c "dpkg-query -W less hello >/dev/null"
		systemctl start docker.service; docker rm e2e-app >/dev/null; docker image rm e2e-empty:1 >/dev/null
		bash /e2e/releases/v$VA/uninstall.sh --yes --remove-packages --yes-delete-data </dev/null | tee /e2e/out/uninstall-2.txt
		check "the hostile lines are all that is left" sh -c "test \"\$(sort /var/lib/cosmos-installer/record | tr \"\\n\" \" \")\" = \"dir /etc file / package -rf piece ../x unit ../../etc/passwd \""
		rm -rf /var/lib/cosmos-installer
		expect_exit 0 bash /e2e/releases/v$VA/uninstall.sh --yes </dev/null
		bash /e2e/releases/v$VA/uninstall.sh --yes </dev/null | grep -q "Nothing to remove" && echo "ok: a second uninstall has nothing to remove"
		DEBIAN_FRONTEND=noninteractive apt-get purge -y -q less hello </dev/null >/dev/null
		snap removed; packages_back before removed
}

m_docker_there_1() {
	check "Docker runs" systemctl is-active --quiet docker
		dpkg-query -W docker.io >/e2e/out/docker-before
		dpkg -L docker.io | while read -r f; do if [ -f "$f" ]; then md5sum "$f"; fi; done >/e2e/out/docker-files-before
		# upstream's Cosmos as a container (stopped): refused, pointed to migrate.sh, nothing changed
		tar -cf - --files-from /dev/null | docker import - azukaar/cosmos-server:e2e >/dev/null
		docker create --name cosmos-server azukaar/cosmos-server:e2e /none >/dev/null
		snap container; expect_exit 11 install_a --yes </dev/null; same container after-container
		docker rm cosmos-server >/dev/null; docker image rm azukaar/cosmos-server:e2e >/dev/null
		snap before
		printf "placement=cloud\nssh=from:192.0.2.0/24,2001:db8::/32\n" >/root/answers
		expect_exit 0 install_a --yes --reachable internet --http-port 8080 --https-port 8443 --answers /root/answers </dev/null; installed_ok 8080; record_matches before
		rm /root/answers
		check "a cloud server: the wizard on 127.0.0.1 only" test -f /var/lib/cosmos/setup-loopback
		check "the wizard does not answer on the network" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://$(hostname -I | cut -d" " -f1):8080/cosmos-ui/" || true)" = 000
		check "but on 127.0.0.1" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:8080/cosmos-ui/)" = 200
		check "the guard got where the server stands and how SSH answers" grep -q -- "--exposed --ssh from:192.0.2.0/24,2001:db8::/32" /run/cosmos-e2e-stub-nftables.install.args
		check "no Docker package in the record" sh -c "! grep -q \"^package docker\" /var/lib/cosmos-installer/record"
		check "Docker's repository was not added" test ! -e /etc/apt/sources.list.d/docker.sources
		check "docker.io unchanged" sh -c "dpkg-query -W docker.io | cmp - /e2e/out/docker-before"
		for p in unattended-upgrades fail2ban nftables; do
			check "piece $p called with flags it accepts" grep -q "args ok" /run/cosmos-e2e-stub-$p.install
		done
		check "the pieces' record lines are in the record" grep -qP "^piece nftables\tfile\t/etc/cosmos-e2e-stub-nftables$" /var/lib/cosmos-installer/record
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null
		for p in unattended-upgrades fail2ban nftables; do check "piece $p removed" test ! -e /etc/cosmos-e2e-stub-$p; done
		check "Docker still runs" systemctl is-active --quiet docker
		check "docker.io still installed, unchanged" sh -c "dpkg-query -W docker.io | cmp - /e2e/out/docker-before"
		check "none of Docker's files changed" md5sum -c --quiet /e2e/out/docker-files-before
		snap removed; packages_back before removed
}

m_hop_1() { # VB from the host
	expect_exit 0 install_a --yes </dev/null; installed_ok 80
		check "B installed by its own script" test "$(version_installed)" = $VB
		check "no temporary folder left" sh -c "! ls -d /var/tmp/cosmos-install* 2>/dev/null"
}

m_aptfail_1() {
	snap before
		(setsid /e2e/bin/helper lock-file /var/lib/apt/lists/lock 120 >/e2e/out/lock.txt 2>&1 &); sleep 2; cat /e2e/out/lock.txt
		rc=0; COSMOS_INSTALL_APT_WAIT=20 install_a --yes </dev/null >/e2e/out/locked.txt 2>&1 || rc=$?; tail -n 4 /e2e/out/locked.txt
		check "the lists held too long end the run with 6" test $rc = 6
		check "it said so" grep -q "held apt for longer than 20 seconds" /e2e/out/locked.txt
		pkill -x helper || true
		same before after-locked
		# a package that apt simulates well but cannot install (its unpacking is refused)
		printf "DPkg::Pre-Install-Pkgs { \"grep -q snapraid && exit 1 || exit 0\"; };\n" >/etc/apt/apt.conf.d/99e2e-refuse-snapraid
		snap before2
		rc=0; install_a --yes </dev/null >/e2e/out/failed.txt 2>&1 || rc=$?; tail -n 12 /e2e/out/failed.txt
		check "a failed install ends with 1" test $rc = 1
		check "and says what it changed" grep -q "after these changes" /e2e/out/failed.txt
		check "the run is not completed" sh -c "! grep -q ^completed=yes /var/lib/cosmos-installer/state"
		check "snapraid is not recorded" sh -c "! grep -q \"^package snapraid\$\" /var/lib/cosmos-installer/record"
		rm /etc/apt/apt.conf.d/99e2e-refuse-snapraid
		# a package left half-installed by the failed transaction: its
		# configuration script fails (the hook rewrites the downloaded .deb)
		printf "#!/bin/sh\nwhile read -r f; do case \$f in */snapraid_*.deb) d=\$(mktemp -d); dpkg-deb -R \"\$f\" \$d; printf \"#!/bin/sh\\\\nexit 1\\\\n\" >\$d/DEBIAN/postinst; chmod 755 \$d/DEBIAN/postinst; dpkg-deb -b \$d \"\$f\" >/dev/null; rm -rf \$d;; esac; done\n" >/root/break-snapraid
		chmod 755 /root/break-snapraid
		printf "DPkg::Pre-Install-Pkgs { \"/root/break-snapraid\"; };\n" >/etc/apt/apt.conf.d/99e2e-break-snapraid
		rc=0; install_a --yes </dev/null >/e2e/out/half.txt 2>&1 || rc=$?; tail -n 12 /e2e/out/half.txt
		check "the failed transaction ends with 1" test $rc = 1
		check "snapraid is half-installed (dpkg)" sh -c "dpkg-query -W -f=\"\\\${db:Status-Abbrev}\" snapraid | grep -q \"^iF\""
		check "it is named half-installed" grep -q "half-installed (dpkg): .*snapraid" /e2e/out/half.txt
		check "never \"Nothing is left\"" sh -c "! grep -q \"Nothing is left\" /e2e/out/half.txt"
		check "snapraid stays recorded" grep -qx "package snapraid" /var/lib/cosmos-installer/record
		rm /etc/apt/apt.conf.d/99e2e-break-snapraid /root/break-snapraid
		printf "#!/bin/sh\nexit 0\n" >/var/lib/dpkg/info/snapraid.postinst
		expect_exit 0 install_a --yes </dev/null; installed_ok 80; record_matches before2
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/dev/null
		snap removed; packages_back before2 removed
}

m_trust_1() {
	snap before
}

m_trust_2() { # to from the host
	echo "== a download sent to $to"; rc=0; install_a --yes --http-port 8080 --https-port 8443 </dev/null >/e2e/out/r.txt 2>&1 || rc=$?
			check "refused (exit $rc)" test $rc = 1
			check "it says why" grep -q "refusing to download from" /e2e/out/r.txt
			same before after-redirect
}

m_trust_3() { # n from the host
	echo "== the next install.sh of $n"; expect_exit 4 install_a --yes --version 0.24.0-fork.$n --http-port 8080 --https-port 8443 </dev/null; same before after-hop
}

m_trust_4() {
	echo "== latest is a release whose next install.sh was changed"; expect_exit 4 install_a --yes --http-port 8080 --https-port 8443 </dev/null; same before after-hoplatest
}

m_trust_5() { # VO from the host
	echo "== forward only"
		expect_exit 15 install_a --yes --http-port 8080 --https-port 8443 </dev/null
		expect_exit 15 install_a --yes --version $VO --http-port 8080 --https-port 8443 </dev/null
		expect_exit 15 install_a --yes --from /e2e/releases/v$VO/cosmos-cloud-$VO-amd64.zip --http-port 8080 --https-port 8443 </dev/null
		expect_exit 0 install_a --dry-run --allow-older --from /e2e/releases/v$VO/cosmos-cloud-$VO-amd64.zip --http-port 8080 --https-port 8443 </dev/null
		echo "== a signature file whose version line is another release"
		expect_exit 4 install_a --yes --from /e2e/variants/otherversion --http-port 8080 --https-port 8443 </dev/null
		same before after-trust
}

m_env_1() { # VA from the host
	mkdir -p /root/evil && printf "#!/bin/sh\ntouch /root/pwned\n" >/root/evil/sh && chmod 755 /root/evil/sh && cp /root/evil/sh /root/evil/bash
		snap before
		cd /root/evil
		rc=0; cat /e2e/releases/v$VA/install.sh | sh >/e2e/out/pipe.txt 2>&1 || rc=$?
		check "curl | sh is refused (exit $rc)" sh -c "test $rc = 2 && grep -q \"not from a pipe\" /e2e/out/pipe.txt"
		rc=0; cat /e2e/releases/v$VA/install.sh | sudo sh >/dev/null 2>&1 || rc=$?
		check "curl | sudo sh is refused" test $rc = 2
		rc=0; cat /e2e/releases/v$VA/install.sh | bash >/dev/null 2>&1 || rc=$?
		check "curl | bash is refused" test $rc = 2
		rc=0; cat /e2e/releases/v$VA/uninstall.sh | sh >/dev/null 2>&1 || rc=$?
		check "uninstall.sh from a pipe is refused" test $rc = 2
		check "the sh of the current folder never ran" test ! -e /root/pwned
		cd /
		same before after-pipes
		# sudo -E with a hostile environment: a proxy that does not exist, a curlrc and an OPENSSL_CONF that would break both
		printf "proxy = \"http://127.0.0.1:9\"\ninsecure\n" >/root/.curlrc
		printf "this is not an openssl configuration\n[[[\n" >/root/bad-openssl.cnf
		rc=0; https_proxy=http://127.0.0.1:9 HTTPS_PROXY=http://127.0.0.1:9 OPENSSL_CONF=/root/bad-openssl.cnf CURL_HOME=/root \
			sudo -E bash /e2e/releases/v$VA/install.sh --yes --http-port 8080 --https-port 8443 </dev/null >/e2e/out/sudo-e.txt 2>&1 || rc=$?
		tail -n 3 /e2e/out/sudo-e.txt
		check "sudo -E with a hostile environment installs all the same" test $rc = 0
		rm -f /root/.curlrc /root/bad-openssl.cnf # (the checks below use curl too)
		installed_ok 8080
		mkdir -p /root/x && cp /e2e/releases/v$VA/install.sh /root/x/install.sh && cd /root/x
		expect_exit 10 sh install.sh --yes </dev/null
		expect_exit 10 su -c "sh /root/x/install.sh --yes" root </dev/null
		cd /
		rm -f /root/.curlrc /root/bad-openssl.cnf; rm -rf /root/x /root/evil
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/dev/null
		expect_exit 0 bash /e2e/releases/v$VA/uninstall.sh --yes </dev/null
}

m_pieces_1() { # VR from the host
	rc=0; install_a --yes --version $VR --reachable internet --http-port 8080 --https-port 8443 </dev/null >/e2e/out/pieces.txt 2>&1 || rc=$?
		grep -E "Protection|PIECE|piece|done\\.|not installed" /e2e/out/pieces.txt || true
		# Cosmos is installed whatever the pieces are; protection that was asked
		# for (reachable from the internet) and is missing ends with 16 and says so
		if grep -qE "not installed:|failed:" /e2e/out/pieces.txt; then
			check "protection asked for but missing: exit 16" test $rc = 16
			check "and the last line says so" sh -c "tail -n 1 /e2e/out/pieces.txt | grep -q \"installed WITHOUT the protection you asked for:\""
		else
			check "every piece ran: exit 0" test $rc = 0
		fi
		installed_ok 8080
		for p in unattended-upgrades fail2ban nftables; do
			if grep -q "PIECE NOT RUN: $p" /e2e/out/pieces.txt || grep -q "protection piece $p is not in this release" /e2e/out/pieces.txt; then
				echo "SKIPPED: the real piece $p does not speak the installer's interface yet (work/guard not merged): it was not run"
				check "and nothing of $p is recorded" sh -c "! grep -qE \"^piece(-ran)? $p\" /var/lib/cosmos-installer/record"
			else
				check "the real piece $p ran and is recorded" grep -qx "piece-ran $p" /var/lib/cosmos-installer/record
			fi
		done
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/pieces-uninstall.txt 2>&1
		check "uninstall ends well" sh -c "test ! -e /opt/cosmos"
}

m_pieces_2() {
	# the pieces' packages are simulated only for pieces that will run, as
	# they name them: never a list of the installer's own
	# (two of the pieces' own packages: python3 and nftables are needed by
	# packages of the system on some releases, and stay)
	printf "Package: fail2ban unattended-upgrades\nPin: release *\nPin-Priority: -1\n" >/etc/apt/preferences.d/e2e-no-pieces
	DEBIAN_FRONTEND=noninteractive apt-get -q update >/dev/null
	check "apt cannot install fail2ban here" sh -c "! apt-get -s -q install fail2ban >/dev/null 2>&1"
	# 1. the stubs (they run) name no package: installed, whatever apt lacks
	snap before
	rc=0; install_a --yes --protection yes --unattended-upgrades yes </dev/null >/e2e/out/pieces2.txt 2>&1 || rc=$?
	tail -n 8 /e2e/out/pieces2.txt
	check "installed with the three pieces run (exit 0)" test $rc = 0
	for p in unattended-upgrades fail2ban nftables; do check "the piece $p ran" grep -qx "piece-ran $p" /var/lib/cosmos-installer/record; done
	check "no package of the pieces was simulated" sh -c "! grep -qE 'Missing: .*(fail2ban|unattended-upgrades)' /e2e/out/pieces2.txt"
	check "none of them was installed" sh -c "! dpkg-query -W -f='\${db:Status-Abbrev}\\n' fail2ban unattended-upgrades 2>/dev/null | grep -q '^ii'"
	bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/pieces2-un.txt 2>&1
	check "uninstalled" test ! -e /opt/cosmos
	# 2. a piece that will run names a package apt cannot install: refused
	# before any change (exit 6)
	snap refused-before
	echo fail2ban >/etc/cosmos-e2e-stub-fail2ban.packages
	rc=0; install_a --yes --protection yes </dev/null >/e2e/out/pieces3.txt 2>&1 || rc=$?
	tail -n 6 /e2e/out/pieces3.txt
	check "a package a piece needs and apt lacks: exit 6" test $rc = 6
	check "it names the package" grep -q "fail2ban" /e2e/out/pieces3.txt
	rm -f /etc/cosmos-e2e-stub-fail2ban.packages
	same refused-before after-refused
	rm -f /etc/apt/preferences.d/e2e-no-pieces
}

m_fresh_fast_1() {
	snap before; install_a --yes </dev/null; installed_ok 80; record_matches before
		code=$(curl -s -o /e2e/out/r.json -w "%{http_code}" -X POST -H "Content-Type: application/json" --data "{}" http://127.0.0.1/cosmos/api/setup)
		check "POST /cosmos/api/setup without the setup code is refused" sh -c "test $code = 403 && grep -q SC001 /e2e/out/r.json"
		snap installed; expect_exit 10 install_a --yes </dev/null; same installed after-second
		bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null; snap removed
		packages_back before removed
		expect_exit 4 install_a --yes --from /e2e/variants/tampered-zip </dev/null; snap after-tampered; same removed after-tampered
}

m_byhand_1() {
	# a fork installed by hand, the README's manual way, as the owner's server
	# is: the release unpacked to /opt/cosmos, the unit "cosmos service
	# install" writes, the data folder; no install.sh, so no record
	arch=$(dpkg --print-architecture)
	mkdir /root/unpack && unzip -q "/e2e/releases/v$VA/cosmos-cloud-$VA-$arch.zip" -d /root/unpack
	mv "/root/unpack/cosmos-cloud-$VA" /opt/cosmos && rmdir /root/unpack
	mkdir -p /var/lib/cosmos
	(cd /opt/cosmos; env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin COSMOS_HTTP_PORT=80 COSMOS_HTTPS_PORT=443 ./cosmos service install >/dev/null 2>&1) || true # (it ends with 1 on success)
	systemctl daemon-reload
	systemctl enable --now CosmosCloud >/dev/null 2>&1
	wait_panel 80
	check "the hand-installed fork runs" systemctl is-active --quiet CosmosCloud
	check "there is no installer record" test ! -e /var/lib/cosmos-installer
	check "its uninstall.sh is there (as it will be on the owner's server)" test -f /opt/cosmos/fork/install/uninstall.sh
	# 1. running: every flag that removes something, and nothing is touched
	find /var/lib/cosmos /opt/cosmos -xdev | sort >/e2e/out/running-before
	rc=0; bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/un-running.txt 2>&1 || rc=$?
	cat /e2e/out/un-running.txt
	check "uninstall.sh refuses with 3" test $rc = 3
	check "saying this Cosmos was not installed by install.sh: nothing to remove" grep -q "this Cosmos was not installed by install.sh: nothing to remove" /e2e/out/un-running.txt
	check "the service still runs and is enabled" sh -c "systemctl is-active --quiet CosmosCloud && systemctl is-enabled --quiet CosmosCloud"
	find /var/lib/cosmos /opt/cosmos -xdev | sort >/e2e/out/running-after
	check "no file of the installation or its data went" sh -c "comm -23 /e2e/out/running-before /e2e/out/running-after | grep -c . | grep -qx 0"
	# 2. stopped, for a still picture: files, contents, unit and packages compared
	systemctl stop CosmosCloud
	snap before
	rc=0; bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/un-stopped.txt 2>&1 || rc=$?
	check "stopped too: uninstall.sh refuses with 3" test $rc = 3
	same before after-uninstall
	# 3. install.sh refuses over it before anything (10)
	expect_exit 10 install_a --yes </dev/null
	same before after-install
	# 4. a record or state install.sh did not write: empty, stray, open to all,
	# naming nothing, or a state alone; refused the same, nothing run
	# (no apt-get update, the folder of the installer left as it is)
	for planted in "empty" "0644" "names-nothing" "state-only"; do
		mkdir -p /var/lib/cosmos-installer
		case $planted in
		empty) : >/var/lib/cosmos-installer/record; chmod 600 /var/lib/cosmos-installer/record ;;
		0644) echo "dir /opt/cosmos" >/var/lib/cosmos-installer/record; chmod 644 /var/lib/cosmos-installer/record ;;
		names-nothing) printf "data /var/lib/cosmos\nnonsense\n" >/var/lib/cosmos-installer/record; chmod 600 /var/lib/cosmos-installer/record ;;
		state-only) echo "step=packages" >/var/lib/cosmos-installer/state ;;
		esac
		snap "planted-$planted"
		lists=$(stat -c %Y /var/lib/apt/lists 2>/dev/null || echo none)
		rc=0; bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/un-$planted.txt 2>&1 || rc=$?
		check "a $planted record: uninstall.sh refuses with 3" test $rc = 3
		check "and says this Cosmos was not installed by install.sh" grep -q "this Cosmos was not installed by install.sh: nothing to remove" /e2e/out/un-$planted.txt
		check "it ran no apt-get update" test "$(stat -c %Y /var/lib/apt/lists 2>/dev/null || echo none)" = "$lists"
		check "the installer's folder is left as it was" test -d /var/lib/cosmos-installer
		same "planted-$planted" "after-planted-$planted"
		rm -rf /var/lib/cosmos-installer
	done
	systemctl start CosmosCloud
	wait_panel 80
}

m_apthang_1() {
	# a package script that hangs: install.sh stops the WHOLE apt transaction
	# at its bound (apt, dpkg and the script), says so, and the next run
	# finishes the package database and installs
	printf "#!/bin/sh\nwhile read -r f; do case \$f in */snapraid_*.deb) d=\$(mktemp -d); dpkg-deb -R \"\$f\" \$d; printf \"#!/bin/sh\\\\nexec sleep 3600\\\\n\" >\$d/DEBIAN/postinst; chmod 755 \$d/DEBIAN/postinst; dpkg-deb -b \$d \"\$f\" >/dev/null; rm -rf \$d;; esac; done\n" >/root/hang-snapraid
	chmod 755 /root/hang-snapraid
	printf "DPkg::Pre-Install-Pkgs { \"/root/hang-snapraid\"; };\n" >/etc/apt/apt.conf.d/99e2e-hang-snapraid
	snap before
	start=$(date +%s); rc=0
	COSMOS_INSTALL_APT_TIMEOUT=240 install_a --yes </dev/null >/e2e/out/hang.txt 2>&1 || rc=$?
	took=$(( $(date +%s) - start ))
	tail -n 12 /e2e/out/hang.txt
	check "the hung transaction ends the run with 1" test $rc = 1
	check "within the bound, not the script's hour (took ${took}s)" test "$took" -lt 900
	check "it says what was stopped" grep -q "it was stopped, with everything it had started (dpkg, package scripts)" /e2e/out/hang.txt
	running=$(for f in /proc/[0-9]*/cmdline; do tr '\0' ' ' <"$f" 2>/dev/null; echo; done)
	check "no package script is left running" sh -c "! printf '%s\n' \"\$1\" | grep -q '^sleep 3600'" _ "$running"
	check "no dpkg is left running" sh -c "! grep -qsx dpkg /proc/[0-9]*/comm"
	check "nobody holds dpkg's lock" sh -c "! lslocks -n -r -o PATH | grep -q '^/var/lib/dpkg/lock'"
	rm /etc/apt/apt.conf.d/99e2e-hang-snapraid /root/hang-snapraid
	if [ -e /var/lib/dpkg/info/snapraid.postinst ]; then printf "#!/bin/sh\nexit 0\n" >/var/lib/dpkg/info/snapraid.postinst; fi
	expect_exit 0 install_a --yes </dev/null
	installed_ok 80; record_matches before
}

m_wizardbind_1() {
	# the marker made untrusted before the server reads it (a drop-in of the
	# unit, as a careless admin's could: here a second name for it): the server listens on every address,
	# says so, and install.sh says it LOUDLY instead of "this server only"
	mkdir -p /etc/systemd/system/CosmosCloud.service.d
	# (a second name: the server tightens the modes of its folder's files itself)
	printf "[Service]\nExecStartPre=-/bin/ln -f /var/lib/cosmos/setup-loopback /var/lib/cosmos/setup-loopback.second-name\n" >/etc/systemd/system/CosmosCloud.service.d/e2e.conf
	rc=0; install_a --yes </dev/null >/e2e/out/install.txt 2>&1 || rc=$?
	tail -n 20 /e2e/out/install.txt
	check "installed" test $rc = 0
	echo "marker: $(stat -c '%a %U %h names' /var/lib/cosmos/setup-loopback 2>&1); the server says: $(journalctl -u CosmosCloud --no-pager | grep -o 'Setup: [^[]*' | tail -n 2 | tr '\n' '|')"
	check "install.sh says loudly that the wizard answers on the network" grep -q "THE FIRST-RUN WIZARD ANSWERS ON THE NETWORK, NOT ON 127.0.0.1 ONLY AS ASKED" /e2e/out/install.txt
	check "and never that it answers on this server only" sh -c "! grep -q 'answers on this server only' /e2e/out/install.txt"
	check "the server said so in its journal" sh -c "journalctl -u CosmosCloud --no-pager | grep -q 'Setup: WARNING: .*EVERY address'"
}

m_wizardbind_2() {
	# at home, but with a global IPv6 address: the wizard answers on 127.0.0.1 only
	check "the machine has a global IPv6 address" sh -c "awk '\$4 == \"00\"' /proc/net/if_inet6 | grep -q '^20010db8'"
	rc=0; install_a --yes --placement home </dev/null >/e2e/out/install.txt 2>&1 || rc=$?
	tail -n 14 /e2e/out/install.txt
	check "installed" test $rc = 0
	check "a global IPv6 makes it loopback" grep -q "the server has a global IPv6 address (2001:db8:" /e2e/out/install.txt
	check "it says this server only" grep -q "answers on this server only (127.0.0.1)" /e2e/out/install.txt
	check "nothing listens on [::]:80 nor 0.0.0.0:80" sh -c "! awk '\$4 == \"0A\" {print \$2}' /proc/net/tcp /proc/net/tcp6 | grep -qE '^0+:0050\$'"
	check "the wizard does not answer on the IPv6 address" test "$(curl -s -o /dev/null -m 5 -w "%{http_code}" "http://[$(ip -6 -o addr show scope global | awk '{sub(/\/.*/, "", $4); print $4; exit}')]/cosmos-ui/" || true)" = 000
}

m_aptrace_1() {
	# another apt holds the lock while it installs psmisc, a package of
	# install.sh's plan; install.sh waits for it, and psmisc is not recorded
	# as install.sh's (apt's history says whose transaction installed it)
	# the container image's docker-clean deletes downloaded packages after
	# every apt run, the other's included (a server does not have it)
	rm -f /etc/apt/apt.conf.d/docker-clean
	DEBIAN_FRONTEND=noninteractive apt-get -q update >/dev/null
	(DEBIAN_FRONTEND=noninteractive setsid sh -c 'apt-get install -y -q -o "DPkg::Pre-Invoke::=sleep 120" psmisc </dev/null >/e2e/out/admin.txt 2>&1; echo "admin-exit $?" >>/e2e/out/admin.txt' &)
	for _ in $(seq 1 30); do lslocks -n -r -o PATH | grep -q '^/var/lib/dpkg/lock' && break; sleep 1; done
	check "the other apt holds dpkg's lock" sh -c "lslocks -n -r -o PATH | grep -q '^/var/lib/dpkg/lock'"
	check "psmisc is not installed yet" sh -c "! dpkg-query -W -f='\${db:Status-Abbrev}' psmisc 2>/dev/null | grep -q '^ii'"
	snap before
	rc=0; install_a --yes </dev/null >/e2e/out/install.txt 2>&1 || rc=$?
	tail -n 6 /e2e/out/install.txt
	check "installed" test $rc = 0
	check "it waited for the other apt" grep -qE "Waiting for cache lock|Another program is using apt" /e2e/out/install.txt
	for _ in $(seq 1 60); do grep -q "^admin-exit" /e2e/out/admin.txt && break; sleep 1; done
	check "the other apt installed psmisc (it ended with 0)" grep -qx "admin-exit 0" /e2e/out/admin.txt
	check "psmisc is installed" sh -c "dpkg-query -W -f='\${db:Status-Abbrev}' psmisc | grep -q '^ii'"
	check "psmisc is not in install.sh's record" sh -c "! grep -qx 'package psmisc' /var/lib/cosmos-installer/record"
	grep -A3 "sleep 120" /var/log/apt/history.log || true
	check "apt's history names psmisc in the other transaction" sh -c "grep -A3 'sleep 120' /var/log/apt/history.log | grep -q '^Install: .*psmisc'"
	installed_ok 80
}

# ---- migrate.sh: upstream's Cosmos moved to the fork ---------------------------
# Every case ends with migration_invariant: what is on the machine is a state
# the design allows, and every file "the owner" wrote is still somewhere.

# mig_bg <name> <env assignments...> -- <args...>: migrate.sh (stamped, or the
# test mode when TEST=1 is among the assignments) in a session of its own
mig_bg() {
	local name=$1 cmd
	shift
	cmd="bash /e2e/releases/v$VA/migrate.sh"
	local envs=()
	while [ "$1" != -- ]; do
		if [ "$1" = TEST=1 ]; then cmd="migrate_test"; else envs+=("$1"); fi
		shift
	done
	shift
	(env "${envs[@]}" setsid bash -c "echo \$\$ >/e2e/out/$name.pgid; export E2E_RUN_ID='$E2E_RUN_ID' VA='$VA'; . /e2e/bin/machine-lib.sh; $cmd $* </dev/null >/e2e/out/$name.txt 2>&1" &)
}
mig_kill() { # name: the whole session, then the lock free again, and the unit settled
	kill -9 -- -"$(cat "/e2e/out/$1.pgid")" 2>/dev/null || true
	for _ in $(seq 1 300); do flock -n /run/cosmos-installer.lock true && break; sleep 0.2; done
	# a start or stop systemd had begun goes on without the script: wait for its end
	for _ in $(seq 1 120); do
		case $(systemctl is-active CosmosCloud 2>/dev/null || true) in activating | deactivating | reloading) sleep 0.5 ;; *) break ;; esac
	done
	sleep 2
}
mstep_now() { sed -n 's/^mstep=//p' /var/lib/cosmos-installer/state 2>/dev/null | tail -n 1 || true; }
# kill_at <step>: migrate.sh killed with -9 at the first state at or past the step
kill_at() {
	local order=" stopping stopped backing-up backed-up swapping swapped started " s before
	mig_bg "k-$1" -- --yes
	for _ in $(seq 1 6000); do
		s=$(mstep_now)
		if [ -n "$s" ]; then
			before=${order%% "$1" *}
			case $before in *" $s"*) ;; *) mig_kill "k-$1"; echo "killed at $1 (the state then: $(mstep_now))"; return 0 ;; esac
		fi
		sleep 0.1
	done
	echo "FAIL: migrate.sh never reached $1"; tail -n 20 "/e2e/out/k-$1.txt"; return 1
}
# kill_on_text <name> <text>: wait for the text in the run's output, then kill it
kill_on_text() {
	# (up to 20 minutes: a backup's copy and its lists can take long on a loaded host)
	for _ in $(seq 1 6000); do
		if grep -q "$2" "/e2e/out/$1.txt" 2>/dev/null; then mig_kill "$1"; echo "killed at '$2'"; return 0; fi
		sleep 0.2
	done
	echo "FAIL: '$2' never came"; tail -n 20 "/e2e/out/$1.txt"; return 1
}
expect_rc() { # code, what, args...: migrate.sh (stamped) ends with the code
	local want=$1 what=$2 rc=0
	shift 2
	migrate_a "$@" </dev/null >"/e2e/out/$what.txt" 2>&1 || rc=$?
	grep -vE '^  |debconf' "/e2e/out/$what.txt" | tail -n 6
	check "$what: exit $want" test "$rc" = "$want"
}
# same_as_now <name>: nothing changed since a state was taken (a refusal, a dry run)
take_now() { upstream_state "$1"; [ ! -f /var/lib/cosmos-installer/state ] || cp /var/lib/cosmos-installer/state "$OUT/state-$1"; }
unchanged_since() {
	upstream_unchanged "$1"
	if [ -f "$OUT/state-$1" ]; then check "the state is as it was" cmp -s "$OUT/state-$1" /var/lib/cosmos-installer/state; fi
}

m_migrate_1() { # UPV from the host: upstream's version
	upstream_install "$UPV"
	note_upstream
	plant_marker before-dry-run
	upstream_state dry
	expect_rc 0 dry-run --yes --dry-run
	upstream_unchanged dry
	plant_marker before-migration
	expect_rc 0 migrate --yes
	migrated_ok "$UPV"
	check "it said upstream $UPV was older than the base, or the base" sh -c "grep -qE 'older than 0.24.0|Found: upstream' /e2e/out/migrate.txt"
	plant_marker on-the-fork
	expect_exit 10 install_a --yes </dev/null
	expect_rc 10 again --yes
	rc=0; bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/un.txt 2>&1 || rc=$?
	cat /e2e/out/un.txt
	check "uninstall.sh refuses a migrated Cosmos with 3" test $rc = 3
	check "and names the way back" grep -q "GOING-BACK.txt" /e2e/out/un.txt
	migrated_ok "$UPV"
	migration_invariant
}
m_migrate_2() { # VB from the host: the fork's own updater after a migration
	PANEL_HOST=srv-e2e.local
	code=$(api POST login "{\"nickname\":\"admin\",\"password\":\"$E2E_PASSWORD\"}"); echo "login: $code"
	code=$(api POST sudo "{\"password\":\"$E2E_PASSWORD\"}"); echo "sudo: $code"
	update_to "$VB"
	migration_invariant
}
m_migrate_hop() { # VB from the host: B is the newest: A's migrate.sh takes B's own, verified
	upstream_install 0.23.4
	note_upstream
	plant_marker before-hop
	expect_rc 0 hop --yes
	check "it took the newer release's own migrate.sh" grep -q "taking that release's own migrate.sh" /e2e/out/hop.txt
	check "and did not take the first script's state for an interrupted run" sh -c "! grep -q 'interrupted' /e2e/out/hop.txt"
	migrated_ok 0.23.4 "$VB"
	migration_invariant
}

m_migrate_refusals_1() {
	# a machine with nothing: 19
	expect_rc 19 none --yes
	check "nothing was made" test ! -e /var/lib/cosmos-installer
	# a Cosmos container (upstream's image name): 17, not yet
	tar -cf - --files-from /dev/null | docker import - azukaar/cosmos-server:latest >/dev/null
	docker create --name cosmos-server azukaar/cosmos-server:latest /none >/dev/null
	expect_rc 17 container --yes
	check "saying it is not done yet for containers" grep -q "not yet its Docker container" /e2e/out/container.txt
	docker rm -f cosmos-server >/dev/null
	check "nothing was made" test ! -e /var/lib/cosmos-installer
}
m_migrate_refusals_2() { # the fork installed by install.sh: 10, before anything
	install_a --yes </dev/null >/dev/null 2>&1
	take_now fork
	expect_rc 10 fork --yes --accept-community-edition --backup-dir /root/b
	unchanged_since fork
	expect_rc 10 fork-undo --undo
	expect_rc 10 fork-dry --dry-run
	unchanged_since fork
}
m_migrate_refusals_3() { # the fork installed by hand (the README's way, no state): 10 before any package or download
	arch=$(dpkg --print-architecture)
	mkdir /root/unpack && unzip -q "/e2e/releases/v$VA/cosmos-cloud-$VA-$arch.zip" -d /root/unpack
	mv "/root/unpack/cosmos-cloud-$VA" /opt/cosmos && rmdir /root/unpack
	mkdir -p /var/lib/cosmos
	(cd /opt/cosmos; env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin COSMOS_HTTP_PORT=80 COSMOS_HTTPS_PORT=443 ./cosmos service install >/dev/null 2>&1) || true
	systemctl daemon-reload; systemctl enable --now CosmosCloud >/dev/null 2>&1
	wait_panel 80
	check "jq is not there yet (migrate.sh would install it)" sh -c "! command -v jq"
	take_now byhand
	expect_rc 10 byhand --yes --accept-community-edition
	unchanged_since byhand
	check "no package was installed and no state made (the refusal came first)" sh -c "! command -v jq && test ! -e /var/lib/cosmos-installer"
}
m_migrate_refusals_4() { # upstream, each refusal on its own; then a licence migrated with the flag
	upstream_install 0.24.0
	note_upstream
	plant_marker refusals
	config=/var/lib/cosmos/cosmos.config.json
	cp -p "$config" /root/config.orig
	refused() { # code, what, migrate.sh's args...
		local want=$1 what=$2
		shift 2
		take_now "$what"
		expect_rc "$want" "$what" --yes "$@"
		unchanged_since "$what"
		# the dry run says the same, and changes nothing either
		expect_rc "$want" "$what-dry" --dry-run "$@"
		unchanged_since "$what"
	}
	dropin() { mkdir -p /etc/systemd/system/CosmosCloud.service.d; printf '[Service]\n%s\n' "$2" >"/etc/systemd/system/CosmosCloud.service.d/$1.conf"; systemctl daemon-reload; }
	undropin() { rm -f "/etc/systemd/system/CosmosCloud.service.d/$1.conf"; systemctl daemon-reload; }
	# a newer upstream than the base
	cp -p /opt/cosmos/meta.json /root/meta.orig
	sed -i 's/"version": "0.24.0"/"version": "0.25.1"/' /opt/cosmos/meta.json
	refused 17 newer
	check "it says what the fork is built on" grep -q "newer than 0.24.0" /e2e/out/newer.txt
	cp -p /root/meta.orig /opt/cosmos/meta.json
	# users in MongoDB; the wizard not finished
	/e2e/bin/helper config-set "$config" MongoDB '"mongodb://192.0.2.10:27017"'
	refused 17 mongodb
	cp -p /root/config.orig "$config"
	/e2e/bin/helper config-set "$config" NewInstall true
	refused 17 newinstall
	cp -p /root/config.orig "$config"
	# an update of upstream waiting; Cosmos not running
	: >/opt/cosmos/cosmos-update.zip
	refused 17 staged-update
	rm /opt/cosmos/cosmos-update.zip
	systemctl stop CosmosCloud
	refused 17 stopped
	systemctl start CosmosCloud; wait_panel 80
	# the unit as systemd runs it: drop-ins, environment files, variables, folder, user
	dropin config-folder 'Environment=COSMOS_CONFIG_FOLDER=/srv/cosmos-elsewhere/'
	refused 17 config-folder
	check "config-folder: it names it" grep -q "COSMOS_CONFIG_FOLDER" /e2e/out/config-folder.txt
	undropin config-folder
	mkdir -p /etc/default; printf 'CONFIG_FILE=/srv/x/cosmos.config.json\n' >/etc/default/e2e-cosmos
	dropin envfile 'EnvironmentFile=/etc/default/e2e-cosmos'
	refused 17 envfile
	undropin envfile; rm /etc/default/e2e-cosmos
	mkdir -p /etc/sysconfig; printf 'CONFIG_FILE=/srv/x/cosmos.config.json\n' >/etc/sysconfig/CosmosCloud
	refused 17 sysconfig-config-file
	rm /etc/sysconfig/CosmosCloud
	# a value with a glob character (review N4), systemd's own environment (N5),
	# and a variable only the running process still has (set, restarted, unset)
	dropin glob 'Environment=COSMOS_CONFIG_FOLDER=/srv/cosmos-nomatch*'
	refused 17 glob
	check "glob: it names it" grep -q "COSMOS_CONFIG_FOLDER" /e2e/out/glob.txt
	undropin glob
	systemctl set-environment COSMOS_CONFIG_FOLDER=/srv/cosmos-elsewhere/
	refused 17 manager-env
	check "manager-env: it names systemd's own environment" grep -q "systemd's own environment" /e2e/out/manager-env.txt
	systemctl restart CosmosCloud; wait_panel 80
	systemctl unset-environment COSMOS_CONFIG_FOLDER
	refused 17 process-env
	check "process-env: it names the running Cosmos" grep -q "the running Cosmos (process" /e2e/out/process-env.txt
	systemctl restart CosmosCloud; wait_panel 80
	dropin unknown-var 'Environment=COSMOS_SOMETHING_NEW=1'
	refused 17 unknown-var
	undropin unknown-var
	mkdir -p /srv/elsewhere
	dropin workdir 'WorkingDirectory=/srv/elsewhere'
	refused 17 workdir
	undropin workdir
	dropin user 'User=nobody'
	refused 17 user
	undropin user
	# links and mount points at the folders (the data on a big disk through a link)
	systemctl stop CosmosCloud
	mkdir -p /srv; mv -T /var/lib/cosmos /srv/cosmos-data; ln -s /srv/cosmos-data /var/lib/cosmos
	systemctl start CosmosCloud; wait_panel 80
	refused 17 data-link
	check "data-link: it says it is a link" grep -q "is a link" /e2e/out/data-link.txt
	systemctl stop CosmosCloud; rm /var/lib/cosmos; mv -T /srv/cosmos-data /var/lib/cosmos; systemctl start CosmosCloud; wait_panel 80
	systemctl stop CosmosCloud
	mv -T /opt/cosmos /srv/cosmos-opt; ln -s /srv/cosmos-opt /opt/cosmos
	systemctl start CosmosCloud; wait_panel 80
	refused 17 opt-link
	systemctl stop CosmosCloud; rm /opt/cosmos; mv -T /srv/cosmos-opt /opt/cosmos; systemctl start CosmosCloud; wait_panel 80
	mount --bind /var/lib/cosmos /var/lib/cosmos
	refused 17 data-mount
	check "data-mount: it says it is a mount point" grep -q "mount point" /e2e/out/data-mount.txt
	umount /var/lib/cosmos
	mount --bind /opt/cosmos /opt/cosmos
	refused 17 opt-mount
	umount /opt/cosmos
	mkdir -p /var/lib/cosmos/e2e-mnt; mount -t tmpfs -o size=1m tmpfs /var/lib/cosmos/e2e-mnt
	refused 17 mount-inside
	umount /var/lib/cosmos/e2e-mnt; rmdir /var/lib/cosmos/e2e-mnt
	# what an earlier migration left, named by no state: the real installation is never replaced
	mkdir /opt/.cosmos-upstream-before-fork; echo "a leftover" >/opt/.cosmos-upstream-before-fork/e2e-leftover
	refused 17 leftover-aside
	check "leftover-aside: it names it" grep -q "/opt/.cosmos-upstream-before-fork" /e2e/out/leftover-aside.txt
	check "the real installation is still upstream's" grep -q '"version": "0.24.0"' /opt/cosmos/meta.json
	rm -rf /opt/.cosmos-upstream-before-fork
	mkdir -p /var/lib/cosmos-installer/migration
	refused 17 leftover-notes
	rm -rf /var/lib/cosmos-installer
	mkdir /opt/.cosmos-fork-tried.AbCdEf12
	refused 17 leftover-tried
	rm -rf /opt/.cosmos-fork-tried.AbCdEf12
	# the backup's place
	mkdir -p /mnt/small /mnt/big /root/ro /root/real /home/e2e-user
	# (mode 0755: a place only root can change, so that space and memory are what is refused)
	mount -t tmpfs -o size=1m,mode=0755 tmpfs /mnt/small
	mount -t tmpfs -o size=8g,mode=0755 tmpfs /mnt/big
	chmod 755 /root/ro /root/real
	mount --bind /root/ro /root/ro && mount -o remount,bind,ro /root/ro
	ln -s /root/real /root/link
	chown 1000:1000 /home/e2e-user
	refused 6 full --backup-dir /mnt/small/b
	check "full: it says the space" grep -q "not enough free space" /e2e/out/full.txt
	refused 6 memory --backup-dir /mnt/big/b
	check "memory: it says the reboot" grep -q "lost at the next reboot" /e2e/out/memory.txt
	refused 6 read-only --backup-dir /root/ro/b
	check "read-only: it says so" grep -q "cannot be written" /e2e/out/read-only.txt
	refused 6 not-roots --backup-dir /home/e2e-user/b
	check "not-roots: it says whose" grep -q "not root's alone" /e2e/out/not-roots.txt
	refused 2 var-tmp --backup-dir /var/tmp/b
	refused 2 inside --backup-dir /var/lib/cosmos/b
	refused 2 link --backup-dir /root/link/b
	refused 2 exists --backup-dir /root/real
	refused 2 relative --backup-dir backups
	umount /mnt/small /mnt/big /root/ro
	# the file system of the backup's place: the decision, fed each type as
	# stat -f reports it (the test mode stands in for the mount: vfat, NFS and
	# the like are not mounted in the test machines)
	for t in msdos vfat exfat ntfs fuseblk fuse cifs smb2 smb3 nfs nfs4 9p unknown tmpfs ramfs; do
		take_now "fs-$t"
		rc=0; COSMOS_INSTALL_TEST_FSTYPE=$t migrate_test --yes --dry-run </dev/null >"/e2e/out/fs-$t.txt" 2>&1 || rc=$?
		check "a backup place on $t: refused with 6" test "$rc" = 6
		unchanged_since "fs-$t"
	done
	for t in ext2/ext3 xfs btrfs zfs; do
		rc=0; COSMOS_INSTALL_TEST_FSTYPE=$t migrate_test --yes --dry-run </dev/null >"/e2e/out/fs-ok.txt" 2>&1 || rc=$?
		check "a backup place on $t: taken (the dry run ends with 0)" test "$rc" = 0
	done
	# a licence: 18 without the flag, and the words say what really happens
	/e2e/bin/helper config-set "$config" Licence '"e2e-made-up-licence"'
	refused 18 licence
	check "it says what the licence keeps and what it loses" sh -c "grep -q 'renewed with upstream' /e2e/out/licence.txt && grep -q 'Pro build alone' /e2e/out/licence.txt"
	# the other script's options
	refused 2 install-option --http-port 8080
	refused 2 storage-option --storage-tools no
	expect_exit 2 install_a --yes --backup-dir /root/x </dev/null
	expect_exit 2 install_a --yes --continue </dev/null
	# with the flag: migrated, the licence kept in the settings
	expect_rc 0 licence-migrate --yes --accept-community-edition
	check "the licence is still in the settings (only read, never touched)" grep -q '"Licence": *"e2e-made-up-licence"' "$config"
	migrated_ok 0.24.0
	migration_invariant
}

m_migrate_fail_1() { # UPV from the host
	upstream_install "$UPV"
	note_upstream
	add_snapshot_hooks
	# a failure at each step (the test mode makes it): everything back, byte for byte; the backup kept
	for point in stopped backed-up swapped started checked; do
		plant_marker "before-fail-$point"
		rc=0; COSMOS_INSTALL_TEST_FAIL_AT=$point migrate_test --yes </dev/null >"/e2e/out/fail-$point.txt" 2>&1 || rc=$?
		grep -E "TEST MODE: the step|runs again|Checked against|Moved into|backup stays" "/e2e/out/fail-$point.txt" || true
		check "a failure at '$point': rolled back, exit 20" test $rc = 20
		rolled_back_exactly
		upstream_back "$UPV"
		b=$(sed -n 's/^The backup stays in \(.*\) (delete.*/\1/p' "/e2e/out/fail-$point.txt" | tail -n 1)
		if [ "$point" != stopped ]; then check "the backup it named is still there, marked ($b)" sh -c "test -n '$b' && test -f '$b/.made-by-migrate.sh'"; fi
		check "the app still answers" sh -c "curl -s -m 10 -H 'Host: app.srv-e2e.local' http://127.0.0.1/ | grep -qx e2e-app"
	done
	if [ "$UPV" = 0.23.4 ]; then
		check "0.23.4 itself runs again on its own data (after the fork had converted it forward)" grep -q '"version": "0.23.4"' /opt/cosmos/meta.json
	fi
	migration_invariant
}
m_migrate_fail_2() { # VC from the host: a release that does not start; a container and the unit changed while the fork is checked
	add_snapshot_hooks
	rc=0; migrate_a --yes --from "/e2e/releases/v$VC" </dev/null >/e2e/out/fail-nostart.txt 2>&1 || rc=$?
	grep -vE '^  |debconf' /e2e/out/fail-nostart.txt | tail -n 8
	check "a fork that does not start: rolled back, exit 20" test $rc = 20
	rolled_back_exactly
	upstream_back 0.24.0
	# a container appears while the fork is checked: put back, the difference named (22)
	(
		for _ in $(seq 1 600); do
			if grep -q "\"version\": \"$VA\"" /opt/cosmos/meta.json 2>/dev/null; then
				sleep 3; docker create --name e2e-intruder e2e-empty:1 /none >/dev/null 2>&1; exit 0
			fi
			sleep 0.5
		done
	) &
	rc=0; migrate_a --yes --from "/e2e/releases/v$VA" </dev/null >/e2e/out/fail-container.txt 2>&1 || rc=$?
	wait
	grep -vE '^  |debconf' /e2e/out/fail-container.txt | tail -n 14
	check "a container changed during the check: put back, the difference said (exit 22)" test $rc = 22
	check "the reason and the end both name it" sh -c "test \$(grep -c e2e-intruder /e2e/out/fail-container.txt) -ge 2"
	check "and it says containers are not put back" grep -q "does not put them back" /e2e/out/fail-container.txt
	docker rm -f e2e-intruder >/dev/null
	rolled_back_exactly
	upstream_back 0.24.0
	# the unit changed while the fork is checked: put back as it was (compared)
	(
		for _ in $(seq 1 600); do
			if grep -q "\"version\": \"$VA\"" /opt/cosmos/meta.json 2>/dev/null; then
				sleep 3; printf '# changed while the fork ran\n' >>/etc/systemd/system/CosmosCloud.service; exit 0
			fi
			sleep 0.5
		done
	) &
	rc=0; migrate_a --yes --from "/e2e/releases/v$VA" </dev/null >/e2e/out/fail-unit.txt 2>&1 || rc=$?
	wait
	grep -vE '^  |debconf' /e2e/out/fail-unit.txt | tail -n 8
	check "the unit changed during the check: rolled back, exit 20" test $rc = 20
	check "the unit is the one of before" sh -c "sha256sum /etc/systemd/system/CosmosCloud.service | cut -d' ' -f1 | grep -qx \$(cat $OUT/unit.sha)"
	rolled_back_exactly
	upstream_back 0.24.0
	migration_invariant
}

m_migrate_interrupt_1() { # kills at each step, --undo; the owner using the server meanwhile; uninstall and the dry run in between
	upstream_install 0.24.0
	note_upstream
	add_snapshot_hooks
	for point in stopped backed-up swapped started; do
		plant_marker "before-kill-$point"
		kill_at "$point"
		take_now "after-kill-$point"
		expect_rc 0 "dry-after-kill-$point" --dry-run
		unchanged_since "after-kill-$point"
		expect_rc 0 "undo-$point" --undo
		if ls /e2e/out/hook-*-stop >/dev/null 2>&1; then rolled_back_exactly; fi
		upstream_back 0.24.0
	done
	# P1: killed while the fork is checked; the owner works on it (a file, a container)
	plant_marker before-p1
	kill_at started
	sleep 5
	plant_marker p1-written-on-the-fork
	docker create --name e2e-owner-app e2e-empty:1 /none >/dev/null
	take_now p1
	expect_rc 21 p1-run --yes
	check "it says what it found and the two choices" sh -c "grep -q -- '--undo' /e2e/out/p1-run.txt && grep -q -- '--continue' /e2e/out/p1-run.txt"
	unchanged_since p1
	expect_rc 0 p1-dry --dry-run
	unchanged_since p1
	# P3/B4: uninstall.sh during an unfinished migration refuses and keeps its state
	rc=0; bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/p1-un.txt 2>&1 || rc=$?
	cat /e2e/out/p1-un.txt
	check "uninstall.sh during the migration: exit 3" test $rc = 3
	check "and it names --undo" grep -q -- "--undo" /e2e/out/p1-un.txt
	unchanged_since p1
	expect_rc 22 p1-undo --undo
	check "the owner's file written on the fork is kept in the backup, named" sh -c "grep -q 'var-lib-cosmos-as-it-was' /e2e/out/p1-undo.txt && ls /var/backups/cosmos-before-fork-*/var-lib-cosmos-as-it-was-*/e2e-owner-p1-written-on-the-fork"
	check "and the container the owner made is named, not removed" sh -c "grep -q e2e-owner-app /e2e/out/p1-undo.txt && docker inspect e2e-owner-app >/dev/null"
	docker rm -f e2e-owner-app >/dev/null
	rolled_back_exactly
	upstream_back 0.24.0
	# P6: the backup moved away, then --undo: nothing changed, and the truth said
	plant_marker before-p6
	kill_at started
	b=$(sed -n 's/^backup=//p' /var/lib/cosmos-installer/state | tail -n 1)
	mv -T "$b" /root/moved-backup
	take_now p6
	expect_rc 1 p6-undo --undo
	check "it says the backup is missing" grep -q "missing, incomplete, or no longer matches" /e2e/out/p6-undo.txt
	unchanged_since p6
	mv -T /root/moved-backup "$b"
	expect_rc 0 p6-undo-again --undo
	rolled_back_exactly
	upstream_back 0.24.0
	# killed during the rollback itself, then --undo
	plant_marker before-kill-in-rollback
	mig_bg rb COSMOS_INSTALL_TEST_FAIL_AT=checked COSMOS_INSTALL_TEST_PAUSE_AT=rolling-back TEST=1 -- --yes
	kill_on_text rb "waiting at 'rolling-back'"
	expect_rc 0 undo-after-rollback-kill --undo
	rolled_back_exactly
	upstream_back 0.24.0
	# 0700 from its creation: seen while the copy is under way
	mig_bg perm COSMOS_INSTALL_TEST_PAUSE_AT=backing-up TEST=1 -- --yes
	kill_on_text perm "waiting at 'backing-up'" || true
	b=$(sed -n 's/^backup=//p' /var/lib/cosmos-installer/state | tail -n 1)
	check "the backup folder is 0700 and root's from its creation ($b)" sh -c "[ \"\$(stat -c '%u %a' '$b')\" = '0 700' ]"
	expect_rc 0 undo-after-perm --undo
	upstream_back 0.24.0
	migration_invariant
}
m_migrate_interrupt_2() { # P2: killed while the manifests are written; upstream restarted (a reboot); the owner writes; --dry-run; then a run
	upstream_install 0.24.0
	note_upstream
	mig_bg man COSMOS_INSTALL_TEST_PAUSE_AT=manifests TEST=1 -- --yes
	kill_on_text man "waiting at 'manifests'"
	systemctl start CosmosCloud
	wait_panel 80
	plant_marker p2-written-after-the-reboot
	take_now p2
	expect_rc 0 p2-dry --dry-run
	check "the dry run says what a run and --undo would do" sh -c "grep -q 'A run would' /e2e/out/p2-dry.txt && grep -q -- '--undo would' /e2e/out/p2-dry.txt"
	unchanged_since p2
	check "the owner's file is in the data" test -f /var/lib/cosmos/e2e-owner-p2-written-after-the-reboot
	expect_rc 0 p2-run --yes
	check "the earlier backup was kept and named" grep -q "the earlier backup is kept" /e2e/out/p2-run.txt
	migrated_ok 0.24.0
	check "the owner's file came along" test -f /var/lib/cosmos/e2e-owner-p2-written-after-the-reboot
	migration_invariant
}
m_migrate_interrupt_3() { # killed after the swap, nothing ran since: a run finishes it
	upstream_install 0.24.0
	note_upstream
	add_snapshot_hooks
	# killed after the swap, then the unit started (as a reboot would start
	# it): the fork ran on the data since, so a run waits for the owner's word
	plant_marker before-swap-and-start
	mig_bg sw1 COSMOS_INSTALL_TEST_PAUSE_AT=swapped TEST=1 -- --yes
	kill_on_text sw1 "waiting at 'swapped'"
	systemctl start CosmosCloud
	wait_panel 80
	take_now swap-started
	expect_rc 21 swap-started-run --yes
	unchanged_since swap-started
	# the same, with the data put back as the backup's list has it: only the
	# unit's start says something ran since, and that alone is enough
	systemctl stop CosmosCloud
	b=$(sed -n 's/^backup=//p' /var/lib/cosmos-installer/state | tail -n 1)
	mv -T /var/lib/cosmos /root/data-the-fork-used
	cp -a "$b/var-lib-cosmos" /var/lib/cosmos
	take_now swap-started-same-data
	expect_rc 21 swap-started-same-data-run --yes
	unchanged_since swap-started-same-data
	rm -rf /root/data-the-fork-used
	expect_rc 0 swap-started-undo --undo
	rolled_back_exactly
	upstream_back 0.24.0
	# killed after the swap with nothing run since: a run finishes it
	plant_marker before-resume
	mig_bg sw COSMOS_INSTALL_TEST_PAUSE_AT=swapped TEST=1 -- --yes
	kill_on_text sw "waiting at 'swapped'"
	expect_rc 0 resume --yes
	check "it said it finishes" grep -q "before it ever ran" /e2e/out/resume.txt
	migrated_ok 0.24.0
	migration_invariant
}
m_migrate_interrupt_4() { # review round 2: --continue (N6, S-N1, S-N3, its normal path) and a rollback killed half-way (N1, N2)
	upstream_install 0.24.0
	note_upstream
	add_snapshot_hooks
	# N6: --continue over a migration killed before the swap: refused (2) before anything is done
	plant_marker before-n6
	kill_at backed-up
	take_now n6
	expect_rc 2 n6-continue --yes --continue
	check "n6: it says a run resumes it" grep -q "a run without --continue resumes it" /e2e/out/n6-continue.txt
	unchanged_since n6
	expect_rc 0 n6-undo --undo
	if ls "$OUT"/hook-*-stop >/dev/null 2>&1; then rolled_back_exactly; fi
	upstream_back 0.24.0
	# N1: the fork failed its check, the way back was killed after the data was
	# put back (upstream in /opt/cosmos): only --undo, never --continue
	plant_marker before-n1
	mig_bg n1 COSMOS_INSTALL_TEST_FAIL_AT=checked COSMOS_INSTALL_TEST_PAUSE_AT=rollback-data TEST=1 -- --yes
	kill_on_text n1 "waiting at 'rollback-data'"
	take_now n1
	expect_rc 21 n1-run --yes
	check "n1: it says the way back was interrupted, and only --undo" sh -c "grep -q 'way back to upstream began' /e2e/out/n1-run.txt && grep -q 'Only --undo completes it' /e2e/out/n1-run.txt"
	check "n1: it does not offer --continue" sh -c "! grep -q -- '--continue' /e2e/out/n1-run.txt"
	unchanged_since n1
	expect_rc 21 n1-continue --yes --continue
	unchanged_since n1
	expect_rc 0 n1-dry --dry-run
	check "n1: the dry run says the same" grep -q "way back to upstream began" /e2e/out/n1-dry.txt
	unchanged_since n1
	expect_rc 0 n1-undo --undo
	rolled_back_exactly
	upstream_back 0.24.0
	# N2: killed while the data was out of its place (moved into the backup, not yet copied back)
	plant_marker before-n2
	mig_bg n2 COSMOS_INSTALL_TEST_FAIL_AT=checked COSMOS_INSTALL_TEST_PAUSE_AT=rollback-data-moved TEST=1 -- --yes
	kill_on_text n2 "waiting at 'rollback-data-moved'"
	take_now n2
	expect_rc 21 n2-run --yes
	check "n2: it names the data folder as missing or partly copied" grep -q "may be missing or only partly copied back" /e2e/out/n2-run.txt
	expect_rc 21 n2-continue --yes --continue
	unchanged_since n2
	expect_rc 0 n2-undo --undo
	rolled_back_exactly
	upstream_back 0.24.0
	# S-N3: --continue with no room for its copy: refused before Cosmos is
	# stopped, nothing changed (the backup on a tmpfs taken as ext4 by the test mode)
	mkdir -p /mnt/mid
	mount -t tmpfs -o size=8g,mode=0755 tmpfs /mnt/mid
	plant_marker before-sn3
	mig_bg sn3 COSMOS_INSTALL_TEST_FSTYPE=ext2/ext3 COSMOS_INSTALL_TEST_PAUSE_AT=started TEST=1 -- --yes --backup-dir /mnt/mid/b
	kill_on_text sn3 "waiting at 'started'"
	plant_marker written-on-the-fork-before-continue
	expect_rc 21 sn3-run --yes
	check "sn3: the fork is in place, so --continue is offered" grep -q -- "--continue   keep the fork" /e2e/out/sn3-run.txt
	data_k=$(du -sk /var/lib/cosmos | cut -f1)
	free_k=$(df -Pk /mnt/mid | awk 'NR==2{print $4}')
	fallocate -l "$(( (free_k - data_k / 2) * 1024 ))" /mnt/mid/e2e-fill
	take_now sn3
	rc=0; COSMOS_INSTALL_TEST_FSTYPE=ext2/ext3 migrate_test --yes --continue </dev/null >/e2e/out/sn3-continue.txt 2>&1 || rc=$?
	tail -n 4 /e2e/out/sn3-continue.txt
	check "sn3: no room: exit 1" test "$rc" = 1
	check "sn3: it says there is not room, and nothing changed" sh -c "grep -q 'there is not room in' /e2e/out/sn3-continue.txt && grep -q 'nothing was changed' /e2e/out/sn3-continue.txt"
	unchanged_since sn3
	rm -f /mnt/mid/e2e-fill
	# C1: --continue's normal path: the data copied first, the fork checked, the migration completed
	rc=0; COSMOS_INSTALL_TEST_FSTYPE=ext2/ext3 migrate_test --yes --continue </dev/null >/e2e/out/c1-continue.txt 2>&1 || rc=$?
	grep -vE '^  |debconf' /e2e/out/c1-continue.txt | tail -n 8
	check "c1: --continue completed (exit 0)" test "$rc" = 0
	check "c1: the owner's file written on the fork is in the copy made at --continue" sh -c "ls /mnt/mid/b/var-lib-cosmos-at-continue-*/e2e-owner-written-on-the-fork-before-continue"
	check "c1: and still in the data" test -f /var/lib/cosmos/e2e-owner-written-on-the-fork-before-continue
	migrated_ok 0.24.0
	expect_rc 10 c1-undo-after --undo
	migration_invariant
}
