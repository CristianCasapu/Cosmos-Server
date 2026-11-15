package upnp

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func factOf(info *utils.NotificationInfo, key string) string {
	for _, f := range info.Facts {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

func render(t *utils.NotificationText) string {
	if t == nil {
		return ""
	}
	return utils.RenderNotificationText(*t)
}

// The owner's morning: the router lost Cosmos' forward and broke the
// connection on every request for it. The notification says which port,
// that Cosmos cannot be reached, what the router answered, when Cosmos
// tries again, offers Check now, and is followed by "forwarded again".
func TestTheOwnersMorningInWords(t *testing.T) {
	w := newWorld(t)
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	w.router.reboot()
	w.router.breakAdd = true
	w.minutes(6)
	if len(w.notices) != 1 {
		t.Fatalf("told %d times", len(w.notices))
	}
	n := w.notices[0].notification()
	info := n.Fork
	if info.Kind != "upnp.forward.failed" || info.Thread != "upnp:cosmos:HTTPS" || n.Level != "error" {
		t.Fatalf("%+v", info)
	}
	if got := render(info.What); got != "The router no longer forwards port 2053 to this server: Cosmos cannot be reached from the internet." {
		t.Errorf("what: %q", got)
	}
	if got := render(info.Title); got != "The router does not forward port 2053" {
		t.Errorf("title: %q", got)
	}
	if info.Means.Key != "notif.upnp.means.router" {
		t.Errorf("means: %s", info.Means.Key)
	}
	failedAt := w.now
	want := "Cosmos asks the router again by itself at " + w.notices[0].retry.next.Local().Format("15:04") + "; it has tried 1 times so far."
	if got := render(info.Doing); !strings.HasPrefix(got, want) || !w.notices[0].retry.next.After(failedAt.Add(-time.Second)) {
		t.Errorf("doing: %q", got)
	}
	if info.Steps[0].Action != "upnp.check" || info.Steps[1].Link != settingsLink {
		t.Errorf("steps: %+v", info.Steps)
	}
	if !strings.Contains(render(&info.Steps[2].NotificationText), "port 2053 is forwarded to 192.168.1.50, port 2053") {
		t.Errorf("router step: %q", render(&info.Steps[2].NotificationText))
	}
	if factOf(info, "notif.fact.answer") == "" || factOf(info, "notif.fact.nextTry") == "" || factOf(info, "notif.fact.attempts") != "1" {
		t.Errorf("facts: %+v", info.Facts)
	}
	if st := own(GetStatus(), "HTTPS"); st.RetryAt == nil || st.Attempts < 1 {
		t.Errorf("the status says when it is tried again: %+v", st)
	}

	w.router.breakAdd = false
	w.minutes(3)
	if len(w.resolved) != 1 {
		t.Fatalf("resolved %d times", len(w.resolved))
	}
	back := w.resolved[0].Fork
	if back.Kind != "upnp.forward.restored" || back.Resolves != "upnp:cosmos:HTTPS" || w.resolved[0].Level != "success" {
		t.Fatalf("%+v", back)
	}
	if got := render(back.What); !strings.HasPrefix(got, "The router forwards port 2053 to this server again since ") || !strings.HasSuffix(got, "Cosmos can be reached from the internet.") {
		t.Errorf("follow-up: %q", got)
	}
	if got := render(back.Means); got != "It was not forwarded for 2 minutes." && got != "It was not forwarded for 3 minutes." {
		t.Errorf("down for: %q", got)
	}
	// later passes resolve nothing more
	w.minutes(30)
	if len(w.resolved) != 1 {
		t.Errorf("resolved again: %d", len(w.resolved))
	}
}

func TestARefusalSaysTheRouterWillNotChangeItsMind(t *testing.T) {
	w := newWorld(t)
	w.router.refuse["TCP/2053"] = 606
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	info := w.notices[0].notification().Fork
	if info.Means.Key != "notif.upnp.means.refused" || info.Doing.Key != "notif.upnp.doing.retryRefused" {
		t.Fatalf("means %s doing %s", info.Means.Key, info.Doing.Key)
	}
	if info.Steps[len(info.Steps)-1].Key != "notif.step.upnp.refused" {
		t.Errorf("steps %+v", info.Steps)
	}
}

func TestAPortHeldByAnotherDeviceNamesIt(t *testing.T) {
	w := newWorld(t)
	w.router.put("TCP/2053", "192.168.1.50", 443, "NAS")
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	if len(w.notices) == 0 {
		t.Fatal("no notice")
	}
	info := w.notices[0].notification().Fork
	if info.Means.Key != "notif.upnp.means.conflict.holder" || !strings.Contains(render(info.Means), "192.168.1.50:443 (NAS)") {
		t.Errorf("means: %q", render(info.Means))
	}
	if !strings.Contains(render(info.What), "no longer forwards port 2053") {
		t.Errorf("the table shows it is not forwarded here: %q", render(info.What))
	}
}

func TestAnAppsPortIsAboutTheApp(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("game", true, udp(27015, 27015))}
	w.router.refuse["UDP/27015"] = 606
	w.config(func(c *utils.Config) {
		c.HTTPConfig.UPnP.Enabled = true
		c.HTTPConfig.UPnP.AppPorts = []utils.UPnPAppPort{{Container: "game", Port: 27015, Protocol: "udp"}}
	})
	reconcile(true)
	var info *utils.NotificationInfo
	for _, n := range w.notices {
		if n.mapping.Kind == "app" {
			info = n.notification().Fork
		}
	}
	if info == nil {
		t.Fatalf("no notice about the app: %+v", w.notices)
	}
	if !strings.Contains(render(info.What), "the app game cannot be reached from the internet on that port") || info.Subject.Name != "game 27015/UDP" {
		t.Errorf("%q %+v", render(info.What), info.Subject)
	}
	if info.Security.Target != "game" {
		t.Errorf("security target %q", info.Security.Target)
	}
}

func TestAnUnreachableRouterAndItsReturn(t *testing.T) {
	w := newWorld(t)
	w.routerErr = errors.New("no UPnP router answered")
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	if len(w.notices) != 1 || w.notices[0].kind != noticeRouter {
		t.Fatalf("%+v", w.notices)
	}
	info := w.notices[0].notification().Fork
	if info.Kind != "upnp.router.unreachable" || factOf(info, "notif.fact.port") != "HTTPS 2053/TCP" || info.Raw != "no UPnP router answered" {
		t.Errorf("%+v", info)
	}
	if st := GetStatus(); st.RetryAt == nil {
		t.Error("the status says when the router is asked again")
	}
	w.minutes(7)
	w.routerErr = nil
	w.minutes(2)
	if len(w.resolved) != 1 || w.resolved[0].Fork.Kind != "upnp.router.restored" {
		t.Fatalf("%+v", w.resolved)
	}
	if got := render(w.resolved[0].Fork.Means); got != "It could not be reached for 8 minutes." {
		t.Errorf("%q", got)
	}
}

func TestEveryUPnPSentenceHasEnglish(t *testing.T) {
	keys := []string{}
	for _, which := range []string{"https", "http", "app"} {
		for _, state := range []string{"absent", "unknown"} {
			keys = append(keys, "notif.upnp.forward.failed."+which+"."+state)
		}
		keys = append(keys, "notif.upnp.forward.restored."+which)
	}
	for _, key := range keys {
		if utils.NotificationEnglish(key) == "" {
			t.Errorf("%s has no English", key)
		}
	}
	if duration(90*time.Minute) != "1 hour 30 minutes" || duration(30*time.Second) != "less than a minute" || duration(2*time.Hour) != "2 hours" {
		t.Error("duration")
	}
}
