package market

// Fork addition: the Market's index is fetched with a time limit and a size
// limit, and one request at a time works on the cache.
//
// Upstream fetched each source with Go's default client (no time limit, any
// size, any status) and two requests to the Market page could rewrite the
// shared cache at the same time.

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// marketIndexLimit is far above the size of a market index (the official
// one is a few megabytes).
const marketIndexLimit = 32 << 20

var marketClient = &http.Client{Timeout: 30 * time.Second}

// marketMu is held by MarketGet while it updates and reads the cache.
var marketMu sync.Mutex

type limitedBody struct {
	io.Reader
	io.Closer
}

// marketFetch gets a market index: an answer other than 200 is an error, and
// what is read of the body ends at marketIndexLimit (the index then fails to
// parse, as a broken one would).
func marketFetch(url string) (*http.Response, error) {
	resp, err := marketClient.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("the market answered %d", resp.StatusCode)
	}
	resp.Body = limitedBody{Reader: io.LimitReader(resp.Body, marketIndexLimit), Closer: resp.Body}
	return resp, nil
}
