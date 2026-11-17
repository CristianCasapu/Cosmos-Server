package appsignin

// Fork addition: tests of fork_postgres.go.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func withPostgres(t *testing.T, candidates []pgCandidate, inside bool) {
	t.Helper()
	withFakes(t, map[string]containerInfo{})
	oldList, oldInside := postgresCandidates, insideContainer
	postgresCandidates = func(context.Context) ([]pgCandidate, error) { return candidates, nil }
	insideContainer = func() bool { return inside }
	t.Cleanup(func() { postgresCandidates, insideContainer = oldList, oldInside })
}

func proposalsOf(t *testing.T, auth *utils.AuthContext) (int, []interface{}, string) {
	t.Helper()
	code, answer, body := call(PostgresProposalsRoute, "GET", "/cosmos/api/config/postgres-proposals", nil, "", auth)
	data, _ := answer["data"].(map[string]interface{})
	list, _ := data["proposals"].([]interface{})
	return code, list, body
}

func TestPostgresProposalsCarryNoPassword(t *testing.T) {
	db := postgresContainer()
	// a password that is also the user's and the database's name must not come out through them
	shadow := containerInfo{Name: "/shadow-db", Image: "postgres:17", Env: []string{
		"POSTGRES_PASSWORD=" + testRoot, "POSTGRES_USER=" + testRoot, "POSTGRES_DB=" + testRoot}}
	withPostgres(t, []pgCandidate{
		{Info: db, Published: []pgBinding{{HostIP: "0.0.0.0", HostPort: "15432"}}},
		{Info: shadow, Addresses: []string{"172.18.0.4"}},
	}, false)
	code, list, body := proposalsOf(t, sudoed)
	if code != http.StatusOK || len(list) != 2 {
		t.Fatalf("%d %s", code, body)
	}
	mustHaveNoSecret(t, "the proposals", body, testPassword, testRoot, testOther)
	first := list[0].(map[string]interface{})
	if first["container"] != "notes-db" || first["user"] != "owner_name" || first["database"] != "notes" || first["passwordSecret"] != "password.0" {
		t.Errorf("first: %v", first)
	}
	if first["host"] != "127.0.0.1:15432" || first["hostSource"] != "published" {
		t.Errorf("published host: %v", first)
	}
}

func TestPostgresProposalHostChoices(t *testing.T) {
	db := postgresContainer()
	cases := []struct {
		name    string
		c       pgCandidate
		inside  bool
		host    string
		source  string
		present bool
	}{
		{"published on all addresses", pgCandidate{Info: db, Published: []pgBinding{{"0.0.0.0", "5433"}}}, false, "127.0.0.1:5433", "published", true},
		{"published on loopback", pgCandidate{Info: db, Published: []pgBinding{{"127.0.0.1", "5432"}}}, true, "127.0.0.1:5432", "published", true},
		{"published on another address only", pgCandidate{Info: db, Published: []pgBinding{{"192.168.1.20", "5432"}}, Addresses: []string{"172.18.0.2"}}, false, "172.18.0.2:5432", "address", true},
		{"Cosmos in a container", pgCandidate{Info: db, Addresses: []string{"172.18.0.2"}}, true, "notes-db:5432", "container-network", true},
		{"own address", pgCandidate{Info: db, Addresses: []string{"not-an-ip", "172.18.0.9"}}, false, "172.18.0.9:5432", "address", true},
		{"unreachable", pgCandidate{Info: db}, false, "", "", false},
	}
	for _, c := range cases {
		withPostgres(t, []pgCandidate{c.c}, c.inside)
		_, list, body := proposalsOf(t, sudoed)
		if !c.present {
			if len(list) != 0 {
				t.Errorf("%s: %s", c.name, body)
			}
			continue
		}
		if len(list) != 1 {
			t.Fatalf("%s: %s", c.name, body)
		}
		p := list[0].(map[string]interface{})
		if p["host"] != c.host || p["hostSource"] != c.source {
			t.Errorf("%s: %v", c.name, p)
		}
		if c.source == "address" && !strings.Contains(p["hostNote"].(string), "change") {
			t.Errorf("%s: no note: %v", c.name, p)
		}
	}
}

func TestPostgresProposalsIgnoreOtherImages(t *testing.T) {
	maria := containerInfo{Name: "/maria", Image: "mariadb:lts", Env: []string{"MARIADB_USER=app"}}
	web := containerInfo{Name: "/web", Image: "nginx:1"}
	odd := postgresContainer()
	odd.Name = "/bad name"
	withPostgres(t, []pgCandidate{{Info: maria, Addresses: []string{"172.18.0.3"}}, {Info: web, Addresses: []string{"172.18.0.4"}}, {Info: odd, Addresses: []string{"172.18.0.5"}}}, false)
	if _, list, body := proposalsOf(t, sudoed); len(list) != 0 {
		t.Errorf("only PostgreSQL is proposed: %s", body)
	}
}

func TestPostgresProposalsNeedCredentialsReadAndGet(t *testing.T) {
	withPostgres(t, nil, false)
	for _, c := range []struct {
		auth *utils.AuthContext
		code int
	}{{nil, http.StatusUnauthorized}, {notSudo, http.StatusUnauthorized}, {noCreds, http.StatusUnauthorized}, {tokenNo, http.StatusForbidden}} {
		if code, _, body := proposalsOf(t, c.auth); code != c.code {
			t.Errorf("%+v: %d %s", c.auth, code, body)
		}
	}
}

func TestPostgresProposalsWhenDockerIsDown(t *testing.T) {
	withPostgres(t, nil, false)
	postgresCandidates = func(context.Context) ([]pgCandidate, error) { return nil, errors.New("down") }
	code, _, body := call(PostgresProposalsRoute, "GET", "/x", nil, "", sudoed)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "Docker does not answer") {
		t.Errorf("%d %s", code, body)
	}
	code, _, _ = call(PostgresProposalsRoute, "POST", "/x", nil, "", sudoed)
	if code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", code)
	}
}
