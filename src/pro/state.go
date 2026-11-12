package pro

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Community build stub of the Pro cluster-state mirror (export/import of every
// Pro record, carried across a force-reform). Types mirror the Pro build
// field-for-field so the generated SDK is identical.

const (
	StateMirrorFile = "backup.cosmos-state.json"
	StateReseedFile = "reseed.cosmos-state.json"
	ImportingKey    = "__importing__"
)

// ProState is the on-disk and over-the-wire format: raw KV values per bucket.
type ProState struct {
	Version    int                                   `json:"version"`
	ExportedAt time.Time                             `json:"exportedAt"`
	Node       string                                `json:"node"`
	Buckets    map[string]map[string]json.RawMessage `json:"buckets"`
}

// StateImportResult counts, per bucket, records written and records that
// already existed (left untouched).
type StateImportResult struct {
	Created map[string]int `json:"created"`
	Present map[string]int `json:"present"`
}

// StateSummary is the mirror's shape for the UI.
type StateSummary struct {
	Counts        map[string]int `json:"counts"`
	MirroredAt    time.Time      `json:"mirroredAt"`
	MirrorError   string         `json:"mirrorError,omitempty"`
	ReseedPending bool           `json:"reseedPending"`
	Importing     bool           `json:"importing"`
}

func SetStateClusterHandles(fn func() (*sync.RWMutex, nats.JetStreamContext, *nats.Conn)) {}

func StartStateMirror() {}

func StageStateReseed() error { return nil }

func ApplyStateReseed(lock *sync.RWMutex, js nats.JetStreamContext) {}

func RemoveStateFiles() {}
