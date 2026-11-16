package anyimage

// The built-in recipes (recipes.json): data, matched by the image's name.

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed recipes.json
var recipesJSON []byte

type RecipePort struct {
	Port int    `json:"port"`
	Why  string `json:"why,omitempty"`
	// Undeclared: the port is opened by the recipe's command, not declared by the image
	Undeclared bool `json:"undeclared,omitempty"`
}

type RecipeVolume struct {
	Target     string `json:"target"`
	From       int    `json:"from,omitempty"`
	Until      int    `json:"until,omitempty"`
	Evidence   string `json:"evidence"`
	KeepVolume bool   `json:"keepVolume,omitempty"`
	Backup     bool   `json:"backup,omitempty"`
}

// RecipeUpkeep says when the image was last published, read on the day checked.
type RecipeUpkeep struct {
	Checked   string `json:"checked"`
	LastImage string `json:"lastImage"`
}

// RecipeRunAs is the uid:gid a container runs as, for tags containing a text ("" for any).
type RecipeRunAs struct {
	TagContains string `json:"tagContains,omitempty"`
	User        string `json:"user"`
}

type RecipeEnv struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Value  string `json:"value,omitempty"`
	Strong bool   `json:"strong,omitempty"`
}

type RecipeBind struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type RecipeHealth struct {
	Test     []string `json:"test"`
	Interval string   `json:"interval"`
	Timeout  string   `json:"timeout"`
	Retries  int      `json:"retries"`
}

type RecipeNeeds struct {
	Kinds    []string          `json:"kinds"`
	Optional bool              `json:"optional"`
	Env      map[string]string `json:"env"`
	// Plan 45: the variable that carries a manager's list of servers
	// (pgAdmin's servers.json) and the one that carries its password file
	Servers  string `json:"servers,omitempty"`
	PassFile string `json:"passFile,omitempty"`
}

type RecipeClient struct {
	URL      string `json:"url"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
}

// RecipeLoginAccount is one account of an app: which of its variables hold
// the username, the e-mail, the password and the database (plan 45).
type RecipeLoginAccount struct {
	// Role: "app" (the account apps and people use), "admin" or "root"
	Role string `json:"role"`
	// User is the variable that holds the username, UserFixed a username the
	// image fixes (elastic), UserUnset the username when User is not set
	User      string `json:"user,omitempty"`
	UserFixed string `json:"userFixed,omitempty"`
	UserUnset string `json:"userUnset,omitempty"`
	Email     string `json:"email,omitempty"`
	Password  string `json:"password,omitempty"`
	Database  string `json:"database,omitempty"`
	// Defaults: this account takes the username and e-mail of the
	// configuration (Configuration > Sign-in details for new apps)
	Defaults bool `json:"defaults,omitempty"`
}

// RecipeUserRule is the form a username must have for this app.
type RecipeUserRule struct {
	// Pattern is a regular expression both Go and JavaScript read the same way
	Pattern  string   `json:"pattern"`
	Max      int      `json:"max"`
	Reserved []string `json:"reserved,omitempty"`
	Evidence string   `json:"evidence"`
}

// RecipeEmailRule: what the app's e-mail check refuses.
type RecipeEmailRule struct {
	// RefuseSpecialUse: domains such as .local, .test, .invalid, .localhost are refused
	RefuseSpecialUse bool   `json:"refuseSpecialUse,omitempty"`
	Evidence         string `json:"evidence"`
}

// RecipePasswordRule is what a generated password must be for this app.
type RecipePasswordRule struct {
	Min int `json:"min"`
	Max int `json:"max"`
	// Symbols are the symbols that may be used besides letters and digits,
	// a subset of the URL-unreserved "-._~"
	Symbols string `json:"symbols"`
	// Require: the classes that must appear: lower, upper, digit, symbol
	Require  []string `json:"require,omitempty"`
	Evidence string   `json:"evidence"`
}

// RecipeLogin says how one signs in to the app and what its passwords must be.
type RecipeLogin struct {
	// Where: "database" (its protocol, with a client), "web" (its page),
	// "s3", "amqp", "http" (an API with basic authentication)
	Where    string               `json:"where"`
	Accounts []RecipeLoginAccount `json:"accounts,omitempty"`
	// Via "database": a tool one signs in to with the database's own accounts
	Via      string              `json:"via,omitempty"`
	User     *RecipeUserRule     `json:"user,omitempty"`
	Email    *RecipeEmailRule    `json:"email,omitempty"`
	Password *RecipePasswordRule `json:"password,omitempty"`
}

// RecipeManager marks a web tool that manages databases of these kinds.
type RecipeManager struct {
	For  []string `json:"for"`
	Rank int      `json:"rank"`
}

// Recipe is one image's settings, as Cosmos proposes them.
type Recipe struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Images      []string          `json:"images"`
	Source      string            `json:"source"`
	Maintained  RecipeUpkeep      `json:"maintained"`
	Kind        string            `json:"kind"`
	Provides    string            `json:"provides,omitempty"`
	Pin         string            `json:"pin,omitempty"`
	Prefer      []string          `json:"prefer,omitempty"`
	TagSuffix   string            `json:"tagSuffix,omitempty"`
	Service     *RecipePort       `json:"service,omitempty"`
	Web         *RecipePort       `json:"web,omitempty"`
	Ports       []RecipePort      `json:"ports,omitempty"`
	User        string            `json:"user,omitempty"`
	RunAs       []RecipeRunAs     `json:"runAs,omitempty"`
	Volumes     []RecipeVolume    `json:"volumes,omitempty"`
	Binds       []RecipeBind      `json:"binds,omitempty"`
	Env         []RecipeEnv       `json:"env,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Hostname    bool              `json:"hostname,omitempty"`
	Healthcheck *RecipeHealth     `json:"healthcheck,omitempty"`
	Needs       *RecipeNeeds      `json:"needs,omitempty"`
	Memory      string            `json:"memory,omitempty"`
	Backup      string            `json:"backup,omitempty"`
	Notes       []string          `json:"notes,omitempty"`
	Client      *RecipeClient     `json:"client,omitempty"`
	// Family: the id of the recipe of the same software from its main
	// publisher (this one is another publisher's image of it)
	Family string `json:"family,omitempty"`
	// Plan 45: how one signs in, the password rules, and the managers
	Login      *RecipeLogin   `json:"login,omitempty"`
	Manager    *RecipeManager `json:"manager,omitempty"`
	Entrypoint []string       `json:"entrypoint,omitempty"`
}

// FirstRunApp is an app that makes its first account in its own page: Cosmos
// cannot set it, only propose what to type there (plan 45).
type FirstRunApp struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Images []string `json:"images"`
	Source string   `json:"source"`
	// Note: "firstRunPage" (the page asks for the first account) or
	// "logPassword" (a temporary password is printed in its log; User is fixed)
	Note string `json:"note"`
	User string `json:"user,omitempty"`
}

// MarketAlias names the Market entry to prefer for some images.
type MarketAlias struct {
	Market string   `json:"market"`
	Images []string `json:"images"`
}

type recipeFile struct {
	Recipes  []Recipe      `json:"recipes"`
	Market   []MarketAlias `json:"market"`
	FirstRun []FirstRunApp `json:"firstRun,omitempty"`
}

var recipes = func() recipeFile {
	var file recipeFile
	if err := json.Unmarshal(recipesJSON, &file); err != nil {
		panic("anyimage: recipes.json: " + err.Error())
	}
	return file
}()

// imageName is the name recipes use: "postgres", "valkey/valkey", "ghcr.io/a/b".
func imageName(ref Reference) string {
	return strings.ToLower(ref.Name())
}

// runAsFor is the uid:gid of a recipe for a tag, "" when it sets none.
func runAsFor(recipe Recipe, tag string) string {
	for _, r := range recipe.RunAs {
		if r.TagContains == "" || strings.Contains(tag, r.TagContains) {
			return r.User
		}
	}
	return ""
}

// recipesDisabled turns every recipe off: recipeFor answers nil, as for an
// image without one. Only a file compiled with a test build tag sets it (to
// check the flow without recipes); a release build never does.
var recipesDisabled bool

func recipeFor(ref Reference) *Recipe {
	if recipesDisabled {
		return nil
	}
	name := imageName(ref)
	for i := range recipes.Recipes {
		for _, image := range recipes.Recipes[i].Images {
			if image == name {
				recipe := recipes.Recipes[i]
				return &recipe
			}
		}
	}
	return nil
}

func marketFor(ref Reference) string {
	name := imageName(ref)
	for _, alias := range recipes.Market {
		for _, image := range alias.Images {
			if image == name {
				return alias.Market
			}
		}
	}
	return ""
}

// anyRegistry lets a container's image name a registry on the local network:
// it is only matched against the recipes here, never read.
func anyRegistry(string) bool { return true }

// RecipeForImage is the recipe of an image as a container names it
// ("postgres:18", "ghcr.io/a/b@sha256:…"), nil when there is none (plan 45).
func RecipeForImage(image string) *Recipe {
	ref, err := Parse(strings.TrimSpace(image), anyRegistry)
	if err != nil {
		return nil
	}
	return recipeFor(ref)
}

// FirstRunForImage is the first-run entry of an image, nil when there is none.
func FirstRunForImage(image string) *FirstRunApp {
	ref, err := Parse(strings.TrimSpace(image), anyRegistry)
	if err != nil {
		return nil
	}
	name := imageName(ref)
	for i := range recipes.FirstRun {
		for _, candidate := range recipes.FirstRun[i].Images {
			if candidate == name {
				entry := recipes.FirstRun[i]
				return &entry
			}
		}
	}
	return nil
}

// FirstRunApps is the whole first-run list, for the panel.
func FirstRunApps() []FirstRunApp {
	return append([]FirstRunApp{}, recipes.FirstRun...)
}

// Managers are the recipes of web tools that manage a database of a kind, by rank.
func Managers(kind string) []Recipe {
	out := []Recipe{}
	for _, r := range recipes.Recipes {
		if r.Manager == nil {
			continue
		}
		for _, k := range r.Manager.For {
			if k == kind {
				out = append(out, r)
				break
			}
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Manager.Rank < out[j-1].Manager.Rank; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// managerRecipes are every manager's recipe, by rank (the panel picks per kind).
func managerRecipes() []Recipe {
	out := []Recipe{}
	seen := map[string]bool{}
	for _, kind := range []string{"postgres", "mysql", "mongo", "redis"} {
		for _, r := range Managers(kind) {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// databaseRecipes are the recipes of databases an app can be given.
func databaseRecipes() []Recipe {
	out := []Recipe{}
	for _, r := range recipes.Recipes {
		if r.Kind == "database" && r.Provides != "" {
			out = append(out, r)
		}
	}
	return out
}
