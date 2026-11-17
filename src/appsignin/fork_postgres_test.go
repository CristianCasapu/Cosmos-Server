package appsignin

// Fork addition: tests of fork_postgres.go.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func withPostgres(t *testing.T, candidates []pgCandidate, inside bool) {
	t.Helper()
	withFakes(t, map[string]containerInfo{})
	oldList, oldInside, oldNets, oldDial := postgresCandidates, insideContainer, cosmosNetworks, reachDial
	postgresCandidates = func(context.Context) ([]pgCandidate, error) { return candidates, nil }
	insideContainer = func() bool { return inside }
	cosmosNetworks = func(context.Context) map[string]bool { return map[string]bool{"cosmos-shared": true} }
	reachDial = func(context.Context, string) error { return nil }
	reachMu.Lock()
	reachCache = map[string]reachEntry{}
	reachMu.Unlock()
	t.Cleanup(func() {
		postgresCandidates, insideContainer, cosmosNetworks, reachDial = oldList, oldInside, oldNets, oldDial
	})
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
		// bare-metal (plan 51): the address the port is bound to, as it is
		{"published on all addresses", pgCandidate{Info: db, Published: []pgBinding{{"0.0.0.0", "5433"}}}, false, "127.0.0.1:5433", "published", true},
		{"published on all IPv6 addresses", pgCandidate{Info: db, Published: []pgBinding{{"::", "5433"}}}, false, "127.0.0.1:5433", "published", true},
		{"published on loopback", pgCandidate{Info: db, Published: []pgBinding{{"127.0.0.1", "5432"}}}, false, "127.0.0.1:5432", "published", true},
		{"published on the home-network address only (the owner's)", pgCandidate{Info: db, Published: []pgBinding{{"192.168.1.20", "5432"}}, Addresses: []string{"172.17.0.2"}}, false, "192.168.1.20:5432", "published", true},
		{"published on a LAN address and on loopback: loopback", pgCandidate{Info: db, Published: []pgBinding{{"192.168.1.20", "5432"}, {"127.0.0.1", "15432"}}}, false, "127.0.0.1:15432", "published", true},
		{"published on an IPv6 address", pgCandidate{Info: db, Published: []pgBinding{{"2001:db8::5", "5432"}}}, false, "[2001:db8::5]:5432", "published", true},
		{"own address", pgCandidate{Info: db, Addresses: []string{"not-an-ip", "172.18.0.9"}}, false, "172.18.0.9:5432", "address", true},
		// in a container: the name on a shared network, else a published non-loopback address, else said
		{"Cosmos in a container, a shared network", pgCandidate{Info: db, Networks: []string{"bridge", "cosmos-shared"}, Addresses: []string{"172.17.0.2", "172.20.0.2"}}, true, "notes-db:5432", "container-network", true},
		{"Cosmos in a container, published on loopback only", pgCandidate{Info: db, Networks: []string{"bridge"}, Published: []pgBinding{{"127.0.0.1", "5432"}}}, true, "", "none", true},
		{"Cosmos in a container, published on a LAN address", pgCandidate{Info: db, Networks: []string{"bridge"}, Published: []pgBinding{{"192.168.1.20", "5432"}}}, true, "192.168.1.20:5432", "published", true},
		{"Cosmos in a container, nothing shared", pgCandidate{Info: db, Networks: []string{"bridge"}, Addresses: []string{"172.17.0.2"}}, true, "", "none", true},
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
		if c.source == "address" && (!strings.Contains(p["hostNote"].(string), "changes") || !strings.Contains(p["hostNote"].(string), "127.0.0.1")) {
			t.Errorf("%s: no note: %v", c.name, p)
		}
		if c.source == "none" && !strings.Contains(p["hostNote"].(string), "Let these apps reach it") {
			t.Errorf("%s: no advice: %v", c.name, p)
		}
		if (c.host != "") != (p["reach"] == "answers") {
			t.Errorf("%s: reach %v", c.name, p["reach"])
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

// Plan 51: each proposal is checked from Cosmos, bounded and cached; only
// the addresses the proposals name are dialled.
func TestPostgresProposalsAreChecked(t *testing.T) {
	db := postgresContainer()
	other := postgresContainer()
	other.Name = "/other-db"
	withPostgres(t, []pgCandidate{
		{Info: db, Published: []pgBinding{{"192.168.1.20", "5432"}}},
		{Info: other, Addresses: []string{"172.17.0.3"}},
	}, false)
	dialled := []string{}
	var mu sync.Mutex
	reachDial = func(ctx context.Context, address string) error {
		mu.Lock()
		dialled = append(dialled, address)
		mu.Unlock()
		if address == "192.168.1.20:5432" {
			return nil
		}
		<-ctx.Done() // a silent address: the bound ends it
		return ctx.Err()
	}
	start := time.Now()
	_, list, body := proposalsOf(t, sudoed)
	if len(list) != 2 || time.Since(start) > 3*time.Second {
		t.Fatalf("%s (%v)", body, time.Since(start))
	}
	got := map[string]string{}
	for _, item := range list {
		p := item.(map[string]interface{})
		got[p["host"].(string)] = p["reach"].(string)
	}
	if got["192.168.1.20:5432"] != "answers" || got["172.17.0.3:5432"] != "silent" {
		t.Errorf("reach %v", got)
	}
	sort.Strings(dialled)
	if strings.Join(dialled, ",") != "172.17.0.3:5432,192.168.1.20:5432" {
		t.Errorf("dialled %v", dialled)
	}
	// cached: asked again at once, nothing is dialled
	proposalsOf(t, sudoed)
	if len(dialled) != 2 {
		t.Errorf("not cached: %v", dialled)
	}
}

// Plan 51: the advice after a typed server failed, from the containers.
func TestPostgresHostAdvice(t *testing.T) {
	owner := postgresContainer()
	owner.Name = "/postgresql"
	lan := []pgCandidate{{Info: owner, Published: []pgBinding{{"192.168.1.20", "5432"}}, Networks: []string{"bridge"}, Addresses: []string{"172.17.0.2"}}}
	unpublished := []pgCandidate{{Info: owner, Networks: []string{"bridge"}, Addresses: []string{"172.17.0.2"}}}
	cases := []struct {
		name, typed, reason string
		candidates          []pgCandidate
		inside              bool
		want                []string
	}{
		{"localhost, port on the LAN address only", "localhost:5432", "closed", lan, false,
			[]string{"published on 192.168.1.20 only, not on localhost", "use 192.168.1.20:5432"}},
		{"127.0.0.1 the same", "127.0.0.1:5432", "closed", lan, false, []string{"use 192.168.1.20:5432"}},
		{"a .local guess", "postgresql.local:5432", "dns", lan, false,
			[]string{".local exist only for apps that have a URL", "use 192.168.1.20:5432"}},
		{"the container's name on bare metal", "postgresql:5432", "dns", lan, false,
			[]string{"A container's name is not a host name for Cosmos itself", "use 192.168.1.20:5432"}},
		{"the container's name, nothing published", "postgresql:5432", "dns", unpublished, false,
			[]string{"A container's name is not a host name for Cosmos itself", "use 172.17.0.2:5432"}},
		{"localhost from a container", "localhost:5432", "closed", lan, true, []string{"Cosmos' own container", "use 192.168.1.20:5432"}},
		{"another address that does not answer", "192.168.1.99:5432", "timeout", lan, false, []string{"PostgreSQL runs on this server in the container postgresql", "use 192.168.1.20:5432"}},
		{"a refused password: no advice", "192.168.1.20:5432", "password", lan, false, nil},
		{"nothing known", "localhost:5432", "closed", nil, false, nil},
	}
	for _, c := range cases {
		withPostgres(t, c.candidates, c.inside)
		got := postgresHostAdvice(context.Background(), c.typed, c.reason)
		if len(c.want) == 0 && got != "" {
			t.Errorf("%s: %q", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
	}
	// a proposal that does not answer is not advised
	withPostgres(t, lan, false)
	reachDial = func(context.Context, string) error { return errors.New("refused") }
	if got := postgresHostAdvice(context.Background(), "localhost:5432", "closed"); !strings.Contains(got, "published on 192.168.1.20") {
		t.Errorf("the binding is still the fact: %q", got)
	}
	if got := postgresHostAdvice(context.Background(), "192.168.1.99:5432", "timeout"); got != "" {
		t.Errorf("a silent proposal advised: %q", got)
	}
}
