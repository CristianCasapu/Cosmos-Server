package snapshots

// Fork addition (plan 47, I2e): what a folder that is backed up holds that is
// usually not worth keeping, so that the form can offer it as ticks instead of
// a pattern typed by hand.
//
//	POST /cosmos/api/snapshots/look  {"source": "/storage/apps"}
//
// The look is bounded: links are never followed (a link is listed, not
// entered), at most lookMaxDepth levels below the folder, lookMaxEntries
// entries and lookMaxTime; past a bound the answer says "complete": false and
// the sizes are lower bounds. Admin only, one look at a time. Every pattern
// offered passes cleanExclude, so a tick never makes a setting the server
// refuses.

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	lookMaxDepth   = 4
	lookMaxEntries = 50000
	lookMaxTime    = 3 * time.Second
	lookMaxTop     = 200
	lookBusy       sync.Mutex
)

const (
	lookMaxBody  = 4 << 10
	lookMaxFound = 3
	lookBatch    = 256
)

// LookEntry is one entry at the top of the folder.
type LookEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Link bool   `json:"link"`
	Size int64  `json:"size"`
	// Pattern leaves this entry out (a folder: what it holds), "" when its name cannot be one
	Pattern string `json:"pattern,omitempty"`
}

// LookOffer is something worth leaving out that was found.
type LookOffer struct {
	Pattern string   `json:"pattern"`
	Kind    string   `json:"kind"` // cache, thumbnails, temporary, trash, docker
	Found   []string `json:"found"`
	Count   int      `json:"count"`
	Size    int64    `json:"size"`
}

// LookAnswer is what the route answers.
type LookAnswer struct {
	Source   string      `json:"source"`
	Complete bool        `json:"complete"`
	Entries  int         `json:"entries"`
	Top      []LookEntry `json:"top"`
	Offers   []LookOffer `json:"offers"`
}

// the names of folders worth leaving out, by kind (compared without case)
var lookFolderKinds = map[string]string{
	"cache": "cache", ".cache": "cache", "caches": "cache",
	"thumbnails": "thumbnails", ".thumbnails": "thumbnails", "thumbs": "thumbnails", "thumbnail": "thumbnails",
	"tmp": "temporary", "temp": "temporary", ".tmp": "temporary", "transcodes": "temporary", "transcode": "temporary",
}

// files left half-written by downloads and editors
var lookFilePatterns = []string{"*.tmp", "*.part", "*.partial"}

// escapeGlob makes a name match itself only.
func escapeGlob(name string) string {
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(`*?[]\`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validPattern(pattern string) bool {
	clean, err := cleanExclude("", pattern)
	return err == nil && clean == pattern
}

type looker struct {
	root     string
	deadline time.Time
	entries  int
	complete bool
	top      []LookEntry
	offers   []*LookOffer
	byKey    map[string]*LookOffer
	docker   string // Docker's data folder below the root, as a relative path; "" when not inside
}

func (l *looker) offer(pattern, kind, rel string) *LookOffer {
	if !validPattern(pattern) {
		return nil
	}
	o := l.byKey[pattern]
	if o == nil {
		o = &LookOffer{Pattern: pattern, Kind: kind, Found: []string{}}
		l.byKey[pattern] = o
		l.offers = append(l.offers, o)
	}
	o.Count++
	if len(o.Found) < lookMaxFound {
		o.Found = append(o.Found, rel)
	}
	return o
}

func (l *looker) stopped() bool {
	if l.entries >= lookMaxEntries || time.Now().After(l.deadline) {
		l.complete = false
		return true
	}
	return false
}

// lookOpenFlags open a folder for reading its names only: never through a
// link (O_NOFOLLOW), never anything but a folder (O_DIRECTORY: a FIFO or a
// device swapped in fails at once instead of blocking), never waiting
// (O_NONBLOCK).
const lookOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

// walk reads one folder, given as an open descriptor it takes over; sizes go
// to every counter in add (the top entry and the offers that hold this
// folder). Every entry is read and opened relative to its folder's
// descriptor, so a name swapped for a link or a FIFO between the look at it
// and the opening is never followed into or waited on.
func (l *looker) walk(dirfd int, rel string, depth int, add []*int64) {
	f := os.NewFile(uintptr(dirfd), "look")
	if f == nil {
		unix.Close(dirfd)
		return
	}
	defer f.Close()
	for {
		if l.stopped() {
			return
		}
		names, err := f.Readdirnames(lookBatch)
		for _, name := range names {
			if l.stopped() {
				return
			}
			l.entries++
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			var st unix.Stat_t
			if unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
				continue
			}
			kind := st.Mode & unix.S_IFMT
			isLink := kind == unix.S_IFLNK
			isDir := kind == unix.S_IFDIR
			counters := add
			if depth == 1 {
				top := LookEntry{Name: name, Dir: isDir, Link: isLink}
				if p := escapeGlob(name); isDir && validPattern(p+"/*") {
					top.Pattern = p + "/*"
				} else if !isDir && validPattern(p) && !strings.Contains(p, "/") {
					top.Pattern = p
				}
				l.top = append(l.top, top)
				counters = append(append([]*int64{}, add...), &l.top[len(l.top)-1].Size)
			}
			if !isLink {
				if isDir && l.docker != "" && childRel == l.docker {
					if o := l.offer(escapeGlob(childRel)+"/*", "docker", childRel); o != nil {
						counters = append(append([]*int64{}, counters...), &o.Size)
					}
				} else if kind, ok := lookFolderKinds[strings.ToLower(name)]; ok && isDir {
					if o := l.offer(escapeGlob(name), kind, childRel); o != nil {
						counters = append(append([]*int64{}, counters...), &o.Size)
					}
				} else if isDir && strings.HasPrefix(name, ".Trash-") {
					if o := l.offer(".Trash-*", "trash", childRel); o != nil {
						counters = append(append([]*int64{}, counters...), &o.Size)
					}
				} else if !isDir {
					for _, pattern := range lookFilePatterns {
						if ok, _ := path.Match(pattern, strings.ToLower(name)); ok {
							if o := l.offer(pattern, "temporary", childRel); o != nil {
								counters = append(append([]*int64{}, counters...), &o.Size)
							}
							break
						}
					}
				}
			}
			if !isDir {
				if kind == unix.S_IFREG {
					for _, c := range counters {
						*c += st.Size
					}
				}
				continue
			}
			if depth >= lookMaxDepth {
				l.complete = false // what lies deeper is not looked at
				continue
			}
			lookBeforeOpen(l.root, childRel)
			child, errO := unix.Openat(dirfd, name, lookOpenFlags, 0)
			if errO != nil {
				// gone, no longer a folder, or a link now: not entered
				l.complete = false
				continue
			}
			// top entries are appended to while walking: the pointers into
			// l.top must stay valid, so it is never grown past its capacity
			l.walk(child, childRel, depth+1, counters)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				l.complete = false
			}
			return
		}
	}
}

// Look looks at one folder. err is a sentence for the administrator.
func Look(source string) (LookAnswer, error) {
	dir, err := cleanDirectory("The folder", source)
	if err != nil {
		return LookAnswer{}, err
	}
	real := hostPath(dir)
	rootfd, err := unix.Open(real, lookOpenFlags, 0)
	switch {
	case errors.Is(err, unix.ELOOP):
		return LookAnswer{}, errors.New(dir + " is a link to another place: back up the folder it points to instead")
	case errors.Is(err, unix.ENOTDIR):
		return LookAnswer{}, errors.New(dir + " is not a folder")
	case err != nil:
		return LookAnswer{}, errors.New("The folder " + dir + " does not exist or cannot be read")
	}
	l := &looker{root: real, deadline: time.Now().Add(lookMaxTime), complete: true, byKey: map[string]*LookOffer{}}
	// the top entries are kept in place: capacity for every one that can come
	l.top = make([]LookEntry, 0, lookMaxEntries+1)
	if root := dockerRoot(); root != "" {
		if clean := filepath.Clean(root); inside(clean, dir) {
			l.docker = strings.TrimPrefix(clean, strings.TrimSuffix(dir, "/")+"/")
		}
	}
	l.walk(rootfd, "", 1, nil)
	if l.docker != "" && l.byKey[escapeGlob(l.docker)+"/*"] == nil {
		// deeper than the look went: offered all the same, its size not known
		if o := l.offer(escapeGlob(l.docker)+"/*", "docker", l.docker); o != nil {
			o.Count = 0
		}
	}

	answer := LookAnswer{Source: dir, Complete: l.complete, Entries: l.entries, Top: []LookEntry{}, Offers: []LookOffer{}}
	top := append([]LookEntry{}, l.top...)
	sort.SliceStable(top, func(i, j int) bool { return top[i].Size > top[j].Size })
	if len(top) > lookMaxTop {
		top = top[:lookMaxTop]
	}
	for _, entry := range top {
		entry.Name = shownName(entry.Name)
		answer.Top = append(answer.Top, entry)
	}
	order := map[string]int{"docker": 0, "cache": 1, "thumbnails": 2, "temporary": 3, "trash": 4}
	offers := l.offers
	sort.SliceStable(offers, func(i, j int) bool {
		if order[offers[i].Kind] != order[offers[j].Kind] {
			return order[offers[i].Kind] < order[offers[j].Kind]
		}
		return offers[i].Size > offers[j].Size
	})
	for _, o := range offers {
		found := []string{}
		for _, rel := range o.Found {
			found = append(found, shownName(rel))
		}
		o.Found = found
		answer.Offers = append(answer.Offers, *o)
	}
	return answer, nil
}

// lookBeforeOpen runs between the look at a folder and its opening; the tests
// swap the folder for a FIFO or a link there.
var lookBeforeOpen = func(root string, rel string) {}

// lookRun is the look the route runs; replaced by the tests.
var lookRun = Look

// lookGrace is how much longer than its own bound the route waits for a look:
// the bound is checked between entries, and one system call can hang (a
// network share, a FUSE mount that does not answer).
const lookGrace = time.Second

// lookAbandoned is set while a look the route gave up on is still running:
// its goroutine is blocked in the kernel and ends when the disk answers. At
// most one such look exists, since no new one starts while it is set; the
// route's own lock (lookBusy) is never held longer than lookMaxTime+lookGrace.
var lookAbandoned atomic.Bool

// errLookFailed is what a look that panicked answers.
var errLookFailed = errors.New("the look failed")

// lookWithin runs Look in a goroutine and waits at most wait for it. done is
// false when it gave up; the look then marks itself abandoned until it ends.
func lookWithin(source string, wait time.Duration) (LookAnswer, error, bool) {
	type result struct {
		answer LookAnswer
		err    error
	}
	results := make(chan result, 1)
	var mu sync.Mutex
	finished, gaveUp := false, false
	go func() {
		var r result
		func() {
			// a panic here would end Cosmos (net/http recovers only its own goroutine)
			defer func() {
				if recovered := recover(); recovered != nil {
					utils.Error("Snapshots: the look into a folder failed", fmt.Errorf("%v", recovered))
					r = result{err: errLookFailed}
				}
			}()
			r.answer, r.err = lookRun(source)
		}()
		results <- r
		mu.Lock()
		finished = true
		if gaveUp {
			lookAbandoned.Store(false)
		}
		mu.Unlock()
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-results:
		return r.answer, r.err, true
	case <-timer.C:
		mu.Lock()
		if finished {
			mu.Unlock()
			r := <-results
			return r.answer, r.err, true
		}
		gaveUp = true
		lookAbandoned.Store(true)
		mu.Unlock()
		return LookAnswer{}, nil, false
	}
}

type lookRequest struct {
	Source string `json:"source"`
}

// LookRoute godoc
// @Summary Incremental backup: what a folder holds that is worth leaving out
// @Description Looks at a folder (bounded, links not followed) and lists its top entries with their sizes and the caches, thumbnails, temporary files, trash and Docker data found in it, each with the exclusion pattern that leaves it out. Changes nothing.
// @Tags snapshots
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/snapshots/look [post]
func LookRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body lookRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, lookMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "SN010")
		return
	}
	if !lookBusy.TryLock() {
		utils.HTTPError(w, "Another folder is being looked at: wait a moment", http.StatusTooManyRequests, "SN011")
		return
	}
	defer lookBusy.Unlock()
	if lookAbandoned.Load() {
		utils.HTTPError(w, "The last look is still waiting for a disk that does not answer (a network share or a hung mount?): try again later, or restart Cosmos", http.StatusTooManyRequests, "SN013")
		return
	}
	answer, err, done := lookWithin(body.Source, lookMaxTime+lookGrace)
	if !done {
		utils.HTTPError(w, "The folder did not answer in time: a disk or a network share below it may be hanging. Nothing was changed", http.StatusGatewayTimeout, "SN014")
		return
	}
	if errors.Is(err, errLookFailed) {
		utils.HTTPError(w, "The look into the folder failed: nothing was changed", http.StatusInternalServerError, "SN015")
		return
	}
	if err != nil {
		utils.HTTPError(w, sentence(err.Error()), http.StatusBadRequest, "SN012")
		return
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: answer})
}
