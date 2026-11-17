package anyimage

// Plan 45: the sign-in rules of the recipes (login), the apps that make their
// first account in their own page (firstRun) and the web managers. What can
// only be proved against the real images is in fork_login_live_test.go.

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const (
	lowerLetters = "abcdefghijklmnopqrstuvwxyz"
	upperLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitChars   = "0123456789"
	// the only symbols a generated password may hold: they never need
	// encoding in a URL and mean nothing to a shell, SQL quoting or YAML
	unreservedSymbols = "-._~"
)

var loginWheres = map[string]bool{"database": true, "web": true, "s3": true, "amqp": true, "http": true}
var accountRoles = map[string]bool{"app": true, "admin": true, "root": true}
var passwordClasses = map[string]bool{"lower": true, "upper": true, "digit": true, "symbol": true}
var firstRunNotes = map[string]bool{"firstRunPage": true, "logPassword": true}
var managerRoles = map[string]bool{"host": true, "port": true, "user": true, "password": true, "database": true, "url": true}

// jsOnly: regular expression syntax JavaScript reads differently from Go's
// RE2, or not at all (lookbehind, inline flags, \A \z, Unicode classes…).
var jsOnly = []string{`(?<`, `(?i`, `(?m`, `(?s`, `(?U`, `(?P`, `(?=`, `(?!`, `\A`, `\z`, `\Z`, `\p`, `\P`, `\Q`, `\C`, `[[:`, `\b`, `\B`}

func randomIndex(n int) int {
	i, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(i.Int64())
}

// generatePassword is what the panel's generator does: one character of each
// class the rule allows (both cases of letters, digits, and here every symbol
// of the rule so that a live test proves each one), the rest from the whole
// set, shuffled; length is kept inside the rule's bounds.
func generatePassword(rule *RecipePasswordRule, length int) string {
	if length < rule.Min {
		length = rule.Min
	}
	if length > rule.Max {
		length = rule.Max
	}
	all := lowerLetters + upperLetters + digitChars + rule.Symbols
	out := []byte{lowerLetters[randomIndex(len(lowerLetters))], upperLetters[randomIndex(len(upperLetters))], digitChars[randomIndex(len(digitChars))]}
	out = append(out, rule.Symbols...)
	for len(out) < length {
		out = append(out, all[randomIndex(len(all))])
	}
	for i := len(out) - 1; i > 0; i-- {
		j := randomIndex(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// checkPassword answers why a password breaks the rule, "" when it does not.
func checkPassword(rule *RecipePasswordRule, password string) string {
	if len(password) < rule.Min || len(password) > rule.Max {
		return fmt.Sprintf("length %d outside %d..%d", len(password), rule.Min, rule.Max)
	}
	has := map[string]bool{}
	for _, c := range password {
		switch {
		case strings.ContainsRune(lowerLetters, c):
			has["lower"] = true
		case strings.ContainsRune(upperLetters, c):
			has["upper"] = true
		case strings.ContainsRune(digitChars, c):
			has["digit"] = true
		case strings.ContainsRune(rule.Symbols, c):
			has["symbol"] = true
		default:
			return fmt.Sprintf("%q is not allowed", c)
		}
	}
	for _, class := range rule.Require {
		if !has[class] {
			return "no " + class
		}
	}
	return ""
}

var notUserChar = regexp.MustCompile(`[^a-z0-9_]`)

// userVariant is the username the rule makes of a configured default: lower
// case, other characters to "_", a leading digit prefixed, cut to the
// maximum, a reserved name suffixed. "" when the result still does not fit
// the pattern (the panel then keeps the recipe's own proposal).
func userVariant(name string, rule *RecipeUserRule) string {
	v := notUserChar.ReplaceAllString(strings.ToLower(name), "_")
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		v = "u_" + v
	}
	cut := func(s string, max int) string {
		if len(s) > max {
			return s[:max]
		}
		return s
	}
	v = cut(v, rule.Max)
	for _, reserved := range rule.Reserved {
		if v == reserved {
			v = cut(v, rule.Max-2) + "_1"
		}
	}
	if !regexp.MustCompile(rule.Pattern).MatchString(v) {
		return ""
	}
	return v
}

func TestRecipeLogins(t *testing.T) {
	withLogin := 0
	for _, r := range recipes.Recipes {
		l := r.Login
		if l == nil {
			continue
		}
		withLogin++
		env := map[string]bool{}
		for _, e := range r.Env {
			env[e.Name] = true
		}
		if !loginWheres[l.Where] {
			t.Errorf("%s: where %q", r.ID, l.Where)
		}
		if l.Via != "" {
			if l.Via != "database" || l.Where != "web" || r.Kind != "tool" || len(l.Accounts) > 0 || l.Password != nil || r.Needs == nil {
				t.Errorf("%s: a tool one signs in to with the database's accounts has no accounts or rules of its own", r.ID)
			}
			continue
		}
		if len(l.Accounts) == 0 || l.Password == nil {
			t.Errorf("%s: a login without accounts or a password rule", r.ID)
			continue
		}
		defaults, changeable := 0, 0
		for _, a := range l.Accounts {
			if !accountRoles[a.Role] || a.Password == "" || (a.User != "" && a.UserFixed != "") || (a.User == "" && a.UserUnset != "") {
				t.Errorf("%s: account %+v", r.ID, a)
			}
			for _, name := range []string{a.User, a.Email, a.Password, a.Database} {
				if name != "" && !env[name] {
					t.Errorf("%s: the account's %s is not one of its variables", r.ID, name)
				}
			}
			for _, e := range r.Env {
				if e.Name == a.Password && e.Type != "secret" {
					t.Errorf("%s: %s is a password the panel does not generate", r.ID, a.Password)
				}
			}
			if a.User != "" || a.Email != "" {
				changeable++
			}
			if a.Defaults {
				defaults++
				if a.Role == "root" && len(l.Accounts) > 1 {
					t.Errorf("%s: the defaults go to the app's account, never root", r.ID)
				}
				if a.User == "" && a.Email == "" {
					t.Errorf("%s: the defaults' account has no user or e-mail to take them", r.ID)
				}
			}
		}
		// exactly one account takes the defaults, where an account has a username or e-mail one may choose
		if (changeable > 0 && defaults != 1) || (changeable == 0 && defaults != 0) {
			t.Errorf("%s: %d accounts take the defaults", r.ID, defaults)
		}
		p := l.Password
		if p.Min < 1 || p.Min > p.Max || p.Max > 64 || p.Evidence == "" {
			t.Errorf("%s: password length %d..%d (at most 64), evidence %q", r.ID, p.Min, p.Max, p.Evidence)
		}
		seen := map[rune]bool{}
		for _, c := range p.Symbols {
			if !strings.ContainsRune(unreservedSymbols, c) || seen[c] {
				t.Errorf("%s: symbol %q (only %s, each once)", r.ID, c, unreservedSymbols)
			}
			seen[c] = true
		}
		for _, class := range p.Require {
			if !passwordClasses[class] || (class == "symbol" && p.Symbols == "") {
				t.Errorf("%s: requires %q", r.ID, class)
			}
		}
		if p.Min < len(p.Require) {
			t.Errorf("%s: %d classes required in at least %d characters", r.ID, len(p.Require), p.Min)
		}
		if u := l.User; u != nil {
			if u.Evidence == "" || u.Max < 1 || !strings.HasPrefix(u.Pattern, "^") || !strings.HasSuffix(u.Pattern, "$") {
				t.Errorf("%s: user rule %+v", r.ID, u)
			}
			re, err := regexp.Compile(u.Pattern)
			if err != nil {
				t.Errorf("%s: the user pattern does not compile: %v", r.ID, err)
				continue
			}
			for _, token := range jsOnly {
				if strings.Contains(u.Pattern, token) {
					t.Errorf("%s: the user pattern uses %s, which JavaScript reads differently", r.ID, token)
				}
			}
			for _, reserved := range u.Reserved {
				if reserved == "" || len(reserved) > u.Max {
					t.Errorf("%s: reserved %q", r.ID, reserved)
				}
			}
			// the recipe's own proposals fit the rule (else the panel would propose a refused name)
			for _, a := range l.Accounts {
				for _, e := range r.Env {
					if a.User != "" && e.Name == a.User && a.Defaults && (!re.MatchString(e.Value) || len(e.Value) > u.Max) {
						t.Errorf("%s: its proposal %q breaks its own user rule", r.ID, e.Value)
					}
				}
			}
			if userVariant("Owner.Name", u) == "" {
				t.Errorf("%s: a plain default makes no username", r.ID)
			}
		}
		if e := l.Email; e != nil && e.Evidence == "" {
			t.Errorf("%s: e-mail rule without evidence", r.ID)
		}
	}
	if withLogin < 24 {
		t.Errorf("only %d recipes with a login", withLogin)
	}
	// every recipe that generates a secret says how one signs in with it
	for _, r := range recipes.Recipes {
		for _, e := range r.Env {
			if e.Type == "secret" && r.Login == nil && r.ID != "redisinsight" {
				t.Errorf("%s: generates %s but has no login", r.ID, e.Name)
			}
		}
	}
}

func TestRecipeKnownRules(t *testing.T) {
	user := func(id string) *RecipeUserRule { return byID(id).Login.User }
	pass := func(id string) *RecipePasswordRule { return byID(id).Login.Password }
	if user("mysql").Max != 32 || user("percona-server").Max != 32 || user("mariadb").Max != 80 || user("postgres").Max != 63 {
		t.Error("user name lengths")
	}
	for _, id := range []string{"mysql", "percona-server", "mariadb", "bitnami-mariadb", "linuxserver-mariadb"} {
		if userVariant("root", user(id)) == "root" {
			t.Errorf("%s: root as the app's user", id)
		}
	}
	pg := regexp.MustCompile(user("postgres").Pattern)
	for name, want := range map[string]bool{"app": true, "postgres": true, "p": true, "pg": true, "pgx": true, "pga_x": true, "pg_monitor": false, "pg_": false, "1app": false, "App": false, "a-b": false} {
		if pg.MatchString(name) != want {
			t.Errorf("postgres user %q: %v", name, !want)
		}
	}
	if userVariant("public", user("postgres")) != "public_1" || userVariant("guest", user("rabbitmq")) != "guest_1" {
		t.Error("a reserved name is suffixed")
	}
	if userVariant("Owner.Name", user("postgres")) != "owner_name" || userVariant("2nd Owner", user("mysql")) != "u_2nd_owner" {
		t.Error("variant")
	}
	if got := userVariant(strings.Repeat("a", 70), user("mysql")); len(got) != 32 {
		t.Errorf("cut to 32: %q", got)
	}
	if userVariant("ab", user("rustfs")) != "" {
		t.Error("an access key of 2 characters")
	}
	os := pass("opensearch")
	if os.Min < 8 || strings.Join(os.Require, ",") != "lower,upper,digit,symbol" {
		t.Errorf("OpenSearch requires all four classes: %+v", os)
	}
	if byID("pgadmin").Login.Email == nil || !byID("pgadmin").Login.Email.RefuseSpecialUse {
		t.Error("pgAdmin's e-mail validator refuses special-use domains")
	}
	for _, id := range []string{"adminer", "phpmyadmin"} {
		if l := byID(id).Login; l == nil || l.Via != "database" {
			t.Errorf("%s signs in with the database's accounts", id)
		}
	}
	for _, id := range []string{"redis", "valkey", "redis-stack", "redis-stack-server", "memcached", "nginx", "caddy", "traefik", "redisinsight"} {
		if byID(id).Login != nil {
			t.Errorf("%s has no sign-in of its own", id)
		}
	}
}

// A password made by the generator holds every class its rule allows and
// satisfies the rule, at every length the rule permits.
func TestGeneratedPasswordsFitTheirRules(t *testing.T) {
	for _, r := range recipes.Recipes {
		if r.Login == nil || r.Login.Password == nil {
			continue
		}
		rule := r.Login.Password
		for length := rule.Min; length <= rule.Max; length++ {
			if length < 3+len(rule.Symbols) {
				continue // too short to hold one of each: the generator lengthens it
			}
			for i := 0; i < 5; i++ {
				pw := generatePassword(rule, length)
				if why := checkPassword(rule, pw); why != "" || len(pw) != length {
					t.Fatalf("%s: %q (%d) breaks its rule: %s", r.ID, pw, length, why)
				}
				for _, class := range []string{lowerLetters, upperLetters, digitChars} {
					if !strings.ContainsAny(pw, class) {
						t.Fatalf("%s: %q misses a class", r.ID, pw)
					}
				}
				for _, s := range rule.Symbols {
					if !strings.ContainsRune(pw, s) {
						t.Fatalf("%s: %q misses %q", r.ID, pw, s)
					}
				}
				// it needs no encoding in a URL
				if url.PathEscape(pw) != pw || url.QueryEscape(pw) != pw {
					t.Fatalf("%s: %q needs encoding in an address", r.ID, pw)
				}
			}
		}
		if pw := generatePassword(rule, 1); checkPassword(rule, pw) != "" {
			t.Errorf("%s: a short length is raised to the rule's minimum", r.ID)
		}
	}
	// the checker refuses what the rules forbid
	rule := &RecipePasswordRule{Min: 8, Max: 12, Symbols: "-", Require: []string{"upper", "symbol"}}
	for pw, want := range map[string]bool{"Abcdefg-": true, "abcdefg-": false, "Abcdefgh": false, "Abcdef_-": false, "Ab-": false, "Abcdefghijk-x": false} {
		if (checkPassword(rule, pw) == "") != want {
			t.Errorf("%q: %s", pw, checkPassword(rule, pw))
		}
	}
}

func TestFirstRunApps(t *testing.T) {
	if len(recipes.FirstRun) < 10 {
		t.Fatalf("only %d first-run apps", len(recipes.FirstRun))
	}
	ids, images := map[string]bool{}, map[string]bool{}
	for _, f := range recipes.FirstRun {
		if f.ID == "" || ids[f.ID] || f.Name == "" || len(f.Images) == 0 || !firstRunNotes[f.Note] {
			t.Errorf("%q: id, name, images or note %q", f.ID, f.Note)
		}
		ids[f.ID] = true
		if u, err := url.Parse(f.Source); err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("%s: the source must be the documentation's https address", f.ID)
		}
		if (f.Note == "logPassword") != (f.User != "") {
			t.Errorf("%s: a fixed user goes with logPassword only", f.ID)
		}
		for _, image := range f.Images {
			ref, err := Parse(image, nil)
			if err != nil || imageName(ref) != image || image != strings.ToLower(image) || images[image] {
				t.Errorf("%s: image %q (as recipes write it: lower case, no docker.io/ or library/)", f.ID, image)
			}
			images[image] = true
			if recipeFor(ref) != nil {
				t.Errorf("%s: %s also has a recipe", f.ID, image)
			}
			if got := FirstRunForImage(image + ":latest"); got == nil || got.ID != f.ID {
				t.Errorf("%s: %s is not found by FirstRunForImage", f.ID, image)
			}
		}
	}
	for _, image := range []string{"jellyfin/jellyfin", "lscr.io/linuxserver/sonarr", "ghcr.io/hotio/radarr", "emby/embyserver", "portainer/portainer-ce", "linuxserver/qbittorrent"} {
		if FirstRunForImage(image) == nil {
			t.Errorf("%s: not a first-run app", image)
		}
	}
	if q := FirstRunForImage("lscr.io/linuxserver/qbittorrent:latest"); q == nil || q.Note != "logPassword" || q.User != "admin" {
		t.Errorf("qBittorrent: %+v", q)
	}
	if FirstRunForImage("postgres") != nil || FirstRunForImage("docker.io/library/jellyfin") != nil {
		t.Error("near names")
	}
}

func TestManagers(t *testing.T) {
	provided := map[string]bool{}
	for _, r := range recipes.Recipes {
		if r.Provides != "" {
			provided[r.Provides] = true
		}
	}
	ranks := map[string]map[int]string{}
	for _, r := range recipes.Recipes {
		if r.Manager == nil {
			continue
		}
		m := r.Manager
		if r.Kind != "tool" || r.Web == nil || r.Needs == nil || m.Rank < 1 || len(m.For) == 0 {
			t.Errorf("%s: a manager is a web tool that needs a database, with a rank", r.ID)
			continue
		}
		for _, kind := range m.For {
			if !provided[kind] || !contains(r.Needs.Kinds, kind) {
				t.Errorf("%s: manages %s, which no recipe provides or it does not need", r.ID, kind)
			}
			if ranks[kind] == nil {
				ranks[kind] = map[int]string{}
			}
			if other := ranks[kind][m.Rank]; other != "" {
				t.Errorf("%s and %s share rank %d for %s", r.ID, other, m.Rank, kind)
			}
			ranks[kind][m.Rank] = r.ID
		}
		env := map[string]bool{}
		for _, e := range r.Env {
			env[e.Name] = true
		}
		for role, name := range r.Needs.Env {
			if !managerRoles[role] || !envName.MatchString(name) || env[name] {
				t.Errorf("%s: needs.env %s=%s (a role it fills, not a variable the recipe sets itself)", r.ID, role, name)
			}
		}
		// a login the manager takes (its own, or the database's)
		if r.Login == nil && !contains(r.Notes, "noAuth") {
			t.Errorf("%s: a manager says how one signs in to it, or that it has no sign-in", r.ID)
		}
	}
	for kind, want := range map[string][]string{"postgres": {"pgadmin", "adminer"}, "mysql": {"phpmyadmin", "adminer"}, "redis": {"redisinsight"}, "mongo": {}} {
		got := []string{}
		for _, m := range Managers(kind) {
			got = append(got, m.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("managers of %s: %v, want %v", kind, got, want)
		}
	}
	// Adminer's image has no MongoDB extension: it does not manage mongo
	if contains(byID("adminer").Needs.Kinds, "mongo") {
		t.Error("adminer needs mongo")
	}
}

// pgAdmin reads servers.json and a pgpass file only from files: a wrapper
// writes them from two variables into its own volume, then runs the image's
// own entrypoint. The script itself holds no value.
func TestPgAdminWrapper(t *testing.T) {
	r := byID("pgadmin")
	if r.Needs.Servers == "" || r.Needs.PassFile == "" || r.Needs.Servers == r.Needs.PassFile {
		t.Fatalf("needs %+v", r.Needs)
	}
	ep := r.Entrypoint
	if len(ep) != 4 || ep[0] != "/bin/sh" || ep[1] != "-c" {
		t.Fatalf("entrypoint %q", ep)
	}
	script := ep[2]
	for _, want := range []string{"umask 077", "exec /entrypoint.sh \"$@\"", "/var/lib/pgadmin/", `"$` + r.Needs.Servers + `"`, `"$` + r.Needs.PassFile + `"`, "unset " + r.Needs.Servers + " " + r.Needs.PassFile} {
		if !strings.Contains(script, want) {
			t.Errorf("the wrapper lacks %q", want)
		}
	}
	// no value in the script: no assignment of a variable it reads, no eval, no expansion outside double quotes
	for _, bad := range []string{"eval", "PASSWORD", "=", "`", "$(", "@"} {
		if bad == "=" || bad == "@" {
			// "=" and "@" appear only in "$@" and the d= of the folder
			if strings.Count(script, bad) != map[string]int{"=": 1, "@": 1}[bad] {
				t.Errorf("the wrapper has %q where it should not", bad)
			}
			continue
		}
		if strings.Contains(script, bad) {
			t.Errorf("the wrapper has %q", bad)
		}
	}
	unquoted := regexp.MustCompile(`(^|[^"])\$[A-Z{]`)
	if unquoted.MatchString(script) {
		t.Error("an expansion outside double quotes")
	}
	files := map[string]string{}
	for _, e := range r.Env {
		files[e.Name] = e.Value
	}
	if files["PGADMIN_SERVER_JSON_FILE"] != "/var/lib/pgadmin/cosmos/servers.json" || files["PGPASS_FILE"] != "/var/lib/pgadmin/cosmos/pgpass" {
		t.Errorf("the image reads the files the wrapper writes: %v", files)
	}
	for _, v := range r.Volumes {
		if v.Target == "/var/lib/pgadmin" && v.KeepVolume {
			return
		}
	}
	t.Error("the files are written into pgAdmin's own volume")
}
