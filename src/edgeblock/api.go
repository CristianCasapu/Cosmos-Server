package edgeblock

// The routes of the card, on the admin router:
//
//	GET  /cosmos/api/cloudflare/edge-blocks   the state (configuration readers; addresses for log readers too)
//	POST /cosmos/api/cloudflare/edge-blocks   {action, ...}  changes (configuration permission: password confirmation)
//
// Actions: enable (after the test of every zone's token), disable (every rule
// Cosmos made is removed), block {address, duration, mode}, unblock {key},
// unblockAll, forget {key} (an orphaned rule), retry. Bodies are bounded,
// unknown fields refused, every value checked here.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

const maxBody = 4 << 10

// the test rule of the proof
const testAddress = "192.0.2.1"

// the durations a manual block may have
var manualDurations = map[string]time.Duration{
	"1h": time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
}

// replaced by the tests
var isLocalAddress = func(ip string) bool { return false }

type request struct {
	Action   string `json:"action"`
	Address  string `json:"address"`
	Key      string `json:"key"`
	Duration string `json:"duration"`
	Mode     string `json:"mode"`
}

func answer(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

// failure answers with a fixed reason the card turns into a sentence.
func failure(w http.ResponseWriter, status int, reason string, extra map[string]interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]interface{}{"status": "error", "code": "EB010", "message": "Edge blocks: " + reason, "reason": reason}
	for k, v := range extra {
		body[k] = v
	}
	json.NewEncoder(w).Encode(body)
}

// ZoneState is what the card shows of a zone.
type ZoneState struct {
	Name     string    `json:"name"`
	Shielded int       `json:"shielded"`
	Proven   bool      `json:"proven"`
	ProvenAt time.Time `json:"provenAt,omitzero"`
	// TokenChanged: a test passed with another token
	TokenChanged bool     `json:"tokenChanged"`
	Problem      *Problem `json:"problem,omitempty"`
}

// RuleView is one line of the list.
type RuleView struct {
	Key     string    `json:"key"`
	Address string    `json:"address,omitempty"`
	Zone    string    `json:"zone"`
	Mode    string    `json:"mode"`
	Reason  string    `json:"reason"`
	State   string    `json:"state"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until"`
	Actor   string    `json:"actor,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// Status is the state for the card.
func (e *Engine) Status(withAddresses bool) map[string]interface{} {
	config := settings()
	proofs := map[string]utils.ForkEdgeProof{}
	for _, proof := range config.Proven {
		proofs[proof.Zone] = proof
	}
	cf := utils.GetMainConfig().HTTPConfig.Cloudflare

	e.mu.Lock()
	defer e.mu.Unlock()
	zones := []ZoneState{}
	for _, zone := range edgeZones() {
		if zone.Shielded == 0 {
			continue
		}
		proof, has := proofs[zone.Name]
		state := ZoneState{Name: zone.Name, Shielded: zone.Shielded, Proven: has && proof.Token == zone.TokenTag, TokenChanged: has && proof.Token != zone.TokenTag}
		if state.Proven {
			state.ProvenAt = proof.At
		}
		if problem, bad := e.problems[zone.Name]; bad {
			copy := problem
			state.Problem = &copy
		}
		zones = append(zones, state)
	}
	rules := []RuleView{}
	counts := map[string]int{}
	for _, r := range e.st.Rules {
		counts[r.State]++
		if !withAddresses || r.Reason == "test" {
			continue
		}
		rules = append(rules, RuleView{Key: r.Key, Address: r.Address, Zone: r.Zone, Mode: r.Mode, Reason: r.Reason, State: r.State, Since: r.Since, Until: r.Until, Actor: r.Actor, Note: r.Note})
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Since.After(rules[j].Since) })
	leftovers := []Leftover{}
	if withAddresses {
		for _, list := range e.leftovers {
			leftovers = append(leftovers, list...)
		}
	}
	jailsView := []map[string]interface{}{}
	for _, j := range jails {
		limits := thresholds(j.f2b, j.defaults)
		jailsView = append(jailsView, map[string]interface{}{"name": j.name, "maxRetry": limits.MaxRetry, "findTime": limits.FindTime, "banTime": limits.BanTime, "mode": j.mode})
	}
	repeat := repeatThresholds()
	jailsView = append(jailsView, map[string]interface{}{"name": "repeat", "maxRetry": repeat.MaxRetry, "findTime": repeat.FindTime, "banTime": repeat.BanTime, "mode": ModeBlock})

	return map[string]interface{}{
		"enabled":       config.Enabled,
		"zones":         zones,
		"rules":         rules,
		"counts":        counts,
		"live":          e.liveLocked(),
		"maxRules":      MaxRules,
		"maxPerHour":    MaxBlocksAnHour,
		"dropped":       e.st.Dropped,
		"droppedAt":     map[bool]interface{}{true: e.st.DroppedAt, false: nil}[e.st.Dropped > 0],
		"leftovers":     leftovers,
		"jails":         jailsView,
		"adminDays":     int(AdminFor / (24 * time.Hour)),
		"originPulls":   utils.OriginPullMode(cf) == utils.OriginPullEnforce,
		"pullsKind":     cf.OriginPulls.Kind,
		"durations":     []string{"1h", "1d", "7d", "30d"},
		"withAddresses": withAddresses,
	}
}

// StatusRoute answers GET and POST /api/cloudflare/edge-blocks.
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method == "POST" {
		changeRoute(w, req)
		return
	}
	if req.Method != "GET" {
		w.Header().Set("Allow", "GET, POST")
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	answer(w, std.Status(utils.HasPermission(req, utils.PERM_ADMIN_READ)))
}

var changeMu sync.Mutex

func changeRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		utils.HTTPError(w, "Request too large", http.StatusRequestEntityTooLarge, "EB011")
		return
	}
	var body request
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || len(body.Address) > 64 || len(body.Key) > 64 {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "EB012")
		return
	}
	changeMu.Lock()
	defer changeMu.Unlock()

	switch body.Action {
	case "enable":
		std.enable(w, req)
	case "disable":
		updateSettings(func(c *utils.ForkEdgeBlocksConfig) { c.Enabled = false })
		std.mu.Lock()
		for _, r := range append([]*Rule(nil), std.st.Rules...) {
			std.liftLocked(r, "off")
		}
		std.mu.Unlock()
		utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", "cloudflare-edge", "change", "disabled")
		answer(w, std.Status(true))
	case "block":
		std.manualBlock(w, req, body)
	case "unblock":
		key, ok := keyOfInput(body.Key)
		if !ok {
			failure(w, http.StatusBadRequest, "not_an_address", nil)
			return
		}
		found := false
		std.mu.Lock()
		for _, r := range append([]*Rule(nil), std.st.Rules...) {
			if r.Key == key && r.Reason != "test" {
				found = true
				std.liftLocked(r, "admin")
			}
		}
		delete(std.st.Offenders, key)
		std.mu.Unlock()
		if !found {
			failure(w, http.StatusNotFound, "not_blocked", nil)
			return
		}
		utils.AuditSecurity(req, utils.SecEdgeUnblocked, key, "reason", "admin")
		answer(w, std.Status(true))
	case "unblockAll":
		std.mu.Lock()
		for _, r := range append([]*Rule(nil), std.st.Rules...) {
			if r.State == StatePending || r.State == StateActive {
				std.liftLocked(r, "all")
			}
		}
		std.mu.Unlock()
		utils.AuditSecurity(req, utils.SecEdgeUnblocked, "", "reason", "all")
		answer(w, std.Status(true))
	case "forget":
		key, ok := keyOfInput(body.Key)
		if !ok {
			failure(w, http.StatusBadRequest, "not_an_address", nil)
			return
		}
		std.mu.Lock()
		for _, r := range append([]*Rule(nil), std.st.Rules...) {
			if r.Key == key && r.State == StateOrphaned {
				std.removeLocked(r)
				std.dirty = true
			}
		}
		std.mu.Unlock()
		answer(w, std.Status(true))
	case "retry":
		std.mu.Lock()
		std.pausedUntil = map[string]time.Time{}
		std.backoff = map[string]time.Duration{}
		std.reconciled = map[string]time.Time{}
		std.mu.Unlock()
		std.poke()
		answer(w, std.Status(true))
	default:
		utils.HTTPError(w, "Unknown action", http.StatusBadRequest, "EB013")
	}
}

func updateSettings(change func(c *utils.ForkEdgeBlocksConfig)) {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	config := utils.ReadConfigFromFile()
	change(&config.HTTPConfig.Cloudflare.EdgeBlocks)
	utils.SetBaseMainConfig(config)
}

// ProofResult is the outcome of the test of one zone's token.
type ProofResult struct {
	Zone   string `json:"zone"`
	OK     bool   `json:"ok"`
	Step   string `json:"step,omitempty"`   // list, create, delete: where it failed
	Reason string `json:"reason,omitempty"` // permission, unreachable, rate_limited, server, invalid_token, other
	// Inferred: Cloudflare refused the documentation address, so creation was
	// shown by the kind of refusal and deletion by the id nobody has
	Inferred bool `json:"inferred,omitempty"`
	// LeftTestRule: the test rule could not be deleted; the worker will
	LeftTestRule bool `json:"leftTestRule,omitempty"`
}

func reasonOf(err error) string {
	kind, _, _ := cloudflare.ErrorKind(err)
	switch kind {
	case "forbidden":
		return "permission"
	case "invalid_token", "malformed_token":
		return "invalid_token"
	case "rate_limited", "unreachable", "server", "not_found":
		return kind
	}
	return "other"
}

// prove tests a zone's token: may it list, create and delete IP Access Rules.
func (e *Engine) prove(ctx context.Context, zone cloudflare.EdgeZone) ProofResult {
	result := ProofResult{Zone: zone.Name}
	zoneID, err := zoneIDOf(ctx, zone.Token, zone.Name)
	if err != nil {
		result.Step, result.Reason = "zone", reasonOf(err)
		return result
	}
	e.mu.Lock()
	e.zoneIDs[zone.Name] = zoneID
	marker := e.st.markerText()
	e.mu.Unlock()

	api := clientOf(zone.Token)
	if _, _, err := api.ListAccessRules(ctx, zoneID, marker, ""); err != nil {
		result.Step, result.Reason = "list", reasonOf(err)
		return result
	}
	rule, err := api.CreateAccessRule(ctx, zoneID, testAddress, ModeChallenge, "Cosmos edge block test ["+marker+"], deleted at once")
	if err != nil {
		kind, status, _ := cloudflare.ErrorKind(err)
		if status != http.StatusBadRequest || kind == "forbidden" {
			result.Step, result.Reason = "create", reasonOf(err)
			return result
		}
		// the documentation address was refused (or a rule for it exists):
		// the refusal came after the permission check; deletion is shown
		// by the id nobody has
		switch api.CanDeleteAccessRules(ctx, zoneID) {
		case "yes":
			result.OK, result.Inferred = true, true
		case "no":
			result.Step, result.Reason = "delete", "permission"
		default:
			result.Step, result.Reason = "delete", "other"
		}
		return result
	}
	if err := api.DeleteAccessRule(ctx, zoneID, rule.ID); err != nil {
		// the worker deletes it once it can
		e.mu.Lock()
		e.addRuleLocked(&Rule{Key: testAddress, Zone: zone.Name, RuleID: rule.ID, Mode: ModeChallenge, Reason: "test", State: StateDeleting, Since: e.now(), Until: e.now(), Why: "test"})
		e.dirty = true
		e.mu.Unlock()
		result.Step, result.Reason, result.LeftTestRule = "delete", reasonOf(err), true
		return result
	}
	result.OK = true
	return result
}

func (e *Engine) enable(w http.ResponseWriter, req *http.Request) {
	zones := []cloudflare.EdgeZone{}
	for _, zone := range edgeZones() {
		if zone.Shielded > 0 {
			zones = append(zones, zone)
		}
	}
	if len(zones) == 0 {
		failure(w, http.StatusConflict, "no_shielded_zone", nil)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
	defer cancel()
	results := []ProofResult{}
	proofs := []utils.ForkEdgeProof{}
	all := true
	for _, zone := range zones {
		result := e.prove(ctx, zone)
		results = append(results, result)
		if result.OK {
			proofs = append(proofs, utils.ForkEdgeProof{Zone: zone.Name, Token: zone.TokenTag, At: e.now().UTC()})
		} else {
			all = false
		}
	}
	updateSettings(func(c *utils.ForkEdgeBlocksConfig) {
		c.Proven = proofs
		if all {
			c.Enabled = true
		}
	})
	e.poke()
	if !all {
		failure(w, http.StatusBadGateway, "proof_failed", map[string]interface{}{"results": results})
		return
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", "cloudflare-edge", "change", "enabled")
	answer(w, map[string]interface{}{"results": results, "status": e.Status(true)})
}

func (e *Engine) manualBlock(w http.ResponseWriter, req *http.Request, body request) {
	if !enabled() {
		failure(w, http.StatusConflict, "off", nil)
		return
	}
	address := strings.TrimSpace(body.Address)
	key, ok := KeyOf(address)
	if !ok {
		failure(w, http.StatusBadRequest, "not_an_address", nil)
		return
	}
	length, ok := manualDurations[body.Duration]
	if !ok {
		failure(w, http.StatusBadRequest, "duration", nil)
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = ModeBlock
	}
	if mode != ModeBlock && mode != ModeChallenge {
		failure(w, http.StatusBadRequest, "mode", nil)
		return
	}
	now := e.now()
	if isLocalAddress(address) {
		failure(w, http.StatusConflict, "exempt", map[string]interface{}{"exempt": ExemptLocal})
		return
	}
	zones := provenZones()
	if len(zones) == 0 {
		failure(w, http.StatusConflict, "no_proven_zone", nil)
		return
	}
	// the administrator's own address is exempt like every administrator's
	own, _ := KeyOf(utils.SecurityClientIP(req))
	e.mu.Lock()
	if reason := exemptReason(key, e.st.Admins, now); reason != "" || own == key {
		e.mu.Unlock()
		if reason == "" {
			reason = ExemptAdmin
		}
		failure(w, http.StatusConflict, "exempt", map[string]interface{}{"exempt": reason})
		return
	}
	// a block that stands is replaced by this one
	for _, r := range append([]*Rule(nil), e.st.Rules...) {
		if r.Key == key && (r.State == StatePending || r.State == StateActive) {
			e.liftLocked(r, "replaced")
		}
		if r.Key == key && (r.State == StateGone || r.State == StateOwner || r.State == StateRefused) {
			e.removeLocked(r)
		}
	}
	if !e.makeRoomLocked(len(zones), now) {
		e.mu.Unlock()
		failure(w, http.StatusConflict, "full", nil)
		return
	}
	actor := utils.GetAuthContext(req).Nickname
	until := now.Add(length)
	for _, zone := range zones {
		e.addRuleLocked(&Rule{Key: key, Zone: zone, Mode: mode, Reason: "manual", State: StatePending, Since: now, Until: until, Actor: actor, Address: address})
	}
	e.dirty = true
	e.mu.Unlock()
	e.poke()
	utils.AuditSecurity(req, utils.SecEdgeBlockAdded, key, "mode", mode, "until", until.UTC().Format(time.RFC3339))
	answer(w, e.Status(true))
}

var startOnce sync.Once
var stopWorker chan struct{}

// Start reads the state, subscribes to the security record and starts the
// worker. publicIPv4 gives the server's public address; isLocal is the
// fork's local network rule.
func Start(publicIPv4 func() string, isLocal func(ip string) bool) {
	startOnce.Do(func() {
		if publicIPv4 != nil {
			publicAddress = publicIPv4
		}
		if isLocal != nil {
			isLocalAddress = isLocal
		}
		std.load()
		security.Subscribe("edgeblock", std.Observe)
		stopWorker = make(chan struct{})
		go std.run(stopWorker)
	})
}

// Stop writes the state and ends the worker (an orderly shutdown).
func Stop() {
	if stopWorker != nil {
		close(stopWorker)
		stopWorker = nil
	}
}

// NoteAdmin is Engine.NoteAdmin of the running engine.
func NoteAdmin(ip string) { std.NoteAdmin(ip) }

// keyOfInput reads a key sent by the card: an address, or an IPv6 /64.
func keyOfInput(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "/") {
		prefix, err := netip.ParsePrefix(text)
		if err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() || prefix.Bits() != 64 {
			return "", false
		}
		return prefix.Masked().String(), true
	}
	return KeyOf(text)
}

// IsAdminAddress reports whether an administrator used an address (or its
// /64) within AdminFor.
func IsAdminAddress(ip string) bool {
	key, ok := KeyOf(ip)
	if !ok {
		return false
	}
	std.mu.Lock()
	defer std.mu.Unlock()
	seen, known := std.st.Admins[key]
	return known && std.now().Sub(seen) < AdminFor
}
