package authorizationserver

// Fork addition: the sign-in of an app on its name for use at home
// (utils/fork_home_names.go). Such a name is a copy of the app's route under
// another host, with the same route name and so the same OpenID client:
// registering it as a client of its own would replace the route's. Its
// return addresses are added to the route's client instead, so the sign-in
// can end on the home name and nowhere else.

import (
	"github.com/ory/fosite"

	"github.com/azukaar/cosmos-server/src/utils"
)

// addHomeNameRedirects is the hook of Init, after the route clients are
// registered.
func addHomeNameRedirects(clients map[string]fosite.Client) {
	for _, route := range utils.HomeRoutes() {
		if !(route.AuthEnabled || route.PublicOpenIDName != "") || !route.UseHost || route.Disabled {
			continue
		}
		twin := utils.GetProxyOIDCredentials(route, true)
		existing, ok := clients[twin.ID].(*fosite.DefaultClient)
		if !ok {
			clients[twin.ID] = twin
			continue
		}
		known := map[string]bool{}
		for _, uri := range existing.RedirectURIs {
			known[uri] = true
		}
		for _, uri := range twin.RedirectURIs {
			if !known[uri] {
				existing.RedirectURIs = append(existing.RedirectURIs, uri)
				known[uri] = true
			}
		}
	}
}
