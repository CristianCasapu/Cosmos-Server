package proxy

// Fork addition: what the proxy and its shields report to the security
// record (utils/fork_security.go, package security).

import (
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// recordShieldBan records a strike or a ban the SmartShield issued on this
// server. It is called with the ban store locked, which is fine: recording
// only queues. A ban is keyed by the client's address, or by "user:<name>"
// when the requests were authenticated.
func recordShieldBan(ban *UserBan) {
	kind := utils.SecShieldStrike
	switch ban.BanType {
	case TEMP:
		kind = utils.SecShieldBanTemp
	case PERM:
		kind = utils.SecShieldBanPerm
	}

	event := utils.SecurityEvent{
		Kind: kind,
		Time: ban.Time,
		Details: map[string]string{
			"limit": ban.Reason.Limit,
			"route": ban.Reason.Route,
		},
	}
	if nickname, isUser := strings.CutPrefix(ban.ClientID, "user:"); isUser {
		event.Actor = nickname
	} else {
		event.IP = ban.ClientID
		// SmartShield counts an address whatever way its requests came: one
		// that a proxy reported lately is not acted on at the firewall
		event.Forwarded = utils.WasForwardedClient(ban.ClientID)
	}
	utils.RecordSecurityEvent(event)
}

// recordBotBlock records a request the bot filter of a URL refused.
func recordBotBlock(req *http.Request) {
	utils.RecordSecurity(req, utils.SecShieldBot, "")
}
