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
m_migrate_1() { # UPV from the host: upstream's version
	upstream_install "$UPV"
	note_upstream
	# a dry run changes nothing
	upstream_state dry
	rc=0; migrate_a --yes --dry-run </dev/null >/e2e/out/dry.txt 2>&1 || rc=$?
	tail -n 8 /e2e/out/dry.txt
	check "the dry run ends with 0" test $rc = 0
	upstream_unchanged dry
	# the migration
	rc=0; migrate_a --yes </dev/null >/e2e/out/migrate.txt 2>&1 || rc=$?
	grep -vE '^  |debconf' /e2e/out/migrate.txt | tail -n 30
	check "migrated (exit 0)" test $rc = 0
	migrated_ok "$UPV"
	check "it said upstream $UPV was older than the base, or the base" sh -c "grep -qE 'older than 0.24.0|Found: upstream' /e2e/out/migrate.txt"
	# then neither script runs again, and uninstall.sh refuses
	expect_exit 10 install_a --yes </dev/null
	expect_exit 10 migrate_a --yes </dev/null
	rc=0; bash /opt/cosmos/fork/install/uninstall.sh --yes --remove-packages --purge --yes-delete-data </dev/null >/e2e/out/un.txt 2>&1 || rc=$?
	cat /e2e/out/un.txt
	check "uninstall.sh refuses a migrated Cosmos with 3" test $rc = 3
	check "and names the way back" grep -q "GOING-BACK.txt" /e2e/out/un.txt
	migrated_ok "$UPV"
}
m_migrate_2() { # VB from the host: the fork's own updater after a migration
	PANEL_HOST=srv-e2e.local
	code=$(api POST login "{\"nickname\":\"admin\",\"password\":\"$E2E_PASSWORD\"}"); echo "login: $code"
	code=$(api POST sudo "{\"password\":\"$E2E_PASSWORD\"}"); echo "sudo: $code"
	update_to "$VB"
}

m_migrate_refusals_1() {
	# a machine with nothing: 19
	expect_exit 19 migrate_a --yes </dev/null
	check "nothing was made" test ! -e /var/lib/cosmos-installer
	# a Cosmos container (upstream's image name): 17, not yet
	tar -cf - --files-from /dev/null | docker import - azukaar/cosmos-server:latest >/dev/null
	docker create --name cosmos-server azukaar/cosmos-server:latest /none >/dev/null
	rc=0; migrate_a --yes </dev/null >/e2e/out/container.txt 2>&1 || rc=$?
	cat /e2e/out/container.txt
	check "a Cosmos container: 17" test $rc = 17
	check "saying it is not done yet for containers" grep -q "not yet its Docker container" /e2e/out/container.txt
	docker rm -f cosmos-server >/dev/null
	check "nothing was made" test ! -e /var/lib/cosmos-installer
}
m_migrate_refusals_2() { # the fork installed by install.sh: 10, before anything
	install_a --yes </dev/null >/dev/null 2>&1
	upstream_state fork
	rc=0; migrate_a --yes --accept-community-edition --backup-dir /root/b </dev/null >/e2e/out/fork.txt 2>&1 || rc=$?
	cat /e2e/out/fork.txt
	check "the fork already there: 10, whatever the flags" test $rc = 10
	upstream_unchanged fork
	rc=0; migrate_a --undo </dev/null >/e2e/out/fork-undo.txt 2>&1 || rc=$?
	check "--undo over the fork: 10 too" test $rc = 10
}
m_migrate_refusals_3() { # upstream, each refusal on its own, then a licence migrated with the flag
	upstream_install 0.24.0
	note_upstream
	config=/var/lib/cosmos/cosmos.config.json
	cp -p "$config" /root/config.orig
	refused() { # code, what, setup..., then the migration
		local want=$1 what=$2 rc=0
		upstream_state "$what"
		migrate_a --yes "${@:3}" </dev/null >"/e2e/out/ref-$what.txt" 2>&1 || rc=$?
		tail -n 2 "/e2e/out/ref-$what.txt"
		check "$what: exit $want" test $rc = "$want"
		upstream_unchanged "$what"
	}
	# a newer upstream than the base
	cp -p /opt/cosmos/meta.json /root/meta.orig
	sed -i 's/"version": "0.24.0"/"version": "0.25.1"/' /opt/cosmos/meta.json
	refused 17 newer
	check "it says what the fork is built on" grep -q "newer than 0.24.0" /e2e/out/ref-newer.txt
	cp -p /root/meta.orig /opt/cosmos/meta.json
	# users in MongoDB
	/e2e/bin/helper config-set "$config" MongoDB '"mongodb://192.0.2.10:27017"'
	refused 17 mongodb
	cp -p /root/config.orig "$config"
	# the wizard not finished
	/e2e/bin/helper config-set "$config" NewInstall true
	refused 17 newinstall
	cp -p /root/config.orig "$config"
	# an update of upstream waiting
	: >/opt/cosmos/cosmos-update.zip
	refused 17 staged-update
	rm /opt/cosmos/cosmos-update.zip
	# Cosmos not running
	systemctl stop CosmosCloud
	refused 17 stopped
	systemctl start CosmosCloud; wait_panel 80
	# the backup's place: full, in memory, read-only, inside what is backed up, through a link, existing
	mkdir -p /mnt/small /mnt/big /root/ro /root/real
	mount -t tmpfs -o size=1m tmpfs /mnt/small
	mount -t tmpfs -o size=8g tmpfs /mnt/big
	mount --bind /root/ro /root/ro && mount -o remount,bind,ro /root/ro
	ln -s /root/real /root/link
	refused 6 full --backup-dir /mnt/small/b
	check "full: it says the space" grep -q "not enough free space" /e2e/out/ref-full.txt
	refused 6 memory --backup-dir /mnt/big/b
	check "memory: it says the reboot" grep -q "lost at the next reboot" /e2e/out/ref-memory.txt
	refused 6 read-only --backup-dir /root/ro/b
	check "read-only: it says so" grep -q "cannot be written" /e2e/out/ref-read-only.txt
	refused 2 inside --backup-dir /var/lib/cosmos/b
	refused 2 link --backup-dir /root/link/b
	refused 2 exists --backup-dir /root/real
	refused 2 relative --backup-dir backups
	umount /mnt/small /mnt/big /root/ro
	# a licence: 18 without the flag
	/e2e/bin/helper config-set "$config" Licence '"e2e-made-up-licence"'
	refused 18 licence
	check "it explains the community edition" grep -q "community edition" /e2e/out/ref-licence.txt
	# install.sh's options are refused by migrate.sh, and the other way
	refused 2 install-option --http-port 8080
	expect_exit 2 install_a --yes --backup-dir /root/x </dev/null
	# with the flag: migrated, the licence kept in the settings, unused
	rc=0; migrate_a --yes --accept-community-edition </dev/null >/e2e/out/licence-migrate.txt 2>&1 || rc=$?
	tail -n 6 /e2e/out/licence-migrate.txt
	check "with --accept-community-edition: migrated" test $rc = 0
	check "the licence is still in the settings (only read, never touched)" grep -q '"Licence": *"e2e-made-up-licence"' "$config"
	migrated_ok 0.24.0
}

m_migrate_fail_1() {
	upstream_install 0.24.0
	note_upstream
	add_snapshot_hooks
	# a failure at each step (the test mode makes it): everything back, byte for byte
	for point in stopped backed-up swapped started checked; do
		rc=0; COSMOS_INSTALL_TEST_FAIL_AT=$point migrate_test --yes </dev/null >"/e2e/out/fail-$point.txt" 2>&1 || rc=$?
		grep -E "TEST MODE: the step|back as it was|stopped \\(exit" "/e2e/out/fail-$point.txt" || true
		check "a failure at '$point': rolled back, exit 20" test $rc = 20
		rolled_back_exactly
		upstream_back 0.24.0
		check "the app still answers" sh -c "curl -s -m 10 -H 'Host: app.srv-e2e.local' http://127.0.0.1/ | grep -qx e2e-app"
	done
}
m_migrate_fail_2() { # VC from the host: a release that does not start
	add_snapshot_hooks
	rc=0; migrate_a --yes --from "/e2e/releases/v$VC" </dev/null >/e2e/out/fail-nostart.txt 2>&1 || rc=$?
	grep -vE '^  |debconf' /e2e/out/fail-nostart.txt | tail -n 12
	check "a fork that does not start: rolled back, exit 20" test $rc = 20
	rolled_back_exactly
	upstream_back 0.24.0
	# a container that changes while the fork is checked
	(
		for _ in $(seq 1 600); do
			if grep -q "\"version\": \"$VA\"" /opt/cosmos/meta.json 2>/dev/null; then
				sleep 3; docker create --name e2e-intruder e2e-empty:1 /none >/dev/null 2>&1; exit 0
			fi
			sleep 0.5
		done
	) &
	# (release A itself: C, which does not start, is still the newest on the stand-in)
	rc=0; migrate_a --yes --from "/e2e/releases/v$VA" </dev/null >/e2e/out/fail-container.txt 2>&1 || rc=$?
	wait
	grep -vE '^  |debconf' /e2e/out/fail-container.txt | tail -n 25
	check "a container changed during the check: rolled back, exit 20" test $rc = 20
	check "and the reason named it" grep -q "e2e-intruder" /e2e/out/fail-container.txt
	docker rm -f e2e-intruder >/dev/null
	rolled_back_exactly
	upstream_back 0.24.0
}

m_migrate_interrupt_1() {
	upstream_install 0.24.0
	note_upstream
	add_snapshot_hooks
	# the steps in their order: a step passes quickly into the next (backed-up
	# into swapping), so the kill comes at the first state at or past the one asked
	order=" stopping stopped backed-up swapping swapped started "
	at_or_past() { # state wanted
		[ -n "$1" ] || return 1
		local before=${order%% "$2" *}
		case $before in *" $1"* | *" $1 "*) return 1 ;; esac
		case $order in *" $1 "*) return 0 ;; esac
		return 1
	}
	kill_at() { # step: migrate.sh killed with -9 once the state says it reached the step
		local s i
		(setsid bash -c "echo \$\$ >/e2e/out/mpgid; exec bash /e2e/releases/v$VA/migrate.sh --yes </dev/null >/e2e/out/mrun-$1.txt 2>&1" &)
		for i in $(seq 1 6000); do
			s=$(sed -n "s/^mstep=//p" /var/lib/cosmos-installer/state 2>/dev/null | tail -n1 || true)
			if at_or_past "$s" "$1"; then
				kill -9 -- -"$(cat /e2e/out/mpgid)"
				for _ in $(seq 1 300); do flock -n /run/cosmos-installer.lock true && break; sleep 0.2; done
				echo "killed at $1 (the state then: $(sed -n 's/^mstep=//p' /var/lib/cosmos-installer/state | tail -n1))"
				return 0
			fi
			sleep 0.1
		done
		echo "FAIL: migrate.sh never reached $1 (or a later step)"
		tail -n 20 "/e2e/out/mrun-$1.txt"
		return 1
	}
	for point in stopped backed-up swapped started; do
		kill_at "$point"
		rc=0; migrate_a --undo </dev/null >"/e2e/out/undo-$point.txt" 2>&1 || rc=$?
		tail -n 4 "/e2e/out/undo-$point.txt"
		check "killed at '$point', --undo: exit 0" test $rc = 0
		# when the kill came before the stop's hook ran, there is nothing to compare
		if ls /e2e/out/hook-*-stop >/dev/null 2>&1; then rolled_back_exactly; fi
		upstream_back 0.24.0
	done
	# killed after the swap, then run again: it finishes
	kill_at swapped
	rc=0; migrate_a --yes </dev/null >/e2e/out/resume.txt 2>&1 || rc=$?
	grep -E "interrupted|runs the fork" /e2e/out/resume.txt || true
	check "killed after the swap, run again: migrated (exit 0)" test $rc = 0
	migrated_ok 0.24.0
	# killed before the swap on another try is covered by --undo above; and the
	# resume before the swap: upstream back first, then migrated
}
