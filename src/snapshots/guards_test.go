package snapshots

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// empty removes everything a folder holds, as an unmounted disk leaves it.
func empty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmptyFolderThatHadFilesIsNotSnapshotted(t *testing.T) {
	cfg, source := fixture(t)
	dir := filepath.Join(cfg.Destination, Slug(source))
	write(t, filepath.Join(source, "a.txt"), "a", 0644)
	write(t, filepath.Join(source, "sub", "b.txt"), "b", 0644)
	for n := 2; n <= 5; n++ {
		snap(t, cfg, source, day(n))
	}
	before := describe(t, cfg.Destination)
	if len(List(cfg.Destination, source)) != 4 {
		t.Fatalf("snapshots before: %v", List(cfg.Destination, source))
	}

	// the disk is gone: the folder is there, with nothing in it. The retention
	// is set so that a run would leave one snapshot only.
	empty(t, source)
	strict := cfg
	strict.KeepRecent, strict.KeepDaily, strict.KeepWeekly, strict.KeepMonthly = 1, 1, 1, 1
	id, _, err := snapshotSource(strict, source, day(6))
	if err == nil || id != "" {
		t.Fatalf("a snapshot %q was made of the emptied folder: %v", id, err)
	}
	for _, want := range []string{"the folder is empty", "had 2 files", "is the disk mounted?"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the reason %q does not say %q", err.Error(), want)
		}
	}
	// no snapshot, nothing unfinished, and no old snapshot aged out
	if after := describe(t, cfg.Destination); !reflect.DeepEqual(after, before) {
		t.Errorf("the refused run changed the destination:\nbefore %v\nafter  %v", before, after)
	}

	// a run reports it as that folder's error and tells the admins
	notices := []string{}
	realNotify := notifyFailure
	notifyFailure = func(summary string) { notices = append(notices, summary) }
	defer func() { notifyFailure = realNotify }()
	useConfig(t, strict)
	if err := RunAll(); err != nil {
		t.Fatal(err)
	}
	result := GetStatus().LastResults[source]
	if result.Snapshot != "" || !strings.Contains(result.Error, "is the disk mounted?") {
		t.Errorf("result of the run: %+v", result)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], source) || !strings.Contains(notices[0], "is the disk mounted?") {
		t.Errorf("notices: %q", notices)
	}
	if after := describe(t, cfg.Destination); !reflect.DeepEqual(after, before) {
		t.Errorf("the refused run changed the destination:\nbefore %v\nafter  %v", before, after)
	}

	// without a record, the previous snapshot is looked at itself
	newest := filepath.Join(dir, List(cfg.Destination, source)[0].ID)
	if err := os.Remove(filepath.Join(newest, metaName)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := snapshotSource(cfg, source, day(6)); err == nil || !strings.Contains(err.Error(), "is the disk mounted?") {
		t.Errorf("previous snapshot without a record: %v", err)
	}

	// the disk is back: snapshots go on, and the retention with them
	write(t, filepath.Join(source, "a.txt"), "a", 0644)
	if _, _, err := snapshotSource(strict, source, day(7)); err != nil {
		t.Fatal(err)
	}
	if list := List(cfg.Destination, source); len(list) != 1 || list[0].ID != "2026-11-07_030000" {
		t.Errorf("snapshots after the folder came back: %v", list)
	}
	// one file before: the message says so in the singular
	empty(t, source)
	if _, _, err := snapshotSource(cfg, source, day(8)); err == nil || !strings.Contains(err.Error(), "had 1 file ") {
		t.Errorf("emptied after a snapshot of one file: %v", err)
	}
}

func TestFolderThatWasAlreadyEmptyStaysAllowed(t *testing.T) {
	cfg, source := fixture(t)
	first, meta := snap(t, cfg, source, day(2))
	if meta.Files != 0 {
		t.Fatalf("files in the snapshot of an empty folder: %d", meta.Files)
	}
	// empty again: nothing was lost
	second, _ := snap(t, cfg, source, day(3))
	if first == second || !exists(second) {
		t.Errorf("no second snapshot of the empty folder: %s, %s", first, second)
	}

	// only empty folders before, nothing now: no file was lost either
	if err := os.MkdirAll(filepath.Join(source, "empty", "inside"), 0755); err != nil {
		t.Fatal(err)
	}
	snap(t, cfg, source, day(4))
	empty(t, source)
	snap(t, cfg, source, day(5))

	// files before, and now a folder without files: it is not empty, so it is
	// what the folder holds now
	write(t, filepath.Join(source, "a.txt"), "a", 0644)
	snap(t, cfg, source, day(6))
	empty(t, source)
	if err := os.Mkdir(filepath.Join(source, "left"), 0755); err != nil {
		t.Fatal(err)
	}
	last, meta := snap(t, cfg, source, day(7))
	if meta.Files != 0 || !exists(filepath.Join(last, "left")) || exists(filepath.Join(last, "a.txt")) {
		t.Errorf("snapshot of a folder that only holds an empty folder: %+v", meta)
	}
}

func TestExcluded(t *testing.T) {
	cases := []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"*.tmp", "a.tmp", true},
		{"*.tmp", "deep/down/a.tmp", true}, // by its name, at any depth
		{"*.tmp", "a.tmp.txt", false},
		{"cache", "cache", true},
		{"cache", "apps/x/cache", true},
		{"cache", "cache2", false},
		{"cache", "mycache", false},
		{"apps/*/cache", "apps/x/cache", true},
		{"apps/*/cache", "apps/x/y/cache", false}, // "*" stays within one name
		{"apps/*/cache", "other/x/cache", false},
		{"sub/*.log", "sub/app.log", true},
		{"sub/*.log", "other/app.log", false},
		{"sub/*.log", "sub/deeper/app.log", false},
		{"logs/*", "logs/2026", true},
		{"logs/*", "logs", false},
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file10.txt", false},
		{"[ab]*.bin", "sub/b1.bin", true},
		{"[ab]*.bin", "sub/c1.bin", false},
		{"transcodes", "media/Transcodes", false}, // names are compared as they are written
	}
	for _, c := range cases {
		if got := excluded([]string{"unrelated", c.pattern}, c.rel, filepath.Base(c.rel)); got != c.want {
			t.Errorf("excluded(%q, %q) = %v, want %v", c.pattern, c.rel, got, c.want)
		}
	}
	if excluded(nil, "a.tmp", "a.tmp") {
		t.Error("excluded without a pattern")
	}
}

func TestExcludedFilesAndFoldersAreSkipped(t *testing.T) {
	cfg, source := fixture(t)
	cfg.Excludes = []string{"*.tmp", "cache", "sub/*.log", "transcodes"}
	if err := ValidateConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		"keep.txt",
		"notes.tmp",
		"cache/a.bin",
		"cache/deep/b.bin",
		"sub/cache/c.bin",
		"sub/app.log",
		"sub/keep.txt",
		"sub/deeper/app.log",
		"sub/deeper/x.tmp",
		"other/app.log",
		"media/transcodes/x.ts",
		"media/film.mkv",
		"cache2/kept.bin",
	} {
		write(t, filepath.Join(source, file), "content of "+file, 0644)
	}
	os.Symlink("keep.txt", filepath.Join(source, "link.tmp")) // a link is left out by its own name too
	// a folder that cannot be read is no problem when it is left out
	if err := os.Mkdir(filepath.Join(source, "sub", "transcodes"), 0000); err != nil {
		t.Fatal(err)
	}

	wanted := []string{"keep.txt", "sub/keep.txt", "sub/deeper/app.log", "other/app.log", "media/film.mkv", "cache2/kept.bin"}
	unwanted := []string{"notes.tmp", "link.tmp", "cache", "sub/cache", "sub/app.log", "sub/deeper/x.tmp", "media/transcodes", "sub/transcodes"}
	check := func(dir string, meta Meta) {
		t.Helper()
		for _, file := range wanted {
			if !exists(filepath.Join(dir, file)) {
				t.Errorf("%s is missing from the snapshot", file)
			}
		}
		for _, file := range unwanted {
			if exists(filepath.Join(dir, file)) {
				t.Errorf("%s is in the snapshot although it is excluded", file)
			}
		}
		if meta.Errors != 0 || len(meta.ErrorMessages) != 0 || meta.Files != int64(len(wanted)) {
			t.Errorf("record: %d files (want %d), %d errors %q", meta.Files, len(wanted), meta.Errors, meta.ErrorMessages)
		}
	}

	first, meta := snap(t, cfg, source, day(2))
	check(first, meta)

	// the next one links what did not change, and still leaves the rest out
	second, meta := snap(t, cfg, source, day(3))
	check(second, meta)
	if !sameFile(t, filepath.Join(first, "keep.txt"), filepath.Join(second, "keep.txt")) {
		t.Error("an unchanged file was copied again")
	}

	// something that was backed up and is excluded now is in the old snapshot only
	cfg.Excludes = append(cfg.Excludes, "other")
	third, _ := snap(t, cfg, source, day(4))
	if exists(filepath.Join(third, "other")) || !exists(filepath.Join(second, "other", "app.log")) {
		t.Error("a folder excluded later: wrong in the new snapshot or gone from the old one")
	}

	// and without exclusions everything is back (the unreadable folder as an error, as root reads it)
	cfg.Excludes = nil
	fourth, meta := snap(t, cfg, source, day(5))
	for _, file := range []string{"notes.tmp", "link.tmp", "cache/deep/b.bin", "sub/cache/c.bin", "sub/app.log", "media/transcodes/x.ts", "other/app.log"} {
		if !exists(filepath.Join(fourth, file)) {
			t.Errorf("%s is missing from the snapshot without exclusions", file)
		}
	}
	if os.Geteuid() != 0 && meta.Errors != 1 {
		t.Errorf("errors without exclusions: %d %q", meta.Errors, meta.ErrorMessages)
	}
	os.Chmod(filepath.Join(source, "sub", "transcodes"), 0755)

	// the source itself is never left out, whatever it is called
	cfg.Excludes = []string{filepath.Base(source)}
	fifth, meta := snap(t, cfg, source, day(6))
	if !exists(filepath.Join(fifth, "keep.txt")) || meta.Files == 0 {
		t.Error("a pattern that matches the name of the folder itself emptied the snapshot")
	}
}

func TestValidateConfigExcludes(t *testing.T) {
	good := func(excludes ...string) utils.SnapshotsConfig {
		return utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/data"}, Excludes: excludes}
	}

	// stored in their plain form, once each, in the order given
	shared := []string{"*.tmp", "cache/", "transcodes", "apps/*/logs//", "*.tmp", "cache", "[a-c]?.bak", "with space", "a..b", "été*"}
	cfg := good(shared...)
	if err := ValidateConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if want := []string{"*.tmp", "cache", "transcodes", "apps/*/logs", "[a-c]?.bak", "with space", "a..b", "été*"}; !reflect.DeepEqual(cfg.Excludes, want) {
		t.Errorf("stored %q, want %q", cfg.Excludes, want)
	}
	if shared[1] != "cache/" {
		t.Error("the caller's list was changed in place")
	}
	// none is fine, and the limits themselves are allowed
	cfg = good()
	if err := ValidateConfig(&cfg); err != nil || cfg.Excludes == nil || len(cfg.Excludes) != 0 {
		t.Errorf("no exclusion: %q, %v", cfg.Excludes, err)
	}
	most := []string{}
	for i := 0; i < maxExcludes; i++ {
		most = append(most, fmt.Sprintf("pattern-%d", i))
	}
	cfg = good(most...)
	if err := ValidateConfig(&cfg); err != nil {
		t.Errorf("%d exclusions: %v", maxExcludes, err)
	}
	cfg = good(strings.Repeat("é", maxExcludeLength))
	if err := ValidateConfig(&cfg); err != nil {
		t.Errorf("an exclusion of %d characters: %v", maxExcludeLength, err)
	}

	bad := map[string][]string{
		"too many":                   append(append([]string{}, most...), "one-more"),
		"too long":                   {strings.Repeat("a", maxExcludeLength+1)},
		"empty":                      {"*.tmp", ""},
		"only a slash":               {"/"},
		"absolute":                   {"/data/cache"},
		"absolute pattern":           {"/*.tmp"},
		"two dots":                   {".."},
		"two dots in front":          {"../other"},
		"two dots in the middle":     {"cache/../../etc"},
		"two dots at the end":        {"cache/.."},
		"two dots and a slash":       {"cache/../"},
		"double star":                {"**/cache"},
		"double star at the end":     {"cache/**"},
		"a bracket that is not shut": {"[a-"},
		"a backslash at the end":     {"cache\\"},
		"a newline":                  {"cache\n*.tmp"},
		"a NUL":                      {"cache\x00"},
		"a tab":                      {"ca\tche"},
		"a space in front":           {" cache"},
		"a space behind":             {"cache "},
	}
	for name, excludes := range bad {
		cfg := good(excludes...)
		err := ValidateConfig(&cfg)
		if err == nil {
			t.Errorf("%s: accepted as %q", name, cfg.Excludes)
			continue
		}
		for _, r := range err.Error() {
			if r < 0x20 || r == 0x7f {
				t.Errorf("%s: the message contains a control character: %q", name, err.Error())
				break
			}
		}
		if len(err.Error()) > 600 {
			t.Errorf("%s: the message is %d bytes long", name, len(err.Error()))
		}
		// refused while the module is off too: nothing unusable is stored
		off := utils.SnapshotsConfig{Excludes: excludes}
		if err := ValidateConfig(&off); err == nil {
			t.Errorf("%s: accepted while off", name)
		}
	}
}
