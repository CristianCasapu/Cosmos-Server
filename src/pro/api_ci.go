// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"net/http"
	"sync"
)

// CIDetectRequest is the body of the detect endpoint.
type CIDetectRequest struct {
	Source CISource        `json:"source"`
	Build  CIBuildSettings `json:"build"`
	// Project names an existing project whose stored git credentials are used when Source has
	// neither a connection nor a token.
	Project string `json:"project,omitempty"`
}

// CIBuildTriggerRequest is the body of a manual build. Both fields are optional.
type CIBuildTriggerRequest struct {
	// Branch defaults to the project's default branch.
	Branch string `json:"branch,omitempty"`
	// SHA pins the commit; empty builds the branch head.
	SHA string `json:"sha,omitempty"`
}

// CIProjectsRoute lists (GET) or creates (POST) projects.
func CIProjectsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciListProjectsRoute godoc
// @Summary List CI projects
// @Description Returns every CI project with its last build, secrets and tokens redacted (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects [get]
func ciListProjectsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciCreateProjectRoute godoc
// @Summary Create a CI project
// @Description Validates the project, mints its registry tokens, registers the webhook on the
// @Description git provider (best effort: a failure comes back as a warning) and stores it.
// @Description Server-owned fields (webhook, stats, previews, counters, dates) are ignored (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CIProject true "Project to create"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects [post]
func ciCreateProjectRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIProjectsIdRoute reads (GET), replaces (PUT) or deletes (DELETE) a project.
func CIProjectsIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciGetProjectRoute godoc
// @Summary Get one CI project
// @Description Returns the project with its last build, secrets and tokens redacted (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "Project name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name} [get]
func ciGetProjectRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciUpdateProjectRoute godoc
// @Summary Update a CI project
// @Description Replaces the editable fields of the project. A secret sent with an empty value
// @Description keeps its stored value, and so does the git token (both are write-only) (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Project name"
// @Param body body CIProject true "Project to store"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name} [put]
func ciUpdateProjectRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciDeleteProjectRoute godoc
// @Summary Delete a CI project
// @Description Removes the project with its previews and its webhook, and revokes its registry tokens (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "Project name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name} [delete]
func ciDeleteProjectRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIProjectWebhookRoute godoc
// @Summary Rotate or re-register the webhook of a CI project
// @Description rotate mints a new webhook secret; register re-creates the hook on the git provider (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param action path string true "Action" Enums(rotate, register)
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/webhook/{action} [post]
func CIProjectWebhookRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIDetectRoute godoc
// @Summary Detect what CI would build from a repository
// @Description Clones the repository and reports what CI would do with it, without creating anything (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CIDetectRequest true "Repository to inspect"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/detect [post]
func CIDetectRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIBuildsRoute lists (GET, ?limit=) or triggers (POST {branch, sha}) builds
// of a project.
func CIBuildsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciListBuildsRoute godoc
// @Summary List the builds of a CI project
// @Description Returns the most recent builds of the project, newest first (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param limit query int false "Maximum number of builds (default 50)"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/builds [get]
func ciListBuildsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciTriggerBuildRoute godoc
// @Summary Trigger a build of a CI project
// @Description Queues a manual build of a branch (the project's default branch when none is
// @Description given), optionally pinned to a commit. Manual builds run with secrets (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param body body CIBuildTriggerRequest false "Branch and commit to build"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/builds [post]
func ciTriggerBuildRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIAllBuildsRoute godoc
// @Summary List recent builds across CI projects
// @Description Returns the most recent builds of every project, newest first (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Maximum number of builds"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/builds [get]
func CIAllBuildsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIBuildIdRoute reads one build (GET) or deletes its record (DELETE).
func CIBuildIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIBuildActionRoute godoc
// @Summary Act on a build of a CI project
// @Description Runs one action on the build: cancel, retry, approve (a pull-request build waiting for
// @Description approval) or deploy (Pro feature).
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param number path int true "Build number"
// @Param action path string true "Action" Enums(cancel, retry, approve, deploy)
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/builds/{number}/{action} [post]
func CIBuildActionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIBuildLogsRoute godoc
// @Summary Read the logs of a build step
// @Description Returns the output of one step from chunk `from`. The answer carries `next`, the chunk to
// @Description ask for on the next poll, and `done` once the step has finished (Pro feature).
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param number path int true "Build number"
// @Param job query string false "Job name (empty: the build's own steps)"
// @Param step query int false "Step index"
// @Param from query int false "First chunk to return"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/builds/{number}/logs [get]
func CIBuildLogsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIRunnersRoute godoc
// @Summary List the CI build capacity of the cluster
// @Description Returns every node with its running jobs, buildkitd state, architecture and the
// @Description platforms it can build for (native and emulated) (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/runners [get]
func CIRunnersRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIWebhookRoute godoc
// @Summary Receive a CI provider webhook
// @Description Accepts a push / pull-request delivery for the named project and queues a build (Pro feature)
// @Tags ci
// @Accept json
// @Produce json
// @Param name path string true "CI project name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/hooks/{name} [post]
func CIWebhookRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciGetBuildRoute godoc
// @Summary Get one build of a CI project
// @Description Returns the build with its trigger, its own steps (prepare, deploy), its jobs with their
// @Description steps and artifacts, and the deploy result (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param number path int true "Build number"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/builds/{number} [get]
func ciGetBuildRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciDeleteBuildRoute godoc
// @Summary Delete one build of a CI project
// @Description Removes the build record, its logs and its stored artifacts. Refused while the build is
// @Description queued or running: cancel it first (Pro feature).
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "CI project name"
// @Param number path int true "Build number"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/builds/{number} [delete]
func ciDeleteBuildRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CICopyRequest is the body of the copy endpoint: what to take from another
// project. Copied entries replace same-named ones; the rest is kept.
type CICopyRequest struct {
	// From is the project to copy from.
	From string `json:"from"`
	// Secrets copies the project secrets, values and PR availability included.
	Secrets bool `json:"secrets"`
	// BuildEnv copies the build-time environment (build.env).
	BuildEnv bool `json:"buildEnv"`
	// RuntimeEnv copies the runtime environment of the deploy template (deploy.template.env).
	RuntimeEnv bool `json:"runtimeEnv"`
}

// CICopyResult counts what was copied.
type CICopyResult struct {
	Secrets    int `json:"secrets"`
	BuildEnv   int `json:"buildEnv"`
	RuntimeEnv int `json:"runtimeEnv"`
}

// CIGitConnectionView is a connection as the API returns it: redacted, plus
// the projects using it.
type CIGitConnectionView struct {
	CIGitConnection
	Projects []string `json:"projects"`
}

// CIProjectCopyRoute godoc
// @Summary Copy secrets and environment variables from another CI project
// @Description Copies the chosen sets (secrets, build env, runtime env) of the source project onto this one.
// @Description Same-named entries are replaced, the others kept. Secret values never leave the server (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Project name (destination)"
// @Param body body CICopyRequest true "What to copy and from where"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/projects/{name}/copy [post]
func CIProjectCopyRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIConnectionsRoute lists (GET) or creates (POST) git connections.
func CIConnectionsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciListConnectionsRoute godoc
// @Summary List git connections
// @Description Returns every git connection (shared credentials) with its token redacted and the projects using it (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/connections [get]
func ciListConnectionsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciCreateConnectionRoute godoc
// @Summary Create a git connection
// @Description Stores a set of git credentials (provider, host, token) that projects can share
// @Description instead of carrying their own token (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CIGitConnection true "Connection to create"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/connections [post]
func ciCreateConnectionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIConnectionsIdRoute reads (GET), replaces (PUT) or deletes (DELETE) a
// connection.
func CIConnectionsIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciGetConnectionRoute godoc
// @Summary Get one git connection
// @Description Returns the connection with its token redacted and the projects using it (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "Connection name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/connections/{name} [get]
func ciGetConnectionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciUpdateConnectionRoute godoc
// @Summary Update a git connection
// @Description Replaces the connection. An empty token keeps the stored one (write-only).
// @Description Every project on the connection uses the new credentials from its next build (Pro feature).
// @Tags ci
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Connection name"
// @Param body body CIGitConnection true "Connection to store"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/connections/{name} [put]
func ciUpdateConnectionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// ciDeleteConnectionRoute godoc
// @Summary Delete a git connection
// @Description Removes the connection. Refused while a project uses it (Pro feature)
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "Connection name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/connections/{name} [delete]
func ciDeleteConnectionRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// CIConnectionReposRoute godoc
// @Summary List the repositories a git connection can reach
// @Description Asks the provider which repositories the connection's token can access, most recently
// @Description updated first, and filters them by ?q=. Plain git connections cannot list (Pro feature).
// @Tags ci
// @Produce json
// @Security BearerAuth
// @Param name path string true "Connection name"
// @Param q query string false "Filter on the repository name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/ci/connections/{name}/repos [get]
func CIConnectionReposRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}
