package appsignin

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

type sentMail struct {
	to  []string
	msg []byte
}

func withMail(t *testing.T, containers map[string]containerInfo, config utils.Config) (*[]sentMail, *[]utils.Notification) {
	t.Helper()
	withFakes(t, containers)
	configNow = func() utils.Config { return config }
	oldDeliver, oldNotify, oldAsync := deliver, notify, async
	mails := &[]sentMail{}
	notes := &[]utils.Notification{}
	deliver = func(cfg utils.EmailConfig, from string, to []string, msg []byte) error {
		*mails = append(*mails, sentMail{to: to, msg: msg})
		return nil
	}
	notify = func(n utils.Notification) { *notes = append(*notes, n) }
	async = func(f func()) { f() }
	sentMu.Lock()
	sent = nil
	sentMu.Unlock()
	t.Cleanup(func() { deliver, notify, async = oldDeliver, oldNotify, oldAsync })
	return mails, notes
}

func mailConfig() utils.Config {
	c := utils.Config{}
	c.EmailConfig.Enabled = true
	c.EmailConfig.From = "Cosmos <cosmos@example.org>"
	return c
}

func creationRequest(names ...string) docker.DockerServiceCreateRequest {
	r := docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{}}
	for _, n := range names {
		r.Services[n] = docker.ContainerCreateRequestContainer{Name: n, Image: "x"}
	}
	return r
}

// readParts gives the decoded plain and HTML parts of a message, and its headers.
func readParts(t *testing.T, raw []byte) (mail.Header, string, string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/alternative" {
		t.Fatalf("content type %q %v", media, err)
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	plain, htmlPart := "", ""
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(quotedprintable.NewReader(part))
		if strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") {
			plain = string(body)
		} else if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
			htmlPart = string(body)
		}
	}
	return msg.Header, plain, htmlPart
}

func TestTheInstallationEmailCarriesNoSecret(t *testing.T) {
	app := containerInfo{Name: "/forms", Image: "registry.example.org/team/forms:1",
		Env:    []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=" + testPassword, "SECRET_KEY=" + testOther},
		Labels: map[string]string{LabelKey: "where=web;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=review"},
		Mounts: []mountInfo{{Type: "bind", Source: "/mnt/data/forms", Destination: "/data"}}}
	db := postgresContainer()
	db.Env = append(db.Env, "MARIADB_ROOT_PASSWORD="+testRoot)
	mails, notes := withMail(t, map[string]containerInfo{"forms": app, "notes-db": db}, mailConfig())
	routesNow = func() []utils.ProxyRouteConfig {
		return []utils.ProxyRouteConfig{{Mode: "SERVAPP", UseHost: true, Host: "forms.example.org", Target: "http://forms:3000"}}
	}
	req := httptest.NewRequest("POST", "http://cosmos.example.org/cosmos/api/docker-service", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, sudoed))
	afterCreation(req, creationRequest("forms", "notes-db"), docker.StartCheck{Containers: []docker.StartReport{{Name: "forms"}, {Name: "notes-db"}}})

	if len(*mails) != 1 {
		t.Fatalf("one e-mail per creation, got %d", len(*mails))
	}
	m := (*mails)[0]
	if len(m.to) != 1 || m.to[0] != "owner@example.org" {
		t.Fatalf("to the installer's own address: %v", m.to)
	}
	mustHaveNoSecret(t, "the raw e-mail", string(m.msg), testPassword, testOther, testRoot)
	header, plain, htmlPart := readParts(t, m.msg)
	for _, part := range []string{plain, htmlPart} {
		mustHaveNoSecret(t, "the e-mail", part, testPassword, testOther, testRoot)
		for _, want := range []string{"forms", "notes-db", "owner@example.org", "owner_name", "https://forms.example.org:2053",
			"https://cosmos.example.org:2053/cosmos-ui/servapps/containers/forms#sign-in", "/mnt/data/forms", "never contains a password"} {
			if !strings.Contains(part, want) {
				t.Errorf("the e-mail lacks %q:\n%s", want, part)
			}
		}
	}
	if subject := header.Get("Subject"); !strings.Contains(subject, "2 apps installed") {
		t.Errorf("subject %q", subject)
	}
	if len(*notes) != 1 || (*notes)[0].Recipient != "owner" || (*notes)[0].Fork.Kind != "app.installed" || (*notes)[0].Fork.Doing.Key != "notif.app.installed.mailSent" {
		t.Fatalf("notification: %+v", *notes)
	}
	if (*notes)[0].Fork.Subject.Link != "/cosmos-ui/servapps/containers/forms#sign-in" {
		t.Errorf("link: %+v", (*notes)[0].Fork.Subject)
	}
}

func TestTheEmailEscapesWhatTheInstallerTyped(t *testing.T) {
	app := containerInfo{Name: "/x", Image: "registry.example.org/a/b:1",
		Env:    []string{"ADMIN_USER=<script>alert(1)</script>", "ADMIN_PASSWORD=" + testPassword},
		Labels: map[string]string{LabelKey: "where=web;user=ADMIN_USER;password=ADMIN_PASSWORD"}}
	mails, _ := withMail(t, map[string]containerInfo{"x": app}, mailConfig())
	req := httptest.NewRequest("POST", "http://cosmos.example.org/", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, sudoed))
	afterCreation(req, creationRequest("x"), docker.StartCheck{})
	_, _, htmlPart := readParts(t, (*mails)[0].msg)
	if strings.Contains(htmlPart, "<script>") || !strings.Contains(htmlPart, "&lt;script&gt;") {
		t.Fatalf("not escaped: %s", htmlPart)
	}
}

func TestNoEmailWhenOffOrNotPossibleButANotificationSaysWhy(t *testing.T) {
	c := postgresContainer()
	cases := []struct {
		config func() utils.Config
		auth   *utils.AuthContext
		want   string
	}{
		{func() utils.Config { x := mailConfig(); x.AppSignIn.MailOff = true; return x }, sudoed, "switched off"},
		{func() utils.Config { return utils.Config{} }, sudoed, "no e-mail settings"},
		{mailConfig, tokenYes, "API token"},
	}
	for _, k := range cases {
		mails, notes := withMail(t, map[string]containerInfo{"notes-db": c}, k.config())
		req := httptest.NewRequest("POST", "http://cosmos.example.org/", nil)
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, k.auth))
		afterCreation(req, creationRequest("notes-db"), docker.StartCheck{})
		if len(*mails) != 0 || len(*notes) != 1 || !strings.Contains((*notes)[0].Fork.Doing.Vars["reason"], k.want) {
			t.Errorf("%s: %d mails, %+v", k.want, len(*mails), *notes)
		}
	}
	// no address on the account
	mails, notes := withMail(t, map[string]containerInfo{"notes-db": c}, mailConfig())
	userRecord = func(string) (string, error) { return "", nil }
	req := httptest.NewRequest("POST", "http://cosmos.example.org/", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, sudoed))
	afterCreation(req, creationRequest("notes-db"), docker.StartCheck{})
	if len(*mails) != 0 || !strings.Contains((*notes)[0].Fork.Doing.Vars["reason"], "no e-mail address") {
		t.Errorf("no address: %d %+v", len(*mails), *notes)
	}
}

func TestStartHooksTheCreationRoute(t *testing.T) {
	old := docker.ForkAfterCreation
	t.Cleanup(func() { docker.ForkAfterCreation = old })
	docker.ForkAfterCreation = nil
	Start()
	if docker.ForkAfterCreation == nil {
		t.Fatal("not hooked")
	}
}

func TestTheEmailsAreBoundedAndAFailureIsANotification(t *testing.T) {
	mails, notes := withMail(t, map[string]containerInfo{"notes-db": postgresContainer()}, mailConfig())
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = time.Now })
	req := httptest.NewRequest("POST", "http://cosmos.example.org/", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, sudoed))
	for i := 0; i < mailsPerHour+2; i++ {
		afterCreation(req, creationRequest("notes-db"), docker.StartCheck{})
	}
	if len(*mails) != mailsPerHour {
		t.Fatalf("sent %d", len(*mails))
	}
	failed := 0
	for _, n := range *notes {
		if n.Fork.Kind == "app.installMail.failed" {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("limit notifications: %d", failed)
	}
	now = now.Add(time.Hour)
	deliver = func(utils.EmailConfig, string, []string, []byte) error { return io.ErrUnexpectedEOF }
	*notes = nil
	afterCreation(req, creationRequest("notes-db"), docker.StartCheck{})
	if len(*notes) != 2 || (*notes)[1].Fork.Kind != "app.installMail.failed" || (*notes)[1].Level != "warning" {
		t.Fatalf("failure: %+v", *notes)
	}
}
