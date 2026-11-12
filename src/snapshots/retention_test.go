package snapshots

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestRetention(t *testing.T) {
	// 2026-11-12 is a Thursday in ISO week 46; 2026-11-08 a Sunday in week 45;
	// 2026-11-01 (Sunday) and 2026-10-31 (Saturday) are both in week 44.
	cases := []struct {
		name                   string
		names                  []string
		daily, weekly, monthly int
		keep                   []string
	}{
		{
			name: "days, weeks and months",
			names: []string{
				"2026-11-12_150000", // newest: first day, first week, first month
				"2026-11-12_030000", // older one of a day that has its newest
				"2026-11-11_030000", // second day
				"2026-11-10_030000", // third day
				"2026-11-09_030000", // fourth day, same week and month: goes
				"2026-11-08_030000", // newest of the week before: second week
				"2026-11-07_030000", // same week: goes
				"2026-11-01_030000", // a third week: goes
				"2026-10-31_030000", // newest of the month before: second month
				"2026-10-15_030000", // same month: goes
				"2026-09-30_030000", // a third month: goes
			},
			daily: 3, weekly: 2, monthly: 2,
			keep: []string{"2026-11-12_150000", "2026-11-11_030000", "2026-11-10_030000", "2026-11-08_030000", "2026-10-31_030000"},
		},
		{
			name:  "the order given does not matter",
			names: []string{"2026-11-10_030000", "2026-11-12_030000", "2026-11-11_030000"},
			daily: 2, weekly: 1, monthly: 1,
			keep: []string{"2026-11-12_030000", "2026-11-11_030000"},
		},
		{
			name:  "days without a snapshot do not count",
			names: []string{"2026-11-12_030000", "2026-11-05_030000", "2026-10-01_030000", "2026-03-01_030000"},
			daily: 3, weekly: 1, monthly: 1,
			keep: []string{"2026-11-12_030000", "2026-11-05_030000", "2026-10-01_030000"},
		},
		{
			name: "a week that spans two years is one week",
			names: []string{
				"2027-01-02_030000", // Saturday, ISO week 53 of 2026
				"2027-01-01_030000", // Friday, same week
				"2026-12-31_030000", // Thursday, same week
				"2026-12-27_030000", // Sunday, week 52
				"2026-12-20_030000", // Sunday, week 51
			},
			daily: 1, weekly: 2, monthly: 1,
			keep: []string{"2027-01-02_030000", "2026-12-27_030000"},
		},
		{
			name:  "the newest is kept whatever the counts",
			names: []string{"2026-11-12_030000", "2026-11-11_030000"},
			daily: 0, weekly: 0, monthly: 0,
			keep: []string{"2026-11-12_030000"},
		},
		{
			name:  "one snapshot",
			names: []string{"2026-11-12_030000"},
			daily: 7, weekly: 4, monthly: 6,
			keep: []string{"2026-11-12_030000"},
		},
		{
			name:  "nothing",
			names: nil,
			daily: 7, weekly: 4, monthly: 6,
			keep: nil,
		},
		{
			name:  "only days",
			names: []string{"2026-11-12_030000", "2026-11-11_030000", "2026-11-10_030000", "2026-11-09_030000", "2026-10-09_030000"},
			daily: 5, weekly: 1, monthly: 1,
			keep: []string{"2026-11-12_030000", "2026-11-11_030000", "2026-11-10_030000", "2026-11-09_030000", "2026-10-09_030000"},
		},
	}

	for _, c := range cases {
		keep, remove := retention(c.names, c.daily, c.weekly, c.monthly)
		if !reflect.DeepEqual(keep, c.keep) {
			t.Errorf("%s: keeps %v, want %v", c.name, keep, c.keep)
		}
		// what is not kept is removed, and nothing is in both lists or in neither
		all := append(append([]string{}, keep...), remove...)
		sort.Strings(all)
		want := append([]string{}, c.names...)
		sort.Strings(want)
		if len(all) != len(want) || (len(all) > 0 && !reflect.DeepEqual(all, want)) {
			t.Errorf("%s: keep %v + remove %v is not the list given", c.name, keep, remove)
		}
	}
}

func TestRetentionNeverNamesAnythingElse(t *testing.T) {
	strangers := []string{
		"", ".", "..", "notes.txt", "my-folder", "../2026-11-12_030000", "2026-11-12_030000/..",
		"/2026-11-12_030000", "2026-11-12_030000.partial", "2026-11-12_030000 ", " 2026-11-12_030000",
		"2026-11-12_03000", "2026-11-12-030000", "2026-13-45_999999", "2026-11-12_030000\n",
	}
	names := append([]string{"2026-11-12_030000", "2026-11-11_030000", "2026-11-10_030000"}, strangers...)
	keep, remove := retention(names, 1, 1, 1)
	if !reflect.DeepEqual(keep, []string{"2026-11-12_030000"}) {
		t.Errorf("keeps %q", keep)
	}
	if !reflect.DeepEqual(remove, []string{"2026-11-11_030000", "2026-11-10_030000"}) {
		t.Errorf("removes %q", remove)
	}
}

func TestKeepCounts(t *testing.T) {
	if d, w, m := keepCounts(utils.SnapshotsConfig{}); d != 7 || w != 4 || m != 6 {
		t.Errorf("defaults: %d, %d, %d", d, w, m)
	}
	if d, w, m := keepCounts(utils.SnapshotsConfig{KeepDaily: 30, KeepWeekly: 1, KeepMonthly: 12}); d != 30 || w != 1 || m != 12 {
		t.Errorf("set: %d, %d, %d", d, w, m)
	}
}

func TestPruneRemovesOnlySnapshots(t *testing.T) {
	cfg, source := fixture(t)
	dir := filepath.Join(cfg.Destination, Slug(source))
	outside := filepath.Join(filepath.Dir(source), "outside")
	write(t, filepath.Join(outside, "precious.txt"), "not ours", 0644)

	snapshots := []string{"2026-11-12_030000", "2026-11-11_030000", "2026-11-10_030000", "2026-10-01_030000", "2026-09-01_030000"}
	for _, id := range snapshots {
		write(t, filepath.Join(dir, id, "file.txt"), id, 0644)
		write(t, filepath.Join(dir, id, "sub", "readonly.txt"), id, 0444)
		if os.Geteuid() == 0 { // only root can empty a folder it may not write to
			os.Chmod(filepath.Join(dir, id, "sub"), 0555)
		}
		// a link inside a snapshot must be removed as a link, not followed
		os.Symlink(outside, filepath.Join(dir, id, "link-out"))
	}

	// what is not a snapshot, even if old and named almost like one
	write(t, filepath.Join(dir, "notes.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "2020-01-01_000000.bak", "keep.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "x2020-01-01_000000", "keep.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "2020-01-01_000000"), "a file with a snapshot's name", 0644)
	if err := os.Symlink(outside, filepath.Join(dir, "2020-01-02_000000")); err != nil { // a link with a snapshot's name
		t.Fatal(err)
	}
	// and what lies next to the folder of this source
	write(t, filepath.Join(cfg.Destination, "2020-01-03_000000", "keep.txt"), "another folder", 0644)
	write(t, filepath.Join(cfg.Destination, "other-source", "2020-01-04_000000", "keep.txt"), "another source", 0644)

	removed, err := prune(cfg.Destination, source, 2, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if want := []string{"2026-09-01_030000", "2026-11-10_030000"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %v, want %v", removed, want)
	}
	for _, id := range snapshots {
		gone := id == "2026-11-10_030000" || id == "2026-09-01_030000"
		if exists(filepath.Join(dir, id)) == gone {
			t.Errorf("%s: removed=%v, want %v", id, !exists(filepath.Join(dir, id)), gone)
		}
	}
	for _, kept := range []string{
		filepath.Join(dir, "notes.txt"),
		filepath.Join(dir, "2020-01-01_000000.bak", "keep.txt"),
		filepath.Join(dir, "x2020-01-01_000000", "keep.txt"),
		filepath.Join(dir, "2020-01-01_000000"),
		filepath.Join(dir, "2020-01-02_000000"),
		filepath.Join(cfg.Destination, "2020-01-03_000000", "keep.txt"),
		filepath.Join(cfg.Destination, "other-source", "2020-01-04_000000", "keep.txt"),
		filepath.Join(outside, "precious.txt"),
	} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}

	// again: nothing more to remove
	if removed, err := prune(cfg.Destination, source, 2, 1, 2); err != nil || len(removed) != 0 {
		t.Errorf("second prune removed %v, %v", removed, err)
	}
}

func TestRemoveSnapshotDirRefuses(t *testing.T) {
	cfg, source := fixture(t)
	dir := filepath.Join(cfg.Destination, Slug(source))
	outside := filepath.Join(filepath.Dir(source), "outside")
	write(t, filepath.Join(outside, "precious.txt"), "not ours", 0644)
	write(t, filepath.Join(dir, "2026-11-12_030000", "file.txt"), "a snapshot", 0644)
	write(t, filepath.Join(dir, "my-folder", "keep.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "2026-11-11_030000"), "a file", 0644)
	os.Symlink(outside, filepath.Join(dir, "2026-11-10_030000"))
	os.Symlink(outside, filepath.Join(dir, "2026-11-10_030000.partial"))

	for _, name := range []string{
		"", ".", "..", "my-folder", "../data", "../../outside", "/etc", outside,
		"2026-11-12_030000/..", "2026-11-12_030000/../my-folder", "./2026-11-12_030000", "2026-11-12_030000/",
		"2026-11-12_030000/file.txt", "2026-11-12_030000.partial.bak", "2026-13-45_999999",
		"2026-11-11_030000", // a file
		"2026-11-10_030000", // a link to a folder
		"2026-11-10_030000.partial",
		"2026-11-09_030000", // does not exist
	} {
		if err := removeSnapshotDir(cfg.Destination, source, name); err == nil {
			t.Errorf("removeSnapshotDir(%q) did not refuse", name)
		}
	}
	// a destination or a source that gives no folder to work in
	for _, c := range [][2]string{{"", source}, {"/", source}, {"relative", source}, {cfg.Destination, "/"}, {cfg.Destination, ""}} {
		if err := removeSnapshotDir(c[0], c[1], "2026-11-12_030000"); err == nil {
			t.Errorf("removeSnapshotDir(%q, %q) did not refuse", c[0], c[1])
		}
	}
	// the folder of the source is itself a link: nothing below it is ours to remove
	linked := filepath.Join(filepath.Dir(source), "linked-source")
	os.Symlink(dir, filepath.Join(cfg.Destination, Slug(linked)))
	if err := removeSnapshotDir(cfg.Destination, linked, "2026-11-12_030000"); err == nil {
		t.Error("a snapshot was removed through a link")
	}

	for _, kept := range []string{
		filepath.Join(dir, "2026-11-12_030000", "file.txt"),
		filepath.Join(dir, "my-folder", "keep.txt"),
		filepath.Join(dir, "2026-11-11_030000"),
		filepath.Join(dir, "2026-11-10_030000"),
		filepath.Join(dir, "2026-11-10_030000.partial"),
		filepath.Join(outside, "precious.txt"),
		source,
	} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}

	// and what it is for: a snapshot, and the leftover of an interrupted run
	write(t, filepath.Join(dir, "2026-11-09_030000.partial", "half.txt"), "half", 0644)
	for _, name := range []string{"2026-11-12_030000", "2026-11-09_030000.partial"} {
		if err := removeSnapshotDir(cfg.Destination, source, name); err != nil || exists(filepath.Join(dir, name)) {
			t.Errorf("removeSnapshotDir(%q): %v", name, err)
		}
	}
}
