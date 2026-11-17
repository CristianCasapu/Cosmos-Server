package certs

// Fork addition (plan 46): an HTTP-01 order frees the web server's kept
// sockets before it contacts the CA (the restart lifecycle in the main
// package keeps them open between two generations); a DNS-01 order does not.

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestAnHTTPOrderFreesTheServerPortsBeforeTheCA(t *testing.T) {
	setup(t, nil)
	var freed, freedBeforeCA, caAsked atomic.Bool
	ca := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caAsked.CompareAndSwap(false, true) {
			freedBeforeCA.Store(freed.Load())
		}
		http.Error(w, "test authority down", http.StatusServiceUnavailable)
	}))
	defer ca.Close()
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate())
	authority := CA{Kind: "test", Name: "test", Directory: ca.URL + "/directory", roots: roots}
	previous := utils.ForkFreeServerPorts
	utils.ForkFreeServerPorts = func() { freed.Store(true) }
	defer func() { utils.ForkFreeServerPorts = previous }()

	_, failure := order(context.Background(), authority, "local:example.com",
		[]string{"app.example.com"}, nil, utils.GetMainConfig())
	if failure == nil {
		t.Fatal("the order against an authority that is down must fail")
	}
	t.Logf("the order ended: %+v", failure)
	if !caAsked.Load() || !freedBeforeCA.Load() {
		t.Errorf("the ports must be freed before the CA is contacted (CA asked %v, freed first %v)", caAsked.Load(), freedBeforeCA.Load())
	}

	// a DNS-01 order leaves the ports alone
	freed.Store(false)
	zone := utils.GetMainConfig().HTTPConfig.DNSZones[0]
	order(context.Background(), authority, "zone:example.org", []string{"example.org"}, &zone, utils.GetMainConfig())
	if freed.Load() {
		t.Error("a DNS-01 order must not free the web server's ports")
	}
}
