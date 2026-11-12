package constellation

import (
	"net/http"
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/azukaar/cosmos-server/src/pro"
)

// Thin bridges keeping `pro` independent of `constellation`; js/nc are read at call time so reconnects are picked up.

func StateRoute(w http.ResponseWriter, req *http.Request) {
	pro.StateRoute(w, req, &clientConfigLock, js)
}

func StateImportRoute(w http.ResponseWriter, req *http.Request) {
	pro.StateImportRoute(w, req, &clientConfigLock, js)
}

func StateStartEmptyRoute(w http.ResponseWriter, req *http.Request) {
	pro.StateStartEmptyRoute(w, req, &clientConfigLock, js)
}

// StateClusterHandles hands pro the current cluster handles, read at call time.
func StateClusterHandles() (*sync.RWMutex, nats.JetStreamContext, *nats.Conn) {
	return &clientConfigLock, js, nc
}
