package appsignin

// The e-mail and the notification sent when apps are installed (plan 45).
//
// One message per creation (a Market app with its database is one message
// that lists both), never for an edit, a repair or a re-deploy (those carry
// ?replace=1 and docker.forkAfterCreation does not call this). It goes to
// the installing administrator's own address from Cosmos' user record, never
// to an address of the request. It says, per app, its name, the addresses
// Cosmos serves it at, the username or e-mail to sign in with, where its data
// is, whether it started, and the link to its Sign-in details. It never
// carries a password or a connection string with one: the message is built
// only from Details, which holds no secret, and a test fails if any secret of
// the creation appears in it. At most mailsPerHour messages an hour for the
// server; a failure or the limit is a notification, never a failed creation.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	mailsPerHour  = 10 // per recipient
	maxMailApps   = 8
	inspectBudget = 20 * time.Second
)

// Start sets the hook of the creation route.
func Start() {
	docker.ForkAfterCreation = afterCreation
	docker.ForkBeforeCreation = beforeCreation
	utils.ForkGuardRoutes = guardRoutes
}

// installed is one app of a creation, as the e-mail tells it.
type installed struct {
	Details
	Started bool
	Failing bool
	Link    string
}

// deliver and notify are what reaches the outside; replaced by the tests.
var (
	deliver = func(cfg utils.EmailConfig, from string, to []string, msg []byte) error {
		_, err := utils.DeliverEmail(cfg, from, to, msg)
		return err
	}
	notify = utils.WriteNotification
	async  = func(f func()) { go f() }
)

var (
	sentMu sync.Mutex
	sent   = map[string][]time.Time{}
)

// mailAllowed counts the e-mails of the last hour for one recipient (review
// minor 6: one account's installations never use up another's e-mails).
func mailAllowed(address string, now time.Time) bool {
	sentMu.Lock()
	defer sentMu.Unlock()
	kept := []time.Time{}
	for _, t := range sent[address] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if len(kept) >= mailsPerHour {
		sent[address] = kept
		return false
	}
	sent[address] = append(kept, now)
	if len(sent) > 256 {
		for k, times := range sent {
			if len(times) == 0 || now.Sub(times[len(times)-1]) >= time.Hour {
				delete(sent, k)
			}
		}
	}
	return true
}

func createdNames(request docker.DockerServiceCreateRequest) []string {
	names := []string{}
	for key, service := range request.Services {
		name := service.Name
		if name == "" {
			name = key
		}
		if validContainerName(strings.TrimPrefix(name, "/")) {
			names = append(names, strings.TrimPrefix(name, "/"))
		}
	}
	sort.Strings(names)
	if len(names) > maxMailApps {
		names = names[:maxMailApps]
	}
	return names
}

func cardLink(name string) string {
	return strings.TrimSuffix(serverURL(""), "/") + "/cosmos-ui/servapps/containers/" + name + "#sign-in"
}

// afterCreation is docker.ForkAfterCreation.
func afterCreation(req *http.Request, request docker.DockerServiceCreateRequest, check docker.StartCheck) {
	auth := utils.GetAuthContext(req)
	nickname := auth.Nickname
	byToken := auth.APIToken != nil
	names := createdNames(request)
	if len(names) == 0 {
		return
	}
	failing := map[string]bool{}
	watched := map[string]bool{}
	for _, r := range check.Containers {
		watched[r.Name] = true
		failing[r.Name] = r.Failing
	}
	async(func() {
		defer func() { recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), inspectBudget)
		defer cancel()
		apps := []installed{}
		routes := routesNow()
		for _, name := range names {
			c, err := inspect(ctx, name)
			if err != nil {
				continue
			}
			apps = append(apps, installed{Details: resolve(c, routes, serverURL), Started: watched[name] && !failing[name], Failing: failing[name], Link: cardLink(name)})
		}
		if len(apps) == 0 {
			return
		}
		tell(nickname, byToken, apps)
	})
}

// tell sends the e-mail when it may, and writes the notification.
func tell(nickname string, byToken bool, apps []installed) {
	config := configNow()
	reason := ""
	address := ""
	switch {
	case config.AppSignIn.MailOff:
		reason = "the e-mail at installation is switched off (Configuration, General, Sign-in details for new apps)"
	case byToken || nickname == "":
		reason = "the apps were created with an API token, which has no e-mail address"
	case !config.EmailConfig.Enabled:
		reason = "Cosmos has no e-mail settings (Configuration, General, Emails)"
	default:
		email, err := userRecord(nickname)
		if err != nil || email == "" {
			reason = "your account has no e-mail address"
		} else {
			address = email
		}
	}
	mailFailed := ""
	if reason == "" {
		if !mailAllowed(strings.ToLower(address), clock()) {
			mailFailed = fmt.Sprintf("more than %d e-mails about installations in the last hour", mailsPerHour)
		} else {
			subject, msg, clear := buildMessage(config, address, apps)
			if !clear {
				mailFailed = "it would have held a secret value of the app, so it was not sent"
				utils.Warn("App installed: the e-mail would hold a secret value: not sent")
			} else if err := deliver(config.EmailConfig, config.EmailConfig.From, []string{address}, msg); err != nil {
				// the mail server's answer is not repeated: it may name the account
				mailFailed = "the mail server could not be reached or refused it (the e-mail settings have a test)"
				utils.Error("App installed: the e-mail could not be sent", nil)
			} else {
				utils.TriggerEvent("cosmos.email.send", "Email sent", "success", "", map[string]interface{}{"recipients": []string{address}, "subject": subject})
			}
		}
	}
	recipient := nickname
	if recipient == "" || byToken {
		recipient = "admin"
	}
	first := apps[0].Container
	doing := utils.NText("notif.app.installed.mailSent")
	if reason != "" {
		doing = utils.NText("notif.app.installed.mailNone", "reason", reason)
	} else if mailFailed != "" {
		doing = utils.NText("notif.app.installed.mailNone", "reason", mailFailed)
	}
	steps := []utils.NotificationStep{}
	facts := []utils.NotificationFact{}
	for _, app := range apps {
		steps = append(steps, utils.NotificationStep{NotificationText: *utils.NText("notif.step.app.signIn", "name", app.Container), Link: "/cosmos-ui/servapps/containers/" + app.Container + "#sign-in"})
		facts = append(facts, utils.NotificationFact{Key: "notif.fact.container", Value: app.Container})
	}
	installedInfo := &utils.NotificationInfo{
		Kind:     "app.installed",
		Subject:  &utils.NotificationSubject{Type: "container", Name: first, Link: "/cosmos-ui/servapps/containers/" + first + "#sign-in"},
		Title:    utils.NText("notif.app.installed.title", "name", namesText(apps)),
		What:     utils.NText("notif.app.installed.what", "name", namesText(apps)),
		Means:    utils.NText("notif.app.installed.means"),
		Doing:    doing,
		Steps:    steps,
		Facts:    facts,
		Security: &utils.NotificationSecurity{Kinds: []string{"container.*"}, Target: first},
	}
	notify(utils.Notification{Recipient: recipient, Level: "info", Fork: installedInfo})
	if mailFailed != "" {
		notify(utils.Notification{Recipient: recipient, Level: "warning", Fork: &utils.NotificationInfo{
			Kind:    "app.installMail.failed",
			Subject: &utils.NotificationSubject{Type: "container", Name: first, Link: "/cosmos-ui/servapps/containers/" + first + "#sign-in"},
			Title:   utils.NText("notif.app.installMail.title", "name", namesText(apps)),
			What:    utils.NText("notif.app.installMail.what", "name", namesText(apps), "reason", mailFailed),
			Means:   utils.NText("notif.app.installMail.means"),
			Steps:   []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.config.mail"), Link: "/cosmos-ui/config-general"}},
		}})
	}
}

func namesText(apps []installed) string {
	names := []string{}
	for _, a := range apps {
		names = append(names, a.Container)
	}
	return strings.Join(names, ", ")
}

// headerSafe keeps a header value on one line.
func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// signInLine says how one signs in to an app, in plain words, without a secret.
func signInLines(app installed) []string {
	lines := []string{}
	if app.FirstRun != nil {
		if app.FirstRun.Note == "logPassword" {
			lines = append(lines, "It prints a temporary password in its log the first time it starts: open its page in Cosmos, then Logs. Username: "+orDash(app.FirstRun.User)+".")
		} else {
			line := "It makes its first account in its own page, the first time you open it: Cosmos did not set it."
			if app.FirstRun.User != "" {
				line += " The username proposed at installation: " + app.FirstRun.User + "."
			}
			lines = append(lines, line)
		}
	}
	if app.Via == "database" {
		if app.ViaContainer != "" {
			lines = append(lines, "You sign in to it with the user and password of the database "+app.ViaContainer+" (see its Sign-in details).")
		} else {
			lines = append(lines, "You sign in to it with the user and password of the database it manages.")
		}
	}
	for _, a := range app.Accounts {
		who := ""
		switch {
		case a.Email != "":
			who = "e-mail " + a.Email
		case a.User != "":
			who = "username " + a.User
		}
		if who == "" {
			continue
		}
		line := "Sign in with the " + who
		if a.Role == "root" || a.Role == "admin" {
			line = "Its " + a.Role + " account: " + who
		}
		if a.Database != "" {
			line += ", database " + a.Database
		}
		if a.Password != "" {
			line += "; the password is on its page in Cosmos (Sign-in details)"
		}
		lines = append(lines, line+".")
	}
	if app.Host != "" && app.Port > 0 {
		lines = append(lines, fmt.Sprintf("Other apps on this server reach it at %s:%d once they share a network with it.", app.Host, app.Port))
	}
	if app.Source == "guess" {
		lines = append(lines, "Cosmos guessed these from the names of its settings.")
	}
	return lines
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func dataLines(app installed) []string {
	lines := []string{}
	for _, d := range app.Data {
		switch {
		case d.Folder != "":
			lines = append(lines, d.Target+": the folder "+d.Folder+" on the server")
		case d.Volume != "":
			lines = append(lines, d.Target+": the Docker volume "+d.Volume)
		}
	}
	return lines
}

// buildMessage is the whole message (headers, a plain part and an HTML part).
func buildMessage(config utils.Config, to string, apps []installed) (string, []byte, bool) {
	subject := "Cosmos: " + namesText(apps) + " installed"
	if len(apps) > 1 {
		subject = fmt.Sprintf("Cosmos: %d apps installed (%s)", len(apps), namesText(apps))
	}
	subject = headerSafe(subject)
	if len(subject) > 200 {
		subject = subject[:200]
	}

	var plain strings.Builder
	var body strings.Builder
	esc := html.EscapeString
	if len(apps) > 1 {
		plain.WriteString(fmt.Sprintf("These apps were installed on your Cosmos server: %s.\n\n", namesText(apps)))
		body.WriteString("<h1>Apps installed</h1>")
	} else {
		plain.WriteString(fmt.Sprintf("The app %s was installed on your Cosmos server.\n\n", apps[0].Container))
		body.WriteString("<h1>" + esc(apps[0].Container) + " installed</h1>")
	}
	for _, app := range apps {
		title := app.Container
		if app.Name != "" {
			title += " (" + app.Name + ")"
		}
		plain.WriteString(title + "\n")
		body.WriteString("<h2>" + esc(title) + "</h2>")
		if app.Failing {
			plain.WriteString("  It does not run yet: its page in Cosmos says why.\n")
			body.WriteString("<p><b>It does not run yet:</b> its page in Cosmos says why.</p>")
		}
		if len(app.Addresses) > 0 {
			plain.WriteString("  Open it at:\n")
			body.WriteString("<p>Open it at:</p><ul>")
			for _, a := range app.Addresses {
				note := ""
				if a.AdminOnly {
					note = " (administrators only, after signing in to Cosmos)"
				} else if a.SignIn {
					note = " (after signing in to Cosmos)"
				}
				plain.WriteString("    " + a.URL + note + "\n")
				body.WriteString("<li><a href=\"" + esc(a.URL) + "\">" + esc(a.URL) + "</a>" + esc(note) + "</li>")
			}
			body.WriteString("</ul>")
		}
		lines := signInLines(app)
		if len(lines) > 0 {
			body.WriteString("<p>")
			for i, line := range lines {
				plain.WriteString("  " + line + "\n")
				if i > 0 {
					body.WriteString("<br>")
				}
				body.WriteString(esc(line))
			}
			body.WriteString("</p>")
		}
		if data := dataLines(app); len(data) > 0 {
			plain.WriteString("  Its data:\n")
			body.WriteString("<p>Its data:</p><ul>")
			for _, line := range data {
				plain.WriteString("    " + line + "\n")
				body.WriteString("<li>" + esc(line) + "</li>")
			}
			body.WriteString("</ul>")
		}
		plain.WriteString("  Its sign-in details: " + app.Link + "\n\n")
		body.WriteString("<a class=\"button\" href=\"" + esc(app.Link) + "\">Sign-in details of " + esc(app.Container) + "</a>")
	}
	note := "This e-mail never contains a password: Cosmos shows the passwords on the app's page, after you confirm your own password. Cosmos sends it because \"Send me an e-mail when an app is installed\" is on in Configuration, General."
	plain.WriteString(note + "\n")
	body.WriteString("<p style=\"margin-top:20px\">" + esc(note) + "</p>")

	// the last check of this exit (secrets.go): the fields from the apps' variables, checked again
	for i := range apps {
		clean(&apps[i].Details, apps[i].secretValues)
	}

	server := serverURL("")
	htmlPart := emailHTML(server+"logo", body.String(), server)
	_, sender := utils.EmailSender(config.EmailConfig.From)
	boundary := randomBoundary()

	var msg bytes.Buffer
	msg.WriteString("From: " + headerSafe(sender) + "\r\n")
	msg.WriteString("To: " + headerSafe(to) + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	msg.WriteString("Date: " + clock().Format(time.RFC1123Z) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	for _, part := range []struct{ kind, text string }{{"text/plain", plain.String()}, {"text/html", htmlPart}} {
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: " + part.kind + "; charset=UTF-8\r\n")
		msg.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&msg)
		qp.Write([]byte(strings.ReplaceAll(part.text, "\n", "\r\n")))
		qp.Close()
		msg.WriteString("\r\n")
	}
	msg.WriteString("--" + boundary + "--\r\n")
	return subject, msg.Bytes(), true
}

// emailHTML is upstream's template (utils.Template) without its header lines:
// the same look as Cosmos' other e-mails.
func emailHTML(logo string, content string, footer string) string {
	template := utils.Template
	if i := strings.Index(template, "<!DOCTYPE html>"); i >= 0 {
		template = template[i:]
	}
	return fmt.Sprintf(template, html.EscapeString(logo), content, html.EscapeString(footer))
}

func randomBoundary() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "cosmos-" + hex.EncodeToString(b)
}
