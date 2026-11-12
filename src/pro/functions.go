// Community build stub of the Cosmos Pro feature set: types exist so the shared
// code and the SDK generator compile; handlers answer PRO001 and hooks do nothing.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"time"
)

// FunctionSource says where the code comes from: a package in a cluster
// registry, downloaded from that registry's own host (internal ones included).
type FunctionSource struct {
	// Registry is the registry instance name; its type must match the runtime
	// (npm for node*, pypi for python*).
	Registry string `json:"registry" validate:"required,min=3,max=27"`
	// Package is the package name in that registry (npm name, possibly scoped;
	// PyPI project name, normalized on save).
	Package string `json:"package" validate:"required,min=1,max=214"`
	// Version is the PINNED version the deployment runs; set by deploy, never
	// a tag. Empty until the first deploy.
	Version string `json:"version,omitempty"`
	// Token is the read-only registry token minted for this function on the
	// registry (named "function-<name>"). Persisted, redacted in API output.
	Token string `json:"token,omitempty"`
}

// FunctionLimits are the only bounds a function has; they map to docker
// limits and the route timeout. No software limits.
type FunctionLimits struct {
	MemoryMB   int     `json:"memoryMB,omitempty" validate:"omitempty,min=32,max=1048576"`
	CPU        float64 `json:"cpu,omitempty" validate:"omitempty,min=0"`
	TimeoutSec int     `json:"timeoutSec,omitempty" validate:"omitempty,min=1,max=86400"`
	// IdleTTL is how long a replica stays awake without connections before the
	// lazy layer stops it (Go duration string).
	IdleTTL string `json:"idleTTL,omitempty"`
}

// FunctionCronTrigger invokes the function on a schedule, from the leader,
// through the function's own route.
type FunctionCronTrigger struct {
	Name    string `json:"name" validate:"required,min=1,max=64"`
	Enabled bool   `json:"enabled"`
	// Crontab is the 6-field form used everywhere in Cosmos (seconds first).
	Crontab string `json:"crontab" validate:"required"`
	Method  string `json:"method,omitempty"`
	Path    string `json:"path,omitempty"`
	Body    string `json:"body,omitempty"`
}

// FunctionTriggers groups the trigger kinds (cron only in v1).
type FunctionTriggers struct {
	Cron []FunctionCronTrigger `json:"cron,omitempty" validate:"omitempty,dive"`
}

// FunctionRelease is one deploy in the record's history.
type FunctionRelease struct {
	Rev        int       `json:"rev"`
	Version    string    `json:"version"`
	DeployedAt time.Time `json:"deployedAt"`
	DeployedBy string    `json:"deployedBy,omitempty"`
}

// FunctionHandler is one function exposed by a function deployment: a
// container running the package with FUNCTION_TARGET=Handler, fronted by
// Route, invoked by Triggers. Name is the function name — unique cluster-wide
// because it names the container and the route.
type FunctionHandler struct {
	Name    string `json:"name" validate:"required,min=3,max=40,alphanum"`
	Handler string `json:"handler" validate:"required,min=1,max=128"`
	// Entry is FUNCTION_SOURCE: node = file inside the package, python =
	// module path. Empty = the package's main / import name.
	Entry string `json:"entry,omitempty"`
	// Env is merged over the deployment-level env for this handler only.
	Env map[string]string `json:"env,omitempty"`
	// Route is the user-facing part of the function's URL (host, path, auth,
	// shield, restrictions...). Name, mode, target, tunnel and visibility are
	// forced when the compose is derived.
	Route    utils.ProxyRouteConfig `json:"route"`
	Triggers FunctionTriggers       `json:"triggers"`
}

// DeploymentFunction is the `function` attribute of a Deployment, exclusive
// with `compose`.
type DeploymentFunction struct {
	Runtime string `json:"runtime" validate:"required"`
	// Image overrides the runtime's default image (private mirror, patched
	// runtime); it must honour the same contract.
	Image    string            `json:"image,omitempty"`
	Source   FunctionSource    `json:"source"`
	Handlers []FunctionHandler `json:"handlers" validate:"required,min=1,dive"`
	Env      map[string]string `json:"env,omitempty"`
	Limits   FunctionLimits    `json:"limits"`

	Rev      int               `json:"rev"`
	Releases []FunctionRelease `json:"releases,omitempty"`
}

func StartFunctionTriggers() {
	// Pro feature stub.
}

// Function is the API view of ONE handler of a function deployment: the
// deployment-level settings flattened next to the handler's own. It is what
// the Functions page reads and what create/update accept.
type Function struct {
	Name string `json:"name" validate:"required,min=3,max=40,alphanum"`
	// Deployment is the deployment carrying the handler; on create, empty
	// means "fn<name>".
	Deployment string                 `json:"deployment,omitempty"`
	Runtime    string                 `json:"runtime" validate:"required"`
	Image      string                 `json:"image,omitempty"`
	Source     FunctionSource         `json:"source"`
	Handler    string                 `json:"handler" validate:"required,min=1,max=128"`
	Entry      string                 `json:"entry,omitempty"`
	Env        map[string]string      `json:"env,omitempty"`
	Tags       []string               `json:"tags,omitempty" validate:"omitempty,dive,min=1,max=64"`
	Limits     FunctionLimits         `json:"limits"`
	Route      utils.ProxyRouteConfig `json:"route"`
	Triggers   FunctionTriggers       `json:"triggers"`

	Rev      int               `json:"rev"`
	Releases []FunctionRelease `json:"releases,omitempty"`
	// Status is "created" (no version pinned yet) or "deployed".
	Status string `json:"status"`
	// Siblings are the other handlers of the same deployment.
	Siblings  []string  `json:"siblings,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
