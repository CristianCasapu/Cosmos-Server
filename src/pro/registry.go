// Community build stub of the Cosmos Pro feature set: types exist so the shared
// code and the SDK generator compile; handlers answer PRO001 and hooks do nothing.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"sync"
	"time"
)

// RegistryStorage describes where a registry's blobs live. Exactly one backend
// family's fields are meaningful; the others stay zero.
type RegistryStorage struct {
	Backend string `json:"backend" validate:"required,oneof=seaweedfs s3 local"`
	// SeaweedFS is the managed instance name when Backend is "seaweedfs".
	SeaweedFS string `json:"seaweedfs,omitempty"`
	// Bucket is the object-store bucket for both S3-family backends. Generated
	// as RegistryBucketName(name) for managed SeaweedFS.
	Bucket string `json:"bucket,omitempty"`
	// External S3 only.
	Endpoint  string `json:"endpoint,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	Region    string `json:"region,omitempty"`
	// Path is the filesystem root when Backend is "local".
	Path string `json:"path,omitempty"`
}

// RegistryToken is a deploy credential stored ON the registry record, so it
// replicates with it and any serving node can verify a push without a second
// lookup. Only the hash is kept — the raw token is shown once, at mint.
type RegistryToken struct {
	Name        string `json:"name" validate:"required,min=1,max=64"`
	TokenHash   string `json:"tokenHash"`
	TokenSuffix string `json:"tokenSuffix"`
	// Scopes are "pull"/"push", optionally type-qualified ("docker:push").
	Scopes []string `json:"scopes,omitempty"`
	// ExpiresAt zero means never.
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	// LastUsedAt is written lazily by the protocol paths (throttled) — a CAS
	// write per pull would make the record the registry's bottleneck.
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// RegistryInstance is one registry's persistent record: typed storage plus the endpoint publishing it.
type RegistryInstance struct {
	// Name is alphanumeric and lowercased, like SeaweedFSInstance.Name, so
	// every derived identifier (bucket, KV key, route name) is valid without transformation.
	Name string `json:"name" validate:"required,min=3,max=27,alphanum"`
	// Type is the protocol this registry's contents speak. Immutable.
	Type    string          `json:"type" validate:"required,oneof=docker npm static generic pypi"`
	Storage RegistryStorage `json:"storage"`
	// QuotaBytes caps the registry's stored size; 0 is unlimited.
	QuotaBytes int64 `json:"quotaBytes"`

	// Host is the registry's own hostname; required for every type but static, whose sites publish on their own routes.
	Host string `json:"host,omitempty"`
	// Internal restricts the endpoint to the constellation: the materialized
	// route carries RestrictToConstellation, and the direct mux handler
	// enforces the same check itself, because a request can reach the handler
	// without ever passing through the route.
	Internal           bool `json:"internal"`
	AllowAnonymousPull bool `json:"allowAnonymousPull"`
	// Tags select the serving nodes with nodeMatchesTags AND-semantics. Unlike
	// SeaweedFS, EMPTY is meaningful and is the default: no tag filter, so
	// every node serves the endpoint. A default tag here would silently make a
	// fresh registry unreachable on an untagged cluster.
	Tags   []string        `json:"tags"`
	Tokens []RegistryToken `json:"tokens,omitempty"`
	// Route is the user-facing half of the serving route; identity fields are forced at render (BuildRegistryRoute).
	Route utils.ProxyRouteConfig `json:"route" validate:"-"`

	// IndexDB is the managed database instance holding this registry's package
	// index, IndexDatabase the logical database on it. The SeaweedFS filer's
	// instance for a managed-storage registry, a dedicated instance
	// (RegistryIndexDBName) otherwise. Set at creation, never changed.
	IndexDB       string `json:"indexDB,omitempty"`
	IndexDatabase string `json:"indexDatabase,omitempty"`

	// ManagedByKind / ManagedByName mark a registry a feature created for
	// itself (kind "ci", name "artifacts": the CI artifact store). Such a
	// registry cannot be deleted by hand.
	ManagedByKind string `json:"managedByKind,omitempty"`
	ManagedByName string `json:"managedByName,omitempty"`

	// Status: provisioning -> ready; deleting during teardown.
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// RegistryStatus: a record plus derived serving nodes and stored-size rollup — nothing here is persisted.
type RegistryStatus struct {
	RegistryInstance
	// ServingNodes are the devices whose heartbeat advertises this registry.
	ServingNodes []string      `json:"servingNodes"`
	Stats        RegistryStats `json:"stats"`
}

// ListRegistriesWithStatus returns records, their serving nodes and their
// stats; a failed stats read leaves the zero rollup.
func ListRegistriesWithStatus(lock *sync.RWMutex, js nats.JetStreamContext) ([]RegistryStatus, error) {
	// Pro feature stub.
	var r0 []RegistryStatus
	var r1 error
	return r0, r1
}

// RegistriesServedHere returns the registry endpoint names this node serves,
// given its tags; a KV failure degrades to an empty list.
func RegistriesServedHere(lock *sync.RWMutex, js nats.JetStreamContext, nodeTags []string) []string {
	// Pro feature stub.
	var r0 []string
	return r0
}
