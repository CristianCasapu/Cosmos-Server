package proxy

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/constellation"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Ban escalation, shared by the HTTP, TCP and UDP shields.
//
//   strike     blocks for 1h;  3 strikes since the last temporary ban => temporary ban
//   temporary  blocks for 4h, then 24h, then 72h as they repeat within 7 days;
//              3 of them within 7 days                                => permanent ban
//   permanent  blocks until removed (Pro profile: never issued, stays a 72h temporary)
//
// Bans are keyed by shield identity (a Cosmos user when the request is
// authenticated, the source IP otherwise) and apply across every route. When
// the constellation is up they are replicated through constellation.ShieldBucket
// and every node enforces the union.

const (
	STRIKE = utils.SHIELD_STRIKE
	TEMP   = utils.SHIELD_TEMP
	PERM   = utils.SHIELD_PERM
)

const (
	strikeBlock  = time.Hour
	strikeWindow = 24 * time.Hour
	tempWindow   = 7 * 24 * time.Hour
	banRetention = 7 * 24 * time.Hour
)

// tempBlocks is how long a temporary ban blocks, by how many temporary bans
// the client already had in the tempWindow before it.
var tempBlocks = []time.Duration{4 * time.Hour, 24 * time.Hour, 72 * time.Hour}

// tempBlockFor derives the length of a temporary ban from the client's
// history, so every node of the constellation agrees on it without the
// duration being replicated. bans need not be ordered.
func tempBlockFor(bans []*UserBan, ban *UserBan) time.Duration {
	previous := 0
	for _, other := range bans {
		if other != ban && other.BanType == TEMP && other.Time.Before(ban.Time) && other.Time.Add(tempWindow).After(ban.Time) {
			previous++
		}
	}
	if previous >= len(tempBlocks) {
		previous = len(tempBlocks) - 1
	}
	return tempBlocks[previous]
}

type UserBan = utils.ShieldBan

type banStore struct {
	sync.Mutex
	byClient map[string][]*UserBan
	ids      map[string]*UserBan
}

var globalShieldState = newBanStore()

func newBanStore() banStore {
	return banStore{byClient: map[string][]*UserBan{}, ids: map[string]*UserBan{}}
}

func localNodeName() string {
	if name := utils.GetMainConfig().ConstellationConfig.ThisDeviceName; name != "" {
		return name
	}
	return "local"
}

func (b *banStore) count() int {
	b.Lock()
	defer b.Unlock()
	return len(b.ids)
}

// last returns the most recent strike for a client, or nil.
func (b *banStore) last(clientID string) *UserBan {
	b.Lock()
	defer b.Unlock()
	bans := b.byClient[clientID]
	for i := len(bans) - 1; i >= 0; i-- {
		if bans[i].BanType == STRIKE {
			return bans[i]
		}
	}
	return nil
}

// add stores a ban if its ID is new. Must hold the lock.
func (b *banStore) add(ban *UserBan) bool {
	if _, seen := b.ids[ban.ID]; seen {
		return false
	}
	b.ids[ban.ID] = ban
	bans := append(b.byClient[ban.ClientID], ban)
	// remote entries can arrive out of order
	sort.SliceStable(bans, func(i, j int) bool { return bans[i].Time.Before(bans[j].Time) })
	b.byClient[ban.ClientID] = bans
	return true
}

// remove drops one ban by ID. Must hold the lock.
func (b *banStore) remove(id string) {
	ban, ok := b.ids[id]
	if !ok {
		return
	}
	delete(b.ids, id)
	bans := b.byClient[ban.ClientID]
	kept := bans[:0]
	for _, x := range bans {
		if x.ID != id {
			kept = append(kept, x)
		}
	}
	if len(kept) == 0 {
		delete(b.byClient, ban.ClientID)
	} else {
		b.byClient[ban.ClientID] = kept
	}
}

// issue records a ban locally and publishes it to the constellation. Must hold the lock.
func (b *banStore) issue(clientID string, banType int, shieldID string, reason utils.ShieldBanReason, now time.Time) *UserBan {
	node := localNodeName()
	ban := &UserBan{
		ID:       constellation.ShieldBanID(clientID, now, node),
		ClientID: clientID,
		BanType:  banType,
		Time:     now,
		Reason:   reason,
		ShieldID: shieldID,
		Node:     node,
	}
	b.add(ban)
	go func(copy UserBan) {
		if err := constellation.PublishShieldBan(copy); err != nil {
			utils.Debug("SmartShield: could not replicate ban: " + err.Error())
		}
	}(*ban)
	return ban
}

// allowed reports whether a client is currently blocked by an existing ban,
// escalating strikes into temporary bans and temporary bans into permanent
// ones on the way. It does not look at budgets; see strike.
func (b *banStore) allowed(clientID string, shieldID string, now time.Time) bool {
	b.Lock()
	defer b.Unlock()

	nbTempBans := 0
	nbStrikes := 0

	// a ban consumes the strikes that led to it: only strikes issued after
	// the latest temporary ban count towards the next one, otherwise the
	// same three strikes would re-issue a temporary ban as soon as the
	// previous one ends and reach a permanent ban without a new offence
	bans := b.byClient[clientID]
	for _, ban := range bans {
		switch ban.BanType {
		case PERM:
			return false
		case TEMP:
			if ban.Time.Add(tempBlockFor(bans, ban)).After(now) {
				return false
			} else if ban.Time.Add(tempWindow).After(now) {
				nbTempBans++
			}
			nbStrikes = 0
		case STRIKE:
			if ban.Time.Add(strikeBlock).After(now) {
				return false
			} else if ban.Time.Add(strikeWindow).After(now) {
				nbStrikes++
			}
		}
	}

	escalation := utils.ShieldBanReason{Limit: "escalation", Route: shieldID}
	if nbTempBans >= 3 {
		if utils.GetSmartShieldProfile().AllowPermanentBan {
			b.issue(clientID, PERM, shieldID, escalation, now)
			utils.Warn("SmartShield: " + clientID + " has been banned permanently")
		} else {
			b.issue(clientID, TEMP, shieldID, escalation, now)
			utils.Warn("SmartShield: " + clientID + " has been banned temporarily again (permanent bans are disabled by the deployment profile)")
		}
		return false
	} else if nbStrikes >= 3 {
		b.issue(clientID, TEMP, shieldID, escalation, now)
		utils.Warn("SmartShield: " + clientID + " has been banned temporarily")
		return false
	}

	return true
}

// strike records a budget violation for a client.
func (b *banStore) strike(clientID string, shieldID string, reason utils.ShieldBanReason, now time.Time) {
	b.Lock()
	defer b.Unlock()
	b.issue(clientID, STRIKE, shieldID, reason, now)
	utils.Warn("SmartShield: " + clientID + " has received a strike: " + describeReason(reason))
}

func describeReason(r utils.ShieldBanReason) string {
	if r.Limit == "escalation" {
		return "escalation on " + r.Route
	}
	return fmt.Sprintf("%s %.0f of %.0f on %s", r.Limit, r.Used, r.Allowed, r.Route)
}

// unban forgets a client entirely, here and on the constellation.
func (b *banStore) unban(clientID string) int {
	b.Lock()
	bans := b.byClient[clientID]
	removed := len(bans)
	for _, ban := range bans {
		delete(b.ids, ban.ID)
	}
	delete(b.byClient, clientID)
	b.Unlock()

	utils.ResetIPAbuseCounter(clientID)
	if err := constellation.DeleteShieldBans(clientID); err != nil {
		utils.Error("SmartShield: could not replicate unban of "+clientID, err)
	}
	return removed
}

// cleanup drops strikes and temporary bans older than their escalation
// window. Permanent bans are kept. Each node only expires what it issued.
func (b *banStore) cleanup(now time.Time) int {
	node := localNodeName()
	expired := []string{}

	b.Lock()
	for id, ban := range b.ids {
		if ban.BanType != PERM && ban.Time.Add(banRetention).Before(now) {
			expired = append(expired, id)
		}
	}
	mine := []string{}
	for _, id := range expired {
		if b.ids[id].Node == node {
			mine = append(mine, id)
		}
		b.remove(id)
	}
	b.Unlock()

	for _, id := range mine {
		if err := constellation.DeleteShieldBan(id); err != nil {
			utils.Debug("SmartShield: could not expire replicated ban: " + err.Error())
		}
	}
	return len(expired)
}

// remote merge

func (b *banStore) applyRemote(ban utils.ShieldBan) {
	b.Lock()
	defer b.Unlock()
	copy := ban
	if b.add(&copy) {
		utils.Debug("SmartShield: merged ban " + ban.ID + " from " + ban.Node)
	}
}

func (b *banStore) removeRemote(id string) {
	b.Lock()
	defer b.Unlock()
	b.remove(id)
}

// reconcile drops entries from other nodes that the bucket no longer holds
// (unbanned or expired while this node was disconnected).
func (b *banStore) reconcile(present map[string]bool) {
	node := localNodeName()
	b.Lock()
	defer b.Unlock()
	for id, ban := range b.ids {
		if ban.Node != node && !present[id] {
			b.remove(id)
		}
	}
}

// InitShieldSync wires the ban store to the constellation bucket.
func InitShieldSync() {
	constellation.SetShieldSyncHandlers(constellation.ShieldSyncHandlers{
		Put:       globalShieldState.applyRemote,
		Delete:    globalShieldState.removeRemote,
		Reconcile: globalShieldState.reconcile,
	})
}

// dashboard view

type ShieldClientStatus struct {
	ClientID     string     `json:"clientID"`
	Status       string     `json:"status"` // perm, temp, strike, clear
	BlockedUntil *time.Time `json:"blockedUntil,omitempty"`
	History      []UserBan  `json:"history"`
}

func (b *banStore) statuses(now time.Time) []ShieldClientStatus {
	b.Lock()
	defer b.Unlock()

	out := make([]ShieldClientStatus, 0, len(b.byClient))
	for clientID, bans := range b.byClient {
		s := ShieldClientStatus{ClientID: clientID, Status: "clear", History: make([]UserBan, 0, len(bans))}
		for _, ban := range bans {
			s.History = append(s.History, *ban)
			var until time.Time
			switch ban.BanType {
			case PERM:
				s.Status = "perm"
				s.BlockedUntil = nil
				continue
			case TEMP:
				until = ban.Time.Add(tempBlockFor(bans, ban))
			case STRIKE:
				until = ban.Time.Add(strikeBlock)
			}
			if s.Status == "perm" || !until.After(now) {
				continue
			}
			if s.BlockedUntil == nil || until.After(*s.BlockedUntil) {
				u := until
				s.BlockedUntil = &u
				if ban.BanType == TEMP {
					s.Status = "temp"
				} else if s.Status != "temp" {
					s.Status = "strike"
				}
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := out[i].History[len(out[i].History)-1].Time, out[j].History[len(out[j].History)-1].Time
		return li.After(lj)
	})
	return out
}

func GetLastBan(clientID string) *UserBan {
	return globalShieldState.last(clientID)
}

func describeBan(ban *UserBan) string {
	if ban == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{ClientID:%s BanType:%d Time:%s Reason:%s ShieldID:%s Node:%s}", ban.ClientID, ban.BanType, ban.Time.Format(time.RFC3339), describeReason(ban.Reason), ban.ShieldID, ban.Node)
}
