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
	d := Details{Container: "x", Name: "abc-secret-123", Accounts: []Account{{User: "u", Email: "Secret-123@example.org", Connection: "postgres://u:<password>@x/db"}},
		Addresses: []Address{{URL: "https://y.example.org"}}}
	clean(&d, []string{"secret-123"})
	// only the fields from variables are compared, case-insensitively; fixed words and names are not
	if d.Name != "abc-secret-123" || d.Accounts[0].Email != "" || d.Accounts[0].User != "u" || d.Accounts[0].Connection == "" || len(d.Addresses) != 1 {
		t.Fatalf("%+v", d)
	}
	if len(d.Withheld) != 1 || d.Withheld[0] != "e-mail" {
		t.Fatalf("withheld: %v", d.Withheld)
	}
	if !holdsSecret("mysql://a:b@host/db", nil) || holdsSecret("postgres://u:<password>@x/db", nil) || !holdsSecret("root:pw@tcp(db:3306)/app", nil) {
		t.Fatal("addresses with credentials")
	}
	// a switch or a very short value is not a secret for this check
	if got := secretsOf(map[string]string{"PASSWORD_ACCESS": "true", "ADMIN_PIN": "12", "ADMIN_PASSWORD": "Admin"}); len(got) != 1 || got[0] != "admin" {
		t.Fatalf("secrets: %v", got)
	}
	for _, n := range []string{"ADMIN_PIN", "VNC_PW", "TS_AUTHKEY", "SONARR__AUTH__APIKEY", "WIREGUARD_PRIVATEKEY", "JWT", "OTP_SEED", "AUTH_HASH", "DSN", "CONNECTION_STRING", "ADMIN_PASSWORDS", "SESSION_COOKIE", "API_AUTH", "KEY_ID", "PASSWORD_SALT"} {
		if !secretName(n) {
			t.Errorf("%s is not taken for a secret", n)
		}
	}
	for _, n := range []string{"ADMIN_USER", "PUID", "TZ", "DB_HOST", "SPINNER", "AUTHOR"} {
		if secretName(n) {
			t.Errorf("%s is taken for a secret", n)
		}
	}
}

// Review 2, R2: weak or switch-like passwords never stop the card or the e-mail, and none is shown as a field.
func TestWeakPasswordsDoNotStopTheCardOrTheEmail(t *testing.T) {
	cases := []containerInfo{
		{Name: "/pg", Image: "postgres:16", Env: []string{"POSTGRES_PASSWORD=postgres"}},
		{Name: "/my", Image: "mysql:8.4", Env: []string{"MYSQL_ROOT_PASSWORD=root", "MYSQL_USER=app", "MYSQL_PASSWORD=apppw1", "MYSQL_DATABASE=app"}},
		{Name: "/forms", Image: "registry.example.org/a/forms:1", Env: []string{"ADMIN_USER=admin", "ADMIN_PASSWORD=admin"}},
		{Name: "/forms2", Image: "registry.example.org/a/forms:1", Env: []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=password"}},
		{Name: "/ssh", Image: "lscr.io/linuxserver/openssh-server:latest", Env: []string{"PASSWORD_ACCESS=true", "USER_NAME=owner", "USER_PASSWORD=Sshpw-77"}},
	}
	for _, c := range cases {
		name := strings.TrimPrefix(c.Name, "/")
		mails, _ := withMail(t, map[string]containerInfo{name: c}, mailConfig())
		routesNow = func() []utils.ProxyRouteConfig {
			return []utils.ProxyRouteConfig{{Name: name, Mode: "SERVAPP", UseHost: true, Host: name + ".example.org", Target: "http://" + name + ":80", AuthEnabled: true, AdminOnly: true}}
		}
		code, _, body := call(SignInRoute, "GET", "/s", map[string]string{"containerId": name}, "", sudoed)
		if code != 200 {
			t.Fatalf("%s: the card answers %d %s", name, code, body)
		}
		req := httptest.NewRequest("POST", "http://cosmos.example.org/", nil)
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, sudoed))
		afterCreation(req, creationRequest(name), docker.StartCheck{})
		if len(*mails) != 1 {
			t.Fatalf("%s: the e-mail was not sent", name)
		}
		env := envMap(c.Env)
		d := resolve(c, routesNow(), serverURL)
		if name == "pg" || name == "my" {
			// third review: the recipe's own account name ("postgres", "root") is no value of a
			// variable: it stays, also when the password is the same word, and nothing is said withheld
			clean(&d, secretsOf(env))
			if len(d.Accounts) == 0 || d.Accounts[0].User == "" || len(d.Withheld) != 0 {
				t.Errorf("%s: the fixed account name was withheld: %+v withheld %v", name, d.Accounts, d.Withheld)
			}
			if !strings.Contains(decodedMail((*mails)[0].msg), d.Accounts[0].User) {
				t.Errorf("%s: the e-mail does not name the account %q", name, d.Accounts[0].User)
			}
		}
		fixedNames := map[string]bool{} // the recipe's own account names: public words, not values of variables
		for _, a := range d.Accounts {
			shown := []string{a.Email, a.Database}
			if a.userFixed {
				fixedNames[strings.ToLower(a.User)] = true
			} else {
				shown = append(shown, a.User)
			}
			for _, secret := range secretsOf(env) {
				for _, v := range shown {
					if v != "" && strings.EqualFold(v, secret) {
						t.Errorf("%s: a password shown as a field: %+v", name, a)
					}
				}
			}
			if a.Password != "" && !strings.HasPrefix(a.Password, "password.") {
				t.Errorf("%s: %+v", name, a)
			}
		}
		_, plain, _ := readParts(t, (*mails)[0].msg)
		for _, secret := range secretsOf(env) {
			for _, field := range []string{"username ", "e-mail ", "database "} {
				if field == "username " && fixedNames[strings.ToLower(secret)] {
					continue
				}
				if strings.Contains(strings.ToLower(plain), field+secret) {
					t.Errorf("%s: the e-mail shows the password as a %s", name, field)
				}
			}
		}
		if name == "forms" && (len(d.Withheld) != 1 || d.Withheld[0] != "username") {
			t.Errorf("forms: the username equal to the password is withheld and said: %+v", d)
		}
	}
}

// decodedMail is a message with its quoted-printable soft breaks and "=3D" undone, enough to look for a word in it.
func decodedMail(msg []byte) string {
	text := strings.ReplaceAll(string(msg), "=\r\n", "")
	text = strings.ReplaceAll(text, "=\n", "")
	return strings.ReplaceAll(text, "=3D", "=")
}
