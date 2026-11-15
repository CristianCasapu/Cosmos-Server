package anyimage

// The tags of a repository, bounded, and the one to propose.

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxTags     = 2000
	maxTagPages = 2
	tagLimit    = 1 << 20
)

// hubAPI is Docker Hub's API (replaced by the tests).
var hubAPI = "https://hub.docker.com"

// TagList is what the tag picker shows.
type TagList struct {
	List []string `json:"list"`
	// Suggested is the tag proposed; Why: "pinMajor", "newest", "latest" or "" (none known)
	Suggested string `json:"suggested"`
	Why       string `json:"why"`
	// More: the registry has more tags than were read
	More bool `json:"more"`
}

var linkNext = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="?next"?`)

// listTags reads the tags: Docker Hub's API (newest first), else the distribution API.
func listTags(ctx context.Context, ref Reference) ([]string, bool) {
	tags := []string{}
	more := false
	if ref.Registry == hubRegistry {
		parts := strings.SplitN(ref.Repository, "/", 2)
		if len(parts) != 2 {
			return tags, false
		}
		next := hubAPI + "/v2/namespaces/" + url.PathEscape(parts[0]) + "/repositories/" + url.PathEscape(parts[1]) + "/tags?page_size=100&ordering=last_updated"
		for page := 0; page < maxTagPages && next != ""; page++ {
			var answer struct {
				Next    string `json:"next"`
				Results []struct {
					Name string `json:"name"`
				} `json:"results"`
			}
			if getJSON(ctx, next, tagLimit, &answer) != nil {
				break
			}
			for _, r := range answer.Results {
				if tagPattern.MatchString(r.Name) && len(tags) < maxTags {
					tags = append(tags, r.Name)
				}
			}
			next = ""
			if answer.Next != "" {
				if u, err := url.Parse(answer.Next); err == nil && u.Scheme == "https" && strings.HasPrefix(answer.Next, hubAPI+"/") {
					next = answer.Next
				}
			}
		}
		return tags, next != ""
	}
	client := &registryClient{ref: ref}
	path := "/v2/" + ref.Repository + "/tags/list?n=1000"
	for page := 0; page < maxTagPages && path != ""; page++ {
		res, data, err := client.do(ctx, http.MethodGet, path, []string{"application/json"}, tagLimit)
		if err != nil {
			break
		}
		var answer struct {
			Tags []string `json:"tags"`
		}
		if jsonUnmarshal(data, &answer) != nil {
			break
		}
		for _, tag := range answer.Tags {
			if tagPattern.MatchString(tag) && len(tags) < maxTags {
				tags = append(tags, tag)
			}
		}
		path = ""
		if m := linkNext.FindStringSubmatch(res.Header.Get("Link")); m != nil {
			// only a path on the same registry is followed
			if u, err := url.Parse(m[1]); err == nil && u.Host == "" && strings.HasPrefix(u.Path, "/v2/") {
				path = u.RequestURI()
			}
		}
	}
	more = path != "" || len(tags) >= maxTags
	return tags, more
}

var versionTag = regexp.MustCompile(`^v?(\d{1,6})(?:\.(\d{1,6}))?(?:\.(\d{1,6}))?$`)

type version struct {
	tag   string
	parts []int
}

func parseVersion(tag, suffix string) (version, bool) {
	if suffix != "" {
		if !strings.HasSuffix(tag, suffix) {
			return version{}, false
		}
		tag = strings.TrimSuffix(tag, suffix)
	}
	m := versionTag.FindStringSubmatch(tag)
	if m == nil {
		return version{}, false
	}
	v := version{}
	for _, part := range m[1:] {
		if part == "" {
			break
		}
		n, _ := strconv.Atoi(part)
		v.parts = append(v.parts, n)
	}
	return v, true
}

func newer(a, b version) bool {
	for i := 0; i < 3; i++ {
		x, y := -1, -1
		if i < len(a.parts) {
			x = a.parts[i]
		}
		if i < len(b.parts) {
			y = b.parts[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// suggestTag proposes a tag: for a recipe that pins the major, the newest
// major's own tag; otherwise the newest full version; "latest" when that is
// all there is. suffix is a recipe's variant ("-management").
func suggestTag(tags []string, pinMajor bool, suffix string) (string, string) {
	set := map[string]bool{}
	var best version
	found := false
	for _, tag := range tags {
		set[tag] = true
		v, ok := parseVersion(tag, suffix)
		if !ok {
			continue
		}
		if !found || newer(v, best) || (!newer(best, v) && len(v.parts) > len(best.parts)) {
			best = v
			best.tag = tag
			found = true
		}
	}
	if !found {
		if set["latest"] || len(tags) == 0 {
			return "latest", "latest"
		}
		return "", ""
	}
	if pinMajor {
		prefix := ""
		if strings.HasPrefix(best.tag, "v") {
			prefix = "v"
		}
		major := prefix + strconv.Itoa(best.parts[0]) + suffix
		if set[major] {
			return major, "pinMajor"
		}
	}
	return best.tag, "newest"
}

// sortTags puts version tags newest first, then the others as they came.
func sortTags(tags []string) []string {
	versions := []version{}
	others := []string{}
	for _, tag := range tags {
		if v, ok := parseVersion(tag, ""); ok {
			v.tag = tag
			versions = append(versions, v)
		} else {
			others = append(others, tag)
		}
	}
	sort.SliceStable(versions, func(i, j int) bool { return newer(versions[i], versions[j]) })
	out := make([]string, 0, len(tags))
	for _, v := range versions {
		out = append(out, v.tag)
	}
	return append(out, others...)
}
