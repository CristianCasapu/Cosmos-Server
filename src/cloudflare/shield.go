package cloudflare

// Fork addition: Cloudflare's "proxied" flag for the records Cosmos manages.
//
// Whether an application is served through Cloudflare's proxy ("the shield")
// or directly is the admin's choice per hostname
// (HTTPConfig.Cloudflare.Shielded). Upstream's reconciler keeps the address
// records in place through libdns, which can neither set nor read the flag;
// this file sets it, in two places:
//
//   - a record created for a shielded hostname is created proxied (the
//     wrapper around the libdns provider), so that a change of the server's
//     address never publishes the new one for such a name;
//   - after every pass that ended well, the flags of the names of that pass
//     are compared with the choice and corrected (syncShield): this is what
//     flipping the switch does, and what puts a flag back that was changed by
//     hand at Cloudflare.
//
// Only names of the pass are looked at: hostnames Cosmos serves and whose
// records it manages. Any other record of the zone is never changed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	libdnscloudflare "github.com/libdns/cloudflare"
	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

// how often the flags are read again when nothing changed
const flagReadInterval = 10 * time.Minute

// The state of a hostname's record at Cloudflare.
const (
	RecordOK        = "ok"        // the record exists and its flag is as chosen
	RecordPending   = "pending"   // not written or not checked yet
	RecordConflict  = "conflict"  // a record Cosmos did not create is there: left alone
	RecordProtected = "protected" // the domain's own name: never written by Cosmos
	RecordError     = "error"     // Cloudflare refused, or could not be reached
)

// HostRecord is what is known about one hostname's record.
type HostRecord struct {
	State string `json:"state"`
	// Reason is the kind of an error (see APIError.Kind), Message Cloudflare's
	// own text
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// Proxied is the flag as last seen at Cloudflare. For a name Cosmos
	// leaves alone (protected, conflict) Seen tells whether a record was found
	// there at all, and PointsHere whether it points at this server.
	Proxied    bool `json:"proxied"`
	Seen       bool `json:"seen,omitempty"`
	PointsHere bool `json:"pointsHere,omitempty"`
}

type zoneSync struct {
	signature string
	checked   time.Time
	failures  int
	nextTry   time.Time
	zoneID    string
	hosts     map[string]HostRecord
	skipped   map[string]string
	// how Cloudflare talks to this server for the zone, read at most once an
	// hour while a hostname is shielded: off, flexible, full, strict,
	// origin_pull, "unreadable" when the token may not read it, "" when not read
	sslMode    string
	sslChecked time.Time
}

var (
	syncMu sync.Mutex
	synced = map[string]*zoneSync{} // by the domain's name
)

func zoneState(name string) *zoneSync {
	state, ok := synced[name]
	if !ok {
		state = &zoneSync{hosts: map[string]HostRecord{}, skipped: map[string]string{}}
		synced[name] = state
	}
	return state
}

func init() {
	dnsrecords.ForkWrapProvider = wrapProvider
	dnsrecords.ForkFilterDesired = func(zone utils.DNSZoneConfig, providerZone string, desired dnsrecords.Desired, actual []libdns.Record) (dnsrecords.Desired, []libdns.Record) {
		if !isCloudflareZone(zone) {
			return desired, actual
		}
		kept, visible, skipped := filterDesired(providerZone, desired, actual, utils.GetMainConfig().HTTPConfig.Cloudflare.TakeOver, dnsrecords.OwnerID())
		syncMu.Lock()
		zoneState(zone.Zone).skipped = skipped
		syncMu.Unlock()
		return kept, visible
	}
	dnsrecords.ForkAfterReconcile = func(zone utils.DNSZoneConfig, providerZone string, desired dnsrecords.Desired, wrote bool) {
		if isCloudflareZone(zone) {
			syncShield(zone, providerZone, desired, wrote)
		}
	}
}

// shieldProvider creates the address records of shielded hostnames proxied;
// everything else is the libdns provider's.
type shieldProvider struct {
	dnsrecords.Provider
	token string
	// zone ids by zone name, as looked up
	ids sync.Map
}

func wrapProvider(zone utils.DNSZoneConfig, provider dnsrecords.Provider) dnsrecords.Provider {
	if !isCloudflareZone(zone) {
		return provider
	}
	if inner, ok := provider.(*libdnscloudflare.Provider); ok {
		// the calls of the library get this package's client: a timeout, no redirects
		inner.HTTPClient = httpClient
	}
	return &shieldProvider{Provider: provider, token: storedToken(zone)}
}

func (p *shieldProvider) zoneID(ctx context.Context, name string) (string, error) {
	if id, ok := p.ids.Load(name); ok {
		return id.(string), nil
	}
	id, err := lookupZoneID(ctx, NewClient(p.token), name)
	if err == nil {
		p.ids.Store(name, id)
	}
	return id, err
}

func lookupZoneID(ctx context.Context, client *Client, name string) (string, error) {
	zones, _, err := client.ListZones(ctx, name)
	if err != nil {
		return "", err
	}
	for _, zone := range zones {
		if zone.Name == name {
			return zone.ID, nil
		}
	}
	return "", &APIError{Kind: "not_found", Message: "the token does not see this domain"}
}

func (p *shieldProvider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	zoneName := strings.ToLower(strings.TrimSuffix(zone, "."))
	shielded := utils.GetMainConfig().HTTPConfig.Cloudflare.Shielded

	plain := []libdns.Record{}
	created := []libdns.Record{}
	for _, record := range records {
		rr := record.RR()
		fqdn := fqdnOf(rr.Name, zoneName)
		if rr.Type != "A" || p.token == "" || !utils.ShieldedIn(shielded, fqdn) {
			plain = append(plain, record)
			continue
		}
		id, err := p.zoneID(ctx, zoneName)
		if err != nil {
			return created, err
		}
		if err := NewClient(p.token).CreateRecord(ctx, id, Record{Type: "A", Name: fqdn, Content: rr.Data, Proxied: true, TTL: 1}); err != nil {
			return created, err
		}
		created = append(created, record)
	}
	if len(plain) == 0 {
		return created, nil
	}
	added, err := p.Provider.AppendRecords(ctx, zone, plain)
	return append(created, added...), err
}

func tokenMark(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

// syncShield brings the proxied flags of the names of a pass in line with the
// admin's choice. It talks to Cloudflare only when the choice changed, the
// pass wrote records, the last attempt failed and its pause is over, or the
// flags have not been read for ten minutes.
func syncShield(zone utils.DNSZoneConfig, providerZone string, desired dnsrecords.Desired, wrote bool) {
	token := storedToken(zone)
	shielded := utils.GetMainConfig().HTTPConfig.Cloudflare.Shielded

	// what each name of the pass should be: its addresses and its flag
	want := map[string]bool{}
	addresses := map[string][]string{}
	parts := []string{tokenMark(token)}
	for fqdn, ips := range desired {
		name := strings.ToLower(strings.TrimSuffix(fqdn, "."))
		if strings.HasPrefix(name, "*.") || len(ips) == 0 {
			continue
		}
		want[name] = utils.ShieldedIn(shielded, name)
		addresses[name] = ips
		sorted := append([]string{}, ips...)
		sort.Strings(sorted)
		flag := "direct"
		if want[name] {
			flag = "proxied"
		}
		parts = append(parts, name+"="+flag+"@"+strings.Join(sorted, ","))
	}
	sort.Strings(parts[1:])
	signature := strings.Join(parts, " ")

	syncMu.Lock()
	state := zoneState(zone.Zone)
	now := time.Now()
	same := state.signature == signature && !wrote
	if same && state.failures == 0 && now.Sub(state.checked) < flagReadInterval {
		syncMu.Unlock()
		return
	}
	if same && state.failures > 0 && now.Before(state.nextTry) {
		syncMu.Unlock()
		return
	}
	zoneID := state.zoneID
	syncMu.Unlock()

	hosts := map[string]HostRecord{}
	finish := func(err error) {
		syncMu.Lock()
		defer syncMu.Unlock()
		state.signature = signature
		state.checked = time.Now()
		state.hosts = hosts
		state.zoneID = zoneID
		if err == nil {
			state.failures = 0
			return
		}
		state.failures++
		pause := time.Minute
		if state.failures > 2 {
			pause = 15 * time.Minute
		}
		state.nextTry = time.Now().Add(pause)
		// the message carries Cloudflare's code and text, never the token
		utils.Warn("[Cloudflare] " + zone.Zone + ": the proxy setting of the records could not be brought in line: " + err.Error())
	}
	failAll := func(err error) {
		reason, message := kindOf(err), ""
		if apiErr, ok := err.(*APIError); ok {
			message = apiErr.Message
		}
		for name := range want {
			hosts[name] = HostRecord{State: RecordError, Reason: reason, Message: message}
		}
		finish(err)
	}

	if token == "" {
		failAll(&APIError{Kind: "invalid_token", Message: "no API token is stored for this domain"})
		return
	}
	client := NewClient(token)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if zoneID == "" {
		id, err := lookupZoneID(ctx, client, strings.ToLower(providerZone))
		if err != nil {
			failAll(err)
			return
		}
		zoneID = id
	}

	records, _, err := client.ListRecords(ctx, zoneID, "")
	if err != nil {
		if kindOf(err) == "not_found" {
			zoneID = "" // the zone was made anew at Cloudflare: look it up again
		}
		failAll(err)
		return
	}

	var firstErr error
	for name, proxied := range want {
		wantedIP := map[string]bool{}
		for _, ip := range addresses[name] {
			wantedIP[ip] = true
		}
		host := HostRecord{State: RecordPending}
		for _, record := range records {
			if record.Type != "A" || record.Name != name || !wantedIP[record.Content] {
				continue
			}
			host.State, host.Proxied = RecordOK, record.Proxied
			if record.Proxied == proxied {
				continue
			}
			if err := client.SetProxied(ctx, zoneID, record.ID, proxied, int(dnsrecords.DefaultTTL.Seconds())); err != nil {
				host = HostRecord{State: RecordError, Reason: kindOf(err), Proxied: record.Proxied}
				if apiErr, ok := err.(*APIError); ok {
					host.Message = apiErr.Message
				}
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			host.Proxied = proxied
			utils.Log("[Cloudflare] " + name + " is now served " + map[bool]string{true: "through Cloudflare's proxy", false: "directly"}[proxied])
		}
		hosts[name] = host
	}

	// the names of the domain Cosmos serves but leaves alone: how their
	// records are set, for the screens. Nothing is changed there.
	syncMu.Lock()
	left := map[string]string{}
	for name, why := range state.skipped {
		left[name] = why
	}
	syncMu.Unlock()
	address := knownAddress()
	for name := range left {
		host := HostRecord{State: RecordProtected}
		if left[name] == SkipConflict {
			host.State = RecordConflict
		}
		for _, record := range records {
			if record.Name == name && (record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME") {
				host.Seen = true
				host.Proxied = host.Proxied || record.Proxied
				host.PointsHere = host.PointsHere || (record.Type == "A" && address != "" && record.Content == address)
			}
		}
		hosts[name] = host
	}

	anyShielded := false
	for _, proxied := range want {
		anyShielded = anyShielded || proxied
	}
	for name := range left {
		anyShielded = anyShielded || utils.ShieldedIn(shielded, name)
	}
	syncMu.Lock()
	readMode := anyShielded && time.Since(state.sslChecked) > time.Hour
	syncMu.Unlock()
	if readMode {
		mode, err := client.SSLMode(ctx, zoneID)
		if err != nil {
			mode = ""
			if kind := kindOf(err); kind == "forbidden" || kind == "invalid_token" {
				mode = "unreadable"
			}
		}
		syncMu.Lock()
		state.sslMode, state.sslChecked = mode, time.Now()
		syncMu.Unlock()
	}
	finish(firstErr)
}

// forgetSync makes the next pass read the flags again (the choice changed).
func forgetSync() {
	syncMu.Lock()
	for _, state := range synced {
		state.signature = ""
	}
	syncMu.Unlock()
}

// RecordOf tells what is known about the record of a hostname Cosmos serves
// under a Cloudflare domain it manages the records of. ok is false for any
// other hostname.
func RecordOf(host string) (HostRecord, bool) {
	host = utils.CloudflareHostname(host)
	config := utils.GetMainConfig()
	zones := config.HTTPConfig.DNSZones
	index := utils.FindZone(zones, host)
	if index == -1 || !isCloudflareZone(zones[index]) || !zones[index].ManageRecords {
		return HostRecord{}, false
	}
	zone := zones[index]

	syncMu.Lock()
	state, known := synced[zone.Zone]
	var record HostRecord
	var seen bool
	var skip string
	if known {
		record, seen = state.hosts[host]
		skip = state.skipped[host]
	}
	syncMu.Unlock()

	if skip != "" {
		if !seen {
			record = HostRecord{State: RecordProtected}
			if skip == SkipConflict {
				record.State = RecordConflict
			}
		}
		return record, true
	}
	if status, ok := dnsrecords.GetStatus()[zone.Zone]; ok && status.LastError != "" {
		return HostRecord{State: RecordError, Reason: reasonOfReconcileError(status.LastError), Message: cleanText(status.LastError, 300)}, true
	}
	if zone.WildcardRecord && !seen {
		// answered by the domain's wildcard record: no record of its own
		return HostRecord{State: RecordOK}, true
	}
	if !seen {
		return HostRecord{State: RecordPending}, true
	}
	return record, true
}

// reasonOfReconcileError reads the kind of failure out of the text upstream's
// reconciler keeps (the libdns provider reports Cloudflare's answer as text).
func reasonOfReconcileError(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "http 401") || strings.Contains(lower, "invalid api token") || strings.Contains(lower, "code:1000 ") || strings.Contains(lower, "code:9109 "):
		return "invalid_token"
	case strings.Contains(lower, "http 403"):
		return "forbidden"
	case strings.Contains(lower, "expected 1 zone, got 0"):
		return "not_found"
	case strings.Contains(lower, "http 429"):
		return "rate_limited"
	case strings.Contains(lower, "api token"):
		return "invalid_token"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "no such host") || strings.Contains(lower, "connection") || strings.Contains(lower, "deadline"):
		return "unreachable"
	}
	return "other"
}
