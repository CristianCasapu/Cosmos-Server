package appsignin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Secrets made up for the tests, built from parts so no scanner takes them for real ones.
var (
	testPassword = "Kq7-" + "xY.z~9" + "Ab_Cd" + "Ef12"
	testRoot     = "Rt-" + "9988" + "zz.QQ"
	testOther    = "other" + "-secret-" + "value"
)

func postgresContainer() containerInfo {
	return containerInfo{
		Name:   "/notes-db",
		Image:  "postgres:18",
		Env:    []string{"POSTGRES_PASSWORD=" + testPassword, "POSTGRES_USER=owner_name", "POSTGRES_DB=notes", "OTHER_TOKEN=" + testOther, "PATH=/usr/bin"},
		Mounts: []mountInfo{{Type: "bind", Source: "/mnt/data/cosmos-storage/notes-db/postgresql", Destination: "/var/lib/postgresql"}},
	}
}

func noRoutes() []utils.ProxyRouteConfig { return nil }

func testServerURL(host string) string {
	if host == "" {
		host = "cosmos.example.org"
	}
	return "https://" + host + ":2053/"
}

// mustHaveNoSecret fails when any secret appears in what may be shown.
func mustHaveNoSecret(t *testing.T, what string, text string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Fatalf("%s holds a secret %q: %s", what, s, text)
		}
	}
}

func TestARecipeNamesTheSignInAndTheDetailsHoldNoSecret(t *testing.T) {
	c := postgresContainer()
	d := resolve(c, noRoutes(), testServerURL)
	if d.Source != "recipe" || d.Where != "database" || d.Host != "notes-db" || d.Port != 5432 {
		t.Fatalf("details: %+v", d)
	}
	if len(d.Accounts) == 0 || d.Accounts[0].User != "owner_name" || d.Accounts[0].Database != "notes" || d.Accounts[0].Password != "password.0" {
		t.Fatalf("account: %+v", d.Accounts)
	}
	if d.Accounts[0].Connection != "postgres://owner_name:<password>@notes-db:5432/notes" || d.Accounts[0].ConnectionSecret != "connection.0" {
		t.Fatalf("connection: %+v", d.Accounts[0])
	}
	if len(d.Data) != 1 || d.Data[0].Folder != "/mnt/data/cosmos-storage/notes-db/postgresql" {
		t.Fatalf("data: %+v", d.Data)
	}
	out, _ := json.Marshal(d)
	mustHaveNoSecret(t, "the details", string(out), testPassword, testOther)
}

func TestRevealGivesOnlyTheNamedVariable(t *testing.T) {
	c := postgresContainer()
	d := resolve(c, noRoutes(), testServerURL)
	value, ok := secretValue(d, c, "password.0")
	if !ok || value != testPassword {
		t.Fatalf("password: %q %v", value, ok)
	}
	value, ok = secretValue(d, c, "connection.0")
	if !ok || !strings.HasPrefix(value, "postgres://owner_name:") || !strings.HasSuffix(value, "@notes-db:5432/notes") {
		t.Fatalf("connection: %q", value)
	}
	// the URL-unreserved symbols stay as they are, so the address is the password itself
	if !strings.Contains(value, ":"+testPassword+"@") {
		t.Fatalf("connection does not carry the password as is: %q", value)
	}
	for _, id := range []string{"password.1", "connection.3", "OTHER_TOKEN", "password", ""} {
		if v, ok := secretValue(d, c, id); ok || v != "" {
			t.Errorf("%q gave %q", id, v)
		}
	}
	// a variable that is not the sign-in is never reachable, whatever the label says
	c.Labels = map[string]string{LabelKey: "where=web;password=OTHER_TOKEN"}
	d = resolve(c, noRoutes(), testServerURL)
	if v, _ := secretValue(d, c, "password.0"); v != testPassword {
		t.Errorf("the recipe must win over a label: %q", v)
	}
}

func TestAPasswordWithReservedCharactersIsEncodedInTheConnection(t *testing.T) {
	c := postgresContainer()
	c.Env[0] = "POSTGRES_PASSWORD=a@b:c/d?e#f%g h"
	d := resolve(c, noRoutes(), testServerURL)
	value, _ := secretValue(d, c, "connection.0")
	if !strings.Contains(value, ":a%40b%3Ac%2Fd%3Fe%23f%25g%20h@notes-db") {
		t.Fatalf("not encoded: %q", value)
	}
}

func TestTheLabelNamesVariablesOnly(t *testing.T) {
	good := []string{
		"where=web;user=ADMIN_USER;password=ADMIN_PASSWORD;from=market",
		"where=web;first=owner;note=firstRunPage",
		"user=MOODLE_USERNAME;email=MOODLE_EMAIL;password=MOODLE_PASSWORD;from=guess",
		"where=database;user=A;password=B;database=C",
	}
	for _, v := range good {
		if _, ok := parseLabel(v); !ok {
			t.Errorf("refused: %q", v)
		}
	}
	bad := []string{
		"", "where=web;password=ADMIN PASSWORD", "where=web;password=$X", "where=moon", "value=secret",
		"where=web;where=web", "password=A=B", "first=a;b", "note=other", "from=elsewhere",
		strings.Repeat("user=A;", 9), "user=" + strings.Repeat("A", 70), "first=" + strings.Repeat("a", 65),
		"where=web;user=A\nB",
	}
	for _, v := range bad {
		if _, ok := parseLabel(v); ok {
			t.Errorf("taken: %q", v)
		}
	}
}

func TestAFirstRunAppSaysCosmosDidNotSetIt(t *testing.T) {
	c := containerInfo{Name: "/jellyfin", Image: "jellyfin/jellyfin:10.11", Env: []string{"TZ=Europe/Paris"}}
	d := resolve(c, noRoutes(), testServerURL)
	if d.Source != "firstRun" || d.FirstRun == nil || d.FirstRun.Note != "firstRunPage" || len(d.Accounts) != 0 {
		t.Fatalf("details: %+v", d)
	}
	// the username proposed at installation comes from the label
	c.Labels = map[string]string{LabelKey: "where=web;first=owner_name;note=firstRunPage"}
	d = resolve(c, noRoutes(), testServerURL)
	if d.FirstRun == nil || d.FirstRun.User != "owner_name" {
		t.Fatalf("label: %+v", d.FirstRun)
	}
}

func TestGuessesFollowTheSharedCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/guesses.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Names []string `json:"names"`
			Want  *struct {
				User     string `json:"user"`
				Email    string `json:"email"`
				Password string `json:"password"`
			} `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 10 {
		t.Fatal("too few cases")
	}
	for _, c := range file.Cases {
		user, email, password, ok := GuessFromNames(c.Names)
		if c.Want == nil {
			if ok {
				t.Errorf("%v: guessed %s %s %s", c.Names, user, email, password)
			}
			continue
		}
		if !ok || user != c.Want.User || email != c.Want.Email || password != c.Want.Password {
			t.Errorf("%v: %q %q %q %v, want %+v", c.Names, user, email, password, ok, *c.Want)
		}
	}
}

func TestAGuessIsSaidToBeOne(t *testing.T) {
	c := containerInfo{Name: "/forms", Image: "registry.example.org/team/forms:1", Env: []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=" + testPassword, "SMTP_PASSWORD=" + testOther}}
	d := resolve(c, noRoutes(), testServerURL)
	if d.Source != "guess" || len(d.Accounts) != 1 || d.Accounts[0].Email != "owner@example.org" || d.Accounts[0].PasswordVar != "ADMIN_PASSWORD" {
		t.Fatalf("details: %+v", d)
	}
	if v, _ := secretValue(d, c, "password.0"); v != testPassword {
		t.Fatalf("value: %q", v)
	}
	out, _ := json.Marshal(d)
	mustHaveNoSecret(t, "the details", string(out), testPassword, testOther)
}

func TestTheAddressesAreTheRoutesOfThisContainer(t *testing.T) {
	routes := []utils.ProxyRouteConfig{
		{Name: "notes", Mode: "SERVAPP", UseHost: true, Host: "notes.example.org", Target: "http://notes:8080"},
		{Name: "admin", Mode: "SERVAPP", UseHost: true, Host: "notes-admin.example.org", Target: "http://notes:9000", AdminOnly: true, AuthEnabled: true},
		{Name: "other", Mode: "SERVAPP", UseHost: true, Host: "other.example.org", Target: "http://notes-db:5432"},
		{Name: "off", Mode: "SERVAPP", UseHost: true, Host: "off.example.org", Target: "http://notes:8080", Disabled: true},
		{Name: "static", Mode: "STATIC", UseHost: true, Host: "static.example.org", Target: "http://notes:8080"},
	}
	got := addressesOf("notes", routes, testServerURL)
	if len(got) != 2 || got[0].URL != "https://notes.example.org:2053" || !got[1].AdminOnly || !got[1].SignIn {
		t.Fatalf("addresses: %+v", got)
	}
}
