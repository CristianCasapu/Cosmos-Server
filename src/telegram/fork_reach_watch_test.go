package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/reachwatch"
	"github.com/azukaar/cosmos-server/src/utils"
)

// TestTheOutageMessageIsEnoughOnItsOwn: when the server cannot be reached
// from outside, the panel cannot be opened from outside either; the
// Telegram message (sent outward, so it gets through) says the cause, since
// when and what Cosmos does. The follow-up says how long it lasted, quietly.
func TestTheOutageMessageIsEnoughOnItsOwn(t *testing.T) {
	config := testConfig()
	config.HTTPConfig.HTTPSPort = "2053"
	utils.LoadBaseMainConfig(config)
	defer utils.LoadBaseMainConfig(utils.Config{})

	now := time.Date(2026, 11, 15, 9, 20, 0, 0, time.Local)
	since := time.Date(2026, 11, 15, 9, 12, 0, 0, time.Local)
	o := reachwatch.Outage{Since: since, Cause: "connect", Code: "522", Names: []string{"example.org", "app.example.org"},
		Asked: true, Router: "the router forwards port 2053 to this server", Thread: "reach:connect"}
	primary := &reachwatch.NameState{Host: "example.org", Panel: true, Fails: 3}
	n := reachwatch.DownNotification(o, primary, reachwatch.Plan{Port: 2053, Interval: 5 * time.Minute}, now.Add(time.Minute), now)
	n.Fork.Ref = "0123456789abcdef"
	nt, tell := noticeFromNotification(n, config)
	if !tell || nt.priority != priorityNormal || nt.category != CategorySystem {
		t.Fatalf("%v %+v", tell, nt)
	}
	text, _ := textOf(t, settingsOf(config), nt)
	for _, want := range []string{"Cosmos cannot be reached from the internet", "Since 09:12", "error 522", "port 2053",
		"Cosmos asked the router to forward the port again", "keeps testing", "About: example.org, app.example.org"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}

	end := now.Add(26 * time.Minute)
	o.End = &end
	back := reachwatch.RestoredNotification(o, reachwatch.Plan{Port: 2053, Interval: 5 * time.Minute}, end)
	nt, _ = noticeFromNotification(back, config)
	text, _ = textOf(t, settingsOf(config), nt)
	if nt.priority != priorityLow || !strings.Contains(text, "can be reached from the internet again") || !strings.Contains(text, "34 minutes") {
		t.Errorf("follow-up (%v):\n%s", nt.priority, text)
	}
}
