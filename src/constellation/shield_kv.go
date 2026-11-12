package constellation

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
)

// SmartShield strikes and bans shared across the constellation.
//
// One key per ban (its cluster-unique ID), so two nodes striking the same
// client at once never overwrite each other. Every node keeps its local store
// as the fast path and merges what it sees on the bucket; lag of a few hundred
// milliseconds is fine, the block windows are hours long. Memory-backed in the
// community edition (a restart clears everything, as it always has), file-backed
// in Pro so bans survive restarts. Budgets are never shared.
//
// The proxy package registers handlers; this file never imports it.

const ShieldBucket = "constellation-shield"

type ShieldSyncHandlers struct {
	Put       func(utils.ShieldBan)
	Delete    func(id string)
	Reconcile func(present map[string]bool)
}

var shieldSync struct {
	lock     sync.Mutex
	stop     chan struct{}
	handlers ShieldSyncHandlers
}

func SetShieldSyncHandlers(h ShieldSyncHandlers) {
	shieldSync.lock.Lock()
	defer shieldSync.lock.Unlock()
	shieldSync.handlers = h
}

func shieldHandlers() ShieldSyncHandlers {
	shieldSync.lock.Lock()
	defer shieldSync.lock.Unlock()
	return shieldSync.handlers
}

// ShieldBanID builds the bucket key for a ban: the client prefix lets unban
// delete a client's whole history with one listing.
func ShieldBanID(clientID string, at time.Time, node string) string {
	return ShieldClientPrefix(clientID) + at.UTC().Format("20060102T150405.000000000") + "." + hex.EncodeToString([]byte(node))
}

func ShieldClientPrefix(clientID string) string {
	return hex.EncodeToString([]byte(clientID)) + "."
}

func shieldKVConfig() nats.KeyValueConfig {
	storage := nats.MemoryStorage
	if utils.IsPro() {
		storage = nats.FileStorage
	}
	return nats.KeyValueConfig{
		Bucket:  ShieldBucket,
		TTL:     0, // the shield's own cleanup expires entries
		Storage: storage,
	}
}

// ensureShieldBucket attaches to the bucket, creating it on a manager. The
// caller holds clientConfigLock.
func ensureShieldBucket() (nats.KeyValue, error) {
	if js == nil {
		return nil, nats.ErrConnectionClosed
	}
	kv, err := js.KeyValue(ShieldBucket)
	if err == nil {
		return kv, nil
	}
	if isAgentNode() {
		return nil, err
	}
	return createKVAtTopology(shieldKVConfig())
}

func ShieldSyncEnabled() bool {
	return IsClientConnected() && !IsConstellationStandalone()
}

func PublishShieldBan(ban utils.ShieldBan) error {
	if !ShieldSyncEnabled() {
		return nil
	}
	payload, err := json.Marshal(ban)
	if err != nil {
		return err
	}
	clientConfigLock.RLock()
	defer clientConfigLock.RUnlock()
	kv, err := ensureShieldBucket()
	if err != nil {
		return err
	}
	_, err = kv.Put(ban.ID, payload)
	return err
}

// DeleteShieldBans removes every entry of a client from the bucket.
func DeleteShieldBans(clientID string) error {
	if !ShieldSyncEnabled() {
		return nil
	}
	clientConfigLock.RLock()
	defer clientConfigLock.RUnlock()
	kv, err := ensureShieldBucket()
	if err != nil {
		return err
	}
	keys, err := kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return nil
	}
	if err != nil {
		return err
	}
	prefix := ShieldClientPrefix(clientID)
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			if errDel := kv.Delete(key); errDel != nil {
				err = errDel
			}
		}
	}
	return err
}

// DeleteShieldBan removes one entry (the shield's cleanup of its own expired bans).
func DeleteShieldBan(id string) error {
	if !ShieldSyncEnabled() {
		return nil
	}
	clientConfigLock.RLock()
	defer clientConfigLock.RUnlock()
	kv, err := ensureShieldBucket()
	if err != nil {
		return err
	}
	return kv.Delete(id)
}

func StopShieldSync() {
	shieldSync.lock.Lock()
	defer shieldSync.lock.Unlock()
	if shieldSync.stop != nil {
		close(shieldSync.stop)
		shieldSync.stop = nil
	}
}

// ShieldSyncInit watches the bucket and feeds the registered handlers, the
// same shape as the nodes-bucket watcher: the outer loop re-establishes the
// watcher whenever its stream goes away.
func ShieldSyncInit() {
	StopShieldSync()
	shieldSync.lock.Lock()
	shieldSync.stop = make(chan struct{})
	stop := shieldSync.stop
	shieldSync.lock.Unlock()

	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			watcher := func() nats.KeyWatcher {
				if err := ClientConnectToJS(); err != nil {
					return nil
				}
				clientConfigLock.RLock()
				defer clientConfigLock.RUnlock()
				kv, err := ensureShieldBucket()
				if err != nil {
					utils.Debug("[NATS] shield KV not ready: " + err.Error())
					return nil
				}
				w, err := kv.WatchAll()
				if err != nil {
					utils.Debug("[NATS] shield KV watcher: " + err.Error())
					return nil
				}
				return w
			}()

			if watcher != nil {
				utils.Log("[NATS] KV watcher started for shield bans")
				present := map[string]bool{}
				initial := true
			watch:
				for {
					select {
					case <-stop:
						watcher.Stop()
						return
					case entry, ok := <-watcher.Updates():
						if !ok {
							watcher.Stop()
							utils.Warn("[NATS] shield KV watcher channel closed, re-establishing...")
							break watch
						}
						h := shieldHandlers()
						if entry == nil {
							// end of the initial snapshot: drop what other nodes removed while we were away
							if initial && h.Reconcile != nil {
								h.Reconcile(present)
							}
							initial = false
							continue
						}
						switch entry.Operation() {
						case nats.KeyValueDelete, nats.KeyValuePurge:
							if h.Delete != nil {
								h.Delete(entry.Key())
							}
						default:
							var ban utils.ShieldBan
							if err := json.Unmarshal(entry.Value(), &ban); err != nil || ban.ID == "" {
								continue
							}
							if initial {
								present[ban.ID] = true
							}
							if h.Put != nil {
								h.Put(ban)
							}
						}
					}
				}
			}

			select {
			case <-stop:
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}
