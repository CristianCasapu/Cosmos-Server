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
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

// walk reads one folder; sizes go to every counter in add (the top entry and
// the offers that hold this folder).
func (l *looker) walk(dir string, rel string, depth int, add []*int64) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	for {
		if l.stopped() {
			return
		}
		list, err := f.ReadDir(lookBatch)
		for _, entry := range list {
			if l.stopped() {
				return
			}
			l.entries++
			name := entry.Name()
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			info, errI := entry.Info() // lstat: a link is the link itself
			if errI != nil {
				continue
			}
			isLink := info.Mode()&os.ModeSymlink != 0
			isDir := info.IsDir() && !isLink
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
				if info.Mode().IsRegular() {
					for _, c := range counters {
						*c += info.Size()
					}
				}
				continue
			}
			if depth >= lookMaxDepth {
				l.complete = false // what lies deeper is not looked at
				continue
			}
			// top entries are appended to while walking: the pointers into
			// l.top must stay valid, so it is never grown past its capacity
			l.walk(filepath.Join(dir, name), childRel, depth+1, counters)
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
	info, err := os.Lstat(real)
	if err != nil {
		return LookAnswer{}, errors.New("The folder " + dir + " does not exist or cannot be read")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return LookAnswer{}, errors.New(dir + " is a link to another place: back up the folder it points to instead")
	}
	if !info.IsDir() {
		return LookAnswer{}, errors.New(dir + " is not a folder")
	}
	l := &looker{root: real, deadline: time.Now().Add(lookMaxTime), complete: true, byKey: map[string]*LookOffer{}}
	// the top entries are kept in place: capacity for every one that can come
	l.top = make([]LookEntry, 0, lookMaxEntries+1)
	if root := dockerRoot(); root != "" {
		if clean := filepath.Clean(root); inside(clean, dir) {
			l.docker = strings.TrimPrefix(clean, strings.TrimSuffix(dir, "/")+"/")
		}
	}
	l.walk(real, "", 1, nil)
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
	answer, err := Look(body.Source)
	if err != nil {
		utils.HTTPError(w, sentence(err.Error()), http.StatusBadRequest, "SN012")
		return
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: answer})
}
