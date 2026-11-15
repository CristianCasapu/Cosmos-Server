package telegram

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func explainedNotification(ref string) utils.Notification {
	return utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind: "upnp.forward.failed", Source: "upnp", Ref: ref, Count: 4, Seen: []int64{1763190000000},
		Title:   utils.NText("notif.upnp.forward.failed.title", "port", "2053"),
		What:    utils.NText("notif.upnp.forward.failed.https.absent", "port", "2053"),
		Subject: &utils.NotificationSubject{Type: "port", Name: "HTTPS 2053/TCP"},
	}}
}

func TestAnExplainedNotificationIsToldInItsWordsWithALink(t *testing.T) {
	config := testConfig()
	config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort = "80", "2053"
	utils.LoadBaseMainConfig(config)
	defer utils.LoadBaseMainConfig(utils.Config{})
	https := utils.IsHTTPS
	utils.IsHTTPS = true
	defer func() { utils.IsHTTPS = https }()

	nt, tell := noticeFromNotification(explainedNotification("0123456789abcdef"), config)
	if !tell || nt.category != CategorySystem || nt.priority != priorityNormal || nt.key != "n:upnp.forward.failed" {
		t.Fatalf("%v %+v", tell, nt)
	}
	text, entities := textOf(t, settingsOf(config), nt)
	for _, want := range []string{
		"The router no longer forwards port 2053 to this server: Cosmos cannot be reached from the internet.",
		"About: HTTPS 2053/TCP", "Seen 4 times since ",
		"Details: https://home.example:2053/cosmos-ui/notifications/r/0123456789abcdef",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	values := covered(t, text, entities)
	if !strings.Contains(strings.Join(values, "|"), "2053") {
		t.Errorf("the port is a value: %q", values)
	}

	// without "include addresses and names": the fixed label and the link only
	st := settingsOf(config)
	st.hideDetails = true
	text, _ = composeNotice(st, nt, false)
	if strings.Contains(text, "HTTPS 2053/TCP") || !strings.Contains(text, "Details: https://") || !strings.Contains(text, "The router does not forward port …") {
		t.Errorf("hidden details:\n%s", text)
	}

	// no name to reach the server by, or a ref of another shape: no link
	config.HTTPConfig.Hostname = "localhost"
	if nt, _ := noticeFromNotification(explainedNotification("0123456789abcdef"), config); nt.link != "" {
		t.Errorf("a link to localhost: %q", nt.link)
	}
	config.HTTPConfig.Hostname = "home.example"
	if nt, _ := noticeFromNotification(explainedNotification("../../x"), config); nt.link != "" {
		t.Errorf("a link with a bad ref: %q", nt.link)
	}
}

func TestAValueInASentenceCannotBecomeALink(t *testing.T) {
	config := testConfig()
	n := explainedNotification("0123456789abcdef")
	n.Fork.Kind, n.Fork.Source = "container.dying", "containers"
	n.Fork.What = utils.NText("notif.container.dying.what", "name", "https://evil.example.org/@admin #tag", "exits", "3", "code", "1")
	nt, _ := noticeFromNotification(n, config)
	if nt.category != CategoryApps {
		t.Errorf("a container is an app: %s", nt.category)
	}
	text, entities := textOf(t, settingsOf(config), nt)
	values := strings.Join(covered(t, text, entities), "|")
	if !strings.Contains(values, "https://evil.example.org/@admin #tag") {
		t.Errorf("the name must be inside a code entity: %q\n%s", values, text)
	}
}

func TestAFollowUpIsQuiet(t *testing.T) {
	n := explainedNotification("0123456789abcdef")
	n.Level, n.Fork.Resolves = "success", "upnp:cosmos:HTTPS"
	if nt, _ := noticeFromNotification(n, testConfig()); nt.priority != priorityLow {
		t.Errorf("priority %v", nt.priority)
	}
	n.Fork.Kind = "telegram.failed"
	if _, tell := noticeFromNotification(n, testConfig()); tell {
		t.Error("Telegram's own failure is not told through Telegram")
	}
}
