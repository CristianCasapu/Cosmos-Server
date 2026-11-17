package appsignin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// The review's reproduction (F1): a label that points the user, e-mail and
// database at secrets. Nothing of them in the card's answer, the e-mail or the
// notification.
func TestALabelNamingSecretsGivesNothingAway(t *testing.T) {
	admin := "Adm" + "-pw-" + "77aa"
	dbpw := "Db" + "-pw-" + "88bb"
	key := "app" + "-key-" + "99cc"
	c := containerInfo{Name: "/forms", Image: "registry.example.org/team/forms:1",
		Env:    []string{"ADMIN_PASSWORD=" + admin, "DB_PASSWORD=" + dbpw, "APP_SECRET_KEY=" + key},
		Labels: map[string]string{LabelKey: "where=web;user=APP_SECRET_KEY;email=ADMIN_PASSWORD;database=DB_PASSWORD"}}
	exits := allExits(t, c)
	for exit, text := range exits {
		mustHaveNoSecret(t, exit, text, admin, dbpw, key)
	}
}

// The same through the guess: a *_USER whose value is the password.
func TestAGuessWhoseUserIsThePasswordGivesNothingAway(t *testing.T) {
	pw := "Same" + "-value-" + "1234"
	c := containerInfo{Name: "/forms", Image: "registry.example.org/team/forms:1",
		Env: []string{"ADMIN_USER=" + pw, "ADMIN_PASSWORD=" + pw, "ADMIN_EMAIL=x" + pw + "@example.org"}}
	for exit, text := range allExits(t, c) {
		mustHaveNoSecret(t, exit, text, pw)
	}
}

// Containers with a secret in every position a value can take, through every
// source (recipe, label, guess, first-run): no secret value in any exit.
func TestNoSecretInAnyExitWhateverItsPosition(t *testing.T) {
	secret := func(i int) string { return fmt.Sprintf("S%02d-x", i) + "y.z~Q" + fmt.Sprintf("%03d", i*7) }
	images := []string{"postgres:18", "registry.example.org/team/forms:1", "jellyfin/jellyfin:10.11", "phpmyadmin:5", "dpage/pgadmin4:9"}
	vars := []string{"POSTGRES_USER", "POSTGRES_DB", "ADMIN_USER", "ADMIN_EMAIL", "PMA_HOST", "PGADMIN_DEFAULT_EMAIL", "DB_HOST", "SOME_URL"}
	labels := []string{"", "where=web;user=ADMIN_USER;email=ADMIN_EMAIL;password=ADMIN_PASSWORD",
		"where=web;user=POSTGRES_PASSWORD;database=POSTGRES_PASSWORD;password=POSTGRES_PASSWORD",
		"where=web;first=" + secret(90) + ";note=firstRunPage"}
	n := 0
	for _, image := range images {
		for vi, v := range vars {
			for _, label := range labels {
				n++
				s := secret(n)
				env := []string{
					"POSTGRES_PASSWORD=" + s, "ADMIN_PASSWORD=" + s, "PGADMIN_DEFAULT_PASSWORD=" + s, "API_TOKEN=" + secret(n+500),
					v + "=" + s, vars[(vi+1)%len(vars)] + "=pre" + s + "post",
					"DATABASE_URL=postgres://u:" + secret(n+900) + "@db:5432/d",
				}
				c := containerInfo{Name: "/app" + fmt.Sprint(n), Image: image, Env: env, Labels: map[string]string{}}
				if label != "" {
					c.Labels[LabelKey] = label
				}
				all := []string{s, secret(n + 500), secret(n + 900)}
				for exit, text := range allExits(t, c) {
					mustHaveNoSecret(t, fmt.Sprintf("%s (%s, %s=secret, label %q)", exit, image, v, label), text, all...)
				}
			}
		}
	}
	if n < 100 {
		t.Fatalf("only %d cases", n)
	}
}

// allExits gives what leaves for one container: the card's answer, the
// e-mail (raw, both parts decoded), the notification, the security record.
func allExits(t *testing.T, c containerInfo) map[string]string {
	t.Helper()
	name := strings.TrimPrefix(c.Name, "/")
	mails, notes := withMail(t, map[string]containerInfo{name: c}, mailConfig())
	recorded := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(e utils.SecurityEvent) { recorded = append(recorded, e) }, func() {})
	out := map[string]string{}
	_, _, body := call(SignInRoute, "GET", "/s", map[string]string{"containerId": name}, "", sudoed)
	out["the card's answer"] = body
	req := httptest.NewRequest("POST", "http://cosmos.example.org/", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, sudoed))
	afterCreation(req, creationRequest(name), docker.StartCheck{})
	for _, m := range *mails {
		out["the raw e-mail"] += string(m.msg)
		_, plain, htmlPart := readParts(t, m.msg)
		out["the e-mail's plain part"] += plain
		out["the e-mail's HTML part"] += htmlPart
	}
	raw, _ := json.Marshal(*notes)
	out["the notifications"] = string(raw)
	for _, secretID := range []string{"password.0", "connection.0"} {
		call(RevealRoute, "POST", "/r", map[string]string{"containerId": name}, `{"secret":"`+secretID+`","purpose":"copy"}`, sudoed)
	}
	raw, _ = json.Marshal(recorded)
	out["the security record"] = string(raw)
	return out
}

func TestTheLastCheckLeavesOutAFieldThatHoldsASecret(t *testing.T) {
	d := Details{Container: "x", Name: "abc-secret-123", Accounts: []Account{{User: "u", Email: "secret-123@example.org", Connection: "postgres://u:<password>@x/db"}},
		Addresses: []Address{{URL: "https://x.example.org/?k=secret-123"}, {URL: "https://y.example.org"}}}
	clean(&d, []string{"secret-123"})
	if d.Name != "" || d.Accounts[0].Email != "" || d.Accounts[0].User != "u" || d.Accounts[0].Connection == "" || len(d.Addresses) != 1 {
		t.Fatalf("%+v", d)
	}
	if !holdsSecret("mysql://a:b@host/db", nil) || holdsSecret("postgres://u:<password>@x/db", nil) {
		t.Fatal("addresses with credentials")
	}
}
