package anyimage

// The two routes, both POST and for administrators with the resources
// permission (the permission of creating a container):
//
//   POST /cosmos/api/images/search   {"query": "postgres"}
//   POST /cosmos/api/images/describe {"reference": "postgres:18"}
//
// Bodies of at most 1 KiB, unknown fields refused, everything that reaches
// out rate-limited. Answers carry codes, never prose to render.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const maxBody = 1 << 10

func decode(w http.ResponseWriter, req *http.Request, into interface{}) bool {
	if req.Method != http.MethodPost {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "IS001")
		return false
	}
	return true
}

func answer(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

// SearchRoute answers POST /cosmos/api/images/search.
func SearchRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if !decode(w, req, &body) {
		return
	}
	query := NormalQuery(body.Query)
	if query == "" {
		utils.HTTPError(w, "Invalid search", http.StatusBadRequest, "IS002")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 12*time.Second)
	defer cancel()
	results, err := search(ctx, query)
	if err != nil {
		answer(w, map[string]interface{}{"results": []Result{}, "problem": codeOf(err)})
		return
	}
	answer(w, map[string]interface{}{"results": results})
}

// serverPlatform is the platform the daemon runs (linux/amd64 when it does not answer).
var serverPlatform = func() Platform {
	server, _ := docker.CheckImagePlatforms(nil)
	if server.Arch == "" {
		return Platform{OS: "linux", Arch: "amd64"}
	}
	if server.OS == "" {
		server.OS = "linux"
	}
	return Platform{OS: server.OS, Arch: server.Arch}
}

// Describe is what the describe route answers for a reference.
func Describe(ctx context.Context, text string) Description {
	ref, err := Parse(strings.TrimSpace(text), loggedIn)
	if err != nil {
		return Description{Reference: clean(text, 255), Problem: codeOf(err), Platforms: []string{}, Tags: TagList{List: []string{}}}
	}
	desc := describeImage(ctx, ref, serverPlatform())
	desc.Recipe = recipeFor(ref)
	desc.Market = marketFor(ref)
	if desc.Problem == "" || desc.Problem == "notFound" {
		// the tags help when the tag typed does not exist
		tags, more := listTags(ctx, ref)
		pin := desc.Recipe != nil && desc.Recipe.Pin == "major"
		suffix := ""
		if desc.Recipe != nil {
			suffix = desc.Recipe.TagSuffix
		}
		list := sortTags(tags)
		if len(list) > 300 {
			list, more = list[:300], true
		}
		desc.Tags = TagList{List: list, More: more}
		desc.Tags.Suggested, desc.Tags.Why = suggestTag(tags, pin, suffix)
		if desc.Recipe != nil {
			for _, preferred := range desc.Recipe.Prefer {
				for _, tag := range tags {
					if tag == preferred {
						desc.Tags.Suggested, desc.Tags.Why = preferred, "prefer"
					}
				}
			}
		}
	}
	if desc.Tags.List == nil {
		desc.Tags.List = []string{}
	}
	return desc
}

// DescribeRoute answers POST /cosmos/api/images/describe.
func DescribeRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	var body struct {
		Reference string `json:"reference"`
	}
	if !decode(w, req, &body) {
		return
	}
	if len(body.Reference) == 0 || len(body.Reference) > 255 {
		utils.HTTPError(w, "Invalid image name", http.StatusBadRequest, "IS003")
		return
	}
	if !describeWindow.start(time.Now()) {
		answer(w, Description{Reference: clean(body.Reference, 255), Problem: "rateLimited", Platforms: []string{}, Tags: TagList{List: []string{}}})
		return
	}
	defer describeWindow.done()
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	answer(w, Describe(ctx, body.Reference))
}
