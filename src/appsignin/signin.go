// Package appsignin is the fork's sign-in details of apps (plan 45): what a
// container's own settings say about how one signs in to it, shown on its
// page to those who may read credentials, and the e-mail and notification
// sent when an app is installed. The values come from the container's
// environment, read when asked; nothing here keeps a password.
//
// Which variables are the sign-in, strongest first: the recipe of the image
// (src/anyimage, `login`), the label the review of the installation set
// (`cosmos-fork-signin`, names of variables only), the first-run list (apps
// that make their first account in their own page), and a guess by the
// variables' names.
package appsignin

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/utils"
)

// LabelKey is the label the review of an installation sets.
const LabelKey = "cosmos-fork-signin"

// ManagerLabel names the database a web manager was installed for.
const ManagerLabel = "cosmos-fork-manager-of"

const (
	maxLabel    = 512
	maxAccounts = 4
	maxValue    = 200 // a username, an e-mail or a database name longer than this is not shown
)

// containerInfo is what is read of a container (docker inspect).
type containerInfo struct {
	Name   string
	Image  string
	Env    []string
	Labels map[string]string
	Mounts []mountInfo
}

type mountInfo struct {
	Type        string // bind, volume, tmpfs
	Source      string // a bind's folder on the server
	Name        string // a volume's name
	Destination string
}

// Address is one address Cosmos serves the app at.
type Address struct {
	URL       string `json:"url"`
	AdminOnly bool   `json:"adminOnly,omitempty"`
	SignIn    bool   `json:"signIn,omitempty"` // behind Cosmos' sign-in
}

// Account is one way to sign in, without its secret.
type Account struct {
	// userFixed: User is the recipe's own word (the image's fixed or default
	// account name), not a value of one of the container's variables: the last
	// check never withholds it ("postgres" stays the username of a database
	// whose password is "postgres" too).
	userFixed bool

	Role     string `json:"role"`
	User     string `json:"user,omitempty"`
	Email    string `json:"email,omitempty"`
	Database string `json:"database,omitempty"`
	// Password is the id of its password ("password.N") when the container has one
	Password string `json:"password,omitempty"`
	// Connection is the connection string with "<password>" in place of it,
	// ConnectionSecret the id that gives it with the password
	Connection       string `json:"connection,omitempty"`
	ConnectionSecret string `json:"connectionSecret,omitempty"`
	// UserVar etc. name the variables (shown to say where the value is kept)
	UserVar     string `json:"userVar,omitempty"`
	EmailVar    string `json:"emailVar,omitempty"`
	PasswordVar string `json:"passwordVar,omitempty"`
}

// FirstRun says the app makes its first account in its own page.
type FirstRun struct {
	Note   string `json:"note"`
	User   string `json:"user,omitempty"` // the username proposed at installation, or the fixed one
	Source string `json:"source,omitempty"`
}

// DataPlace is where some of its data is.
type DataPlace struct {
	Target string `json:"target"`
	Folder string `json:"folder,omitempty"`
	Volume string `json:"volume,omitempty"`
}

// Details is what the card shows, and the e-mail tells: never a secret.
type Details struct {
	Container string `json:"container"`
	Image     string `json:"image"`
	Name      string `json:"name,omitempty"`
	// Source: recipe, label, guess, firstRun or none
	Source string `json:"source"`
	// Where: database, web, s3, amqp, http, "" when not known
	Where        string      `json:"where,omitempty"`
	Via          string      `json:"via,omitempty"`
	ViaContainer string      `json:"viaContainer,omitempty"`
	ManagerOf    string      `json:"managerOf,omitempty"`
	Addresses    []Address   `json:"addresses"`
	Host         string      `json:"host,omitempty"`
	Port         int         `json:"port,omitempty"`
	Accounts     []Account   `json:"accounts"`
	FirstRun     *FirstRun   `json:"firstRun,omitempty"`
	Data         []DataPlace `json:"data"`
	// Managers: the web managers made for it (their label as the administrator set it, never an image's)
	Managers []string `json:"managers"`
	// Withheld: fields left out because they held a secret value of the app (secrets.go)
	Withheld []string `json:"withheld,omitempty"`
	// NoAuth: its recipe says it has no sign-in of its own (Redis, Redis Insight)
	NoAuth bool `json:"noAuth,omitempty"`
	// Provides: the kind of database it is (postgres, mysql, mongo, redis), for "Add a web manager"
	Provides string `json:"provides,omitempty"`

	// the variables of each secret id, and the container's secret values; never sent
	secrets      map[string]secretSource
	secretValues []string
}

type secretSource struct {
	variable string // a password: the variable that holds it
	account  int    // a connection: the account it is built for
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,63}$`)

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, line := range env {
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		out[line[:i]] = line[i+1:]
	}
	return out
}

// shown is a value that may be displayed: bounded and without control characters.
func shown(value string) string {
	if len(value) > maxValue {
		return ""
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return value
}

// login is the sign-in description used for a container, from whichever source.
type login struct {
	source   string
	name     string
	where    string
	via      string
	accounts []anyimage.RecipeLoginAccount
	client   *anyimage.RecipeClient
	port     int
	firstRun *FirstRun
}

func loginOf(c containerInfo, env map[string]string) login {
	recipe := anyimage.RecipeForImage(c.Image)
	if recipe != nil && recipe.Login != nil {
		l := login{source: "recipe", name: recipe.Name, where: recipe.Login.Where, via: recipe.Login.Via,
			accounts: recipe.Login.Accounts, client: recipe.Client}
		if recipe.Service != nil {
			l.port = recipe.Service.Port
		}
		return l
	}
	if label, ok := parseLabel(c.Labels[LabelKey]); ok {
		l := login{source: "label", where: label.where}
		if recipe != nil {
			l.name = recipe.Name
		}
		if label.password != "" || label.user != "" || label.email != "" {
			l.accounts = []anyimage.RecipeLoginAccount{{Role: "app", User: label.user, Email: label.email, Password: label.password, Database: label.database}}
		}
		if label.first != "" || label.firstNote != "" {
			note := label.firstNote
			if note == "" {
				note = "firstRunPage"
			}
			l.firstRun = &FirstRun{Note: note, User: label.first}
		}
		if label.guess {
			l.source = "guess"
		}
		if len(l.accounts) > 0 || l.firstRun != nil {
			return l
		}
	}
	if first := anyimage.FirstRunForImage(c.Image); first != nil {
		return login{source: "firstRun", name: first.Name, where: "web", firstRun: &FirstRun{Note: first.Note, User: first.User, Source: first.Source}}
	}
	if account, ok := guessAccount(env); ok {
		name := ""
		if recipe != nil {
			name = recipe.Name
		}
		return login{source: "guess", name: name, where: "", accounts: []anyimage.RecipeLoginAccount{account}}
	}
	l := login{source: "none"}
	if recipe != nil {
		l.name = recipe.Name
		if recipe.Login != nil {
			l.where = recipe.Login.Where
		}
	}
	return l
}

// resolve builds the details of a container. routes are the configuration's.
func resolve(c containerInfo, routes []utils.ProxyRouteConfig, serverURL func(host string) string) Details {
	env := envMap(c.Env)
	name := strings.TrimPrefix(c.Name, "/")
	l := loginOf(c, env)
	secrets := secretsOf(env)
	withheld := map[string]bool{}
	// a field from a variable, left out (and said) when it holds a secret value of the app
	pick := func(variable string, shape *regexp.Regexp, what string) string {
		v := field(env, variable, shape, secrets)
		if v == "" && variable != "" && holdsSecret(shown(env[variable]), secrets) {
			withheld[what] = true
		}
		return v
	}
	d := Details{Container: name, Image: c.Image, Name: l.name, Source: l.source, Where: l.where, Via: l.via,
		Addresses: addressesOf(name, routes, serverURL), Accounts: []Account{}, FirstRun: l.firstRun,
		Data: dataOf(c.Mounts), secrets: map[string]secretSource{}, secretValues: secrets}
	if recipe := anyimage.RecipeForImage(c.Image); recipe != nil && (recipe.Kind == "database" || recipe.Kind == "cache") {
		d.Provides = recipe.Provides
	}
	if recipe := anyimage.RecipeForImage(c.Image); recipe != nil && recipe.Login == nil {
		for _, note := range recipe.Notes {
			if note == "noAuth" {
				d.NoAuth = true
			}
		}
	}
	if manager := c.Labels[ManagerLabel]; manager != "" && validContainerName(manager) {
		d.ManagerOf = manager
	}
	if l.via == "database" {
		if recipe := anyimage.RecipeForImage(c.Image); recipe != nil && recipe.Needs != nil {
			if host := field(env, recipe.Needs.Env["host"], containerName, secrets); host != "" {
				d.ViaContainer = host
			}
		}
	}
	if l.where == "database" || (l.port > 0 && l.where != "web") {
		d.Host = name
		d.Port = l.port
	}
	for i, a := range l.accounts {
		if i >= maxAccounts {
			break
		}
		account := Account{Role: a.Role, UserVar: a.User, EmailVar: a.Email, PasswordVar: a.Password}
		switch {
		case a.UserFixed != "":
			account.User, account.userFixed = a.UserFixed, true
		case a.User != "" && pick(a.User, identifier, "username") != "":
			account.User = pick(a.User, identifier, "username")
		case a.UserUnset != "":
			account.User, account.userFixed = a.UserUnset, true
		}
		if a.Email != "" {
			account.Email = pick(a.Email, emailShape, "e-mail")
		}
		if a.Database != "" {
			account.Database = pick(a.Database, databaseShape, "database")
		}
		if a.Password != "" && env[a.Password] != "" {
			account.Password = "password." + strconv.Itoa(i)
			d.secrets[account.Password] = secretSource{variable: a.Password, account: i}
		}
		if l.client != nil && l.client.URL != "" && a.Password != "" && a.Password == l.client.Password && account.Password != "" {
			account.Connection = connection(l.client.URL, name, account, "<password>")
			account.ConnectionSecret = "connection." + strconv.Itoa(i)
			d.secrets[account.ConnectionSecret] = secretSource{variable: a.Password, account: i}
		}
		if secretName(a.User) || secretName(a.Email) || secretName(a.Database) {
			account.UserVar, account.EmailVar = "", "" // a label or a guess that named a secret: not repeated
		}
		d.Accounts = append(d.Accounts, account)
	}
	if d.FirstRun != nil && d.FirstRun.User != "" && (!identifier.MatchString(d.FirstRun.User) || holdsSecret(d.FirstRun.User, secrets)) {
		d.FirstRun.User = ""
	}
	for what := range withheld {
		d.Withheld = append(d.Withheld, what)
	}
	clean(&d, secrets)
	return d
}

// connection fills a recipe's client address; the password is URL-encoded
// unless it is the placeholder.
func connection(template string, host string, a Account, password string) string {
	if password != "<password>" {
		password = uriComponent(password)
	}
	out := strings.ReplaceAll(template, "{host}", host)
	out = strings.ReplaceAll(out, "{user}", uriComponent(a.User))
	out = strings.ReplaceAll(out, "{database}", uriComponent(a.Database))
	return strings.ReplaceAll(out, "{password}", password)
}

// uriComponent encodes everything but the unreserved characters (as the
// panel's encodeURIComponent does for these: @ : / ? # % and spaces).
func uriComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// secretValue is one secret of a container, by its id; "" when there is none.
func secretValue(d Details, c containerInfo, id string) (string, bool) {
	source, ok := d.secrets[id]
	if !ok {
		return "", false
	}
	env := envMap(c.Env)
	value := env[source.variable]
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(id, "connection.") {
		l := loginOf(c, env)
		if l.client == nil || source.account >= len(d.Accounts) {
			return "", false
		}
		return connection(l.client.URL, d.Container, d.Accounts[source.account], value), true
	}
	return value, true
}

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func validContainerName(name string) bool { return containerName.MatchString(name) }

// addressesOf lists the addresses of the configuration's routes to this container.
func addressesOf(name string, routes []utils.ProxyRouteConfig, serverURL func(host string) string) []Address {
	out := []Address{}
	seen := map[string]bool{}
	for _, route := range routes {
		if route.Mode != "SERVAPP" || route.Disabled || !route.UseHost || route.Host == "" {
			continue
		}
		target, err := url.Parse(route.Target)
		if err != nil || !strings.EqualFold(target.Hostname(), name) {
			continue
		}
		address := strings.TrimSuffix(serverURL(route.Host), "/")
		if route.UsePathPrefix && route.PathPrefix != "" {
			address += route.PathPrefix
		}
		if seen[address] {
			continue
		}
		seen[address] = true
		out = append(out, Address{URL: address, AdminOnly: route.AdminOnly, SignIn: route.AuthEnabled})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].URL) < len(out[j].URL) })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func dataOf(mounts []mountInfo) []DataPlace {
	out := []DataPlace{}
	for _, m := range mounts {
		switch m.Type {
		case "bind":
			out = append(out, DataPlace{Target: m.Destination, Folder: m.Source})
		case "volume":
			out = append(out, DataPlace{Target: m.Destination, Volume: m.Name})
		}
		if len(out) >= 16 {
			break
		}
	}
	return out
}
