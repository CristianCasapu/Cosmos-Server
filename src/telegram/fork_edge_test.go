package telegram

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the edge blocks' notification names the permission of the
// way the domain's blocks are made, and a block says how it was made.
func TestEdgeBlockNoticesSayWhichPermission(t *testing.T) {
	for message, want := range map[string]string{
		"header.notification.message.edgeBlocksPermission":        "Zone > Firewall Services > Edit",
		"header.notification.message.edgeBlocksPermissionWAF":     "Zone > Zone WAF > Edit",
		"header.notification.message.edgeBlocksPermissionAccount": "Account Firewall Access Rules",
		"header.notification.message.edgeBlocksToken":             "refuses the token",
	} {
		nt, ok := noticeFromNotification(utils.Notification{Recipient: "admin", Title: titleEdgeBlocksProblem, Message: message, Vars: "example.org"}, testConfig())
		if !ok || !strings.Contains(nt.title, want) {
			t.Errorf("%s: %q", message, nt.title)
		}
	}
	event := eventOf(utils.SecEdgeBlocked)
	event.Details = map[string]string{"reason": "probe", "via": "a WAF custom rule of the domain"}
	nt, _ := noticeFromEvent(event, testSettings())
	text, _ := textOf(t, testSettings(), nt)
	if !strings.Contains(text, "a WAF custom rule of the domain") {
		t.Errorf("%s", text)
	}
}
