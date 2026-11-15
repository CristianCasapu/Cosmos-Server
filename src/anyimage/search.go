package anyimage

// Searching Docker Hub: the query is the only thing sent out (no cookie, no
// credential: Docker Hub's search needs none), answers cached, the number of
// searches the server makes bounded.

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var jsonUnmarshal = json.Unmarshal

var queryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,63}$`)

// Result is one image found.
type Result struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Official    bool   `json:"official"`
	Stars       int64  `json:"stars"`
	Pulls       int64  `json:"pulls"`
	// Recipe: Cosmos has settings of its own for this image
	Recipe string `json:"recipe,omitempty"`
}

// window allows n events per minute and k at a time.
type window struct {
	mu      sync.Mutex
	times   []time.Time
	running int
	perMin  int
	atOnce  int
}

func (w *window) start(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.times[:0]
	for _, t := range w.times {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	w.times = kept
	if len(w.times) >= w.perMin || w.running >= w.atOnce {
		return false
	}
	w.times = append(w.times, now)
	w.running++
	return true
}

func (w *window) done() {
	w.mu.Lock()
	w.running--
	w.mu.Unlock()
}

var (
	searchWindow   = &window{perMin: 20, atOnce: 2}
	describeWindow = &window{perMin: describePerMinute, atOnce: 3}

	searchMu    sync.Mutex
	searchCache = map[string]struct {
		at      time.Time
		results []Result
	}{}
)

// NormalQuery is the query as it is sent, or "" when it is not one.
func NormalQuery(text string) string {
	q := strings.ToLower(strings.TrimSpace(text))
	if !queryPattern.MatchString(q) {
		return ""
	}
	return q
}

// search asks Docker Hub; the official images first, then by pulls.
func search(ctx context.Context, query string) ([]Result, error) {
	searchMu.Lock()
	if entry, ok := searchCache[query]; ok && time.Since(entry.at) < 10*time.Minute {
		searchMu.Unlock()
		return entry.results, nil
	}
	searchMu.Unlock()
	if !searchWindow.start(time.Now()) {
		return nil, errBusy
	}
	defer searchWindow.done()

	var answer struct {
		Results []struct {
			RepoName         string `json:"repo_name"`
			ShortDescription string `json:"short_description"`
			StarCount        int64  `json:"star_count"`
			PullCount        int64  `json:"pull_count"`
			IsOfficial       bool   `json:"is_official"`
		} `json:"results"`
	}
	address := hubAPI + "/v2/search/repositories/?page_size=25&query=" + url.QueryEscape(query)
	if err := getJSON(ctx, address, 512<<10, &answer); err != nil {
		return nil, err
	}
	results := []Result{}
	for _, r := range answer.Results {
		if len(results) >= 25 {
			break
		}
		ref, err := Parse(r.RepoName, nil)
		if err != nil || ref.Registry != hubRegistry {
			continue
		}
		result := Result{
			Name: ref.Name(), Description: clean(r.ShortDescription, 300),
			Official: r.IsOfficial && strings.HasPrefix(ref.Repository, "library/"),
			Stars:    r.StarCount, Pulls: r.PullCount,
		}
		if recipe := recipeFor(ref); recipe != nil {
			result.Recipe = recipe.ID
		}
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Official != results[j].Official {
			return results[i].Official
		}
		return results[i].Pulls > results[j].Pulls
	})
	searchMu.Lock()
	if len(searchCache) > 128 {
		searchCache = map[string]struct {
			at      time.Time
			results []Result
		}{}
	}
	searchCache[query] = struct {
		at      time.Time
		results []Result
	}{time.Now(), results}
	searchMu.Unlock()
	return results, nil
}
