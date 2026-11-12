package snapshots

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// apiRequest builds a request from nobody ("none"), from someone who may only
// read the configuration ("reader"), from an admin who has not confirmed
// their password ("admin") or from one who has ("sudo").
func apiRequest(method string, path string, who string, body string) *http.Request {
	req := httptest.NewRequest(method, "/cosmos"+path, strings.NewReader(body))
	if who != "none" {
		auth := &utils.AuthContext{
			Nickname:    "someone",
			Permissions: []utils.Permission{utils.PERM_CONFIGURATION_READ},
			IsSudoed:    who == "sudo",
		}
		if who != "reader" {
			auth.Permissions = append(auth.Permissions, utils.PERM_ADMIN)
		}
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	return req
}

func call(handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func waitIdle(t *testing.T) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if !GetStatus().Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("still running after ten seconds")
}

func TestRoutesNeedASessionAndTheRightMethod(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	useConfig(t, cfg)

	routes := []struct {
		path    string
		handler http.HandlerFunc
		method  string
	}{
		{"/api/snapshots", StatusRoute, "GET"},
		{"/api/snapshots/run", RunRoute, "POST"},
		{"/api/snapshots/restore", RestoreRoute, "POST"},
	}
	for _, route := range routes {
		// nobody: refused whatever the method, before anything is read or done
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			if rec := call(route.handler, apiRequest(method, route.path, "none", `{}`)); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session: status %d, want 401", method, route.path, rec.Code)
			}
		}
		// the other methods are not served
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"} {
			if method == route.method {
				continue
			}
			if rec := call(route.handler, apiRequest(method, route.path, "sudo", `{}`)); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, route.path, rec.Code)
			}
		}
	}

	// starting a run or a restore takes an admin who confirmed their password
	for _, route := range routes[1:] {
		for _, who := range []string{"reader", "admin"} {
			if rec := call(route.handler, apiRequest("POST", route.path, who, `{}`)); rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s as %s: status %d, want 401", route.path, who, rec.Code)
			}
		}
	}
	if entries, _ := os.ReadDir(cfg.Destination); len(entries) != 0 {
		t.Errorf("a refused request made a snapshot: %v", entries)
	}
	if GetStatus().Running {
		t.Error("a refused request started something")
	}

	// reading the status does not
	for _, who := range []string{"reader", "admin", "sudo"} {
		if rec := call(StatusRoute, apiRequest("GET", "/api/snapshots", who, "")); rec.Code != http.StatusOK {
			t.Errorf("GET /api/snapshots as %s: status %d: %s", who, rec.Code, rec.Body.String())
		}
	}
}

func TestStatusRoute(t *testing.T) {
	cfg, source := fixture(t)
	base := filepath.Dir(source)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	missing := filepath.Join(base, "missing")
	cfg.Sources = append(cfg.Sources, missing)

	// what the server proposes: its own folder, the apps' data, Docker's volumes
	own := filepath.Join(base, "cosmos")
	apps := filepath.Join(base, "apps")
	volumes := filepath.Join(base, "docker", "volumes")
	for _, dir := range []string{own, apps, volumes} {
		os.MkdirAll(dir, 0755)
	}
	realFolder, realDocker := utils.CONFIGFOLDER, dockerRoot
	utils.CONFIGFOLDER = own + "/"
	dockerRoot = func() string { return filepath.Join(base, "docker") }
	defer func() { utils.CONFIGFOLDER, dockerRoot = realFolder, realDocker }()

	config := utils.Config{Snapshots: cfg}
	config.DockerConfig.DefaultDataPath = apps
	utils.LoadBaseMainConfig(config)
	defer utils.LoadBaseMainConfig(utils.Config{})

	if err := RunAll(); err != nil {
		t.Fatal(err)
	}

	rec := call(StatusRoute, apiRequest("GET", "/api/snapshots", "reader", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var reply struct {
		Status string
		Data   struct {
			Enabled     bool
			Destination string
			Running     bool
			Current     string
			StartedAt   *time.Time
			Sources     []struct {
				Path      string
				Slug      string
				Snapshots []Snapshot
				LastError string
			}
			Suggested []string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	data := reply.Data
	if reply.Status != "OK" || !data.Enabled || data.Destination != cfg.Destination || data.Running || data.Current != "" || data.StartedAt != nil {
		t.Errorf("reply: %s", rec.Body.String())
	}
	if len(data.Sources) != 2 {
		t.Fatalf("folders: %s", rec.Body.String())
	}
	good, bad := data.Sources[0], data.Sources[1]
	if good.Path != source || good.Slug != Slug(source) || good.LastError != "" || len(good.Snapshots) != 1 {
		t.Errorf("the folder that exists: %+v", good)
	} else if s := good.Snapshots[0]; !validID(s.ID) || s.Files != 1 || s.BytesCopied != int64(len("content")) || s.BytesLinked != 0 || s.Errors != 0 || s.Finished.IsZero() {
		t.Errorf("its snapshot: %+v", s)
	}
	if bad.Path != missing || len(bad.Snapshots) != 0 || bad.LastError == "" || bad.LastError[0] < 'A' || bad.LastError[0] > 'Z' {
		t.Errorf("the folder that does not exist: %+v", bad)
	}
	if want := []string{own, apps, volumes}; !reflect.DeepEqual(data.Suggested, want) {
		t.Errorf("suggested %q, want %q", data.Suggested, want)
	}

	// lists are lists even when empty, and nothing is suggested that does not exist
	utils.CONFIGFOLDER = filepath.Join(base, "nowhere") + "/"
	dockerRoot = func() string { return filepath.Join(base, "no-docker") }
	config = utils.Config{}
	config.DockerConfig.DefaultDataPath = filepath.Join(base, "no-apps")
	utils.LoadBaseMainConfig(config)
	rec = call(StatusRoute, apiRequest("GET", "/api/snapshots", "reader", ""))
	var raw struct{ Data map[string]json.RawMessage }
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, list := range []string{"sources", "suggested"} {
		if string(raw.Data[list]) != "[]" {
			t.Errorf("%s with nothing configured: %s", list, raw.Data[list])
		}
	}
	if string(raw.Data["enabled"]) != "false" || string(raw.Data["running"]) != "false" || string(raw.Data["destination"]) != `""` {
		t.Errorf("reply with nothing configured: %s", rec.Body.String())
	}

	// settings edited by hand into nonsense: reported, and no folder is read
	utils.LoadBaseMainConfig(utils.Config{Snapshots: utils.SnapshotsConfig{Enabled: true, Destination: "relative/../..", Sources: []string{source}}})
	rec = call(StatusRoute, apiRequest("GET", "/api/snapshots", "reader", ""))
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(raw.Data["error"]) < 4 || !strings.Contains(string(raw.Data["sources"]), `"snapshots":[]`) {
		t.Errorf("reply with unusable settings: %s", rec.Body.String())
	}
}

func TestRunRoute(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)

	// off or incomplete: 409 with the reason
	for _, off := range []utils.SnapshotsConfig{
		{},
		{Destination: cfg.Destination, Sources: cfg.Sources},
		{Enabled: true, Destination: cfg.Destination},
	} {
		useConfig(t, off)
		rec := call(RunRoute, apiRequest("POST", "/api/snapshots/run", "sudo", ""))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not enabled") {
			t.Errorf("run with %+v: status %d: %s", off, rec.Code, rec.Body.String())
		}
	}

	useConfig(t, cfg)

	// already running: 409
	runMu.Lock()
	rec := call(RunRoute, apiRequest("POST", "/api/snapshots/run", "sudo", ""))
	runMu.Unlock()
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already running") {
		t.Errorf("run while busy: status %d: %s", rec.Code, rec.Body.String())
	}
	if len(List(cfg.Destination, source)) != 0 {
		t.Fatal("a refused run made a snapshot")
	}

	// the body is not read: whatever is sent, the saved settings are what runs
	rec = call(RunRoute, apiRequest("POST", "/api/snapshots/run", "sudo", `{"Destination":"/etc","Sources":["/"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("run: status %d: %s", rec.Code, rec.Body.String())
	}
	waitIdle(t)
	if list := List(cfg.Destination, source); len(list) != 1 {
		t.Errorf("snapshots after the run: %+v", list)
	}
	if entries, _ := os.ReadDir(cfg.Destination); len(entries) != 1 {
		t.Errorf("the destination holds %v", entries)
	}
}

func TestRestoreRoute(t *testing.T) {
	cfg, source := fixture(t)
	base := filepath.Dir(source)
	write(t, filepath.Join(source, "file.txt"), "version 1", 0644)
	useConfig(t, cfg)
	first, _ := snap(t, cfg, source, day(12))
	id := filepath.Base(first)
	write(t, filepath.Join(source, "file.txt"), "version 2", 0644)

	body := func(source string, snapshot string) string {
		data, _ := json.Marshal(map[string]string{"Source": source, "Snapshot": snapshot})
		return string(data)
	}
	before := describe(t, base)

	invalid := map[string]string{
		"no body":                 ``,
		"not JSON":                `restore everything`,
		"an array":                `[]`,
		"wrong types":             `{"Source": 1, "Snapshot": true}`,
		"nothing named":           `{}`,
		"no snapshot":             body(source, ""),
		"no folder":               body("", id),
		"a folder not configured": body(base, id),
		"a folder with ..":        body(source+"/../data", id),
		"an id that leaves":       body(source, "../x"),
		"an absolute id":          body(source, "/etc/passwd"),
		"the full path as id":     body(source, first),
		"an id with a path":       body(source, id+"/../"+id),
		"a missing snapshot":      body(source, "2020-01-01_000000"),
		"a body far too large":    `{"Source": "` + strings.Repeat("a", 1<<20) + `", "Snapshot": "` + id + `"}`,
	}
	for name, payload := range invalid {
		rec := call(RestoreRoute, apiRequest("POST", "/api/snapshots/restore", "sudo", payload))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
		var reply utils.HTTPErrorResult
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.Status != "error" || reply.Message == "" {
			t.Errorf("%s: reply %s", name, rec.Body.String())
		}
	}

	// while something runs: 409
	runMu.Lock()
	rec := call(RestoreRoute, apiRequest("POST", "/api/snapshots/restore", "sudo", body(source, id)))
	runMu.Unlock()
	if rec.Code != http.StatusConflict {
		t.Errorf("restore while busy: status %d: %s", rec.Code, rec.Body.String())
	}

	if after := describe(t, base); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused request changed something:\nbefore %v\nafter  %v", before, after)
	}

	// and the real thing, in the background
	rec = call(RestoreRoute, apiRequest("POST", "/api/snapshots/restore", "sudo", body(source, id)))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status %d: %s", rec.Code, rec.Body.String())
	}
	waitIdle(t)
	if got := read(t, filepath.Join(source, "file.txt")); got != "version 1" {
		t.Errorf("after the restore: %q", got)
	}
	kept, _ := filepath.Glob(source + ".before-restore-*")
	if len(kept) != 1 || read(t, filepath.Join(kept[0], "file.txt")) != "version 2" {
		t.Errorf("version kept: %v", kept)
	}

	// the status tells how it went
	rec = call(StatusRoute, apiRequest("GET", "/api/snapshots", "reader", ""))
	var reply struct {
		Data struct {
			Running     bool
			LastRestore *RestoreResult
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if last := reply.Data.LastRestore; reply.Data.Running || last == nil || last.Source != source || last.Snapshot != id || last.Error != "" || last.KeptAs != kept[0] {
		t.Errorf("status after the restore: %s", rec.Body.String())
	}
}

func TestSentence(t *testing.T) {
	for text, want := range map[string]string{"": "", "a backup is running": "A backup is running", "Already": "Already", "/data: gone": "/data: gone", "élan": "Élan"} {
		if got := sentence(text); got != want {
			t.Errorf("sentence(%q) = %q, want %q", text, got, want)
		}
	}
}
