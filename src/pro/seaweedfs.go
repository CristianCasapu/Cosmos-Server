// Community build stub of the Cosmos Pro feature set: types exist so the shared
// code and the SDK generator compile; handlers answer PRO001 and hooks do nothing.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"sync"
	"time"
)

// SwfsMaster is one pinned master: the device that runs it and its Nebula IP.
// The IP is persisted (not re-resolved) because it is baked into every -peers /
// -mserver flag — changing it means re-provisioning the tier.
type SwfsMaster struct {
	Device string `json:"device"`
	IP     string `json:"ip"`
}

// SwfsJobsConfig is the per-instance maintenance-job configuration. Crontabs
// are 6-field seconds-first (the gocron parser).
type SwfsJobsConfig struct {
	// Without vacuum, deleted data never frees disk and full volumes flip read-only.
	VacuumEnabled    bool    `json:"vacuumEnabled"`
	VacuumCrontab    string  `json:"vacuumCrontab"`
	GarbageThreshold float64 `json:"garbageThreshold"`

	// EC is OFF by default: encoding transiently needs a lot of disk headroom,
	// and ECMinFreeDiskPercent is a hard guard the job checks first.
	ECEnabled            bool    `json:"ecEnabled"`
	ECCrontab            string  `json:"ecCrontab"`
	ECFullPercent        float64 `json:"ecFullPercent"`
	ECQuietForSec        int     `json:"ecQuietForSec"`
	ECMinFreeDiskPercent float64 `json:"ecMinFreeDiskPercent"`

	// Repack registers iff ECEnabled: EC shards never vacuum in place, so repack
	// is the only way deleted space inside an EC volume is reclaimed.
	RepackCrontab     string  `json:"repackCrontab"`
	RepackDeleteRatio float64 `json:"repackDeleteRatio"`

	MonitorCrontab   string  `json:"monitorCrontab"`
	DiskAlertPercent float64 `json:"diskAlertPercent"`

	// Scrub runs volume.check.disk to verify replica consistency (bitrot).
	ScrubEnabled bool   `json:"scrubEnabled"`
	ScrubCrontab string `json:"scrubCrontab"`
}

// SeaweedFSInstance is the persistent record of one managed SeaweedFS cluster.
// Written by the node that creates it; thereafter the single authority on the
// instance's shape and credentials. Heartbeats only flip derived liveness.
type SeaweedFSInstance struct {
	// Name is alphanumeric (like Deployment and ManagedDatabase names) so every
	// derived identifier — the filer database "swfs<name>", docker names, NATS
	// tokens, restic tags — is valid without transformation. Max 27 keeps every
	// derived name inside its consumer's own length limit.
	Name string `json:"name" validate:"required,min=3,max=27,alphanum"`
	// Image is the exact SeaweedFS image every tier runs, captured at creation.
	// Changed only by the rolling-upgrade flow.
	Image string `json:"image"`
	// Tags select the volume-server (and filer placement) nodes. The volume
	// deployment fills them: one volume server per tagged, alive node.
	Tags []string `json:"tags" validate:"required,min=1,dive,min=1,max=64"`
	// Masters is the pinned, persisted master set — see SwfsMaster.
	Masters []SwfsMaster `json:"masters"`
	// The four allocated listen ports. gRPC twins are port+SeaweedFSGRPCOffset.
	// MasterPort/VolumePort/FilerPort are published on each node's Nebula IP
	// (overlay-only); S3Port is never published by a container — it is the
	// socket-proxy listen port of the LB'd S3 route.
	MasterPort int `json:"masterPort"`
	VolumePort int `json:"volumePort"`
	FilerPort  int `json:"filerPort"`
	S3Port     int `json:"s3Port"`
	// FilerReplicas is the fixed size of the filer deployment (capped at the
	// tagged-node count at provision time; the scheduler tolerates a shortfall).
	FilerReplicas int `json:"filerReplicas" validate:"omitempty,min=1,max=16"`
	// DefaultReplication is the master's -defaultReplication (e.g. "001": one
	// extra copy on a different server). Per-node rack/DC flags are deliberately
	// never set — with default topology, "001" already lands copies on
	// different nodes.
	DefaultReplication string `json:"defaultReplication" validate:"omitempty,len=3"`
	// IndexMode is the volume servers' -index: leveldb (default) or memory.
	// Memory costs ~20-24 bytes per stored file per volume server.
	IndexMode string `json:"indexMode" validate:"omitempty,oneof=leveldb memory"`
	// VolumeSizeLimitMB caps individual volume size (master flag).
	VolumeSizeLimitMB int `json:"volumeSizeLimitMB" validate:"omitempty,min=64,max=1048576"`
	// MinFreeSpace is the volume servers' -minFreeSpace (percent, as a string —
	// SeaweedFS also accepts absolute sizes, so the string passes through).
	MinFreeSpace string `json:"minFreeSpace"`
	// MaxStorageGBPerNode bounds how much disk ONE volume server may consume,
	// 0 meaning unlimited (the SeaweedFS default of "fill the disk, minus
	// minFreeSpace"). Enforced through the volume server's -max, i.e. in whole
	// volumes — see seaweedFSMaxVolumeCount for why that is the mechanism and
	// how exact the bound is.
	MaxStorageGBPerNode int `json:"maxStorageGBPerNode" validate:"omitempty,min=0,max=1048576"`
	// RestrictToConstellation mirrors onto the S3 route. Default true.
	RestrictToConstellation bool `json:"restrictToConstellation"`
	// S3Route is the user-facing half of the S3 route; BuildSeaweedFSS3Route forces the rest.
	S3Route utils.ProxyRouteConfig `json:"s3Route" validate:"-"`
	// S3AccessKey / S3SecretKey are the admin credentials minted at creation
	// and applied to the filer via `weed shell s3.configure`. Redacted from the
	// list API like the managed-DB superuser password.
	S3AccessKey string `json:"s3AccessKey"`
	S3SecretKey string `json:"s3SecretKey"`
	// S3Configured flips true once the reconciler has successfully applied the
	// credentials. Until then the gateway accepts anonymous requests — which is
	// why RestrictToConstellation defaults on.
	S3Configured bool `json:"s3Configured"`
	// FilerDB is the name of the auto-provisioned managed database holding the
	// filer store ("swfs"+Name, logical database "filer").
	FilerDB string         `json:"filerDB"`
	Jobs    SwfsJobsConfig `json:"jobs"`
	// Usage is the last space snapshot the monitor job wrote — see SwfsUsage.
	// nil until the first monitor pass of an instance completes.
	Usage *SwfsUsage `json:"usage,omitempty"`
	// MetaBackup configures the store-independent second backup path (weed
	// shell fs.meta.save → restic). Same shape and semantics as a managed
	// database's backup block; nil until configured. The PRIMARY metadata
	// backup is the filer database's own managed-DB backup.
	MetaBackup *ManagedDBBackup `json:"metaBackup,omitempty"`
	// Status: provisioning → ready; deleting during teardown; failed:<reason>
	// when creation was rolled back but the record could not be removed.
	Status string `json:"status"`
	// PendingUpgradeImage is non-empty while a rolling upgrade one-time job is
	// in flight.
	PendingUpgradeImage string    `json:"pendingUpgradeImage,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

// SwfsNodeUsage is what one volume server reports about the space it uses and
// the disk it sits on. Bytes throughout.
type SwfsNodeUsage struct {
	Device string `json:"device"`
	// SeaweedFSBytes is the space this instance occupies on the node. EC shards
	// are not counted, so on an EC'd cluster DiskUsedBytes (whole filesystem) is
	// the figure that still tells the truth.
	SeaweedFSBytes uint64 `json:"seaweedFSBytes"`
	// VolumeCount is how many volume slots the node holds — the unit
	// MaxStorageGBPerNode is enforced in.
	VolumeCount int `json:"volumeCount"`
	// Disk*Bytes describe the filesystem the data directory lives on,
	// everything on it included, not just SeaweedFS.
	DiskAllBytes  uint64 `json:"diskAllBytes"`
	DiskUsedBytes uint64 `json:"diskUsedBytes"`
	DiskFreeBytes uint64 `json:"diskFreeBytes"`
}

// SwfsUsage is the per-instance space snapshot written by the monitor job.
// Persisted on the record because the monitor runs on the job-locus node while
// the API is served from anywhere.
type SwfsUsage struct {
	Nodes []SwfsNodeUsage `json:"nodes"`
	// Totals are stored so every reader agrees on the arithmetic.
	TotalSeaweedFSBytes uint64    `json:"totalSeaweedFSBytes"`
	TotalDiskFreeBytes  uint64    `json:"totalDiskFreeBytes"`
	CollectedAt         time.Time `json:"collectedAt"`
}

// SeaweedFSStatus decorates a record with heartbeat-derived liveness.
type SeaweedFSStatus struct {
	SeaweedFSInstance
	// MastersLive lists pinned master devices whose heartbeat reports the master
	// container running; MastersDown is the complement.
	MastersLive []string `json:"mastersLive"`
	MastersDown []string `json:"mastersDown"`
	// QuorumOK is true while a majority of the pinned masters are live.
	QuorumOK bool `json:"quorumOK"`
	// VolumeNodes / FilerNodes list nodes whose heartbeat reports the
	// respective deployment running; the IPs mirror them in the same order.
	VolumeNodes []string `json:"volumeNodes"`
	FilerNodes  []string `json:"filerNodes"`
	// FilerIPs[0] is the deterministic host ${s3.NAME.host} expands to.
	FilerIPs  []string `json:"filerIPs"`
	VolumeIPs []string `json:"volumeIPs"`
	// TagMatchedNodes are the LIVE nodes whose tags satisfy the instance's Tags.
	TagMatchedNodes []string `json:"tagMatchedNodes"`
	// VolumeDesired / FilerDesired are the target tier sizes given the current
	// tag membership. Same "desired vs actual" idiom as DeploymentHealth.
	VolumeDesired int `json:"volumeDesired"`
	FilerDesired  int `json:"filerDesired"`
	// Health is the derived truth about the instance right now — see the
	// SwfsHealth* constants. HealthReason is a script-readable summary; empty
	// when Health is ready.
	Health       string `json:"health"`
	HealthReason string `json:"healthReason,omitempty"`
}

// ListSeaweedFSWithStatus returns records + heartbeat-derived status.
func ListSeaweedFSWithStatus(lock *sync.RWMutex, js nats.JetStreamContext) ([]SeaweedFSStatus, error) {
	// Pro feature stub.
	var r0 []SeaweedFSStatus
	var r1 error
	return r0, r1
}

func SetSeaweedFSClusterHandles(f func() (*sync.RWMutex, nats.JetStreamContext, *nats.Conn)) {
	// Pro feature stub.
}
