package cron

// Fork addition: tests of fork_proposals.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/utils"
)

const hostileValue = `x"; rm -rf / #$(reboot)` + "`id`"

func container(image string, env []string, mounts ...proposalMount) proposalContainer {
	c := proposalContainer{Name: "db", Image: image, Running: true, EnvSet: map[string]bool{}, Mounts: mounts}
	for _, e := range env {
		c.EnvSet[e] = true
	}
	return c
}

func TestADumpIsProposedForPostgres18(t *testing.T) {
	a := proposalsFor(container("postgres:18", []string{"POSTGRES_USER", "POSTGRES_PASSWORD"},
		proposalMount{Source: "/srv/apps/db", Destination: "/var/lib/postgresql"}))
	if a.Recipe != "PostgreSQL" || len(a.Proposals) != 1 {
		t.Fatalf("%+v", a)
	}
	p := a.Proposals[0]
	want := `mkdir -p /var/lib/postgresql/cosmos-dumps && PGPASSWORD="$POSTGRES_PASSWORD" pg_dumpall -U "${POSTGRES_USER:-postgres}" -f /var/lib/postgresql/cosmos-dumps/dump-$(date +%Y%m%d-%H%M%S).sql && find /var/lib/postgresql/cosmos-dumps -name 'dump-*.sql' -mtime +13 -delete`
	if p.Command != want {
		t.Errorf("command\n got %s\nwant %s", p.Command, want)
	}
	if p.FolderOnServer != "/srv/apps/db/cosmos-dumps" || p.FolderInContainer != "/var/lib/postgresql/cosmos-dumps" || p.Schedule != "0 0 3 * * *" || p.KeepDays != 14 || p.Name != "Nightly dump of db" {
		t.Errorf("%+v", p)
	}
	if ScheduleProblem(p.Schedule) != "" {
		t.Error("the proposed schedule is not one the server takes")
	}
}

func TestADumpIsProposedForPostgres17AndOthers(t *testing.T) {
	a := proposalsFor(container("postgres:17-alpine", []string{"POSTGRES_PASSWORD"},
		proposalMount{Source: "/var/lib/docker/volumes/pg/_data", Destination: "/var/lib/postgresql/data/"}))
	if len(a.Proposals) != 1 || !strings.HasPrefix(a.Proposals[0].Command, "mkdir -p /var/lib/postgresql/data/cosmos-dumps && ") ||
		a.Proposals[0].FolderOnServer != "/var/lib/docker/volumes/pg/_data/cosmos-dumps" {
		t.Fatalf("%+v", a)
	}

	// Bitnami: the postgres account when its password is set, else the app's
	b := proposalsFor(container("bitnami/postgresql:latest", []string{"POSTGRESQL_USERNAME", "POSTGRESQL_PASSWORD", "POSTGRESQL_POSTGRES_PASSWORD"},
		proposalMount{Source: "/srv/b", Destination: "/bitnami/postgresql"}))
	if len(b.Proposals) != 1 || !strings.Contains(b.Proposals[0].Command, `PGPASSWORD="$POSTGRESQL_POSTGRES_PASSWORD" pg_dumpall -U postgres -f /bitnami/postgresql/cosmos-dumps/`) {
		t.Fatalf("%+v", b)
	}
	b = proposalsFor(container("bitnami/postgresql:latest", []string{"POSTGRESQL_USERNAME", "POSTGRESQL_PASSWORD"},
		proposalMount{Source: "/srv/b", Destination: "/bitnami/postgresql"}))
	if len(b.Proposals) != 1 || !strings.Contains(b.Proposals[0].Command, `PGPASSWORD="$POSTGRESQL_PASSWORD" pg_dumpall -U "${POSTGRESQL_USERNAME:-postgres}"`) {
		t.Fatalf("%+v", b)
	}

	m := proposalsFor(container("mariadb:lts", []string{"MARIADB_ROOT_PASSWORD", "MARIADB_USER"},
		proposalMount{Source: "/srv/m", Destination: "/var/lib/mysql"}))
	if len(m.Proposals) != 1 || m.Proposals[0].Kind != "mysql" ||
		!strings.Contains(m.Proposals[0].Command, `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" mariadb-dump --all-databases -u root --result-file=/var/lib/mysql/cosmos-dumps/dump-`) {
		t.Fatalf("%+v", m)
	}
	// without a root password (a random one): the app's own database
	m = proposalsFor(container("mariadb:lts", []string{"MARIADB_USER", "MARIADB_PASSWORD", "MARIADB_DATABASE"},
		proposalMount{Source: "/srv/m", Destination: "/var/lib/mysql"}))
	if len(m.Proposals) != 1 || !strings.Contains(m.Proposals[0].Command, `MYSQL_PWD="$MARIADB_PASSWORD" mariadb-dump --databases "$MARIADB_DATABASE" -u "$MARIADB_USER"`) {
		t.Fatalf("%+v", m)
	}
}

func TestNothingIsProposedWithoutARecipeOrADataFolder(t *testing.T) {
	if a := proposalsFor(container("nginx:alpine", nil, proposalMount{Source: "/srv/x", Destination: "/usr/share/nginx/html"})); len(a.Proposals) != 0 || a.Note != "noRecipe" {
		t.Errorf("%+v", a)
	}
	if a := proposalsFor(container("mongo:8", []string{"MONGO_INITDB_ROOT_PASSWORD"}, proposalMount{Source: "/srv/x", Destination: "/data/db"})); len(a.Proposals) != 0 {
		t.Errorf("mongo: %+v", a) // mongodump takes its password on the command line: left out
	}
	// a dump in the container's own layer would go with the container
	if a := proposalsFor(container("postgres:18", []string{"POSTGRES_PASSWORD"})); len(a.Proposals) != 0 || a.Note != "noDataFolder" {
		t.Errorf("%+v", a)
	}
}

func TestTheRouteNeverAnswersAValueOfTheContainer(t *testing.T) {
	was := inspectForProposals
	t.Cleanup(func() { inspectForProposals = was })
	// what the real inspect gets from Docker: values that must never come back
	inspectForProposals = func(ctx context.Context, name string) (proposalContainer, error) {
		if name != "db" {
			return proposalContainer{}, errors.New("no such container")
		}
		inspected := types.ContainerJSON{
			ContainerJSONBase: &types.ContainerJSONBase{Name: "/db", State: &types.ContainerState{Running: false}},
			Config:            &conttype.Config{Image: "postgres:18", Env: []string{"POSTGRES_USER=" + hostileValue, "POSTGRES_PASSWORD=" + hostileValue + "pw", "POSTGRES_DB=" + hostileValue}},
			Mounts:            []types.MountPoint{{Source: "/srv/db", Destination: "/var/lib/postgresql"}},
		}
		c := proposalContainerOf(inspected)
		return c, nil
	}
	call := func(name string, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/cosmos/api/cron-proposals/"+name, nil)
		req = mux.SetURLVars(req, map[string]string{"containerId": name})
		w := httptest.NewRecorder()
		ProposalsRoute(w, as(req))
		return w
	}
	w := call("db", asResourcesAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "rm -rf") || strings.Contains(w.Body.String(), "reboot") {
		t.Fatalf("a value of the container came back: %s", w.Body.String())
	}
	var answer struct {
		Data JobProposals `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &answer)
	if answer.Data.Running || len(answer.Data.Proposals) != 1 {
		t.Errorf("%+v", answer.Data)
	}
	if w := call("db;x", asResourcesAdmin); w.Code != http.StatusBadRequest {
		t.Errorf("a bad name: %d", w.Code)
	}
	if w := call("other", asResourcesAdmin); w.Code != http.StatusNotFound {
		t.Errorf("an unknown container: %d", w.Code)
	}
	if w := call("db", asReader); w.Code == http.StatusOK {
		t.Errorf("a reader got the proposals")
	}
}

func asResourcesAdmin(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "admin",
		APIToken: &utils.APITokenContext{Name: "t", Owner: "admin", Permissions: []utils.Permission{utils.PERM_RESOURCES}},
	}))
}

func asReader(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "reader",
		APIToken: &utils.APITokenContext{Name: "r", Owner: "reader", Permissions: []utils.Permission{utils.PERM_RESOURCES_READ}},
	}))
}

// A recipe volume whose target is not a plain path never reaches a command
// (review, M14): the dump is not proposed rather than built around it.
func TestAnOddMountTargetNeverReachesTheCommand(t *testing.T) {
	for _, target := range []string{"/var/lib/my data", "/var/lib/$(id)", "/var/lib/db;reboot", "/var/lib/`id`", "relative/data", "/var/lib/db\nx"} {
		recipe := &anyimage.Recipe{Volumes: []anyimage.RecipeVolume{{Target: target, Backup: true}}}
		c := container("postgres:18", nil, proposalMount{Source: "/srv/db", Destination: target})
		if m, ok := dataMount(recipe, c); ok {
			t.Errorf("%q was taken as the data folder: %+v", target, m)
		}
	}
	recipe := &anyimage.Recipe{Volumes: []anyimage.RecipeVolume{{Target: "/var/lib/postgresql", Backup: true}}}
	if _, ok := dataMount(recipe, container("postgres:18", nil, proposalMount{Source: "/srv/db", Destination: "/var/lib/postgresql/"})); !ok {
		t.Error("a plain target written with a trailing slash was not found")
	}
}

// Every folder a proposal names is a plain path, and the command holds no
// character the shell would read otherwise outside its fixed parts.
func TestTheDumpFolderIsAPlainPath(t *testing.T) {
	if !plainPathRe.MatchString("/" + dumpFolderName) {
		t.Fatalf("the dump folder name %q is not plain", dumpFolderName)
	}
	for _, c := range []proposalContainer{
		container("postgres:18", []string{"POSTGRES_USER", "POSTGRES_PASSWORD"}, proposalMount{Source: "/srv/a b/db", Destination: "/var/lib/postgresql"}),
		container("postgres:17", []string{"POSTGRES_PASSWORD"}, proposalMount{Source: "/srv/db", Destination: "/var/lib/postgresql/data"}),
		container("mariadb:lts", []string{"MARIADB_ROOT_PASSWORD"}, proposalMount{Source: "/srv/maria", Destination: "/var/lib/mysql"}),
	} {
		a := proposalsFor(c)
		if len(a.Proposals) != 1 {
			t.Fatalf("%s: %+v", c.Image, a)
		}
		p := a.Proposals[0]
		if !plainPathRe.MatchString(p.FolderInContainer) || !strings.HasSuffix(p.FolderInContainer, "/"+dumpFolderName) {
			t.Errorf("%s: folder %q", c.Image, p.FolderInContainer)
		}
		if strings.Contains(p.Command, "/srv/") {
			t.Errorf("%s: the folder on the server reached the command: %s", c.Image, p.Command)
		}
	}
}
