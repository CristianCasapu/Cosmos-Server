package market

// Fork addition: tests of fork_fetch.go.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMarketFetchLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.Error(w, "<html>not found</html>", http.StatusNotFound)
		case "/huge":
			w.Write([]byte(`{"all":"`))
			chunk := []byte(strings.Repeat("a", 1<<20))
			for i := 0; i < marketIndexLimit/len(chunk)+4; i++ {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		case "/slow":
			time.Sleep(2 * time.Second)
		default:
			w.Write([]byte(`{"all":{}}`))
		}
	}))
	defer server.Close()

	if _, err := marketFetch(server.URL + "/missing"); err == nil {
		t.Error("an error page was taken as an index")
	}
	resp, err := marketFetch(server.URL + "/huge")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if n > marketIndexLimit {
		t.Errorf("read %d bytes of an endless index", n)
	}

	previous := marketClient.Timeout
	marketClient.Timeout = 200 * time.Millisecond
	defer func() { marketClient.Timeout = previous }()
	if _, err := marketFetch(server.URL + "/slow"); err == nil {
		t.Error("a source that does not answer held the request")
	}

	resp, err = marketFetch(server.URL + "/ok")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Errorf("a normal index: %v", err)
	}
}

func TestMarketFetchIsHooked(t *testing.T) {
	for file, hook := range map[string]string{"update.go": "marketFetch(cachedMarket.Url)", "index.go": "marketMu.Lock()"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), hook) {
			t.Errorf("%s: the hook %s is missing", file, hook)
		}
	}
}
