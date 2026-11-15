package anyimage

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// Every recipe is complete and consistent; what can only be checked against
// the real image is in recipes_live_test.go.
func TestRecipesAreValid(t *testing.T) {
	if len(recipes.Recipes) < 15 {
		t.Fatalf("only %d recipes", len(recipes.Recipes))
	}
	kinds := map[string]bool{"database": true, "cache": true, "broker": true, "web": true, "tool": true, "proxy": true, "search": true}
	evidence := regexp.MustCompile(`^(declared|workdir|docs|env:[A-Z_]+)$`)
	notes := map[string]bool{"majorUpgrade": true, "pgdata18": true, "noAuth": true, "cpuAvx": true, "toolSignIn": true, "ownedVolume": true,
		"hostnameKeeps": true, "maxMapCount": true, "httpsSelfSigned": true, "nothingToKeep": true, "cosmosIsProxy": true, "socket": true}
	whys := map[string]bool{"xProtocol": true, "webTls": true, "tls": true, "metrics": true, "cluster": true, "cosmosProxies": true, "admin": true}
	provides := map[string]bool{}
	ids := map[string]bool{}
	images := map[string]bool{}
	for _, r := range recipes.Recipes {
		if r.ID == "" || ids[r.ID] || r.Name == "" || !kinds[r.Kind] {
			t.Errorf("%q: id, name or kind", r.ID)
		}
		ids[r.ID] = true
		if u, err := url.Parse(r.Source); err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("%s: the source must be the documentation's https address", r.ID)
		}
		for _, image := range r.Images {
			ref, err := Parse(image, nil)
			if err != nil || imageName(ref) != image || images[image] {
				t.Errorf("%s: image %q", r.ID, image)
			}
			images[image] = true
		}
		if r.Service == nil && r.Web == nil {
			t.Errorf("%s: neither a service port nor a web port", r.ID)
		}
		if r.Provides != "" {
			provides[r.Provides] = true
			if r.Service == nil || r.Client == nil || !strings.Contains(r.Client.URL, "{host}") {
				t.Errorf("%s: what it provides needs its service port and how clients write its address", r.ID)
			}
		}
		for _, v := range r.Volumes {
			if !strings.HasPrefix(v.Target, "/") || !evidence.MatchString(v.Evidence) {
				t.Errorf("%s: volume %+v", r.ID, v)
			}
		}
		secrets := 0
		for _, e := range r.Env {
			if !envName.MatchString(e.Name) || (e.Type != "secret" && e.Type != "name" && e.Type != "optional" && e.Type != "fixed") {
				t.Errorf("%s: env %+v", r.ID, e)
			}
			if e.Type == "secret" {
				secrets++
				if e.Value != "" {
					t.Errorf("%s: a secret has no value in the file", r.ID)
				}
			}
		}
		if r.Client != nil {
			for _, name := range []string{r.Client.User, r.Client.Password, r.Client.Database} {
				if name == "" {
					continue
				}
				found := false
				for _, e := range r.Env {
					found = found || e.Name == name
				}
				if !found {
					t.Errorf("%s: the client's %s is not one of its variables", r.ID, name)
				}
			}
		}
		for _, n := range r.Notes {
			if !notes[n] {
				t.Errorf("%s: note %q has no sentence in the panel", r.ID, n)
			}
		}
		for _, p := range r.Ports {
			if p.Port < 1 || p.Port > 65535 || !whys[p.Why] {
				t.Errorf("%s: port %+v", r.ID, p)
			}
		}
		if r.Needs != nil {
			for _, k := range r.Needs.Kinds {
				if k != "postgres" && k != "mysql" && k != "mongo" {
					t.Errorf("%s: needs %s", r.ID, k)
				}
			}
		}
		if r.Pin != "" && r.Pin != "major" {
			t.Errorf("%s: pin", r.ID)
		}
	}
	for _, kind := range []string{"postgres", "mysql", "mongo", "redis"} {
		if !provides[kind] {
			t.Errorf("no recipe provides %s", kind)
		}
	}
	for _, m := range recipes.Market {
		for _, image := range m.Images {
			if images[image] {
				t.Errorf("%s has a recipe and a Market entry", image)
			}
			if ref, err := Parse(image, nil); err != nil || imageName(ref) != image {
				t.Errorf("market image %q", image)
			}
		}
	}
	// the brief's list: each is a recipe or goes to the Market
	for _, image := range []string{"postgres", "mariadb", "mysql", "redis", "valkey/valkey", "mongo", "influxdb", "adminer", "dpage/pgadmin4", "phpmyadmin",
		"eclipse-mosquitto", "rabbitmq", "elasticsearch", "opensearchproject/opensearch", "memcached", "nginx", "caddy", "traefik", "wordpress",
		"gitea/gitea", "codeberg.org/forgejo/forgejo", "louislam/uptime-kuma", "vaultwarden/server", "homeassistant/home-assistant"} {
		ref, _ := Parse(image, nil)
		if recipeFor(ref) == nil && marketFor(ref) == "" {
			t.Errorf("%s: neither a recipe nor the Market", image)
		}
	}
}

func TestRecipeMatching(t *testing.T) {
	for text, want := range map[string]string{"postgres:18": "postgres", "docker.io/library/postgres": "postgres", "valkey/valkey:9-alpine": "valkey", "ghcr.io/x/postgres": "", "bitnami/postgresql": ""} {
		ref, _ := Parse(text, nil)
		got := ""
		if r := recipeFor(ref); r != nil {
			got = r.ID
		}
		if got != want {
			t.Errorf("%s: %q", text, got)
		}
	}
	ref, _ := Parse("ghcr.io/home-assistant/home-assistant:stable", nil)
	if marketFor(ref) != "Home Assistant" {
		t.Fatal("market alias")
	}
}
