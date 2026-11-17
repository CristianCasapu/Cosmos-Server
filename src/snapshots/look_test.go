package snapshots

// Fork addition: tests of look.go.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func offerOf(answer LookAnswer, pattern string) *LookOffer {
	for i := range answer.Offers {
		if answer.Offers[i].Pattern == pattern {
			return &answer.Offers[i]
		}
	}
	return nil
}

func withDockerRoot(t *testing.T, root string) {
	t.Helper()
	old := dockerRoot
	dockerRoot = func() string { return root }
	t.Cleanup(func() { dockerRoot = old })
}

func TestLookOffersWhatIsInTheFolder(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "jellyfin", "cache", "a.bin"), 3000)
	writeFile(t, filepath.Join(src, "jellyfin", "metadata", "thumbnails", "b.jpg"), 200)
	writeFile(t, filepath.Join(src, "jellyfin", "transcodes", "c.ts"), 500)
	writeFile(t, filepath.Join(src, "photos", "Cache", "x"), 10)
	writeFile(t, filepath.Join(src, "downloads", "film.mkv.part"), 70)
	writeFile(t, filepath.Join(src, ".Trash-1000", "files", "old"), 40)
	writeFile(t, filepath.Join(src, "docker", "overlay2", "l"), 900)
	writeFile(t, filepath.Join(src, "keep", "file*[1].txt"), 5)
	withDockerRoot(t, filepath.Join(src, "docker"))

	answer, err := Look(src)
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Complete || answer.Source != src {
		t.Errorf("%+v", answer)
	}
	for pattern, want := range map[string]struct {
		kind string
		size int64
	}{
		"cache":      {"cache", 3000},
		"Cache":      {"cache", 10},
		"thumbnails": {"thumbnails", 200},
		"transcodes": {"temporary", 500},
		"*.part":     {"temporary", 70},
		".Trash-*":   {"trash", 40},
		"docker/*":   {"docker", 900},
	} {
		o := offerOf(answer, pattern)
		if o == nil || o.Kind != want.kind || o.Size != want.size || len(o.Found) == 0 {
			t.Errorf("%s: %+v", pattern, o)
		}
	}
	if o := offerOf(answer, "cache"); o.Found[0] != "jellyfin/cache" {
		t.Errorf("found %v", o.Found)
	}
	if answer.Offers[0].Kind != "docker" {
		t.Errorf("Docker's folder first: %+v", answer.Offers[0])
	}
	for _, o := range answer.Offers {
		if _, err := cleanExclude("", o.Pattern); err != nil {
			t.Errorf("%s does not pass the save check: %v", o.Pattern, err)
		}
	}
	sizes := map[string]int64{}
	for _, e := range answer.Top {
		sizes[e.Name] = e.Size
		if e.Pattern != "" {
			if _, err := cleanExclude("", e.Pattern); err != nil {
				t.Errorf("top %s: %v", e.Pattern, err)
			}
		}
	}
	if sizes["jellyfin"] != 3700 || sizes["docker"] != 900 || answer.Top[0].Name != "jellyfin" {
		t.Errorf("top: %+v", answer.Top)
	}
	// the pattern of a name with glob characters matches that name only
	wantPattern := `keep/*`
	found := false
	for _, e := range answer.Top {
		found = found || e.Pattern == wantPattern
	}
	if !found {
		t.Errorf("no pattern for keep: %+v", answer.Top)
	}
}

func TestLookNeverFollowsALink(t *testing.T) {
	big := t.TempDir()
	for i := 0; i < 50; i++ {
		writeFile(t, filepath.Join(big, "cache", "f"+strings.Repeat("x", i)), 1000)
	}
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "small"), 1)
	if err := os.Symlink(big, filepath.Join(src, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	withDockerRoot(t, "")
	answer, err := Look(src)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Entries != 2 || offerOf(answer, "cache") != nil {
		t.Errorf("the link was followed: %+v", answer)
	}
	for _, e := range answer.Top {
		if e.Name == "elsewhere" && (!e.Link || e.Dir || e.Size != 0) {
			t.Errorf("link entry %+v", e)
		}
	}
	// a link as the folder itself is refused
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(big, link)
	if _, err := Look(link); err == nil || !strings.Contains(err.Error(), "link") {
		t.Errorf("a link as source: %v", err)
	}
	for _, bad := range []string{"relative", "/proc", "/", src + "/../x", src + "/nothing-here"} {
		if _, err := Look(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestLookIsBounded(t *testing.T) {
	src := t.TempDir()
	for i := 0; i < 30; i++ {
		writeFile(t, filepath.Join(src, "many", "f"+strings.Repeat("y", i)), 1)
	}
	writeFile(t, filepath.Join(src, "a", "b", "c", "d", "e", "cache", "deep"), 1)
	withDockerRoot(t, "")

	oldEntries, oldDepth, oldTime := lookMaxEntries, lookMaxDepth, lookMaxTime
	t.Cleanup(func() { lookMaxEntries, lookMaxDepth, lookMaxTime = oldEntries, oldDepth, oldTime })

	lookMaxEntries = 10
	answer, _ := Look(src)
	if answer.Complete || answer.Entries > 10 {
		t.Errorf("entries: %d complete %v", answer.Entries, answer.Complete)
	}

	lookMaxEntries = oldEntries
	answer, _ = Look(src)
	if answer.Complete || offerOf(answer, "cache") != nil {
		t.Errorf("depth: the cache five levels down was looked at: %+v", answer.Offers)
	}
	lookMaxDepth = 7
	answer, _ = Look(src)
	if o := offerOf(answer, "cache"); !answer.Complete || o == nil || o.Size != 1 {
		t.Errorf("depth 7: %+v", answer)
	}

	lookMaxTime = -time.Second
	answer, _ = Look(src)
	if answer.Complete || answer.Entries != 0 {
		t.Errorf("time: %+v", answer)
	}
}

func TestLookDockerRootDeeperThanTheLook(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "a", "b", "c", "d", "docker"), 0o755)
	withDockerRoot(t, filepath.Join(src, "a", "b", "c", "d", "docker"))
	answer, _ := Look(src)
	if o := offerOf(answer, "a/b/c/d/docker/*"); o == nil || o.Kind != "docker" {
		t.Errorf("%+v", answer.Offers)
	}
	// Docker's folder outside the source: nothing offered
	withDockerRoot(t, "/var/lib/docker")
	answer, _ = Look(src)
	for _, o := range answer.Offers {
		if o.Kind == "docker" {
			t.Errorf("%+v", o)
		}
	}
}

func TestLookRoute(t *testing.T) {
	src := t.TempDir()
	withDockerRoot(t, "")
	look := func(method, body string) int {
		return call(LookRoute, apiRequest(method, "/api/snapshots/look", "sudo", body)).Code
	}
	if code := look("POST", `{"source":"`+src+`"}`); code != http.StatusOK {
		t.Errorf("look: %d", code)
	}
	for _, who := range []string{"none", "reader"} {
		if rec := call(LookRoute, apiRequest("POST", "/api/snapshots/look", who, `{"source":"`+src+`"}`)); rec.Code == http.StatusOK {
			t.Errorf("%s may look", who)
		}
	}
	if code := look("GET", ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", code)
	}
	if code := look("POST", `{"source":"`+src+`","x":1}`); code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", code)
	}
	if code := look("POST", `{"source":"`+strings.Repeat("a", 5000)+`"}`); code != http.StatusBadRequest {
		t.Errorf("big body: %d", code)
	}
	lookBusy.Lock()
	code := look("POST", `{"source":"`+src+`"}`)
	lookBusy.Unlock()
	if code != http.StatusTooManyRequests {
		t.Errorf("while busy: %d", code)
	}
}
