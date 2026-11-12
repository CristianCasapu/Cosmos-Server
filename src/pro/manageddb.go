// Community build stub of the Cosmos Pro feature set.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"sync"
	"time"
)

// ManagedDatabase is the persistent record of one managed database instance. It
// is written once by the node that creates the instance and is thereafter the
// authority on the instance's existence, credentials and home node. Heartbeats
// never write it — they only flip the derived live/stale status reported by the
// list API, so an instance whose home node is down still appears (marked down)
// instead of vanishing.
type ManagedDatabase struct {
	Name   string `json:"name" validate:"required,min=3,max=48,alphanum"`
	Engine string `json:"engine" validate:"omitempty,oneof=postgres"`
	// Image is the exact image tag the container was created from, captured at
	// creation. Version is its human-readable major ("17").
	Image   string `json:"image"`
	Version string `json:"version,omitempty"`
	// HomeNode is the constellation device name of the node running the
	// container, stored verbatim — the same string used as the
	// constellation-nodes KV key and in NATS subjects (device names are
	// validated at creation against `^[a-z0-9_-]{3,32}$`, so no transformation
	// is needed), which is what lets it be matched against a heartbeat and
	// formatted into SubjectManagedDBOp without further translation.
	HomeNode string `json:"homeNode"`
	// HomeNodeName is the raw, human-visible device name, for display only.
	HomeNodeName string `json:"homeNodeName,omitempty"`
	// HomeIP is the home node's Nebula IP. Applications connect here directly;
	// it is what ${db.<name>.host} expands to.
	HomeIP string `json:"homeIP"`
	// Port is the port Cosmos's own socket proxy LISTENS on for this instance —
	// not a docker port publish. The container publishes nothing; it is reachable
	// only on its cosmos network, and every client connection arrives through the
	// proxy route derived from this record (see buildManagedDBProxyRoute).
	Port int `json:"port"`
	// RestrictToConstellation is mirrored onto the proxy route, where
	// TCPSmartShieldMiddleware closes any connection from an IP outside the
	// constellation. Defaults to true on create: a database published to the
	// whole internet by omission is not a defensible default.
	RestrictToConstellation bool `json:"restrictToConstellation"`
	// Route is the user-facing half of the proxy route; buildManagedDBProxyRoute forces the rest.
	Route utils.ProxyRouteConfig `json:"route" validate:"-"`
	// SuperUser / SuperPassword are the instance's bootstrap superuser. Stored
	// in the clear, matching the posture the rest of the cluster state already
	// takes for secrets it must be able to hand back (device API keys in the
	// store, TLS private keys in the config). Redacted out of the list API;
	// readable only through the connection-info endpoint.
	SuperUser     string    `json:"superUser"`
	SuperPassword string    `json:"superPassword"`
	CreatedAt     time.Time `json:"createdAt"`
	// Databases holds the per-application logical databases carved out of this
	// instance, keyed by logical database name.
	Databases map[string]ManagedLogicalDB `json:"databases,omitempty"`
	// Backup is the instance's restic backup configuration, nil until an
	// operator configures one. Interpreted entirely on the HOME node: it is the
	// node that runs the dumps and owns the cron jobs.
	Backup *ManagedDBBackup `json:"backup,omitempty"`
}

// ManagedDBBackup is the backup configuration of one instance.
type ManagedDBBackup struct {
	// Enabled gates scheduled jobs only; manual runs and snapshot listing still work.
	Enabled bool `json:"enabled"`
	// Repository is a restic repository (path or rclone:<remote>:<path>), resolved on the home node.
	Repository string `json:"repository"`
	// Password is generated once and never regenerated; redacted like the superuser password.
	Password string `json:"password"`
	// Crontab and CrontabForget are 6-field (seconds-first) schedules, validated at configure time.
	Crontab       string `json:"crontab"`
	CrontabForget string `json:"crontabForget"`
	// RetentionPolicy is passed verbatim to `restic forget` as arguments.
	RetentionPolicy string    `json:"retentionPolicy"`
	ConfiguredAt    time.Time `json:"configuredAt"`
}

// ManagedLogicalDB is one application's database-and-role pair inside an instance.
type ManagedLogicalDB struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListManagedDatabases returns every stored record, sorted by name.
func ListManagedDatabases(lock *sync.RWMutex, js nats.JetStreamContext) ([]ManagedDatabase, error) {
	// Pro feature stub.
	var r0 []ManagedDatabase
	var r1 error
	return r0, r1
}
