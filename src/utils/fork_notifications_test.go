package utils

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// notificationEnv opens a monitoring database in a temporary folder, with
// two admins and a user, and a clock the test moves.
func notificationEnv(t *testing.T) *time.Time {
	t.Helper()
	setupMonitoringDB(t)
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	prevClock, prevRecipients, prevForward := notificationClock, notificationRecipients, notificationForwarder.Load()
	notificationClock = func() time.Time { return now }
	notificationRecipients = func(group string) []string {
		if group == "admin" {
			return []string{"alice", "bob"}
		}
		return []string{"alice", "bob", "carol"}
	}
	SetNotificationForwarder(nil)
	t.Cleanup(func() {
		notificationClock, notificationRecipients = prevClock, prevRecipients
		if prevForward != nil {
			notificationForwarder.Store(prevForward)
		}
	})
	return &now
}

func upnpNotification(port string) Notification {
	return Notification{Recipient: "admin", Level: "error", Fork: &NotificationInfo{
		Kind:    "upnp.forward.failed",
		Thread:  "upnp:cosmos:HTTPS",
		Subject: &NotificationSubject{Type: "port", Name: "HTTPS " + port + "/TCP", Link: "/cosmos-ui/config-general/http-server"},
		Title:   NText("notif.upnp.forward.failed.title", "port", port),
		What:    NText("notif.upnp.forward.failed.https.absent", "port", port),
		Raw:     `HTTPS: Post "http://192.168.1.1:2828/upnp/control/x/wanipconn-8": EOF`,
	}}
}

func list(t *testing.T, q NotificationQuery) []NotificationView {
	t.Helper()
	items, _, err := ListNotificationViews(q)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestRepeatsAreGroupedAndAlertAgainOnlyWhenItMatters(t *testing.T) {
	now := notificationEnv(t)
	forwarded := []NotificationInfo{}
	SetNotificationForwarder(func(n Notification) {
		if n.Fork != nil {
			forwarded = append(forwarded, *n.Fork)
		}
	})

	WriteNotification(upnpNotification("2053"))
	for i := 0; i < 3; i++ {
		*now = now.Add(10 * time.Minute)
		WriteNotification(upnpNotification("2053"))
	}
	items := list(t, NotificationQuery{Recipient: "alice", Admin: true})
	if len(items) != 1 {
		t.Fatalf("four failures should be one notification, got %d", len(items))
	}
	if items[0].Count != 4 || items[0].FirstSeen == nil || !items[0].FirstSeen.Equal(now.Add(-30*time.Minute)) || !items[0].Date.Equal(*now) {
		t.Fatalf("count %d first %v last %v", items[0].Count, items[0].FirstSeen, items[0].Date)
	}
	if len(forwarded) != 1 || forwarded[0].Ref == "" {
		t.Fatalf("Telegram should be told once, got %d", len(forwarded))
	}

	// read it; a repeat within four hours leaves it read
	MarkNotifications("alice", true, nil, true)
	*now = now.Add(time.Hour)
	WriteNotification(upnpNotification("2053"))
	if items := list(t, NotificationQuery{Recipient: "alice", Admin: true}); !items[0].Read || len(forwarded) != 1 {
		t.Fatalf("a quiet repeat made it unread or told Telegram (read %v, told %d)", items[0].Read, len(forwarded))
	}
	// four hours after it last alerted, it alerts again
	*now = now.Add(4 * time.Hour)
	WriteNotification(upnpNotification("2053"))
	if items := list(t, NotificationQuery{Recipient: "alice", Admin: true}); items[0].Read || len(forwarded) != 2 || items[0].Count != 6 {
		t.Fatalf("a repeat after four hours should alert again (read %v, told %d, count %d)", items[0].Read, len(forwarded), items[0].Count)
	}
	if !forwarded[1].Repeat || forwarded[1].Count != 6 {
		t.Fatalf("the forwarder should hear it is a repeat: %+v", forwarded[1])
	}
	// worse alerts at once
	MarkNotifications("alice", true, nil, true)
	*now = now.Add(time.Minute)
	warn := upnpNotification("2053")
	warn.Level = "warning"
	warn.Fork.Kind = "upnp.router.unreachable"
	WriteNotification(warn) // another kind: a row of its own
	if items := list(t, NotificationQuery{Recipient: "alice", Admin: true}); len(items) != 2 {
		t.Fatalf("another kind is another notification, got %d", len(items))
	}

	// a day without it: a new one
	*now = now.Add(25 * time.Hour)
	WriteNotification(upnpNotification("2053"))
	if items := list(t, NotificationQuery{Recipient: "alice", Admin: true, Source: "upnp"}); len(items) != 3 {
		t.Fatalf("after a day a failure is a new notification, got %d", len(items))
	}
}

func TestResolvingAThreadAndTheFollowUp(t *testing.T) {
	now := notificationEnv(t)
	WriteNotification(upnpNotification("2053"))
	*now = now.Add(14 * time.Minute)
	first, open := ResolveThread("upnp:cosmos:HTTPS")
	if !open || !first.Equal(now.Add(-14*time.Minute)) {
		t.Fatalf("resolve: open %v first %v", open, first)
	}
	WriteNotification(Notification{Recipient: "admin", Level: "success", Fork: &NotificationInfo{
		Kind: "upnp.forward.restored", Thread: "upnp:cosmos:HTTPS", Resolves: "upnp:cosmos:HTTPS",
		What: NText("notif.upnp.forward.restored.https", "port", "2053", "at", "09:14"),
	}})
	if _, again := ResolveThread("upnp:cosmos:HTTPS"); again {
		t.Fatal("nothing should be open any more (the follow-up is resolved from the start)")
	}
	items := list(t, NotificationQuery{Recipient: "bob", Admin: true})
	if len(items) != 2 || items[1].Resolved == nil || items[0].Kind != "upnp.forward.restored" {
		t.Fatalf("%+v", items)
	}
	d, ok, err := GetNotificationDetails("bob", true, items[1].ID, "")
	if err != nil || !ok || len(d.Thread) != 1 || d.Thread[0].Kind != "upnp.forward.restored" {
		t.Fatalf("the failure's details should show the follow-up: %v %v %+v", ok, err, d.Thread)
	}
	// a failure after the resolution is a new notification
	*now = now.Add(time.Minute)
	WriteNotification(upnpNotification("2053"))
	if items := list(t, NotificationQuery{Recipient: "bob", Admin: true}); len(items) != 3 {
		t.Fatalf("got %d", len(items))
	}
	// resolving with nothing open writes nothing
	if _, open := ResolveThread("upnp:cosmos:HTTP"); open {
		t.Fatal("nothing was open")
	}
}

func TestEveryoneSeesOnlyTheirOwn(t *testing.T) {
	notificationEnv(t)
	WriteNotification(upnpNotification("2053"))
	WriteNotification(Notification{Recipient: "carol", Title: "header.notification.title.alertTriggered",
		Message: "header.notification.message.alertTriggered", Vars: "mine", Level: "info"})

	if items := list(t, NotificationQuery{Recipient: "carol", Admin: false}); len(items) != 1 || items[0].Message.Vars["Vars"] != "mine" {
		t.Fatalf("carol: %+v", items)
	}
	aliceRows := list(t, NotificationQuery{Recipient: "alice", Admin: true})
	if len(aliceRows) != 1 {
		t.Fatalf("alice: %+v", aliceRows)
	}
	// alice, no longer an admin, does not see the admins' rows
	if items := list(t, NotificationQuery{Recipient: "alice", Admin: false}); len(items) != 0 {
		t.Fatalf("a demoted admin sees %d admin notifications", len(items))
	}
	// carol cannot read, mark or delete alice's
	if _, ok, _ := GetNotificationDetails("carol", false, aliceRows[0].ID, ""); ok {
		t.Fatal("carol read alice's notification by id")
	}
	if _, ok, _ := GetNotificationDetails("carol", true, 0, aliceRows[0].Ref); ok {
		t.Fatal("carol read alice's notification by ref")
	}
	if n, _ := MarkNotifications("carol", true, []int64{aliceRows[0].ID}, true); n != 0 {
		t.Fatal("carol marked alice's notification")
	}
	if n, _ := DeleteNotifications("carol", true, []int64{aliceRows[0].ID}); n != 0 {
		t.Fatal("carol deleted alice's notification")
	}
	// by ref, the copy of the reader
	if d, ok, _ := GetNotificationDetails("bob", true, 0, aliceRows[0].Ref); !ok || d.ID == aliceRows[0].ID {
		t.Fatalf("bob should get his own copy by the shared ref: %v %d", ok, d.ID)
	}
	if CountUnreadNotifications("alice", false); true {
		if n, _ := CountUnreadNotifications("alice", false); n != 0 {
			t.Fatalf("unread count leaks admin rows: %d", n)
		}
	}
}

func TestFiltersSearchCursorAndBounds(t *testing.T) {
	now := notificationEnv(t)
	for i := 0; i < 7; i++ {
		*now = now.Add(time.Minute)
		WriteNotification(Notification{Recipient: "carol", Level: []string{"error", "warn", "info", "success"}[i%4], Fork: &NotificationInfo{
			Kind: "alert.triggered", Thread: "t" + string(rune('a'+i)),
			What: NText("notif.alert.triggered.what", "name", "alert_"+string(rune('a'+i))+"%"),
		}})
	}
	if items := list(t, NotificationQuery{Recipient: "carol", Levels: []string{"warning"}}); len(items) != 2 || items[0].Level != "warning" {
		t.Fatalf("warn is a warning: %+v", items)
	}
	if items := list(t, NotificationQuery{Recipient: "carol", Search: "alert_c%"}); len(items) != 1 {
		t.Fatalf("search matched %d", len(items))
	}
	if items := list(t, NotificationQuery{Recipient: "carol", Search: "ALERT_"}); len(items) != 7 {
		t.Fatalf("search is not case-insensitive: %d", len(items))
	}
	if items := list(t, NotificationQuery{Recipient: "carol", Search: "_"}); len(items) != 7 {
		t.Fatalf("_ is a character: %d", len(items))
	}
	if items := list(t, NotificationQuery{Recipient: "carol", Search: "alertXc"}); len(items) != 0 {
		t.Fatal("_ in a search must not match any character")
	}
	page, next, _ := ListNotificationViews(NotificationQuery{Recipient: "carol", Limit: 3})
	seen := map[int64]bool{}
	for next != "" || len(page) > 0 {
		for _, item := range page {
			if seen[item.ID] {
				t.Fatal("a row came twice")
			}
			seen[item.ID] = true
		}
		if next == "" {
			break
		}
		page, next, _ = ListNotificationViews(NotificationQuery{Recipient: "carol", Limit: 3, Cursor: next})
	}
	if len(seen) != 7 {
		t.Fatalf("paging gave %d of 7", len(seen))
	}
	for _, bad := range []NotificationQuery{
		{Recipient: "carol", Levels: []string{"loud"}}, {Recipient: "carol", Source: "nope"}, {Recipient: "carol", Read: "maybe"},
		{Recipient: "carol", Cursor: "1:2:3"}, {Recipient: "carol", Cursor: "x"}, {Recipient: "carol", Search: strings.Repeat("a", 101)}, {Recipient: ""},
	} {
		if _, _, err := ListNotificationViews(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if items, _, _ := ListNotificationViews(NotificationQuery{Recipient: "carol", Limit: 100000}); len(items) != 7 {
		t.Fatal("limit")
	}
	if _, err := MarkNotifications("carol", false, make([]int64, NotificationIDsMax+1), true); err == nil {
		t.Fatal("too many ids accepted")
	}
	// mark unread, delete read ones
	items := list(t, NotificationQuery{Recipient: "carol"})
	MarkNotifications("carol", false, nil, true)
	MarkNotifications("carol", false, []int64{items[0].ID}, false)
	if n, _ := CountUnreadNotifications("carol", false); n != 1 {
		t.Fatalf("unread %d", n)
	}
	if n, _ := DeleteNotifications("carol", false, nil); n != 6 {
		t.Fatalf("deleted %d read ones", n)
	}
	if items := list(t, NotificationQuery{Recipient: "carol"}); len(items) != 1 || items[0].ID != items[0].ID {
		t.Fatal("the unread one should be left")
	}
}

func TestTextsAreCleanedAndLinksStayInThePanel(t *testing.T) {
	notificationEnv(t)
	WriteNotification(Notification{Recipient: "carol", Level: "error", Link: "https://evil.example.org/", Fork: &NotificationInfo{
		Kind:    "container.dying",
		Subject: &NotificationSubject{Type: "container", Name: "app\n<img src=x onerror=alert(1)>‮", Link: "//evil.example.org"},
		What:    NText("notif.container.dying.what", "name", "a\x1b[31mb", "exits", "3", "code", "1"),
		Steps:   []NotificationStep{{NotificationText: *NText("notif.step.app.open"), Link: "javascript:alert(1)"}},
	}})
	d, ok, _ := GetNotificationDetails("carol", false, list(t, NotificationQuery{Recipient: "carol"})[0].ID, "")
	if !ok {
		t.Fatal("not found")
	}
	if d.Link != "" || d.Subject.Link != "" || d.Steps[0].Link != "" {
		t.Fatalf("a link out of the panel was kept: %q %q %q", d.Link, d.Subject.Link, d.Steps[0].Link)
	}
	if strings.ContainsAny(d.Subject.Name, "\n‮") || strings.Contains(d.What.Vars["name"], "\x1b") {
		t.Fatalf("control characters kept: %q %q", d.Subject.Name, d.What.Vars["name"])
	}
	if d.Subject.Name != "app <img src=x onerror=alert(1)>" {
		t.Fatalf("the name is data, kept as text: %q", d.Subject.Name)
	}
}

func TestSecretsAreMasked(t *testing.T) {
	token := "1234567:" + strings.Repeat("Ab1", 12)
	cases := map[string]string{
		"failed with token=abcdef0123456789":                            "failed with token=[hidden]",
		"Authorization: Bearer abcdefghijklmnop123":                     "Authorization: Bearer [hidden]",
		"cookie: cosmos-session=xyz":                                    "cookie: [hidden]",
		"password=hunter2!":                                             "password=[hidden]",
		"bot " + token + " refused":                                     "bot [hidden] refused",
		"key " + "QWxhZGRpbjpvcGVuIHNlc2FtZQ12345678":                   "key [hidden]",
		"id " + "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8":                  "id [hidden]",
		"the key of the problem":                                        "the key of the problem",
		"Post \"http://192.168.1.1:2828/upnp/control/WANIPConn1\": EOF": "Post \"http://192.168.1.1:2828/upnp/control/WANIPConn1\": EOF",
		"router 5f3b1c2a-1d2e-4f5a-9b8c-7d6e5f4a3b2c":                   "router 5f3b1c2a-1d2e-4f5a-9b8c-7d6e5f4a3b2c",
	}
	for in, want := range cases {
		if got := MaskNotificationText(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
	prev := NotificationSecretMasker
	NotificationSecretMasker = func(text string) string { return strings.ReplaceAll(text, "s3cr3t-value", "[hidden]") }
	defer func() { NotificationSecretMasker = prev }()
	if got := MaskNotificationText("dns s3cr3t-value"); got != "dns [hidden]" {
		t.Errorf("configuration secret: %q", got)
	}
}

func TestMajorErrorsGetAKindASentenceAndALink(t *testing.T) {
	notificationEnv(t)
	MajorError("CRON job nightly-sync failed", os.ErrPermission)
	MajorError("Backup: Error creating zip file", nil)
	MajorError("Something nobody planned", nil)
	items := list(t, NotificationQuery{Recipient: "alice", Admin: true})
	byKind := map[string]NotificationView{}
	for _, item := range items {
		byKind[item.Kind] = item
	}
	job, ok := byKind["error.job"]
	if !ok || job.Subject == nil || job.Subject.Name != "nightly-sync" || job.Link != "/cosmos-ui/cron" || job.Source != "jobs" {
		t.Fatalf("job: %+v", job)
	}
	if RenderNotificationText(NotificationText{Key: job.Message.Key, Vars: job.Message.Vars}) != "The scheduled job nightly-sync failed." {
		t.Fatalf("sentence: %+v", job.Message)
	}
	d, _, _ := GetNotificationDetails("alice", true, job.ID, "")
	if d.Raw != "CRON job nightly-sync failed: permission denied" {
		t.Fatalf("raw: %q", d.Raw)
	}
	if _, ok := byKind["error.backup"]; !ok {
		t.Fatal("backup")
	}
	if other, ok := byKind["error.other"]; !ok || other.Message.Vars["message"] != "Something nobody planned" {
		t.Fatalf("other: %+v", other)
	}
}

func TestOldRowsStillShow(t *testing.T) {
	notificationEnv(t)
	WriteNotification(Notification{Recipient: "admin", Title: "header.notification.title.containerUpdate",
		Message: "header.notification.message.containerUpdate", Vars: "jellyfin", Level: "info", Link: "/cosmos-ui/servapps/containers/jellyfin"})
	items := list(t, NotificationQuery{Recipient: "bob", Admin: true, Source: "containers"})
	if len(items) != 1 || !items[0].Legacy || items[0].Kind != "container.updated" || items[0].Message.Vars["Vars"] != "jellyfin" ||
		items[0].Message.Text != "Container {{Vars}} updated to the latest version!" || items[0].Link == "" {
		t.Fatalf("%+v", items)
	}
	if items := list(t, NotificationQuery{Recipient: "bob", Admin: true, Search: "latest version"}); len(items) != 1 {
		t.Fatal("an old-style notification is found by its English text")
	}
}

// TestUpgradeFromTheOldSchema builds a database the way upstream's schema
// left it, with rows, and opens it with this version.
func TestUpgradeFromTheOldSchema(t *testing.T) {
	folder := t.TempDir() + "/"
	db, err := sql.Open(DialectSQLite, "file:"+folder+"database.db")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range strings.Split(monitoringSchemaFor(DialectSQLite), ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	db.Exec("INSERT INTO monitoring_kv(key, value) VALUES('schema_version', '1')")
	old := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	rows := [][]interface{}{
		{"local", "alice", "header.notification.title.upnpFailed", "header.notification.message.upnpFailed", `HTTPS: Post "http://192.168.1.1:2828/x": EOF`, "", "", TimeToMillis(old), "error", 0, "null"},
		{"local", "alice", "header.notification.title.serverError", "CRON job a failed : boom", "", "", "", TimeToMillis(old.Add(time.Minute)), "error", 1, "null"},
		{"local", "carol", "something.unknown", "plain words", "", "", "", TimeToMillis(old.Add(2 * time.Minute)), "info", 0, ""},
	}
	for _, r := range rows {
		if _, err := db.Exec("INSERT INTO notifications(node, recipient, title, message, vars, icon, link, date, level, read, actions) VALUES(?,?,?,?,?,?,?,?,?,?,?)", r...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	prev := CONFIGFOLDER
	CONFIGFOLDER = folder
	defer func() { CONFIGFOLDER = prev }()
	for round := 0; round < 2; round++ { // opening twice changes nothing
		if err := InitMetricsDatabase(); err != nil {
			t.Fatal("upgrade:", err)
		}
		items := list(t, NotificationQuery{Recipient: "alice", Admin: true})
		if len(items) != 2 {
			t.Fatalf("round %d: %d rows", round, len(items))
		}
		if items[1].Kind != "upnp.forward.failed" || items[1].Source != "upnp" || !items[1].Legacy || items[1].Message.Vars["Vars"] == "" {
			t.Fatalf("round %d: %+v", round, items[1])
		}
		if items[0].Message.Text != "CRON job a failed : boom" || !items[0].Read {
			t.Fatalf("round %d: %+v", round, items[0])
		}
		if hidden := list(t, NotificationQuery{Recipient: "alice", Admin: false}); len(hidden) != 0 {
			t.Fatal("old admin rows are marked as the admins'")
		}
		if mine := list(t, NotificationQuery{Recipient: "carol", Admin: false}); len(mine) != 1 || mine[0].Message.Text != "plain words" {
			t.Fatalf("an unknown old row: %+v", mine)
		}
		CloseMetricsDatabase()
	}
	// and the old insert of upstream still works on the new table
	if err := InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	defer CloseMetricsDatabase()
	if err := InsertNotification(Notification{Recipient: "carol", Title: "x", Message: "y", Date: old, Level: "info"}); err != nil {
		t.Fatal("upstream's insert:", err)
	}
	if got, _ := ListNotifications("carol", 0, 10); len(got) != 2 {
		t.Fatal("upstream's list")
	}
}

// TestNotificationTextsAreInThePanel: every sentence has the same English
// in the panel's translation file.
func TestNotificationTextsAreInThePanel(t *testing.T) {
	raw, err := os.ReadFile("../../client/src/utils/locales/en/translation.json")
	if err != nil {
		t.Fatal(err)
	}
	var panel map[string]string
	if err := json.Unmarshal(raw, &panel); err != nil {
		t.Fatal(err)
	}
	for key, english := range notificationTexts {
		if panel[key] != english {
			t.Errorf("translation.json %s = %q, want %q", key, panel[key], english)
		}
	}
	for key, english := range legacyNotificationTexts {
		if panel[key] != english {
			t.Errorf("legacy %s differs from translation.json", key)
		}
	}
	for kind, k := range notificationKinds {
		if k.Title != "" && NotificationEnglish(k.Title) == "" {
			t.Errorf("kind %s: title %s has no English", kind, k.Title)
		}
		known := false
		for _, source := range NotificationSources {
			known = known || source == k.Source
		}
		if !known {
			t.Errorf("kind %s: unknown source %s", kind, k.Source)
		}
	}
	for _, kind := range legacyNotificationKinds {
		if _, ok := notificationKinds[kind]; !ok {
			t.Errorf("legacy kind %s is not a kind", kind)
		}
	}
}
