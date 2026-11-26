package protection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/fail2ban"
	"github.com/azukaar/cosmos-server/src/utils"
)

// useCosmosPort sets the ports of Cosmos' configuration for a test.
func useCosmosPort(t *testing.T, mode string, httpPort string, httpsPort string) {
	t.Helper()
	config := utils.GetBaseMainConfig()
	previous := config
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList[mode]
	config.HTTPConfig.HTTPPort = httpPort
	config.HTTPConfig.HTTPSPort = httpsPort
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
}

func TestNothingInstalled(t *testing.T) {
	w := newWorld(t)
	w.noTable()
	status := GetStatus()
	for name, piece := range map[string]Piece{"guard": status.Guard.Piece, "fail2ban": status.Fail2ban.Piece, "updates": status.Updates.Piece} {
		if piece.Installed || piece.Active || piece.State != StateNotInstalled || piece.Since != nil || piece.By != "" || piece.StateFile != fileMissing {
			t.Errorf("%s: %+v, want not installed", name, piece)
		}
	}
	for piece, command := range map[string]string{PieceGuard: status.Guard.Command, PieceFail2ban: status.Fail2ban.Command, PieceUpdates: status.Updates.Command} {
		if command != "sudo sh /opt/cosmos/fork/"+piece+"/install.sh" {
			t.Errorf("%s: command %q, want the installer in /opt/cosmos", piece, command)
		}
	}
	if status.Guard.CommandKind != CommandInstall || status.Guard.PortMissing {
		t.Errorf("guard %+v", status.Guard)
	}
}

func TestAllInstalledAndActive(t *testing.T) {
	w := newWorld(t)
	useCosmosPort(t, "SELFSIGNED", "2052", "2053")
	w.table("nft-guard-loaded.json")
	w.state(PieceGuard, guardState)
	w.state(PieceFail2ban, "piece=fail2ban\nformat=1\ninstalled_at=2026-11-16T09:00:00Z\nsshd=on\ncosmos_jails=on\n")
	w.state(PieceUpdates, "piece=unattended-upgrades\nformat=1\ninstalled_at=2026-11-15T08:00:00Z\nconfig=/etc/apt/apt.conf.d/52cosmos-security-upgrades\n")
	w.fail2ban = fail2ban.Summary{Installed: true, Running: true, ConfigInstalled: true, ConfigSince: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	w.updatesOn()
	// the installers are next to the running executable
	for _, piece := range pieces {
		w.write(filepath.Join(w.root, "cosmos", "fork", piece, "install.sh"), "#!/bin/sh\n", 0644)
	}

	status := GetStatus()
	guard := status.Guard
	if !guard.Installed || !guard.Active || guard.State != StateActive || guard.Reason != "" || guard.By != ByFork {
		t.Errorf("guard %+v", guard.Piece)
	}
	if guard.Since == nil || !guard.Since.Equal(time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("guard since %v", guard.Since)
	}
	if guard.Table == nil || guard.Table.DroppedInput.Packets != 4 || guard.PortMissing || !reflect.DeepEqual(guard.KeptOpen, []int{2052, 2053}) {
		t.Errorf("guard table %+v, port missing %v, kept %v", guard.Table, guard.PortMissing, guard.KeptOpen)
	}
	if guard.IPv4 == nil || !*guard.IPv4 || guard.SSHOpen == nil || *guard.SSHOpen {
		t.Errorf("guard ipv4 %v ssh %v", guard.IPv4, guard.SSHOpen)
	}
	if status.CosmosPort != 2053 {
		t.Errorf("Cosmos' port %d, want the HTTPS port", status.CosmosPort)
	}

	f2b := status.Fail2ban
	if !f2b.Installed || !f2b.Active || f2b.State != StateActive || f2b.Since == nil || f2b.Since.Day() != 16 || f2b.SSHD == nil || !*f2b.SSHD {
		t.Errorf("fail2ban %+v", f2b)
	}
	updates := status.Updates
	if !updates.Installed || !updates.Active || updates.State != StateActive || updates.By != ByFork || updates.Since == nil || updates.Since.Day() != 15 {
		t.Errorf("updates %+v", updates)
	}
	for piece, command := range map[string]string{PieceGuard: guard.Command, PieceFail2ban: f2b.Command, PieceUpdates: updates.Command} {
		if command != "sudo sh "+filepath.Join(w.root, "cosmos", "fork", piece, "uninstall.sh") {
			t.Errorf("%s: command %q, want the uninstaller next to the executable", piece, command)
		}
	}
	if guard.CommandKind != CommandUninstall || f2b.CommandKind != CommandUninstall || updates.CommandKind != CommandUninstall {
		t.Error("the commands are not the uninstallers")
	}
}

func TestGuardInstalledButItsTableIsNotLoaded(t *testing.T) {
	w := newWorld(t)
	useCosmosPort(t, "SELFSIGNED", "2052", "2053")
	w.noTable()
	w.state(PieceGuard, guardState)
	guard := GetStatus().Guard
	if !guard.Installed || guard.Active || guard.State != StateInactive || guard.Reason != reasonTableMissing || guard.CommandKind != CommandUninstall {
		t.Errorf("guard %+v", guard.Piece)
	}
	if guard.RepairCommand != "sudo systemctl reload cosmos-host-guard" {
		t.Errorf("repair command %q", guard.RepairCommand)
	}
	// the port is checked against what the installer recorded
	if guard.PortMissing || !reflect.DeepEqual(guard.KeptOpen, []int{2052, 2053}) {
		t.Errorf("port missing %v, kept %v", guard.PortMissing, guard.KeptOpen)
	}
}

func TestGuardTableWithoutItsHooks(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-loaded.json")
	w.nftOut = []byte(strings.Replace(string(w.nftOut), `"hook": "forward"`, `"hook": "output"`, 1))
	guard := GetStatus().Guard
	if !guard.Installed || guard.State != StateInactive || guard.Reason != reasonNotHooked {
		t.Errorf("guard %+v", guard.Piece)
	}
}

func TestGuardStateUnknown(t *testing.T) {
	w := newWorld(t)
	w.state(PieceGuard, guardState)
	// no nft at all
	guard := GetStatus().Guard
	if guard.State != StateUnknown || guard.Unknown != unknownNoNft || guard.Reason != unknownNoNft || guard.Table != nil || guard.Active {
		t.Errorf("without nft: %+v", guard)
	}
	if !guard.Installed || guard.CommandKind != CommandUninstall {
		t.Errorf("the state file still says it is installed: %+v", guard.Piece)
	}
	forget()
	w.nft()
	w.nftErr, w.nftRun = "Operation not permitted (you must be root)\n", exitError(t)
	if guard := GetStatus().Guard; guard.State != StateUnknown || guard.Unknown != unknownNotPermitted {
		t.Errorf("not root: %+v", guard)
	}
}

func TestGuardWarnsWhenCosmosPortIsNotKeptOpen(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-udp.json") // public_tcp 443
	w.state(PieceGuard, guardState)
	useCosmosPort(t, "SELFSIGNED", "80", "443")
	if guard := GetStatus().Guard; guard.PortMissing || !reflect.DeepEqual(guard.KeptOpen, []int{443}) {
		t.Errorf("443 is kept open: %+v", guard)
	}
	// Cosmos moved: the warning comes at once, though the reading is cached
	useCosmosPort(t, "SELFSIGNED", "80", "8443")
	status := GetStatus()
	if !status.Guard.PortMissing || status.CosmosPort != 8443 {
		t.Errorf("8443 is not kept open: %+v", status.Guard)
	}
	// HTTPS switched off: the HTTP port counts
	useCosmosPort(t, "DISABLED", "443", "8443")
	if status := GetStatus(); status.CosmosPort != 443 || status.Guard.PortMissing {
		t.Errorf("HTTP on 443: port %d, missing %v", status.CosmosPort, status.Guard.PortMissing)
	}
	// nothing to compare with: no warning
	forget()
	w.nftErr, w.nftRun = "", errStandIn
	os.Remove(filepath.Join(StateDir, PieceGuard+".state"))
	w.state(PieceGuard, strings.Replace(guardState, "format=1", "format=9", 1))
	useCosmosPort(t, "SELFSIGNED", "80", "8443")
	if guard := GetStatus().Guard; guard.PortMissing {
		t.Errorf("unknown table and unreadable state file: %+v", guard)
	}
}

func TestFail2banStates(t *testing.T) {
	w := newWorld(t)
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for name, test := range map[string]struct {
		summary fail2ban.Summary
		state   bool
		want    string
		reason  string
	}{
		"not there":                 {fail2ban.Summary{}, false, StateNotInstalled, ""},
		"without the fork's jails":  {fail2ban.Summary{Installed: true, Running: true}, false, StateNotInstalled, ""},
		"by hand with the jails":    {fail2ban.Summary{Installed: true, Running: true, ConfigInstalled: true, ConfigSince: stamp}, false, StateActive, ""},
		"stopped":                   {fail2ban.Summary{Installed: true, ConfigInstalled: true, ConfigSince: stamp}, true, StateInactive, reasonStopped},
		"jails removed":             {fail2ban.Summary{Installed: true, Running: true}, true, StateInactive, reasonNoJails},
		"Fail2ban removed":          {fail2ban.Summary{}, true, StateInactive, reasonNoBinary},
		"installed by the fork now": {fail2ban.Summary{Installed: true, Running: true, ConfigInstalled: true, ConfigSince: stamp}, true, StateActive, ""},
	} {
		forget()
		os.Remove(filepath.Join(StateDir, PieceFail2ban+".state"))
		if test.state {
			w.state(PieceFail2ban, "piece=fail2ban\nformat=1\ninstalled_at=2026-11-16T09:00:00Z\n")
		}
		w.fail2ban = test.summary
		card := GetStatus().Fail2ban
		if card.State != test.want || card.Reason != test.reason {
			t.Errorf("%s: %s/%s, want %s/%s", name, card.State, card.Reason, test.want, test.reason)
		}
		switch {
		case name == "by hand with the jails" && (card.Since == nil || !card.Since.Equal(stamp)):
			t.Errorf("%s: since %v, want the jail file's time", name, card.Since)
		case test.state && (card.Since == nil || card.Since.Day() != 16):
			t.Errorf("%s: since %v, want the state file's time", name, card.Since)
		}
	}
}

func TestUpdatesStates(t *testing.T) {
	w := newWorld(t)
	w.updatesOn()
	// set up by the administrator: no command, since the file that switched them on
	updates := GetStatus().Updates
	if !updates.Active || updates.By != ByAdministrator || updates.Command != "" || updates.CommandKind != "" || updates.Since == nil || filepath.Base(updates.Config) != "20auto-upgrades" {
		t.Errorf("by the administrator: %+v", updates)
	}
	// each of the three conditions
	forget()
	os.Remove(upgradeTimer)
	if updates := GetStatus().Updates; updates.Active || updates.Installed || updates.State != StateNotInstalled || updates.CommandKind != CommandInstall {
		t.Errorf("without the timer and without the piece: %+v", updates)
	}
	w.state(PieceUpdates, "piece=unattended-upgrades\nformat=1\ninstalled_at=2026-11-15T08:00:00Z\n")
	forget()
	if updates := GetStatus().Updates; updates.State != StateInactive || updates.Reason != reasonNoTimer || updates.CommandKind != CommandUninstall || updates.By != ByFork {
		t.Errorf("the piece without the timer: %+v", updates)
	}
	os.Symlink("/lib/systemd/system/apt-daily-upgrade.timer", upgradeTimer)
	aptFile(t, "99off", "APT::Periodic::Unattended-Upgrade \"0\";\n")
	forget()
	if updates := GetStatus().Updates; updates.State != StateInactive || updates.Reason != reasonNotEnabled || updates.Config != "" {
		t.Errorf("switched off: %+v", updates)
	}
	os.Remove(filepath.Join(aptConfigDir, "99off"))
	os.Chmod(upgradeBinary, 0644)
	forget()
	if updates := GetStatus().Updates; updates.State != StateInactive || updates.Reason != reasonNoBinary {
		t.Errorf("without the program: %+v", updates)
	}
	os.Chmod(upgradeBinary, 0755)
	forget()
	if updates := GetStatus().Updates; updates.State != StateActive || updates.By != ByFork {
		t.Errorf("all there: %+v", updates)
	}
}

func TestReadingsAreReusedForTenSeconds(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-loaded.json")
	for i := 0; i < 20; i++ {
		GetStatus()
	}
	if calls := w.nftCalls.Load(); calls != 1 {
		t.Errorf("nft ran %d times for 20 requests, want once", calls)
	}
	cacheLock.Lock()
	cachedAt = time.Now().Add(-cacheFor - time.Second)
	cacheLock.Unlock()
	GetStatus()
	if calls := w.nftCalls.Load(); calls != 2 {
		t.Errorf("nft ran %d times after the reading aged, want twice", calls)
	}
}

func TestCommandQuotesAnUnusualFolder(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "my cosmos's")
	w.write(filepath.Join(dir, "fork", PieceGuard, "install.sh"), "", 0644)
	executable = func() (string, error) { return filepath.Join(dir, "cosmos"), nil }
	got, _ := command(PieceGuard, false)
	want := "sudo sh '" + strings.ReplaceAll(filepath.Join(dir, "fork", PieceGuard, "install.sh"), "'", `'\''`) + "'"
	if got != want {
		t.Errorf("command %q, want %q", got, want)
	}
	// the installer of another piece is not there: /opt/cosmos
	if got, _ := command(PieceUpdates, true); got != "sudo sh /opt/cosmos/fork/unattended-upgrades/uninstall.sh" {
		t.Errorf("command %q", got)
	}
}

func serve(t *testing.T, method string, auth *utils.AuthContext) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://cosmos.example/cosmos/api/protection", nil)
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	w := httptest.NewRecorder()
	StatusRoute(w, req)
	return w
}

func TestRouteIsForAdministratorsAndGetOnly(t *testing.T) {
	w := newWorld(t)
	w.noTable()
	reader := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	user := &utils.AuthContext{Nickname: "bob", Permissions: []utils.Permission{utils.PERM_LOGIN}}

	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		if code := serve(t, method, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", method, code)
		}
		if answer := serve(t, method, user); answer.Code < 400 || strings.Contains(answer.Body.String(), "guard") {
			t.Errorf("%s as a user who is not an administrator: %d %s", method, answer.Code, answer.Body.String())
		}
		if method != "GET" {
			if code := serve(t, method, reader).Code; code != http.StatusMethodNotAllowed {
				t.Errorf("%s as an administrator: %d, want 405", method, code)
			}
		}
	}
	if calls := w.nftCalls.Load(); calls != 0 {
		t.Errorf("refused requests ran nft %d times", calls)
	}

	answer := serve(t, "GET", reader)
	if answer.Code != http.StatusOK {
		t.Fatalf("GET as an administrator: %d", answer.Code)
	}
	var body struct {
		Status string                     `json:"status"`
		Data   map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(answer.Body.Bytes(), &body); err != nil || body.Status != "OK" {
		t.Fatalf("%v %s", err, answer.Body.String())
	}
	keys := func(raw map[string]json.RawMessage) []string {
		list := []string{}
		for key := range raw {
			list = append(list, key)
		}
		sort.Strings(list)
		return list
	}
	if got := keys(body.Data); !reflect.DeepEqual(got, []string{"cosmosPort", "fail2ban", "guard", "time", "updates"}) {
		t.Errorf("keys %v", got)
	}
	var guard map[string]json.RawMessage
	json.Unmarshal(body.Data["guard"], &guard)
	for _, key := range []string{"installed", "active", "state", "stateFile", "command", "commandKind", "table", "publicTcp", "publicUdp", "portMissing", "keptOpen", "otherFilters", "vpnFound", "udpNotOpen", "trustedInterfaces", "outsideSinceInstall"} {
		if _, found := guard[key]; !found {
			t.Errorf("guard has no %q: %s", key, body.Data["guard"])
		}
	}
	var table map[string]json.RawMessage
	json.Unmarshal(guard["table"], &table)
	if got := keys(table); !reflect.DeepEqual(got, []string{"failClosedInterfaces", "forwardFilters", "forwardHooked", "homeNetworks", "inputFilters", "inputHooked", "ipv4PassInterfaces", "ipv4Passed", "lanInterfaces", "loaded", "pendingPrefixes", "publicTcp", "publicUdp", "rpfInterfaces", "sshAllowed", "sshPorts", "trustedInterfaces", "vpnHomeInterfaces", "vpnUdp", "wanInterfaces"}) {
		t.Errorf("table keys %v", got)
	}
	var updates map[string]json.RawMessage
	json.Unmarshal(body.Data["updates"], &updates)
	for _, key := range []string{"installed", "active", "state", "binaryInstalled", "enabled", "timerEnabled", "incomplete"} {
		if _, found := updates[key]; !found {
			t.Errorf("updates has no %q", key)
		}
	}
	var f2b map[string]json.RawMessage
	json.Unmarshal(body.Data["fail2ban"], &f2b)
	for _, key := range []string{"installed", "active", "state", "clientInstalled", "running", "configInstalled"} {
		if _, found := f2b[key]; !found {
			t.Errorf("fail2ban has no %q", key)
		}
	}
}

// Active only when the rules are in force: loaded, hooked, the chains
// holding their rules, an outside interface to filter.
func TestGuardActiveOnlyWhenItFilters(t *testing.T) {
	w := newWorld(t)
	w.state(PieceGuard, guardState)
	for fixture, want := range map[string]struct {
		state, reason, repair string
	}{
		"nft-guard-v2-ipv4.json":       {StateActive, "", ""},
		"nft-guard-v2-lan.json":        {StateActive, "", ""},
		"nft-guard-v2-failclosed.json": {StateActive, "", ""},
		"nft-guard-v2-flushed.json":    {StateInactive, reasonChainEmpty, repairCommand},
		"nft-guard-v2-nowan.json":      {StateInactive, reasonNoOutside, ""},
	} {
		forget()
		w.table(fixture)
		guard := GetStatus().Guard
		if guard.State != want.state || guard.Reason != want.reason || guard.Active != (want.state == StateActive) || guard.RepairCommand != want.repair {
			t.Errorf("%s: %s/%s active %v repair %q, want %s/%s repair %q", fixture, guard.State, guard.Reason, guard.Active, guard.RepairCommand, want.state, want.reason, want.repair)
		}
	}
	// the forward chain flushed alone
	forget()
	w.table("nft-guard-v2-ipv4.json")
	w.nftOut = []byte(strings.Replace(string(w.nftOut), `{"counter": "dropped_forward"}, {"drop": null}`, `{"counter": "dropped_forward"}, {"accept": null}`, 1))
	if guard := GetStatus().Guard; guard.State != StateInactive || guard.Reason != reasonChainEmpty {
		t.Errorf("forward without its drop: %s/%s", guard.State, guard.Reason)
	}
	// not hooked: the reload repairs it too
	forget()
	w.table("nft-guard-v2-ipv4.json")
	w.nftOut = []byte(strings.Replace(string(w.nftOut), `"hook": "input"`, `"hook": "output"`, 1))
	if guard := GetStatus().Guard; guard.Reason != reasonNotHooked || guard.RepairCommand != repairCommand {
		t.Errorf("not hooked: %s repair %q", guard.Reason, guard.RepairCommand)
	}
	// an unreadable record does not hide why it does not filter
	forget()
	w.table("nft-guard-v2-flushed.json")
	w.state(PieceGuard, strings.Replace(guardState, "format=1", "format=9", 1))
	if guard := GetStatus().Guard; guard.Reason != reasonChainEmpty || guard.StateFile != fileInvalid {
		t.Errorf("flushed with an unreadable record: %s, state file %s", guard.Reason, guard.StateFile)
	}
}

func TestGuardAllowancesEndFromTheReading(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-v2-ipv4.json")
	status := GetStatus()
	allowed := status.Guard.Table.SSHAllowed
	if len(allowed) != 2 || allowed[1].Address != "2001:db8:2::20" || allowed[1].Until == nil || allowed[0].Until == nil {
		t.Fatalf("allowances %+v", allowed)
	}
	want := status.Time.Add(86399 * time.Second)
	if diff := allowed[1].Until.Sub(want); diff < -time.Second || diff > time.Second {
		t.Errorf("until %v, want %v", allowed[1].Until, want)
	}
	if got := withUntil([]Allowance{{Address: "192.0.2.1"}}, status.Time); got[0].Until != nil {
		t.Errorf("an allowance without a timeout got an end: %v", got[0].Until)
	}
}

func TestGuardCarriesWhatTheInstallerRecorded(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-v2-lan.json")
	w.state(PieceGuard, strings.Replace(guardState, "ipv4=yes", "ipv4=no\nipv4_reason=behind-router\nexposed=no\nother_filters=inet firewalld;inet cosmos_guard", 1))
	guard := GetStatus().Guard
	if guard.IPv4Reason != "behind-router" || guard.Exposed == nil || *guard.Exposed || !reflect.DeepEqual(guard.OtherFilters, []string{"inet firewalld", "inet cosmos_guard"}) {
		t.Errorf("recorded: reason %q exposed %v others %v", guard.IPv4Reason, guard.Exposed, guard.OtherFilters)
	}
	if guard.IPv4 == nil || *guard.IPv4 || !guard.Table.IPv4Passed {
		t.Errorf("ipv4 %v, passed %v", guard.IPv4, guard.Table.IPv4Passed)
	}
}

// The coordinator's principle: never Active while something the guard does
// not filter is reachable from outside. A trusted uplink is such a path.
func TestGuardWithATrustedUplinkIsNotActive(t *testing.T) {
	w := newWorld(t)
	w.state(PieceGuard, guardState+"trusted_if=tun0\n")
	w.table("nft-guard-v3-trusted.json")
	guard := GetStatus().Guard
	if guard.Active || guard.State != StateInactive || guard.Reason != reasonTrusted || guard.RepairCommand != "" {
		t.Errorf("trusted uplink: %s/%s active %v repair %q", guard.State, guard.Reason, guard.Active, guard.RepairCommand)
	}
	if !reflect.DeepEqual(guard.Table.TrustedInterfaces, []string{"tun0"}) || !reflect.DeepEqual(guard.TrustedInterfaces, []string{"tun0"}) {
		t.Errorf("trusted %v / recorded %v", guard.Table.TrustedInterfaces, guard.TrustedInterfaces)
	}
	// a trust recorded but not in force (the interface is not up): Active
	for _, fixture := range []string{"nft-guard-v3-filtered.json", "nft-guard-v3-pass4.json"} {
		forget()
		w.table(fixture)
		if guard := GetStatus().Guard; !guard.Active || guard.State != StateActive || guard.Reason != "" {
			t.Errorf("%s: %s/%s", fixture, guard.State, guard.Reason)
		}
	}
	// a flushed chain still says so first
	forget()
	w.table("nft-guard-v3-trusted.json")
	w.nftOut = []byte(strings.Replace(string(w.nftOut), `"expr": [{"counter": "dropped_input"}, {"drop": null}]`, `"expr": [{"counter": "dropped_input"}, {"accept": null}]`, 1))
	if guard := GetStatus().Guard; guard.Reason != reasonChainEmpty {
		t.Errorf("flushed with a trusted uplink: %s", guard.Reason)
	}
}

func TestGuardCarriesWhatTheInstallerFound(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-v3-pass4.json")
	w.state(PieceGuard, guardState+"cloud=Amazon EC2\nvirt=kvm\nvpn_found=tailscale:41641;wireguard wg0:unknown\nudp_not_open=5353,51820\nssh_mode=from\n")
	guard := GetStatus().Guard
	port := 41641
	if guard.Cloud != "Amazon EC2" || guard.Virt != "kvm" || guard.SSHMode != "from" || !reflect.DeepEqual(guard.UDPNotOpen, []int{5353, 51820}) ||
		!reflect.DeepEqual(guard.VPNFound, []VPN{{Kind: "tailscale", Port: &port}, {Kind: "wireguard", Interface: "wg0"}}) {
		t.Errorf("found: %+v", guard)
	}
	answer, _ := json.Marshal(guard)
	for _, part := range []string{`"vpnFound":[{"kind":"tailscale","port":41641},{"kind":"wireguard","interface":"wg0"}]`, `"cloud":"Amazon EC2"`, `"ipv4PassInterfaces":["eth0"]`, `"pendingPrefixes":["2001:db8:5::/64"]`, `"droppedRpf":{"packets":0,"bytes":0}`} {
		if !strings.Contains(string(answer), part) {
			t.Errorf("the answer has no %s: %s", part, answer)
		}
	}
}

func TestGuardSaysWhetherItsWatcherAndTimerRun(t *testing.T) {
	w := newWorld(t)
	w.state(PieceGuard, guardState)
	w.table("nft-guard-v3-filtered.json")
	// no marks: not known, no warning
	guard := GetStatus().Guard
	if guard.Liveness == nil || guard.Liveness.WatcherAt != nil || guard.Liveness.WatcherStale || guard.Liveness.RefreshStale {
		t.Errorf("no marks: %+v", guard.Liveness)
	}
	now := time.Now()
	touch(t, alivePath, now.Add(-5*time.Minute))
	touch(t, refreshPath, now.Add(-10*time.Second))
	forget()
	guard = GetStatus().Guard
	if guard.Liveness == nil || !guard.Liveness.WatcherStale || guard.Liveness.RefreshStale || guard.Liveness.WatcherAt == nil {
		t.Errorf("watcher stopped: %+v", guard.Liveness)
	}
	// still Active: the rules filter, the warning says the rest
	if !guard.Active {
		t.Errorf("a stopped watcher made the guard %s", guard.State)
	}
	touch(t, refreshPath, now.Add(-10*time.Minute))
	forget()
	if live := GetStatus().Guard.Liveness; !live.WatcherStale || !live.RefreshStale {
		t.Errorf("both stopped: %+v", live)
	}
	// a guard whose table is not loaded says nothing of them
	forget()
	w.noTable()
	if guard := GetStatus().Guard; guard.Liveness != nil {
		t.Errorf("not loaded: %+v", guard.Liveness)
	}
}

func TestGuardSaysWhatBecameOutsideAndThatItsTableKeepsBeingRemoved(t *testing.T) {
	w := newWorld(t)
	w.table("nft-guard-v3-filtered.json") // filters eth0
	now := time.Now().UTC().Truncate(time.Second)
	cases := []struct {
		state string
		since []string
	}{
		{guardState, []string{}},                               // not recorded: nothing said
		{guardState + "outside_at_install=eth0\n", []string{}}, // as at install
		{guardState + "outside_at_install=\n", []string{"eth0"}},
		{guardState + "outside_at_install=wlan0\n", []string{"eth0"}},
	}
	for _, c := range cases {
		forget()
		w.state(PieceGuard, c.state)
		if guard := GetStatus().Guard; !reflect.DeepEqual(guard.OutsideSinceInstall, c.since) {
			t.Errorf("%q: outside since install %v, want %v", c.state, guard.OutsideSinceInstall, c.since)
		}
	}
	// fail-closed filters every interface: not a new uplink
	forget()
	w.table("nft-guard-v2-failclosed.json")
	w.state(PieceGuard, guardState+"outside_at_install=\n")
	if guard := GetStatus().Guard; len(guard.OutsideSinceInstall) != 0 {
		t.Errorf("fail-closed: %v", guard.OutsideSinceInstall)
	}
	w.table("nft-guard-v3-filtered.json")
	removed := func(at time.Time, count string) *Removals {
		forget()
		w.state(PieceGuard, guardState+"table_removed_hour="+count+"\ntable_removed_at="+at.Format(time.RFC3339)+"\n")
		return GetStatus().Guard.TableRemoved
	}
	if got := removed(now.Add(-10*time.Minute), "25"); got == nil || got.Count != 25 || !got.At.Equal(now.Add(-10*time.Minute)) {
		t.Errorf("ten minutes ago: %+v", got)
	}
	if got := removed(now.Add(-2*time.Hour), "25"); got != nil {
		t.Errorf("two hours ago: %+v, want nothing", got)
	}
	if got := removed(now.Add(time.Hour), "25"); got != nil {
		t.Errorf("in the future: %+v, want nothing", got)
	}
	forget()
	w.state(PieceGuard, guardState+"table_removed_hour=25\ntable_removed_at="+now.Add(-time.Minute).Format(time.RFC3339)+"\n")
	answer, _ := json.Marshal(GetStatus().Guard)
	if !strings.Contains(string(answer), `"tableRemoved":{"count":25,"at":"`) || !strings.Contains(string(answer), `"outsideSinceInstall":[]`) || !strings.Contains(string(answer), `"vpnHomeInterfaces":[]`) {
		t.Errorf("the answer: %s", answer)
	}
}
