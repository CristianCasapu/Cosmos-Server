package configapi

// Fork addition: OpenID clients that can work, and whose secret stays on the
// server.
//
// Upstream saved a new client through the whole configuration with an empty
// secret (a confidential client that can never sign in), made a reset secret
// in the browser and sent its hash back with the configuration, let an edit
// through PUT /api/openid/{id} wipe the secret (the body cannot know the hash)
// or take another client's ID, gave the hashes to every reader of the
// configuration, and checked no redirect URI: a value the sign-in refuses
// (plain http, a typo) was only found when someone tried to sign in.
//
// Now: a redirect URI is an exact, absolute URI (no wildcard, no fragment, no
// user info), https, or http to this very machine (loopback), or the
// private-use scheme of a phone app in reverse-domain form (RFC 8252: an
// "app.immich:" style scheme; "javascript:", "data:" and "file:" have no dot
// and are refused). The ID is a plain name and unique. The stored hash is kept
// whatever an edit sends, masked on every read, and a new secret is made on
// the server by POST /api/openid/{id}/secret, returned once.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/azukaar/cosmos-server/src/constellation"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxOpenIDClients   = 100
	maxRedirectURIs    = 10
	maxRedirectURILen  = 512
	openIDSecretLength = 32
)

var (
	openIDClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	privateSchemePattern  = regexp.MustCompile(`^[a-z][a-z0-9+-]*(\.[a-z0-9+-]+)+$`)
)

// redirectURIProblem says what is wrong with one redirect URI, "" when it is
// one the sign-in accepts.
func redirectURIProblem(raw string) string {
	if raw == "" {
		return "A redirect URI is empty"
	}
	if len(raw) > maxRedirectURILen {
		return fmt.Sprintf("A redirect URI is longer than %d characters", maxRedirectURILen)
	}
	for _, c := range raw {
		if c <= 0x20 || c == 0x7f {
			return "A redirect URI must not contain spaces or control characters: " + plainText(raw)
		}
	}
	if strings.Contains(raw, "*") {
		return "A redirect URI must be written out in full, without *: the app is sent back to exactly this address (" + plainText(raw) + ")"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "Not an address: " + plainText(raw) + ". Write it in full, for example https://app.example.com/oauth/callback"
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "A redirect URI cannot have a # part: " + plainText(raw)
	}
	if u.User != nil {
		return "A redirect URI cannot carry a user name or password: " + plainText(raw)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
		if u.Hostname() == "" {
			return "The address has no host name: " + plainText(raw)
		}
		return ""
	case "http":
		host := strings.ToLower(u.Hostname())
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return ""
		}
		return "Plain http is accepted only for an app on the same device (127.0.0.1 or localhost): use the app's https address instead of " + plainText(raw)
	}
	if privateSchemePattern.MatchString(scheme) {
		// a phone app's own scheme (app.immich:///oauth-callback): nothing
		// after it is an address on the web
		return ""
	}
	return "Not an address an app can be sent back to: " + plainText(raw) + ". Use https://…, or the scheme of a phone app such as app.example:/callback"
}

// splitRedirects is how the authorization server reads the stored value.
func splitRedirects(value string) []string {
	parts := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// checkOpenIDClient checks one client and cleans its redirect list. others are
// the other clients (for the uniqueness of the ID).
func checkOpenIDClient(client *utils.OpenIDClient, others []utils.OpenIDClient) *httpSettingsProblem {
	client.ID = strings.TrimSpace(client.ID)
	if !openIDClientIDPattern.MatchString(client.ID) {
		return &httpSettingsProblem{"OID010", "The client ID is a name of 1 to 64 letters, digits, dots, dashes or underscores, starting with a letter or a digit, for example immich"}
	}
	for _, other := range others {
		if other.ID == client.ID {
			return &httpSettingsProblem{"OID011", "Another client already has the ID " + client.ID + ": choose another one"}
		}
	}
	redirects := splitRedirects(client.Redirect)
	if len(redirects) == 0 {
		return &httpSettingsProblem{"OID012", "Enter the address the app is sent back to after signing in (its redirect URI), for example https://app.example.com/oauth/callback"}
	}
	if len(redirects) > maxRedirectURIs {
		return &httpSettingsProblem{"OID012", fmt.Sprintf("At most %d redirect URIs per client", maxRedirectURIs)}
	}
	seen := map[string]bool{}
	for _, redirect := range redirects {
		if problem := redirectURIProblem(redirect); problem != "" {
			return &httpSettingsProblem{"OID013", problem}
		}
		if seen[redirect] {
			return &httpSettingsProblem{"OID013", "The redirect URI " + plainText(redirect) + " is listed twice"}
		}
		seen[redirect] = true
	}
	client.Redirect = strings.Join(redirects, ",")
	return nil
}

func withoutClient(clients []utils.OpenIDClient, index int) []utils.OpenIDClient {
	others := make([]utils.OpenIDClient, 0, len(clients))
	for i, c := range clients {
		if i != index {
			others = append(others, c)
		}
	}
	return others
}

func looksLikeBcrypt(value string) bool {
	_, err := bcrypt.Cost([]byte(value))
	return err == nil
}

// keepOpenIDSecrets is the hook of PUT /api/config: a client that exists keeps
// its stored hash whatever the request says (the browser gets no hash), a
// public client has none, and a client that is new or changed is checked.
func keepOpenIDSecrets(request *utils.Config, stored utils.Config) *httpSettingsProblem {
	if len(request.OpenIDClients) > maxOpenIDClients {
		return &httpSettingsProblem{"OID012", fmt.Sprintf("At most %d OpenID clients", maxOpenIDClients)}
	}
	byID := map[string]utils.OpenIDClient{}
	for _, c := range stored.OpenIDClients {
		byID[c.ID] = c
	}
	for i := range request.OpenIDClients {
		client := &request.OpenIDClients[i]
		old, exists := byID[client.ID]
		if exists {
			client.Secret = old.Secret
		} else if client.Secret != "" && !looksLikeBcrypt(client.Secret) {
			return &httpSettingsProblem{"OID014", "A new client gets its secret from the server: leave the secret empty, then use \"New secret\""}
		}
		if client.Public {
			client.Secret = ""
		}
		if exists && old.Redirect == client.Redirect && old.Public == client.Public {
			continue
		}
		if problem := checkOpenIDClient(client, withoutClient(request.OpenIDClients, i)); problem != nil {
			return problem
		}
	}
	return nil
}

// maskOpenIDSecrets is the hook of GET /api/config: no reader gets the hashes.
func maskOpenIDSecrets(config *utils.Config) {
	clients := make([]utils.OpenIDClient, len(config.OpenIDClients))
	for i, client := range config.OpenIDClients {
		client.Secret = ""
		clients[i] = client
	}
	config.OpenIDClients = clients
}

// prepareOpenIDUpdate is the hook of PUT /api/openid/{id}: the body replaces
// the client except for its secret, and may not take another client's ID.
func prepareOpenIDUpdate(updated *utils.OpenIDClient, clients []utils.OpenIDClient, index int) *httpSettingsProblem {
	if problem := checkOpenIDClient(updated, withoutClient(clients, index)); problem != nil {
		return problem
	}
	updated.Secret = clients[index].Secret
	if updated.Public {
		updated.Secret = ""
	}
	return nil
}

// newOpenIDSecret returns a new secret and its hash.
func newOpenIDSecret() (string, string, error) {
	plain := utils.GenerateRandomString(openIDSecretLength)
	hashed, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	return plain, string(hashed), nil
}

// publishOpenIDClients is a variable for the tests.
var publishOpenIDClients = func(clients []utils.OpenIDClient) error {
	return constellation.PublishDomainOp(constellation.DomainOpenIDClients, clients)
}

// OpenIDSecretRoute godoc
// @Summary Make a new secret for an OpenID client
// @Description Replaces the client's secret with a new one made on the server and returns it, once. The old secret stops working. Not for public clients.
// @Tags openid
// @Produce json
// @Security BearerAuth
// @Param id path string true "OpenID client ID"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 404 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/openid/{id}/secret [post]
func OpenIDSecretRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	id := mux.Vars(req)["id"]

	utils.ConfigLock.Lock()
	config := utils.ReadConfigFromFile()
	clients := append([]utils.OpenIDClient{}, config.OpenIDClients...)
	index := -1
	for i, client := range clients {
		if client.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		utils.ConfigLock.Unlock()
		utils.HTTPError(w, "OpenID client not found", http.StatusNotFound, "OID015")
		return
	}
	if clients[index].Public {
		utils.ConfigLock.Unlock()
		utils.HTTPError(w, "A public client has no secret: it signs in with PKCE", http.StatusBadRequest, "OID016")
		return
	}
	plain, hashed, err := newOpenIDSecret()
	if err != nil {
		utils.ConfigLock.Unlock()
		utils.Error("OpenIDSecretRoute: hashing the secret", err)
		utils.HTTPError(w, "Failed to generate secret", http.StatusInternalServerError, "OID008")
		return
	}
	clients[index].Secret = hashed
	// released before publishing: the apply loop takes ConfigLock to install this
	utils.ConfigLock.Unlock()

	if err := publishOpenIDClients(clients); err != nil {
		utils.HTTPStoreError(w, err, "OID009")
		return
	}
	utils.Log("OpenID client secret replaced: " + id)
	utils.TriggerEvent("cosmos.settings", "OpenID client secret replaced: "+id, "success", "", map[string]interface{}{})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data": map[string]interface{}{
			"id":     id,
			"secret": plain,
		},
	})
}
