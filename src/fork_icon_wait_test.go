package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_icon_wait.go.

// iconRoutes configures one route per name, with the given targets, and
// returns a function that asks for the icon of one of them.
func iconRoutes(t *testing.T, targets map[string]string) func(name string) *httptest.ResponseRecorder {
	t.Helper()
	prev := utils.GetBaseMainConfig()
	prevCache := IconCache
	prevWait, prevFailed := iconWait, iconFailedFor
	IconCache = map[string]CachedImage{}
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(prev)
		IconCache = prevCache
		iconWait, iconFailedFor = prevWait, prevFailed
	})
	var routes []utils.ProxyRouteConfig
	for name, target := range targets {
		routes = append(routes, utils.ProxyRouteConfig{Name: name, Mode: "PROXY", Target: target})
	}
	utils.LoadBaseMainConfig(utils.Config{HTTPConfig: utils.HTTPConfig{Hostname: "cosmos.example.com", ProxyConfig: utils.ProxyConfig{Routes: routes}}})
	return func(name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/favicon?route="+name, nil)
		ctx := context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
			Nickname: "alice", Role: utils.USER, UserRole: utils.USER, Permissions: utils.GetRolePermissions(utils.USER),
		})
		w := httptest.NewRecorder()
		GetFavicon(w, req.WithContext(ctx))
		return w
	}
}

// an application with an icon, counting the requests for its page
func iconApp(t *testing.T, pageHits *atomic.Int32, delay time.Duration) *httptest.Server {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			pageHits.Add(1)
			time.Sleep(delay)
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><head><link rel="icon" href="/i.png"></head></html>`))
		case "/i.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(tinyPNG)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(app.Close)
	return app
}

func TestIconOfASlowAppDoesNotHoldTheOthers(t *testing.T) {
	// an application that takes its time: longer than the browser is made to wait
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() { close(release); slow.Close() })
	var hits atomic.Int32
	fast := iconApp(t, &hits, 0)
	ask := iconRoutes(t, map[string]string{"slow": slow.URL, "fast": fast.URL})
	iconWait = 300 * time.Millisecond

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	var slowAnswer *httptest.ResponseRecorder
	go func() { defer wg.Done(); slowAnswer = ask("slow") }()
	time.Sleep(50 * time.Millisecond) // the slow lookup has started
	w := ask("fast")
	if took := time.Since(start); took > 250*time.Millisecond {
		t.Errorf("the icon of a fast app waited %v behind a slow one", took)
	}
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("the fast app's icon: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	wg.Wait()
	if took := time.Since(start); took > time.Second {
		t.Errorf("the slow app's icon request was answered after %v (at most %v expected)", took, iconWait)
	}
	if slowAnswer.Header().Get("Content-Type") == "image/png" && slowAnswer.Body.Len() == len(tinyPNG) {
		t.Errorf("the slow app answered with an icon it does not have")
	}
}

func TestIconNotFoundIsRememberedForAWhile(t *testing.T) {
	var hits atomic.Int32
	none := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			hits.Add(1)
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(none.Close)
	ask := iconRoutes(t, map[string]string{"none": none.URL})

	ask("none")
	once := hits.Load() // upstream's search reads the page twice
	if once == 0 {
		t.Fatalf("the application was not asked")
	}
	start := time.Now()
	ask("none")
	ask("none")
	if hits.Load() != once {
		t.Errorf("an application without an icon was asked again at once (%d page requests)", hits.Load())
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("a remembered failure took %v to answer", took)
	}
	// once the time is up it is asked again
	iconFailedFor = 0
	time.Sleep(5 * time.Millisecond)
	ask("none")
	if hits.Load() != 2*once {
		t.Errorf("after the waiting time the application was not asked again (%d page requests)", hits.Load())
	}
}

func TestIconIsLookedUpOnceForManyRequests(t *testing.T) {
	var hits atomic.Int32
	app := iconApp(t, &hits, 150*time.Millisecond)
	ask := iconRoutes(t, map[string]string{"app": app.URL})

	var wg sync.WaitGroup
	answers := make([]*httptest.ResponseRecorder, 6)
	for i := range answers {
		wg.Add(1)
		go func(i int) { defer wg.Done(); answers[i] = ask("app") }(i)
	}
	wg.Wait()
	if hits.Load() > 2 { // one search reads the page twice
		t.Errorf("six requests at once asked the application %d times", hits.Load())
	}
	once := hits.Load()
	for i, w := range answers {
		if w.Header().Get("Content-Type") != "image/png" {
			t.Errorf("request %d: %d %q", i, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// and kept: the next request does not ask
	ask("app")
	if hits.Load() != once {
		t.Errorf("a known icon was looked up again")
	}
}
