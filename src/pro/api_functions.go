// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"net/http"
	"sync"
)

// FunctionDeployRequest is the body of the deploy endpoint.
type FunctionDeployRequest struct {
	// Version to pin; empty or "latest" = the registry's latest.
	Version string `json:"version,omitempty"`
}

// FunctionInvokeRequest is the body of the invoke endpoint. Method defaults to POST.
type FunctionInvokeRequest struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	Body   string `json:"body,omitempty"`
}

// FunctionRuntimesRoute godoc
// @Summary List the function runtimes
// @Description Returns the runtime table: key, label, default image and the registry type it reads packages from (Pro feature)
// @Tags functions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/function-runtimes [get]
func FunctionRuntimesRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// FunctionsRoute lists (GET) or creates (POST) functions.
func FunctionsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// listFunctionsRoute godoc
// @Summary List functions
// @Description Returns every function, its source token redacted (Pro feature)
// @Tags functions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions [get]
func listFunctionsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// createFunctionRoute godoc
// @Summary Create a function
// @Description Creates the function as a handler of a function deployment ("fn<name>" unless
// @Description deployment names one) and deploys source.version (empty = the registry's latest).
// @Description Server-owned fields (rev, releases, status, siblings, dates) are ignored (Pro feature).
// @Tags functions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body Function true "Function to create"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions [post]
func createFunctionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// FunctionsIdRoute reads (GET), replaces (PUT) or deletes (DELETE) a function.
func FunctionsIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// getFunctionRoute godoc
// @Summary Get one function
// @Description Returns the function, its source token redacted (Pro feature)
// @Tags functions
// @Produce json
// @Security BearerAuth
// @Param name path string true "Function name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions/{name} [get]
func getFunctionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// updateFunctionRoute godoc
// @Summary Update a function
// @Description Replaces the editable fields of the function and rewrites its deployment (Pro feature)
// @Tags functions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Function name"
// @Param body body Function true "Function to store"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions/{name} [put]
func updateFunctionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// deleteFunctionRoute godoc
// @Summary Delete a function
// @Description Removes the handler from its deployment; the deployment goes with its last handler (Pro feature)
// @Tags functions
// @Produce json
// @Security BearerAuth
// @Param name path string true "Function name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions/{name} [delete]
func deleteFunctionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// FunctionsDeployRoute godoc
// @Summary Deploy a version of a function
// @Description Pins a published version of the function's package and (re)writes the deployment (Pro feature)
// @Tags functions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Function name"
// @Param body body FunctionDeployRequest false "Version to deploy"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions/{name}/deploy [post]
func FunctionsDeployRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// FunctionsVersionsRoute godoc
// @Summary List the published versions of a function
// @Description Returns the versions of the function's package, newest first, with the registry's latest and the active one flagged (Pro feature)
// @Tags functions
// @Produce json
// @Security BearerAuth
// @Param name path string true "Function name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions/{name}/versions [get]
func FunctionsVersionsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// FunctionsInvokeRoute godoc
// @Summary Invoke a function
// @Description Invokes the function from this node and returns the node, status, body and duration (admin test) (Pro feature)
// @Tags functions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Function name"
// @Param body body FunctionInvokeRequest false "Request to send"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/functions/{name}/invoke [post]
func FunctionsInvokeRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}
