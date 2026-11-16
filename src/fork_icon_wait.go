package main

// Fork addition: the icon of an application never holds the page.
//
// Upstream looked an icon up while holding one lock for every icon of the
// server, for up to ten seconds for the application's page and ten more for
// each icon link: one application that did not answer made every other icon
// of the page wait behind it, and since a failure was not remembered, the
// next page asked again. In a browser that opens six connections to the
// panel, those waiting icons also held back the page's own requests.
//
// Now:
//   - one lookup per application at a time; the lock is held only to read or
//     write the list of known icons, never while asking an application;
//   - the browser waits at most iconWait for an icon not yet known: after
//     that it gets the placeholder, and the lookup goes on by itself, so the
//     icon is there at the next visit;
//   - an application whose icon could not be found gets the placeholder at
//     once for iconFailedFor, then it is asked again;
//   - the icon links are fetched side by side and the first picture wins (as
//     upstream), without leaving the slower ones blocked forever.

import (
	"net/http"
	"sync"
	"time"

	"go.deanishe.net/favicon"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	iconWait      = 2 * time.Second
	iconFailedFor = 5 * time.Minute
)

// an icon lookup in progress; done is closed when it has ended
type iconLookup struct {
	done  chan struct{}
	image *CachedImage
}

var iconLookups = struct {
	sync.Mutex
	running map[string]*iconLookup
	failed  map[string]time.Time
}{running: map[string]*iconLookup{}, failed: map[string]time.Time{}}

// at most one failure is remembered per configured address; this bounds the
// list against a configuration that is changed again and again
const maxIconFailures = 1000

func knownIcon(key string) (CachedImage, bool) {
	IconCacheLock <- true
	defer func() { <-IconCacheLock }()
	image, ok := IconCache[key]
	return image, ok
}

func keepIcon(key string, image CachedImage) {
	IconCacheLock <- true
	defer func() { <-IconCacheLock }()
	IconCache[key] = image
}

// startIconLookup returns the lookup of key, starting it when none runs.
func startIconLookup(key string, find func() *CachedImage) *iconLookup {
	iconLookups.Lock()
	defer iconLookups.Unlock()
	if lookup, ok := iconLookups.running[key]; ok {
		return lookup
	}
	lookup := &iconLookup{done: make(chan struct{})}
	iconLookups.running[key] = lookup
	go func() {
		image := find()
		if image != nil {
			keepIcon(key, *image)
		}
		iconLookups.Lock()
		delete(iconLookups.running, key)
		if image == nil {
			if len(iconLookups.failed) >= maxIconFailures {
				iconLookups.failed = map[string]time.Time{}
			}
			iconLookups.failed[key] = time.Now()
		} else {
			delete(iconLookups.failed, key)
		}
		lookup.image = image
		iconLookups.Unlock()
		close(lookup.done)
	}()
	return lookup
}

func iconRecentlyFailed(key string) bool {
	iconLookups.Lock()
	defer iconLookups.Unlock()
	at, ok := iconLookups.failed[key]
	if ok && time.Since(at) > iconFailedFor {
		delete(iconLookups.failed, key)
		return false
	}
	return ok
}

// forkServeFavicon answers the icon request for the application at siteurl
// (cacheKey: its configured address). It always answers, and says so.
func forkServeFavicon(w http.ResponseWriter, cacheKey string, siteurl string, container string) bool {
	if image, ok := knownIcon(cacheKey); ok {
		sendImage(w, image)
		return true
	}
	if iconRecentlyFailed(cacheKey) {
		sendFallback(w)
		return true
	}
	// A dormant lazy container must never be woken for an icon; serve the placeholder.
	if container != "" && docker.LazyIsDormant(container) {
		sendFallback(w)
		return true
	}
	// A container that is not running has no address: nothing to ask (plan 43;
	// before, the container's name was looked up in the server's DNS instead)
	if container != "" && (!utils.IsInsideContainer || utils.IsHostNetwork) {
		if _, _, err := docker.ContainerTarget(container); err != nil {
			sendFallback(w)
			return true
		}
	}
	dial := faviconDialURL(siteurl, container)
	lookup := startIconLookup(cacheKey, func() *CachedImage { return findIcon(dial) })
	timer := time.NewTimer(iconWait)
	defer timer.Stop()
	select {
	case <-lookup.done:
		if lookup.image != nil {
			sendImage(w, *lookup.image)
			return true
		}
	case <-timer.C:
	}
	sendFallback(w)
	return true
}

// findIcon is upstream's search: the application's page (following its
// redirects), the icons it names and the usual file names; the first picture
// found wins. nil when there is none.
func findIcon(siteurl string) *CachedImage {
	utils.Log("Fetch favicon for " + printable(siteurl, 200))
	defaultIcons := []*favicon.Icon{
		{URL: "/favicon.ico", Width: 0},
		{URL: "/favicon.png", Width: 0},
		{URL: "favicon.ico", Width: 0},
		{URL: "favicon.png", Width: 0},
	}
	var icons []*favicon.Icon
	page, err := httpGetWithTimeout(siteurl)
	if err != nil {
		utils.Debug("FaviconFetch: " + err.Error())
		icons = defaultIcons
	} else {
		page.Body.Close()
		siteurl = page.Request.URL.String()
		icons, err = findIcons(siteurl)
		if err != nil || len(icons) == 0 {
			icons = defaultIcons
		} else {
			for _, d := range defaultIcons {
				found := false
				for _, icon := range icons {
					if icon.URL == d.URL {
						found = true
						break
					}
				}
				if !found {
					icons = append(icons, d)
				}
			}
		}
	}

	// room for every answer, so that no fetch stays blocked once one has won
	results := make(chan result, len(icons))
	var wg sync.WaitGroup
	for _, icon := range icons {
		if icon.Width <= 256 {
			wg.Add(1)
			go func(icon *favicon.Icon) {
				defer wg.Done()
				fetchAndCacheIcon(icon, siteurl, results)
			}(icon)
		}
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	for found := range results {
		image := found.CachedImage
		return &image
	}
	utils.Log("Favicon final fallback")
	return nil
}
