package telegram

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the edge blocks' notification names the permission of the
// way the domain's blocks are made, and a block says how it was made.
func TestEdgeBlockNoticesSayWhichPermission(t *testing.T) {
	keys := map[string]bool{}
	for _, message := range []string{
		"header.notification.message.edgeBlocksPermission",
		"header.notification.message.edgeBlocksPermissionWAF",
		"header.notification.message.edgeBlocksPermissionAccount",
		"header.notification.message.edgeBlocksToken",
	} {
		nt, ok := noticeFromNotification(utils.Notification{Recipient: "admin", Title: titleEdgeBlocksProblem, Message: message, Vars: "example.org"}, testConfig())
		text, _ := textOf(t, testSettings(), nt)
		if !ok || nt.kind != "edgeblock.problem" || !strings.HasPrefix(text, "🟠 Error · Blocks at Cloudflare paused for example.org") {
			t.Errorf("%s: %q", message, text)
		}
		keys[nt.budgetKey()] = true
	}
	if len(keys) != 4 {
		t.Errorf("each problem has a budget of its own: %v", keys)
	}
	event := eventOf(utils.SecEdgeBlocked)
	event.Details = map[string]string{"reason": "probe", "via": "a WAF custom rule of the domain"}
	nt, _ := noticeFromEvent(event, testSettings())
	text, _ := textOf(t, testSettings(), nt)
	if !strings.Contains(text, "a WAF custom rule of the domain") {
		t.Errorf("%s", text)
	}
}
