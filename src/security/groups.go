package security

import "github.com/azukaar/cosmos-server/src/utils"

// Groups of incidents, for the dashboard: what an administrator wants to
// count ("failed sign-ins", "bans") rather than the kinds the record knows.
// Every incident kind is in exactly one group; audit entries are in none.
// The dashboard's sentences are keyed on these identifiers
// (client/src/utils/securityDashboard.js).

// Group is one group of incident kinds.
type Group struct {
	ID    string   `json:"id"`
	Kinds []string `json:"kinds"`
}

// GroupOther collects what is mostly harmless or about the record itself; it
// never makes the dashboard sound an alarm.
const GroupOther = "other"

var groups = []Group{
	{"login", []string{utils.SecLoginFailed, utils.SecMFAFailed, utils.SecSudoFailed, utils.SecRegisterFailed, utils.SecTokenInvalid, utils.SecSessionForged}},
	{"lockout", []string{utils.SecMFALocked, utils.SecShieldStrike, utils.SecShieldRateLimited}},
	{"ban", []string{utils.SecShieldBanTemp, utils.SecShieldBanPerm}},
	{"scan", []string{utils.SecScanProbe, utils.SecShieldHostname, utils.SecShieldBot, utils.SecShieldGeo}},
	{"access", []string{utils.SecAccessDenied, utils.SecAccessGrantDenied, utils.SecAccessIPDenied, utils.SecAccessLocalDenied, utils.SecTokenIPDenied}},
	{GroupOther, []string{utils.SecShieldBlocked, utils.SecSessionRejected, utils.SecResetRequested, utils.SecResetRejected, utils.SecTokenExpired, utils.SecRecorderDropped}},
}

var groupOfKind = func() map[string]string {
	byKind := map[string]string{}
	for _, group := range groups {
		for _, kind := range group.Kinds {
			byKind[kind] = group.ID
		}
	}
	return byKind
}()

// Groups lists the groups in the order the dashboard shows them.
func Groups() []Group {
	list := make([]Group, len(groups))
	for i, group := range groups {
		list[i] = Group{ID: group.ID, Kinds: append([]string(nil), group.Kinds...)}
	}
	return list
}

// GroupOf names the group of an incident kind; GroupOther for a kind that is
// in none (an incident kind added later without a group).
func GroupOf(kind string) string {
	if group, ok := groupOfKind[kind]; ok {
		return group
	}
	return GroupOther
}

func groupByID(id string) (Group, bool) {
	for _, group := range groups {
		if group.ID == id {
			return group, true
		}
	}
	return Group{}, false
}

// hostileKinds are the kinds of every group but GroupOther.
func hostileKinds() []string {
	kinds := []string{}
	for _, group := range groups {
		if group.ID != GroupOther {
			kinds = append(kinds, group.Kinds...)
		}
	}
	return kinds
}

// groupCase is an SQL expression naming the group of a row's kind, and its
// arguments.
func groupCase() (string, []interface{}) {
	expression := "CASE kind"
	args := []interface{}{}
	for _, group := range groups {
		for _, kind := range group.Kinds {
			expression += " WHEN ? THEN ?"
			args = append(args, kind, group.ID)
		}
	}
	return expression + " ELSE '" + GroupOther + "' END", args
}
