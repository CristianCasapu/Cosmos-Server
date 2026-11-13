package snapshots

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// keptFixture puts, next to the folder that is backed up, two folders that
// restores kept and a collection of things that only look like them. It
// returns the names of the two.
func keptFixture(t *testing.T, source string) (older string, newer string) {
	t.Helper()
	base := filepath.Dir(source)
	name := filepath.Base(source)
	older = name + ".before-restore-2026-11-10_030000"
	newer = name + ".before-restore-2026-11-12_120000"
	write(t, filepath.Join(base, older, "file.txt"), "older", 0644)
	write(t, filepath.Join(base, older, "sub", "deep.txt"), "older, deep", 0600)
	write(t, filepath.Join(base, newer, "file.txt"), "newer", 0644)

	// not folders
	write(t, filepath.Join(base, name+".before-restore-2026-11-11_030000"), "a file", 0644)
	write(t, filepath.Join(base, "elsewhere", "precious.txt"), "not ours", 0644)
	if err := os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(base, name+".before-restore-2026-11-09_030000")); err != nil {
		t.Fatal(err)
	}
	// folders with another name
	for _, other := range []string{
		name + ".before-restore-2026-11-10_030000.bak",
		name + ".before-restore-latest",
		name + ".before-restore-",
		name + ".before-restore-2026-11-10_03000",
		name + ".restoring-2026-11-10_030000",
		name + "2.before-restore-2026-11-10_030000",
		"x" + name + ".before-restore-2026-11-10_030000",
		"before-restore-2026-11-10_030000",
	} {
		write(t, filepath.Join(base, other, "keep.txt"), "mine", 0644)
	}
	return older, newer
}

func TestKeptFoldersAreListed(t *testing.T) {
	cfg, source := fixture(t)
	base := filepath.Dir(source)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	older, newer := keptFixture(t, source)
	// a second folder, with nothing kept; its name starts like the first one's
	second := filepath.Join(base, "data2")
	write(t, filepath.Join(second, "file.txt"), "second", 0644)
	os.RemoveAll(filepath.Join(base, "data2.before-restore-2026-11-10_030000"))
	cfg.Sources = append(cfg.Sources, second)
	useConfig(t, cfg)

	want := []KeptFolder{
		{Name: newer, Date: time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local)},
		{Name: older, Date: time.Date(2026, 11, 10, 3, 0, 0, 0, time.Local)},
	}
	if got := listKept(source); !reflect.DeepEqual(got, want) {
		t.Errorf("listKept: %+v, want %+v", got, want)
	}
	for _, none := range []string{second, filepath.Join(base, "missing"), "", "/"} {
		if got := listKept(none); got == nil || len(got) != 0 {
			t.Errorf("listKept(%q): %+v", none, got)
		}
	}

	rec := call(StatusRoute, apiRequest("GET", "/api/snapshots", "reader", ""))
	var reply struct {
		Data struct {
			Sources []struct {
				Path          string
				BeforeRestore []KeptFolder
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Data.Sources) != 2 {
		t.Fatalf("reply: %s", rec.Body.String())
	}
	listed := reply.Data.Sources[0].BeforeRestore
	if len(listed) != 2 || listed[0].Name != newer || listed[1].Name != older || !listed[0].Date.Equal(want[0].Date) {
		t.Errorf("kept folders in the status: %+v", listed)
	}
	if !strings.Contains(rec.Body.String(), `"beforeRestore":[]`) {
		t.Errorf("a folder with nothing kept does not list an empty list: %s", rec.Body.String())
	}

	// settings edited by hand into nonsense: nothing is looked for
	useConfig(t, utils.SnapshotsConfig{Enabled: true, Destination: "relative", Sources: []string{source}})
	rec = call(StatusRoute, apiRequest("GET", "/api/snapshots", "reader", ""))
	if strings.Contains(rec.Body.String(), older) || !strings.Contains(rec.Body.String(), `"beforeRestore":[]`) {
		t.Errorf("reply with unusable settings: %s", rec.Body.String())
	}
}

func TestRemoveKeptRefuses(t *testing.T) {
	cfg, source := fixture(t)
	base := filepath.Dir(source)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	write(t, filepath.Join(source, "sub", "deep.txt"), "deep", 0644)
	older, newer := keptFixture(t, source)
	snap(t, cfg, source, day(12))

	// a folder that is not configured, with a folder kept next to it
	other := filepath.Join(base, "other")
	write(t, filepath.Join(other, "file.txt"), "other", 0644)
	otherKept := "other.before-restore-2026-11-10_030000"
	write(t, filepath.Join(base, otherKept, "file.txt"), "other, kept", 0644)
	// and one kept inside the configured folder
	write(t, filepath.Join(source, "sub.before-restore-2026-11-10_030000", "file.txt"), "inside", 0644)

	useConfig(t, cfg)
	before := describe(t, base)

	cases := []struct{ name, source, kept string }{
		{"a folder that is not configured", other, otherKept},
		{"no folder", "", older},
		{"the folder with a trailing slash", source + "/", older},
		{"a folder inside the configured one", filepath.Join(source, "sub"), "sub.before-restore-2026-11-10_030000"},
		{"the folder above the configured one", base, filepath.Base(base) + ".before-restore-2026-11-10_030000"},
		{"a relative folder", "data", older},
		{"a folder with ..", source + "/../other", otherKept},
		{"a folder with .. that comes back", source + "/../data", older},
		{"the destination", cfg.Destination, "snapshots.before-restore-2026-11-10_030000"},
		{"the root", "/", older},
		{"no name", source, ""},
		{"a dot", source, "."},
		{"two dots", source, ".."},
		{"the folder itself", source, "data"},
		{"the destination's name", source, "snapshots"},
		{"a folder that is not ours", source, "elsewhere"},
		{"a folder kept for another folder", source, otherKept},
		{"a name that starts like the folder's", source, "data2.before-restore-2026-11-10_030000"},
		{"a name that ends like the folder's", source, "xdata.before-restore-2026-11-10_030000"},
		{"no time", source, "data.before-restore-"},
		{"a word for a time", source, "data.before-restore-latest"},
		{"a time that is too short", source, "data.before-restore-2026-11-10_03000"},
		{"something behind the time", source, older + ".bak"},
		{"a newline behind the time", source, older + "\n"},
		{"a NUL behind the time", source, older + "\x00"},
		{"a slash behind the name", source, older + "/"},
		{"a path behind the name", source, older + "/sub"},
		{"a path that comes back", source, older + "/../" + older},
		{"a path that leaves", source, older + "/../elsewhere"},
		{"a path before the name", source, "./" + older},
		{"a path that leaves before the name", source, "../" + filepath.Base(base) + "/" + older},
		{"a folder in front of the name", source, "sub/" + older},
		{"the full path", source, filepath.Join(base, older)},
		{"a backslash path", source, "..\\" + older},
		{"digits that are not ASCII", source, "data.before-restore-２０２６-11-10_030000"},
		{"what an interrupted restore left", source, "data.restoring-2026-11-10_030000"},
		{"a snapshot's name", source, "2026-11-12_030000"},
		{"a file with such a name", source, "data.before-restore-2026-11-11_030000"},
		{"a link with such a name", source, "data.before-restore-2026-11-09_030000"},
		{"a folder that does not exist", source, "data.before-restore-2020-01-01_000000"},
	}
	for _, c := range cases {
		err := RemoveKept(c.source, c.kept)
		if err == nil {
			t.Errorf("%s: deleted", c.name)
		} else if err == ErrBusy {
			t.Errorf("%s: refused as busy instead of as invalid", c.name)
		}
	}

	// while a run or a restore is in progress
	runMu.Lock()
	if err := RemoveKept(source, older); err != ErrBusy {
		t.Errorf("while busy: %v", err)
	}
	if err := RemoveKept(source, "elsewhere"); err == nil || err == ErrBusy {
		t.Errorf("another name while busy: %v", err)
	}
	runMu.Unlock()

	// a folder a disk is mounted on
	realMountPoint := mountPoint
	mountPoint = func(dir string) bool { return dir == filepath.Join(base, older) }
	if err := RemoveKept(source, older); err == nil || !strings.Contains(err.Error(), "mounted") {
		t.Errorf("a mount point: %v", err)
	}
	mountPoint = realMountPoint

	// a folder that holds something in use: the destination, a folder that is
	// backed up, Cosmos' own files
	inside := filepath.Join(base, older, "sub")
	for name, used := range map[string]utils.SnapshotsConfig{
		"the destination":            {Destination: inside, Sources: []string{source}},
		"a folder that is backed up": {Destination: cfg.Destination, Sources: []string{source, inside}},
		"a folder that is the other": {Destination: cfg.Destination, Sources: []string{source, filepath.Join(base, older)}},
	} {
		useConfig(t, used)
		if err := RemoveKept(source, older); err == nil || !strings.Contains(err.Error(), "in use") {
			t.Errorf("a kept folder that holds %s: %v", name, err)
		}
	}
	useConfig(t, cfg)
	realFolder := utils.CONFIGFOLDER
	for _, own := range []string{inside + "/", filepath.Join(base, older) + "/"} {
		utils.CONFIGFOLDER = own
		if err := RemoveKept(source, older); err == nil || !strings.Contains(err.Error(), "in use") {
			t.Errorf("Cosmos' folder is %s: %v", own, err)
		}
	}
	utils.CONFIGFOLDER = realFolder

	// settings edited by hand into something unusable
	useConfig(t, utils.SnapshotsConfig{Enabled: true, Destination: "relative", Sources: []string{source}})
	if err := RemoveKept(source, older); err == nil {
		t.Error("deleted with unusable settings")
	}
	useConfig(t, cfg)

	// nothing above changed anything
	if after := describe(t, base); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused cleanup changed something:\nbefore %v\nafter  %v", before, after)
	}
	if runMu.TryLock() {
		runMu.Unlock()
	} else {
		t.Fatal("a refused cleanup kept the lock")
	}
	if GetStatus().Running {
		t.Error("a refused cleanup left the status at running")
	}

	// and what it is for: one folder goes, with all it holds; a link in it is
	// removed, not followed
	if err := os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(base, older, "sub", "link")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(base, older, "sub"), 0500)
	if err := RemoveKept(source, older); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(base, older)) {
		t.Error("the folder is still there")
	}
	for _, kept := range []string{
		filepath.Join(base, newer, "file.txt"),
		filepath.Join(base, "elsewhere", "precious.txt"),
		filepath.Join(source, "file.txt"),
		filepath.Join(source, "sub.before-restore-2026-11-10_030000", "file.txt"),
		filepath.Join(base, otherKept, "file.txt"),
		filepath.Join(base, "data.before-restore-2026-11-11_030000"),
		filepath.Join(base, "data.before-restore-2026-11-09_030000"),
		filepath.Join(base, "data.before-restore-2026-11-10_030000.bak", "keep.txt"),
		filepath.Join(base, "data2.before-restore-2026-11-10_030000", "keep.txt"),
	} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
	if len(List(cfg.Destination, source)) != 1 {
		t.Error("the snapshots were touched")
	}
	// a second time: it is gone
	if err := RemoveKept(source, older); err == nil {
		t.Error("deleted twice")
	}
	if GetStatus().Running {
		t.Error("the status is still at running")
	}
}

func TestCleanupRoute(t *testing.T) {
	cfg, source := fixture(t)
	base := filepath.Dir(source)
	write(t, filepath.Join(source, "file.txt"), "version 1", 0644)
	useConfig(t, cfg)
	first, _ := snap(t, cfg, source, day(12))
	write(t, filepath.Join(source, "file.txt"), "version 2", 0644)

	// what a real restore keeps is what the status lists and the route deletes
	if err := Restore(source, filepath.Base(first)); err != nil {
		t.Fatal(err)
	}
	kept := listKept(source)
	if len(kept) != 1 || read(t, filepath.Join(base, kept[0].Name, "file.txt")) != "version 2" {
		t.Fatalf("kept by the restore: %+v", kept)
	}
	name := kept[0].Name
	write(t, filepath.Join(base, "elsewhere", "precious.txt"), "not ours", 0644)

	body := func(source string, name string) string {
		data, _ := json.Marshal(map[string]string{"Source": source, "Name": name})
		return string(data)
	}
	before := describe(t, base)

	invalid := map[string]string{
		"no body":                 ``,
		"not JSON":                `delete everything`,
		"an array":                `[]`,
		"wrong types":             `{"Source": 1, "Name": true}`,
		"nothing named":           `{}`,
		"no name":                 body(source, ""),
		"no folder":               body("", name),
		"a folder not configured": body(base, name),
		"a folder with ..":        body(source+"/../data", name),
		"another folder":          body(source, "elsewhere"),
		"the folder itself":       body(source, "data"),
		"a name that leaves":      body(source, "../"+name),
		"an absolute name":        body(source, filepath.Join(base, name)),
		"a name with a path":      body(source, name+"/../elsewhere"),
		"a missing folder":        body(source, "data.before-restore-2020-01-01_000000"),
		"a body far too large":    `{"Source": "` + strings.Repeat("a", 1<<20) + `", "Name": "` + name + `"}`,
	}
	for what, payload := range invalid {
		rec := call(CleanupRoute, apiRequest("POST", "/api/snapshots/cleanup", "sudo", payload))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", what, rec.Code, rec.Body.String())
		}
		var reply utils.HTTPErrorResult
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.Status != "error" || reply.Message == "" {
			t.Errorf("%s: reply %s", what, rec.Body.String())
		}
	}

	// not for someone who may only read, nor before the password is confirmed
	for _, who := range []string{"none", "reader", "admin"} {
		if rec := call(CleanupRoute, apiRequest("POST", "/api/snapshots/cleanup", who, body(source, name))); rec.Code != http.StatusUnauthorized {
			t.Errorf("as %s: status %d, want 401", who, rec.Code)
		}
	}

	// while something runs: 409
	runMu.Lock()
	rec := call(CleanupRoute, apiRequest("POST", "/api/snapshots/cleanup", "sudo", body(source, name)))
	runMu.Unlock()
	if rec.Code != http.StatusConflict {
		t.Errorf("cleanup while busy: status %d: %s", rec.Code, rec.Body.String())
	}

	if after := describe(t, base); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused request changed something:\nbefore %v\nafter  %v", before, after)
	}

	// and the real thing: the answer is the status without the folder
	rec = call(CleanupRoute, apiRequest("POST", "/api/snapshots/cleanup", "sudo", body(source, name)))
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup: status %d: %s", rec.Code, rec.Body.String())
	}
	if exists(filepath.Join(base, name)) {
		t.Error("the folder is still there")
	}
	if !strings.Contains(rec.Body.String(), `"beforeRestore":[]`) || strings.Contains(rec.Body.String(), `"running":true`) {
		t.Errorf("reply: %s", rec.Body.String())
	}
	if got := read(t, filepath.Join(source, "file.txt")); got != "version 1" {
		t.Errorf("the restored folder afterwards: %q", got)
	}
	if !exists(filepath.Join(base, "elsewhere", "precious.txt")) || len(List(cfg.Destination, source)) != 1 {
		t.Error("something else was removed")
	}
}
