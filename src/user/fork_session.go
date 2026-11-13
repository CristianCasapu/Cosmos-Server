package user

// Fork addition: rules for the session cookie and the token in it.
//
//   - Both cookies of a session say SameSite=Lax. Upstream sets no SameSite at
//     all, which leaves the choice to each browser; with Lax every browser
//     sends the session only with requests of the site itself and with plain
//     links to it. (The API has its own check on top, see fork_same_origin.go
//     in the main package.)

import (
	"net/http"
)

// sameSiteLax is the hook of SendUserToken and logOutUser, called on the
// cookies just before they are written.
func sameSiteLax(cookies ...*http.Cookie) {
	for _, cookie := range cookies {
		cookie.SameSite = http.SameSiteLaxMode
	}
}
