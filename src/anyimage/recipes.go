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
}

type RecipeVolume struct {
	Target     string `json:"target"`
	From       int    `json:"from,omitempty"`
	Until      int    `json:"until,omitempty"`
	Evidence   string `json:"evidence"`
	KeepVolume bool   `json:"keepVolume,omitempty"`
	Backup     bool   `json:"backup,omitempty"`
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
}

type RecipeClient struct {
	URL      string `json:"url"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
}

// Recipe is one image's settings, as Cosmos proposes them.
type Recipe struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Images      []string       `json:"images"`
	Source      string         `json:"source"`
	Kind        string         `json:"kind"`
	Provides    string         `json:"provides,omitempty"`
	Pin         string         `json:"pin,omitempty"`
	Prefer      []string       `json:"prefer,omitempty"`
	TagSuffix   string         `json:"tagSuffix,omitempty"`
	Service     *RecipePort    `json:"service,omitempty"`
	Web         *RecipePort    `json:"web,omitempty"`
	Ports       []RecipePort   `json:"ports,omitempty"`
	User        string         `json:"user,omitempty"`
	Volumes     []RecipeVolume `json:"volumes,omitempty"`
	Binds       []RecipeBind   `json:"binds,omitempty"`
	Env         []RecipeEnv    `json:"env,omitempty"`
	Command     []string       `json:"command,omitempty"`
	Hostname    bool           `json:"hostname,omitempty"`
	Healthcheck *RecipeHealth  `json:"healthcheck,omitempty"`
	Needs       *RecipeNeeds   `json:"needs,omitempty"`
	Memory      string         `json:"memory,omitempty"`
	Backup      string         `json:"backup,omitempty"`
	Notes       []string       `json:"notes,omitempty"`
	Client      *RecipeClient  `json:"client,omitempty"`
}

// MarketAlias names the Market entry to prefer for some images.
type MarketAlias struct {
	Market string   `json:"market"`
	Images []string `json:"images"`
}

type recipeFile struct {
	Recipes []Recipe      `json:"recipes"`
	Market  []MarketAlias `json:"market"`
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

func recipeFor(ref Reference) *Recipe {
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

// Recipes lists every recipe (for the panel's "attach a database").
func Recipes() []Recipe { return recipes.Recipes }
