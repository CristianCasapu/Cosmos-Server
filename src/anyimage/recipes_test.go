package anyimage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// Every recipe is complete and consistent; what can only be checked against
// the real image is in recipes_live_test.go.
func TestRecipesAreValid(t *testing.T) {
	if len(recipes.Recipes) < 15 {
		t.Fatalf("only %d recipes", len(recipes.Recipes))
	}
	kinds := map[string]bool{"database": true, "cache": true, "broker": true, "web": true, "tool": true, "proxy": true, "search": true, "storage": true}
	evidence := regexp.MustCompile(`^(declared|workdir|docs|env:[A-Z_]+)$`)
	notes := map[string]bool{"majorUpgrade": true, "pgdata18": true, "noAuth": true, "cpuAvx": true, "toolSignIn": true, "ownedVolume": true,
		"hostnameKeeps": true, "maxMapCount": true, "httpsSelfSigned": true, "nothingToKeep": true, "cosmosIsProxy": true, "socket": true, "consolePath": true, "s3Outside": true, "dashboardSignIn": true,
		"latestOnly": true, "bitnamiDevelopment": true, "bitnamiLegacyFrozen": true, "redisStackFrozen": true}
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
		checked, err1 := time.Parse("2006-01-02", r.Maintained.Checked)
		last, err2 := time.Parse("2006-01-02", r.Maintained.LastImage)
		// an image its publisher froze has a recipe only with the note that says so
		if err1 != nil || err2 != nil || (checked.Sub(last) > 365*24*time.Hour && !frozen(r)) || last.After(checked) {
			t.Errorf("%s: an image not published for a year when checked is not a recipe (%+v)", r.ID, r.Maintained)
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
				if k != "postgres" && k != "mysql" && k != "mongo" && k != "redis" {
					t.Errorf("%s: needs %s", r.ID, k)
				}
			}
		}
		for _, c := range r.Command {
			if strings.Contains(c, "insecure") {
				t.Errorf("%s: no insecure mode in a recipe's command (%s)", r.ID, c)
			}
		}
		for key := range r.Labels {
			if strings.HasPrefix(key, "cosmos-") {
				t.Errorf("%s: a recipe sets no label of Cosmos' own", r.ID)
			}
		}
		if r.Pin != "" && r.Pin != "major" {
			t.Errorf("%s: pin", r.ID)
		}
	}
	for _, r := range recipes.Recipes {
		if r.Family == "" {
			continue
		}
		main := byID(r.Family)
		if main == nil || main.Family != "" || main.Provides != r.Provides {
			t.Errorf("%s: its family %q is not a main recipe providing %s", r.ID, r.Family, r.Provides)
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

func frozen(r Recipe) bool {
	for _, n := range r.Notes {
		if strings.HasSuffix(n, "Frozen") {
			return true
		}
	}
	return false
}

func byID(id string) *Recipe {
	for i := range recipes.Recipes {
		if recipes.Recipes[i].ID == id {
			return &recipes.Recipes[i]
		}
	}
	return nil
}

func TestRecipeMatching(t *testing.T) {
	for text, want := range map[string]string{"postgres:18": "postgres", "docker.io/library/postgres": "postgres", "valkey/valkey:9-alpine": "valkey", "ghcr.io/x/postgres": "",
		// other publishers of the same software
		"bitnami/postgresql": "bitnami-postgresql", "docker.io/bitnami/postgresql:latest": "bitnami-postgresql", "bitnamisecure/postgresql": "bitnami-postgresql",
		"bitnamilegacy/postgresql:17.6.0": "bitnamilegacy-postgresql", "pgvector/pgvector:pg18": "pgvector", "postgis/postgis:18-3.7-alpine": "postgis",
		"timescale/timescaledb:latest-pg18": "timescaledb", "timescale/timescaledb-ha:pg18": "timescaledb-ha", "cgr.dev/chainguard/postgres": "chainguard-postgres",
		"bitnami/mariadb": "bitnami-mariadb", "linuxserver/mariadb:11": "linuxserver-mariadb", "lscr.io/linuxserver/mariadb": "linuxserver-mariadb",
		"percona/percona-server:8.4": "percona-server", "bitnami/redis": "bitnami-redis", "bitnami/valkey": "bitnami-valkey",
		"redis/redis-stack-server": "redis-stack-server", "redis/redis-stack:latest": "redis-stack", "bitnami/mongodb": "bitnami-mongodb",
		// near names are not matched; bitnami/mysql has no tag left on Docker Hub
		"bitnami/postgresql-repmgr": "", "bitnami/mysql": "", "cgr.dev/chainguard/postgres-dev": "", "postgis/docker-postgis": "", "redis/redis-stack-browser": ""} {
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

func TestRecipesCanBeDisabled(t *testing.T) {
	ref, _ := Parse("bitnami/postgresql", nil)
	recipesDisabled = true
	defer func() { recipesDisabled = false }()
	if recipeFor(ref) != nil {
		t.Fatal("a recipe while recipes are disabled")
	}
	recipesDisabled = false
	if recipeFor(ref) == nil {
		t.Fatal("no recipe")
	}
}

// Bitnami's images take variables of their own (POSTGRESQL_*, not POSTGRES_*),
// run as 1001 in group 0 and keep their data under /bitnami: a recipe that
// gave them the official image's names would start nothing.
func TestBitnamiRecipesUseBitnamiVariables(t *testing.T) {
	want := map[string][]string{
		"postgresql": {"POSTGRESQL_PASSWORD", "POSTGRESQL_USERNAME", "POSTGRESQL_DATABASE", "POSTGRESQL_POSTGRES_PASSWORD"},
		"mariadb":    {"MARIADB_ROOT_PASSWORD", "MARIADB_USER", "MARIADB_PASSWORD", "MARIADB_DATABASE"},
		"redis":      {"REDIS_PASSWORD"},
		"valkey":     {"VALKEY_PASSWORD"},
		"mongodb":    {"MONGODB_ROOT_USER", "MONGODB_ROOT_PASSWORD", "MONGODB_USERNAME", "MONGODB_PASSWORD", "MONGODB_DATABASE"},
	}
	seen := 0
	for _, r := range recipes.Recipes {
		for _, image := range r.Images {
			parts := strings.SplitN(image, "/", 2)
			if len(parts) != 2 || (parts[0] != "bitnami" && parts[0] != "bitnamisecure" && parts[0] != "bitnamilegacy") {
				continue
			}
			seen++
			names, ok := want[parts[1]]
			if !ok {
				t.Errorf("%s: no expectation for %s", r.ID, image)
				continue
			}
			env := map[string]RecipeEnv{}
			for _, e := range r.Env {
				env[e.Name] = e
				if strings.HasPrefix(e.Name, "POSTGRES_") || strings.HasPrefix(e.Name, "MYSQL_") || strings.HasPrefix(e.Name, "MONGO_INITDB_") {
					t.Errorf("%s: %s is the official image's name", r.ID, e.Name)
				}
			}
			for _, name := range names {
				if _, ok := env[name]; !ok {
					t.Errorf("%s: %s lacks %s", r.ID, image, name)
				}
				if strings.HasSuffix(name, "PASSWORD") && env[name].Type != "secret" {
					t.Errorf("%s: %s is not generated", r.ID, name)
				}
			}
			if runAsFor(r, "latest") != "1001:0" || r.User != "1001" {
				t.Errorf("%s: Bitnami runs as 1001 in group 0, the recipe as %q", r.ID, runAsFor(r, "latest"))
			}
			for _, v := range r.Volumes {
				if v.Backup && !strings.HasPrefix(v.Target, "/bitnami/"+parts[1]) {
					t.Errorf("%s: data in %s", r.ID, v.Target)
				}
			}
		}
	}
	if seen != 7 {
		t.Errorf("%d Bitnami images", seen)
	}
}

// Publishers that offer only "latest" (Bitnami's free catalogue, Chainguard's
// free images) get no pin, preference or variant, and the note that says an
// update can bring a new major version.
func TestNoPinWithoutVersionTags(t *testing.T) {
	latestOnly := []string{"bitnami/", "bitnamisecure/", "cgr.dev/chainguard/"}
	for _, r := range recipes.Recipes {
		only := false
		for _, image := range r.Images {
			for _, prefix := range latestOnly {
				only = only || strings.HasPrefix(image, prefix)
			}
		}
		has := false
		for _, n := range r.Notes {
			has = has || n == "latestOnly"
		}
		if only && (r.Pin != "" || len(r.Prefer) > 0 || r.TagSuffix != "" || !has) {
			t.Errorf("%s: a publisher with only latest, yet pin %q prefer %v suffix %q (note %v)", r.ID, r.Pin, r.Prefer, r.TagSuffix, has)
		}
		if !only && has {
			t.Errorf("%s: the latestOnly note on a publisher with versions", r.ID)
		}
	}
}

// The tag each family member proposes, from the tags its repository listed
// on the day the recipes were checked (newest first, as Docker Hub answers).
// linuxserver/mariadb's "11" is an image of 2016, not MariaDB 11: no pin there.
func TestFamilyTagProposals(t *testing.T) {
	lists := map[string][]string{
		"bitnami/postgresql":       {"sha256-952f958f13b84c105ea8d5e88d51947eb467f1ec9f6dc1dee74b6df1453c638a.sig", "latest-metadata", "latest"},
		"bitnamilegacy/postgresql": {"17.6.0-debian-12-r4", "17.6.0", "17.5.0", "17", "16", "latest"},
		"pgvector/pgvector":        {"0.8.7-pg18-trixie", "pg18-trixie", "0.8.7-pg17-trixie", "0.8.7-pg18", "pg18", "0.8.7-pg17", "pg17"},
		"postgis/postgis":          {"16-master", "18-master", "19rc2-master", "18-3.7-alpine", "latest", "18-3.7", "17-3.7", "19rc2-3.7", "18-3.6", "17-3.6"},
		"timescale/timescaledb":    {"latest-pg16", "latest-pg17-oss", "2.32.0-pg16", "latest-pg18", "2.32.0-pg18", "latest-pg17", "2.32.0-pg18-oss"},
		"timescale/timescaledb-ha": {"pg16.15-ts2.32.0", "pg16", "pg18.6-ts2.32.0", "pg18-ts2.32", "pg18-all", "pg18", "pg17"},
		"bitnami/mariadb":          {"latest-metadata", "latest"},
		"linuxserver/mariadb":      {"11.8.9", "version-11.8.9-r0", "11.8.9-r0-ls240", "latest", "arm64v8-latest", "11", "11.8.8"},
		"percona/percona-server":   {"9.7", "9.7.2", "9.7.2-3", "9.7.2-3.1-amd64", "latest", "8.4", "8.4.11", "8.4.11-12", "8.0", "8.0.46"},
		"bitnami/redis":            {"latest-metadata", "latest"},
		"bitnami/valkey":           {"latest-metadata", "latest"},
		"redis/redis-stack-server": {"latest", "7.4.0-v8", "7.4.0-v8-x86_64", "7.2.0-v20", "6.2.6-v19"},
		"redis/redis-stack":        {"latest", "7.4.0-v8", "7.2.0-v20"},
		"bitnami/mongodb":          {"latest-metadata", "latest"},
	}
	want := map[string]string{
		"bitnami/postgresql": "latest latest", "bitnamilegacy/postgresql": "17.6.0 newest", "pgvector/pgvector": "pg18 prefer",
		"postgis/postgis": "18-3.7 pinMajor", "timescale/timescaledb": "latest-pg18 prefer", "timescale/timescaledb-ha": "pg18 prefer",
		"bitnami/mariadb": "latest latest", "linuxserver/mariadb": "11.8.9 newest", "percona/percona-server": "8.4 prefer",
		"bitnami/redis": "latest latest", "bitnami/valkey": "latest latest", "redis/redis-stack-server": "latest latest",
		"redis/redis-stack": "latest latest", "bitnami/mongodb": "latest latest",
	}
	f := newFakeRegistry(t, func(f *fakeRegistry) {
		f.hubAPIHandler = func(w http.ResponseWriter, r *http.Request) bool {
			for repo, tags := range lists {
				parts := strings.SplitN(repo, "/", 2)
				if r.URL.Path == "/v2/namespaces/"+parts[0]+"/repositories/"+parts[1]+"/tags" {
					results := []map[string]string{}
					for _, tag := range tags {
						results = append(results, map[string]string{"name": tag})
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
					return true
				}
			}
			return false
		}
	})
	hubAPI = f.main.URL
	defer func() { hubAPI = "https://hub.docker.com" }()
	for repo, expected := range want {
		ref, err := Parse(repo, nil)
		recipe := recipeFor(ref)
		if err != nil || recipe == nil {
			t.Errorf("%s: no recipe", repo)
			continue
		}
		got := tagsFor(context.Background(), ref, recipe)
		if got.Suggested+" "+got.Why != expected {
			t.Errorf("%s (%s): proposes %s (%s), want %s", repo, recipe.ID, got.Suggested, got.Why, expected)
		}
	}
	// Chainguard's registry (not Docker Hub) lists latest, latest-dev and signatures
	if tag, why := suggestTag([]string{"latest", "latest-dev", "sha256-0009cd3de0857d93d3fda0f00447c368ddaad072124d84270fc0824f780068d7.att"}, false, ""); tag != "latest" || why != "latest" {
		t.Errorf("chainguard: %s %s", tag, why)
	}
}
