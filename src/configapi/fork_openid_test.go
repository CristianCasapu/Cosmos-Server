package configapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

func TestRedirectURIs(t *testing.T) {
	good := []string{
		"https://photos.example.com/auth/login",
		"https://grafana.example.com:2053/login/generic_oauth",
		"http://127.0.0.1:8080/callback",
		"http://localhost/cb",
		"http://[::1]:9000/cb",
		"app.immich:///oauth-callback",
		"https://paperless.example.com/accounts/oidc/cosmos/login/callback/",
	}
	for _, uri := range good {
		if p := redirectURIProblem(uri); p != "" {
			t.Errorf("%s refused: %s", uri, p)
		}
	}
	bad := []string{
		"", "http://photos.example.com/cb", "https://*.example.com/cb", "https://example.com/*",
		"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "photos.example.com/cb",
		"https://user:pass@example.com/cb", "https://example.com/cb#frag", "https://example.com/a b",
		"https:///nohost", "https://example.com/\ncb", "https://example.com/" + strings.Repeat("a", 600),
	}
	for _, uri := range bad {
		if p := redirectURIProblem(uri); p == "" {
			t.Errorf("%q accepted", uri)
		}
	}
}

func TestOpenIDClientCheck(t *testing.T) {
	others := []utils.OpenIDClient{{ID: "immich"}}
	c := utils.OpenIDClient{ID: " grafana ", Redirect: " https://g.example.com/login/generic_oauth , https://g2.example.com/x "}
	if p := checkOpenIDClient(&c, others); p != nil {
		t.Fatal(p)
	}
	if c.ID != "grafana" || c.Redirect != "https://g.example.com/login/generic_oauth,https://g2.example.com/x" {
		t.Fatalf("not cleaned: %+v", c)
	}
	for _, c := range []utils.OpenIDClient{
		{ID: "immich", Redirect: "https://a.example.com/"},
		{ID: "bad id", Redirect: "https://a.example.com/"},
		{ID: "x", Redirect: ""},
		{ID: "x", Redirect: "https://a.example.com/,https://a.example.com/"},
		{ID: "x", Redirect: strings.Repeat("https://a.example.com/,", 11)},
	} {
		c := c
		if p := checkOpenIDClient(&c, others); p == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

// The settings save keeps the stored hash of a client whatever is sent (the
// browser never has it), clears it for a public client, and checks what changed.
func TestKeepOpenIDSecrets(t *testing.T) {
	stored := utils.Config{OpenIDClients: []utils.OpenIDClient{
		{ID: "a", Secret: "$2a$10$storedhashstoredhashstoredhashstoredhashstoredhashstor", Redirect: "https://a.example.com/"},
		{ID: "old", Secret: "h", Redirect: "http://legacy.example.com/"},
	}}
	request := utils.Config{OpenIDClients: []utils.OpenIDClient{
		{ID: "a", Secret: "", Redirect: "https://a.example.com/"},
		// an old odd value that did not change is not refused
		{ID: "old", Secret: "", Redirect: "http://legacy.example.com/"},
	}}
	if p := keepOpenIDSecrets(&request, stored); p != nil {
		t.Fatal(p)
	}
	if request.OpenIDClients[0].Secret != stored.OpenIDClients[0].Secret || request.OpenIDClients[1].Secret != "h" {
		t.Fatalf("hash not kept: %+v", request.OpenIDClients)
	}

	request.OpenIDClients[0].Public = true
	if p := keepOpenIDSecrets(&request, stored); p != nil || request.OpenIDClients[0].Secret != "" {
		t.Fatalf("public keeps a secret: %v %+v", p, request.OpenIDClients[0])
	}

	request = utils.Config{OpenIDClients: []utils.OpenIDClient{{ID: "new", Redirect: "http://insecure.example.com/"}}}
	if p := keepOpenIDSecrets(&request, stored); p == nil || p.Code != "OID013" {
		t.Fatalf("an insecure redirect saved: %v", p)
	}
	request = utils.Config{OpenIDClients: []utils.OpenIDClient{{ID: "new", Secret: "plain", Redirect: "https://n.example.com/"}}}
	if p := keepOpenIDSecrets(&request, stored); p == nil || p.Code != "OID014" {
		t.Fatalf("a plain secret saved: %v", p)
	}
	request = utils.Config{OpenIDClients: []utils.OpenIDClient{{ID: "a", Redirect: "https://a.example.com/"}, {ID: "a", Redirect: "https://b.example.com/"}}}
	if p := keepOpenIDSecrets(&request, stored); p == nil {
		t.Fatal("two clients with one ID saved")
	}
}

// Upstream's PUT /api/openid/{id} replaced the client with the body: the
// secret was wiped and the ID could take another client's.
func TestOpenIDUpdateKeepsSecretAndID(t *testing.T) {
	clients := []utils.OpenIDClient{{ID: "a", Secret: "hash-a", Redirect: "https://a.example.com/"}, {ID: "b", Secret: "hash-b", Redirect: "https://b.example.com/"}}
	updated := utils.OpenIDClient{ID: "a", Redirect: "https://a2.example.com/"}
	if p := prepareOpenIDUpdate(&updated, clients, 0); p != nil || updated.Secret != "hash-a" {
		t.Fatalf("%v %+v", p, updated)
	}
	updated = utils.OpenIDClient{ID: "b", Redirect: "https://a.example.com/"}
	if p := prepareOpenIDUpdate(&updated, clients, 0); p == nil {
		t.Fatal("renamed onto another client's ID")
	}
}

func TestMaskOpenIDSecrets(t *testing.T) {
	config := utils.Config{OpenIDClients: []utils.OpenIDClient{{ID: "a", Secret: "hash"}}}
	original := config.OpenIDClients
	maskOpenIDSecrets(&config)
	if config.OpenIDClients[0].Secret != "" || original[0].Secret != "hash" {
		t.Fatal("hash sent, or the stored slice changed")
	}
}

func TestNewOpenIDSecretRoundTrip(t *testing.T) {
	plain, hashed, err := newOpenIDSecret()
	if err != nil || len(plain) != openIDSecretLength || bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) != nil {
		t.Fatalf("%v %q", err, plain)
	}
}

// The route makes the secret, stores only the hash and answers the plain one.
func TestOpenIDSecretRoute(t *testing.T) {
	previous := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Cleanup(func() { utils.CONFIGFOLDER = previous })
	config := utils.ReadConfigFromFile()
	config.OpenIDClients = []utils.OpenIDClient{{ID: "app", Redirect: "https://app.example.com/"}, {ID: "spa", Public: true, Redirect: "https://spa.example.com/"}}
	utils.SetBaseMainConfig(config)

	var published []utils.OpenIDClient
	oldPublish := publishOpenIDClients
	publishOpenIDClients = func(c []utils.OpenIDClient) error { published = c; return nil }
	t.Cleanup(func() { publishOpenIDClients = oldPublish })

	call := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/cosmos/api/openid/"+id+"/secret", nil)
		req = mux.SetURLVars(asAdmin(req), map[string]string{"id": id})
		rec := httptest.NewRecorder()
		OpenIDSecretRoute(rec, req)
		return rec
	}
	rec := call("app")
	var answer struct {
		Data struct{ Secret string } `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &answer)
	if rec.Code != 200 || len(answer.Data.Secret) != openIDSecretLength {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(published) != 2 || bcrypt.CompareHashAndPassword([]byte(published[0].Secret), []byte(answer.Data.Secret)) != nil {
		t.Fatalf("stored %+v", published)
	}
	if rec := call("spa"); rec.Code != 400 {
		t.Fatalf("public client got a secret: %d", rec.Code)
	}
	if rec := call("nobody"); rec.Code != 404 {
		t.Fatalf("unknown client: %d", rec.Code)
	}
}
