package constellation

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/pro"
)

// Thin bridges keeping `pro` independent of `constellation`; js is read at
// call time so reconnects are picked up.

func CIProjectsRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIProjectsRoute(w, req, &clientConfigLock, js)
}

func CIProjectsIdRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIProjectsIdRoute(w, req, &clientConfigLock, js)
}

func CIProjectWebhookRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIProjectWebhookRoute(w, req, &clientConfigLock, js)
}

func CIProjectCopyRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIProjectCopyRoute(w, req, &clientConfigLock, js)
}

func CIConnectionsRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIConnectionsRoute(w, req, &clientConfigLock, js)
}

func CIConnectionsIdRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIConnectionsIdRoute(w, req, &clientConfigLock, js)
}

func CIConnectionReposRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIConnectionReposRoute(w, req, &clientConfigLock, js)
}

func CIDetectRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIDetectRoute(w, req, &clientConfigLock, js)
}

func CIBuildsRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIBuildsRoute(w, req, &clientConfigLock, js)
}

func CIAllBuildsRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIAllBuildsRoute(w, req, &clientConfigLock, js)
}

func CIBuildIdRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIBuildIdRoute(w, req, &clientConfigLock, js)
}

func CIBuildActionRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIBuildActionRoute(w, req, &clientConfigLock, js)
}

func CIBuildLogsRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIBuildLogsRoute(w, req, &clientConfigLock, js)
}

func CIRunnersRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIRunnersRoute(w, req, &clientConfigLock, js)
}

func CIWebhookRoute(w http.ResponseWriter, req *http.Request) {
	pro.CIWebhookRoute(w, req, &clientConfigLock, js)
}
