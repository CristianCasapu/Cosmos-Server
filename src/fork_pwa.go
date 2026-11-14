package main

// Fork addition: what the server adds to the panel's static files so that
// the panel can be installed as an app (a web app manifest and a service
// worker, both built with the UI into static/).
//
//   - /cosmos-ui/sw.js, the service worker: always checked with the server
//     (Cache-Control: no-cache), sent as a script, and with its widest
//     allowed scope stated as /cosmos-ui/, so that no registration can ever
//     cover the API under /cosmos/ or an application.
//   - /cosmos-ui/manifest.webmanifest: its own content type, no-cache.
//   - The manifest exists on the server's own hostname only (where the API
//     answers, utils.EnsureHostnameCosmosAPI). Every hostname of an
//     application serves the sign-in pages under /cosmos-ui/ too; there the
//     manifest is "not found", so that the panel is never installed as an app
//     of the address of an application.
//   - The worker's address always answers with a script, never "not found":
//     a browser that gets 404 when it checks a registered worker keeps the
//     old one for good. On another hostname, when the build has no worker, or
//     with DisableWebApp set in the configuration, it answers with a few
//     lines that delete what the real worker kept and unregister it (the kill
//     switch: browsers check the worker at every page load, even when the
//     page's own code is broken). The page learns the switch from
//     /api/status and no longer registers anything.
//   - Files with a content hash in their name (/cosmos-ui/assets/) may be
//     kept by browsers for good; the page that names them is never kept, so
//     that a new release is picked up at the next load.
//
// Nothing here reads the request beyond its path, method and Host, and
// nothing is added to what the server offers: these are the files of
// static/, which are public like the sign-in page.

import (
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

const forkPWAScope = "/cosmos-ui/"

// forkPWAOffWorker replaces the service worker while the installable app is
// switched off. It has no fetch handler: it answers no request.
const forkPWAOffWorker = `// Cosmos: the installable app is switched off on this server.
// This worker deletes what the panel's worker kept and removes itself.
self.addEventListener('install', function () { self.skipWaiting(); });
self.addEventListener('activate', function (event) {
  event.waitUntil(caches.keys().then(function (names) {
    return Promise.all(names.filter(function (name) { return name.indexOf('cosmos-ui-') === 0; }).map(function (name) { return caches.delete(name); }));
  }).then(function () { return self.registration.unregister(); }));
});
`

// forkPWAOwnHost says whether the request came to the server's own hostname:
// the same rule as utils.EnsureHostnameCosmosAPI, without its side effects
// (a refusal there is recorded as an incident; a browser asking for the
// manifest on the address of an application did nothing wrong).
func forkPWAOwnHost(r *http.Request) bool {
	config := utils.GetMainConfig()
	own := config.HTTPConfig.Hostname
	if config.NewInstall || own == "0.0.0.0" {
		return true
	}
	host := utils.HostWithoutPort(r.Host)
	if config.HTTPConfig.AllowHTTPLocalIPAccess && utils.IsLocalIP(host) {
		return true
	}
	return utils.HostWithoutPort(own) == host
}

// forkAPINoStore keeps the answers of the API out of the browser's caches:
// they are per user, and must not stay readable on the device after a
// logout. A handler that wants something else (the icons) sets its own.
func forkAPINoStore(routers ...*mux.Router) {
	for _, router := range routers {
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
			})
		})
	}
}

// forkPWAStatic wraps the handler of the panel's static files (the path it
// sees has no /cosmos-ui prefix any more).
func forkPWAStatic(next http.Handler, staticDir string) http.Handler {
	exists := func(name string) bool {
		stat, err := os.Stat(staticDir + name)
		return err == nil && !stat.IsDir()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + r.URL.Path)

		switch {
		case name == "/sw.js" || name == "/manifest.webmanifest":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			off := utils.GetMainConfig().DisableWebApp || !forkPWAOwnHost(r)
			if name == "/manifest.webmanifest" {
				// never the panel's page in its place (what the static handler answers for a missing file)
				if off || !exists(name) {
					w.Header().Set("Cache-Control", "no-store")
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Service-Worker-Allowed", forkPWAScope)
			if off || !exists(name) {
				w.WriteHeader(http.StatusOK)
				if r.Method == http.MethodGet {
					w.Write([]byte(forkPWAOffWorker))
				}
				return
			}
			next.ServeHTTP(w, r)
			return

		case strings.HasPrefix(name, "/assets/") && exists(name):
			// the name holds a hash of the content: another content has another name
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

		case !exists(name) || name == "/index.html":
			// the panel's page (also what a missing file is answered with): never
			// kept by a browser, so that a new release is always what opens and
			// a page of the panel never opens from a stale copy
			w.Header().Set("Cache-Control", "no-store")

		case name == "/offline.html":
			w.Header().Set("Cache-Control", "no-cache")
		}

		next.ServeHTTP(w, r)
	})
}
