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
	user := principal(req)
	if !searchUsers.start(user, time.Now()) {
		answer(w, map[string]interface{}{"results": []Result{}, "problem": "busy"})
		return
	}
	defer searchUsers.done(user)
	ctx, cancel := context.WithTimeout(req.Context(), 12*time.Second)
	defer cancel()
	results, err := search(ctx, query)
	if err != nil {
		answer(w, map[string]interface{}{"results": []Result{}, "problem": codeOf(err)})
		return
	}
	answer(w, map[string]interface{}{"results": results})
}

// principal names who asks, for the per-user bounds: the user, or the API token.
func principal(req *http.Request) string {
	ctx := utils.GetAuthContext(req)
	if ctx.APIToken != nil {
		return "token:" + ctx.APIToken.Name
	}
	return "user:" + ctx.Nickname
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

// typedTag tells whether the text names a tag or a digest itself.
func typedTag(text string) bool {
	return strings.Contains(text, "@") || strings.LastIndex(text, ":") > strings.LastIndex(text, "/")
}

// tagsFor lists the tags and the proposal for a reference.
func tagsFor(ctx context.Context, ref Reference, recipe *Recipe) TagList {
	tags, more := listTags(ctx, ref)
	pin := recipe != nil && recipe.Pin == "major"
	suffix := ""
	if recipe != nil {
		suffix = recipe.TagSuffix
	}
	list := sortTags(tags)
	if len(list) > 300 {
		list, more = list[:300], true
	}
	answer := TagList{List: list, More: more}
	answer.Suggested, answer.Why = suggestTag(tags, pin, suffix)
	if recipe != nil {
		for _, preferred := range recipe.Prefer {
			for _, tag := range tags {
				if tag == preferred {
					answer.Suggested, answer.Why = preferred, "prefer"
				}
			}
		}
	}
	return answer
}

// Describe is what the describe route answers for a reference. A name
// without a tag is described at the tag Cosmos proposes (the newest major
// of a database rather than "latest"), so that the image is read once.
func Describe(ctx context.Context, text string, user string) Description {
	text = strings.TrimSpace(text)
	ref, err := Parse(text, loggedIn)
	if err != nil {
		return Description{Reference: clean(text, 255), Problem: codeOf(err), Platforms: []string{}, Tags: TagList{List: []string{}}}
	}
	recipe := recipeFor(ref)
	// the publisher's description, read alongside the image; with a recipe only
	// from the cache (the recipe says more, and a read is a call of Docker Hub's API)
	hints := make(chan *Requirements, 1)
	go func(ref Reference) {
		hintCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		hints <- descriptionHints(hintCtx, ref, recipe == nil)
	}(ref)
	tags := TagList{List: []string{}}
	tagsRead := false
	if !typedTag(text) {
		tags, tagsRead = tagsFor(ctx, ref, recipe), true
		if tags.Suggested != "" {
			ref.Tag = tags.Suggested
		}
	}
	desc := describeImage(ctx, ref, serverPlatform(), user)
	desc.Recipe = recipe
	desc.Market = marketFor(ref)
	desc.TagTyped = typedTag(text)
	if !tagsRead && (desc.Problem == "" || desc.Problem == "notFound") {
		tags = tagsFor(ctx, ref, recipe)
	}
	desc.Tags = tags
	desc.Databases = databaseRecipes()
	desc.Managers = managerRecipes() // plan 45
	desc.Hints = withDocs(<-hints, desc.Declared)
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
	user := principal(req)
	if !describeUsers.start(user, time.Now()) {
		answer(w, Description{Reference: clean(body.Reference, 255), Problem: "busy", Platforms: []string{}, Tags: TagList{List: []string{}}})
		return
	}
	defer describeUsers.done(user)
	if !describeWindow.start(time.Now()) {
		// Cosmos' own bound, not the registry's
		answer(w, Description{Reference: clean(body.Reference, 255), Problem: "busy", Platforms: []string{}, Tags: TagList{List: []string{}}})
		return
	}
	defer describeWindow.done()
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	answer(w, Describe(ctx, body.Reference, user))
}
