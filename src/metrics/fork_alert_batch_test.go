package metrics

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

func stubLiveness(t *testing.T, f func(string) (docker.ContainerLiveness, error)) *[]string {
	t.Helper()
	asked := []string{}
	old := containerLiveness
	containerLiveness = func(name string) (docker.ContainerLiveness, error) {
		asked = append(asked, name)
		return f(name)
	}
	t.Cleanup(func() { containerLiveness = old })
	return &asked
}

func stubMails(t *testing.T) *[][]string {
	t.Helper()
	sent := [][]string{}
	old := sendAlertMail
	sendAlertMail = func(alert utils.Alert, keys []string) {
		sent = append(sent, append([]string{alert.Name}, keys...))
	}
	t.Cleanup(func() {
		sendAlertMail = old
		alertMailsMu.Lock()
		alertMails = nil
		alertMailsMu.Unlock()
	})
	return &sent
}

func TestAlertOnGoneContainer(t *testing.T) {
	gone := errors.New("unused")
	cases := []struct {
		name   string
		object string
		live   docker.ContainerLiveness
		err    error
		want   bool
		asks   bool
	}{
		{"removed container", "container@cosmos-test-1", docker.ContainerLiveness{Exists: false}, nil, true, true},
		{"running container", "container@jellyfin", docker.ContainerLiveness{Exists: true, Running: true}, nil, false, true},
		{"stopped container still there", "container@jellyfin", docker.ContainerLiveness{Exists: true}, nil, false, true},
		{"Docker not answering", "container@jellyfin", docker.ContainerLiveness{}, errors.New("cannot connect"), false, true},
		{"a route", "route@media", docker.ContainerLiveness{}, gone, false, false},
		{"the system", "", docker.ContainerLiveness{}, gone, false, false},
		{"no name", "container@", docker.ContainerLiveness{}, gone, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asked := stubLiveness(t, func(string) (docker.ContainerLiveness, error) { return c.live, c.err })
			got := alertOnGoneContainer(utils.AlertMetricTrack{Key: "cosmos.system.docker.cpu.x", Object: c.object})
			if got != c.want {
				t.Fatalf("alertOnGoneContainer(%q) = %v, want %v", c.object, got, c.want)
			}
			if (len(*asked) > 0) != c.asks {
				t.Fatalf("asked Docker %v, want asked=%v", *asked, c.asks)
			}
			if c.asks && (*asked)[0] != strings.TrimPrefix(c.object, "container@") {
				t.Fatalf("asked about %q", (*asked)[0])
			}
		})
	}
}

// A gone container's alert returns before any action: a stop here would reach
// Docker (nil in a test) and an e-mail would be queued.
func TestGoneContainerAlertDoesNothing(t *testing.T) {
	stubLiveness(t, func(string) (docker.ContainerLiveness, error) { return docker.ContainerLiveness{Exists: false}, nil })
	sent := stubMails(t)
	beginAlertMails()
	alert := utils.Alert{Name: "Anti Crypto-Miner", Actions: []utils.AlertAction{{Type: "email"}, {Type: "stop"}}}
	ExecuteAllActions(alert, alert.Actions, utils.AlertMetricTrack{Key: "cosmos.system.docker.cpu.cosmos-test-1", Object: "container@cosmos-test-1"})
	sendAlertMails()
	if len(*sent) != 0 {
		t.Fatalf("mails sent for a gone container: %v", *sent)
	}
}

func TestAlertMailsOnePerAlertAndRun(t *testing.T) {
	sent := stubMails(t)
	miner := utils.Alert{Name: "Anti Crypto-Miner"}
	disk := utils.Alert{Name: "Disk full"}
	beginAlertMails()
	for _, key := range []string{"cosmos.system.docker.cpu.b", "cosmos.system.docker.cpu.a", "cosmos.system.docker.cpu.c"} {
		if !queueAlertMail(miner, utils.AlertMetricTrack{Key: key}) {
			t.Fatal("not queued during a run")
		}
	}
	queueAlertMail(disk, utils.AlertMetricTrack{Key: "cosmos.system.disk./"})
	sendAlertMails()
	want := [][]string{
		{"Anti Crypto-Miner", "cosmos.system.docker.cpu.a", "cosmos.system.docker.cpu.b", "cosmos.system.docker.cpu.c"},
		{"Disk full", "cosmos.system.disk./"},
	}
	if len(*sent) != len(want) {
		t.Fatalf("sent %v, want %v", *sent, want)
	}
	for i := range want {
		if strings.Join((*sent)[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("mail %d: %v, want %v", i, (*sent)[i], want[i])
		}
	}
	// outside a run (the latest-period alerts) nothing waits
	if queueAlertMail(miner, utils.AlertMetricTrack{Key: "k"}) {
		t.Fatal("queued outside a run")
	}
	sendAlertMails()
	if len(*sent) != 2 {
		t.Fatalf("a second send sent again: %v", *sent)
	}
}

func TestAlertMailText(t *testing.T) {
	alert := utils.Alert{Name: "Anti Crypto-Miner", Severity: "warn"}
	subject, one := alertMailText(alert, []string{"cosmos.system.docker.cpu.jellyfin"})
	if subject != "Alert Triggered: Anti Crypto-Miner" {
		t.Fatalf("subject %q", subject)
	}
	if !strings.Contains(one, "Alert triggered on cosmos.system.docker.cpu.jellyfin. Please") || strings.Contains(one, "<ul>") {
		t.Fatalf("one metric: %s", one)
	}
	_, many := alertMailText(alert, []string{"a<b", "c"})
	if !strings.Contains(many, "2 metrics:<ul><li>a&lt;b</li><li>c</li></ul>") {
		t.Fatalf("several metrics: %s", many)
	}
}

// The night's daily check end to end on the local store: containers measured
// during the day, two of them removed since. One e-mail, naming only the two
// containers still there that went over the limit.
func TestDailyContainerAlertSkipsGoneAndSendsOneMail(t *testing.T) {
	setupMetricsStore(t)
	prev := utils.GetBaseMainConfig()
	config := utils.Config{MonitoringAlerts: map[string]utils.Alert{"Anti Crypto-Miner": {
		Name: "Anti Crypto-Miner", Enabled: true, Period: "daily", TrackingMetric: "cosmos.system.docker.cpu.*",
		Condition: utils.AlertCondition{Operator: "gt", Value: 80}, Actions: []utils.AlertAction{{Type: "email"}}, Severity: "warn",
	}}}
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })

	present := map[string]bool{"jellyfin": true, "plex": true, "quiet": true}
	stubLiveness(t, func(name string) (docker.ContainerLiveness, error) {
		return docker.ContainerLiveness{Exists: present[name], Running: present[name]}, nil
	})
	sent := stubMails(t)

	date := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	pushes := []DataPush{}
	for name, cpu := range map[string]int{"cosmos-test-go-1": 150, "cosmos-test-build": 120, "jellyfin": 95, "plex": 81, "quiet": 10} {
		pushes = append(pushes, DataPush{Date: date, Key: "cosmos.system.docker.cpu." + name, Value: cpu, Period: 30 * time.Second,
			AggloType: "avg", Label: "CPU", Object: "container@" + name})
	}
	if err := writeBuckets(pushes); err != nil {
		t.Fatal("writeBuckets:", err)
	}

	runPoolAlerts(granularityDay, "daily", ModuloTime(date, 24*time.Hour))

	want := "Anti Crypto-Miner|cosmos.system.docker.cpu.jellyfin|cosmos.system.docker.cpu.plex"
	if len(*sent) != 1 || strings.Join((*sent)[0], "|") != want {
		t.Fatalf("mails %v, want one: %s", *sent, want)
	}
}
