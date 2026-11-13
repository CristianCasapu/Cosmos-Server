package cloudflare

// Fork addition: the preview of what connecting a domain writes.

import (
	"context"
	"testing"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/utils"
)

func rowsByName(preview Preview) map[string]PreviewRow {
	rows := map[string]PreviewRow{}
	for _, row := range preview.Rows {
		rows[row.Name] = row
	}
	return rows
}

func TestPreviewBeforeConnecting(t *testing.T) {
	// the domain is not connected yet; the panel is about to be named
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = nil
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "www", UseHost: true, Host: "www.example.org", Mode: "SERVAPP", Target: "http://x:80"})
	})
	env.fake.Calls(true)

	preview, err := MakePreview(context.Background(), goodToken, "example.org", "cosmos.example.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	if writes := cffake.Writes(env.fake.Calls(false)); writes != 0 {
		t.Fatalf("the preview changed something at Cloudflare: %+v", env.fake.Calls(false))
	}

	rows := rowsByName(preview)
	want := map[string]string{
		"cosmos.example.org":   "create",
		"radarr.example.org":   "create",
		"jellyfin.example.org": "create",
		"www.example.org":      "conflict", // an alias of the website: left alone
	}
	if len(rows) != len(want) {
		t.Errorf("rows: %+v", preview.Rows)
	}
	for name, action := range want {
		if rows[name].Action != action {
			t.Errorf("%s: %s, want %s", name, rows[name].Action, action)
		}
	}
	if rows["cosmos.example.org"].Role != "panel" || preview.Rows[0].Name != "cosmos.example.org" {
		t.Errorf("the panel's row: %+v", preview.Rows)
	}
	if rows["radarr.example.org"].New != "A "+testAddress || rows["www.example.org"].Current != "CNAME example.org" {
		t.Errorf("contents: %+v", preview.Rows)
	}
	if _, listed := rows["radarr.local"]; listed {
		t.Error("a local address is in the preview")
	}

	// every existing record is listed as not touched, the apex first
	if len(preview.Untouched) != 5 || !preview.Untouched[0].Apex || preview.Untouched[0].Name != "example.org" {
		t.Errorf("untouched: %+v", preview.Untouched)
	}
	if preview.Address != testAddress || preview.CanEdit != "yes" {
		t.Errorf("address %q, canEdit %q", preview.Address, preview.CanEdit)
	}

	// with the admin's tick the alias is replaced
	preview, _ = MakePreview(context.Background(), goodToken, "example.org", "cosmos.example.org", []string{"www.example.org"})
	if row := rowsByName(preview)["www.example.org"]; row.Action != "replace" || row.New != "A "+testAddress {
		t.Errorf("www with the tick: %+v", row)
	}
	for _, record := range preview.Untouched {
		if record.Name == "www.example.org" {
			t.Errorf("a record that will be replaced is listed as not touched: %+v", record)
		}
	}
}

func TestPreviewOfTheApexAndOfAConnectedDomain(t *testing.T) {
	env := setup(t, nil)
	pass(t) // radarr and jellyfin exist, with Cosmos' markers

	// the domain itself as the panel's name: listed, never written, even with a tick
	preview, err := MakePreview(context.Background(), goodToken, "example.org", "example.org", []string{"example.org"})
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsByName(preview)
	if row := rows["example.org"]; row.Action != "protected" || row.Role != "panel" || !row.Proxied || row.PointsHere || row.New != "" {
		t.Errorf("the apex: %+v", row)
	}
	if !preview.Apex.Exists || !preview.Apex.Proxied || preview.Apex.PointsHere {
		t.Errorf("what the apex holds: %+v", preview.Apex)
	}
	// once it points at this server (through Cloudflare), the preview says so
	for _, record := range env.fake.Records("example.org") {
		if record.Name == "example.org" && record.Type == "A" {
			env.fake.SetContent("example.org", record.ID, testAddress)
		}
	}
	here, _ := MakePreview(context.Background(), goodToken, "example.org", "example.org", nil)
	if row := rowsByName(here)["example.org"]; row.Action != "protected" || !row.Proxied || !row.PointsHere || !here.Apex.PointsHere {
		t.Errorf("the apex pointing here: %+v %+v", row, here.Apex)
	}
	if rows["radarr.example.org"].Action != "keep" || rows["jellyfin.example.org"].Action != "keep" {
		t.Errorf("records that are already right: %+v", preview.Rows)
	}
	for _, record := range preview.Untouched {
		if record.Type == "TXT" && record.Name != "example.org" {
			t.Errorf("a marker of Cosmos is listed among the existing records: %+v", record)
		}
	}

	// the address changed since: an own record gets the new one
	knownAddress = func() string { return "203.0.113.99" }
	preview, _ = MakePreview(context.Background(), goodToken, "example.org", "", nil)
	if row := rowsByName(preview)["radarr.example.org"]; row.Action != "update" || row.New != "A 203.0.113.99" {
		t.Errorf("radarr after a change of address: %+v", row)
	}

	// a domain that was detached meanwhile: its own record will go
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.ProxyConfig.Routes = config.HTTPConfig.ProxyConfig.Routes[:2]
		return nil
	})
	knownAddress = func() string { return testAddress }
	preview, _ = MakePreview(context.Background(), goodToken, "example.org", "", nil)
	if row := rowsByName(preview)["jellyfin.example.org"]; row.Action != "remove" {
		t.Errorf("jellyfin, detached: %+v (%+v)", row, preview.Rows)
	}

	// the address is not known yet: nothing is compared with a made-up one
	knownAddress = func() string { return "" }
	preview, _ = MakePreview(context.Background(), goodToken, "example.org", "new.example.org", nil)
	rows = rowsByName(preview)
	if rows["radarr.example.org"].Action != "keep" || rows["new.example.org"].Action != "create" || rows["new.example.org"].New != "A" {
		t.Errorf("without a known address: %+v", preview.Rows)
	}
	_ = env
}

func TestPreviewWarnings(t *testing.T) {
	env := setup(t, nil)
	env.fake.AddZone("example.com",
		cffake.Record{Type: "CAA", Name: "example.com", Content: `0 issue "digicert.com"`},
		cffake.Record{Type: "A", Name: "*.example.com", Content: "198.51.100.1"},
	)
	env.fake.SetSSLMode("example.com", "flexible")

	preview, err := MakePreview(context.Background(), otherToken, "example.com", "cosmos.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, warning := range preview.Warnings {
		has[warning] = true
	}
	if !has["caa"] || !has["wildcard"] {
		t.Errorf("warnings: %v", preview.Warnings)
	}
	if preview.SSLMode != "flexible" {
		t.Errorf("SSL mode: %q", preview.SSLMode)
	}

	// a token that may not read the setting: unknown, not an error
	preview, err = MakePreview(context.Background(), goodToken, "example.org", "", nil)
	if err != nil || preview.SSLMode != "" || len(preview.Warnings) != 0 {
		t.Errorf("preview with a DNS-only token: %+v %v", preview, err)
	}

	// a domain the token does not see
	if _, err := MakePreview(context.Background(), goodToken, "example.net", "", nil); kindOf(err) != "not_found" {
		t.Errorf("a domain outside the token: %v", err)
	}
}

func TestPreviewRouteChecksItsInput(t *testing.T) {
	setup(t, nil)
	bad := []map[string]interface{}{
		{"token": goodToken, "zone": "not a domain"},
		{"token": goodToken, "zone": "example.org/../x"},
		{"token": goodToken, "zone": "10.0.0.1"},
		{"token": goodToken, "zone": "example.org", "panel": "cosmos example"},
		{"token": goodToken, "zone": "example.org", "takeOver": []string{"a b"}},
		{"token": "cfk_0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a1234", "zone": "example.org"},
		{"token": "", "zone": "example.net"}, // nothing stored for it
		{"token": goodToken, "zone": "example.org", "unknown": true},
	}
	for _, body := range bad {
		if code, answer := post(t, PreviewRoute, body); code != 400 {
			t.Errorf("%v: status %d %v", body, code, answer)
		}
	}
	many := make([]string, maxTakeOver+1)
	for i := range many {
		many[i] = "a.example.org"
	}
	if code, _ := post(t, PreviewRoute, map[string]interface{}{"token": goodToken, "zone": "example.org", "takeOver": many}); code != 400 {
		t.Errorf("too many names: status %d", code)
	}

	// the stored token serves for a connected domain
	code, answer := post(t, PreviewRoute, map[string]interface{}{"token": "***", "zone": "example.org", "panel": "cosmos.example.org"})
	if code != 200 || data(answer)["zone"] != "example.org" {
		t.Errorf("preview with the stored token: %d %v", code, answer)
	}
	// Cloudflare's refusal is passed on as a kind, not as a server error of Cosmos
	code, answer = post(t, PreviewRoute, map[string]interface{}{"token": "wrong0123456789abcdefghijklmnopqrstuvwxyzABCD", "zone": "example.org"})
	if code != 502 || answer["reason"] != "invalid_token" {
		t.Errorf("preview with an unknown token: %d %v", code, answer)
	}
}

// Before a domain is connected Cosmos may not know its public address: the
// preview then asks Cloudflare's API host how it sees this server, so that it
// can tell that the domain's own record already points here.
func TestPreviewLearnsTheAddressFromCloudflareWhenItIsNotKnown(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = nil
		config.HTTPConfig.AdvertisedAddress = ""
	})
	knownAddress = func() string { return "" }
	for _, record := range env.fake.Records("example.org") {
		if record.Name == "example.org" && record.Type == "A" {
			env.fake.SetContent("example.org", record.ID, testAddress)
		}
	}

	env.fake.SetSeenAs(testAddress)
	preview, err := MakePreview(context.Background(), goodToken, "example.org", "example.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Address != testAddress || !preview.Apex.PointsHere || !preview.Apex.Proxied {
		t.Errorf("address %q, apex %+v", preview.Address, preview.Apex)
	}

	// an answer that is no public IPv4 address is not used
	for _, seen := range []string{"", "192.168.1.10", "2001:db8::1", "not an address", "127.0.0.1"} {
		env.fake.SetSeenAs(seen)
		preview, err := MakePreview(context.Background(), goodToken, "example.org", "example.org", nil)
		if err != nil || preview.Address != "" || preview.Apex.PointsHere {
			t.Errorf("seen as %q: address %q, apex %+v, %v", seen, preview.Address, preview.Apex, err)
		}
	}
}
