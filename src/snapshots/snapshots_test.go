package snapshots

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Nothing here flushes the machine's disks, writes a notification to the
// database or asks Docker anything.
func TestMain(m *testing.M) {
	syncDisks = func() {}
	notifyFailure = func(string) {}
	dockerRoot = func() string { return "" }
	os.Exit(m.Run())
}

// day is three o'clock at night on a day of November 2026. The tests take
// their snapshots on different days: of two taken on the same day, the
// retention only keeps the newer one.
func day(n int) time.Time {
	return time.Date(2026, 11, n, 3, 0, 0, 0, time.Local)
}

func write(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile is subject to the umask
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sameFile(t *testing.T, a string, b string) bool {
	t.Helper()
	infoA, errA := os.Lstat(a)
	infoB, errB := os.Lstat(b)
	if errA != nil || errB != nil {
		t.Fatalf("cannot compare %s and %s: %v, %v", a, b, errA, errB)
	}
	return os.SameFile(infoA, infoB)
}

// fixture makes a folder to back up and a destination, both in the test's
// own temporary folder.
func fixture(t *testing.T) (cfg utils.SnapshotsConfig, source string) {
	t.Helper()
	base := t.TempDir()
	source = filepath.Join(base, "data")
	destination := filepath.Join(base, "snapshots")
	for _, dir := range []string{source, destination} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return utils.SnapshotsConfig{Enabled: true, Destination: destination, Sources: []string{source}}, source
}

// snap makes a snapshot and returns the folder it is in.
func snap(t *testing.T, cfg utils.SnapshotsConfig, source string, now time.Time) (dir string, meta Meta) {
	t.Helper()
	id, meta, err := snapshotSource(cfg, source, now)
	if err != nil {
		t.Fatalf("snapshot of %s: %v", source, err)
	}
	return filepath.Join(cfg.Destination, Slug(source), id), meta
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"/var/lib/cosmos":   "var-lib-cosmos",
		"/var/lib/cosmos/":  "var-lib-cosmos",
		"/var/lib-cosmos":   "var-lib~-cosmos",
		"/var-lib/cosmos":   "var~-lib-cosmos",
		"/a/-b":             "a-~-b",
		"/a-/b":             "a~--b",
		"/a~/b":             "a~~-b",
		"/a/~b":             "a-~~b",
		"/storage/my data":  "storage-my data",
		"/storage/app_data": "storage-app_data",
		"/x":                "x",
	}
	seen := map[string]string{}
	for path, want := range cases {
		got := Slug(path)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", path, got, want)
		}
		if strings.ContainsAny(got, "/\x00") || got == "" || got == "." || got == ".." {
			t.Errorf("Slug(%q) = %q is not a folder name", path, got)
		}
		clean := filepath.Clean(path)
		if other, taken := seen[got]; taken && other != clean {
			t.Errorf("%q and %q share the name %q", other, clean, got)
		}
		seen[got] = clean
	}

	// the name can be turned back into the path, so no two paths share one
	back := func(slug string) string {
		path := "/"
		for i := 0; i < len(slug); i++ {
			switch {
			case slug[i] == '~' && i+1 < len(slug):
				i++
				path += string(slug[i])
			case slug[i] == '-':
				path += "/"
			default:
				path += string(slug[i])
			}
		}
		return path
	}
	for _, path := range []string{"/a-b/c", "/a/b-c", "/a/b/c", "/a--b", "/a-/-b", "/~-/-~", "/a~-b/c", "/-", "/~", "/-/-", "/--"} {
		if got := back(Slug(path)); got != path {
			t.Errorf("%q -> %q -> %q", path, Slug(path), got)
		}
	}
}

func TestSlugDirStaysInsideTheDestination(t *testing.T) {
	if dir, err := slugDir("/backups", "/var/lib/cosmos"); err != nil || dir != "/backups/var-lib-cosmos" {
		t.Errorf("slugDir = %q, %v", dir, err)
	}
	for _, c := range [][2]string{{"", "/data"}, {"/", "/data"}, {"relative", "/data"}, {"/backups", "/"}, {"/backups", ""}} {
		if dir, err := slugDir(c[0], c[1]); err == nil {
			t.Errorf("slugDir(%q, %q) = %q, want an error", c[0], c[1], dir)
		}
	}
	// whatever the source, the result is a direct child of the destination
	for _, source := range []string{"/a/../../etc", "/..", "/a/./b", "//a//b//", "/a\x00b"} {
		dir, err := slugDir("/backups", source)
		if err == nil && filepath.Dir(dir) != "/backups" {
			t.Errorf("slugDir(/backups, %q) = %q leaves the destination", source, dir)
		}
	}
}

func TestSecondSnapshotLinksUnchangedAndCopiesChanged(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "same.txt"), "stays the same", 0644)
	write(t, filepath.Join(source, "sub/deep/same.bin"), "also the same", 0600)
	write(t, filepath.Join(source, "changed.txt"), "before", 0644)
	write(t, filepath.Join(source, "mode.txt"), "mode changes", 0644)
	write(t, filepath.Join(source, "gone.txt"), "will be deleted", 0644)
	// same size and content, but touched: the time is part of what "unchanged" means
	write(t, filepath.Join(source, "touched.txt"), "touched", 0644)

	first, meta := snap(t, cfg, source, day(12))
	if meta.Files != 6 || meta.BytesLinked != 0 || meta.Errors != 0 {
		t.Errorf("first snapshot: %+v", meta)
	}
	if want := int64(len("stays the same") + len("also the same") + len("before") + len("mode changes") + len("will be deleted") + len("touched")); meta.BytesCopied != want {
		t.Errorf("first snapshot copied %d bytes, want %d", meta.BytesCopied, want)
	}
	// the snapshot is a copy, not a link to the original
	if sameFile(t, filepath.Join(source, "same.txt"), filepath.Join(first, "same.txt")) {
		t.Error("the snapshot shares a file with the folder it was taken from")
	}

	write(t, filepath.Join(source, "changed.txt"), "after!", 0644) // same size, new content
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(source, "changed.txt"), later, later)
	os.Chtimes(filepath.Join(source, "touched.txt"), later, later)
	os.Chmod(filepath.Join(source, "mode.txt"), 0600)
	os.Remove(filepath.Join(source, "gone.txt"))
	write(t, filepath.Join(source, "new.txt"), "new", 0644)

	second, meta := snap(t, cfg, source, day(13))
	if first == second {
		t.Fatal("both snapshots have the same name")
	}

	for _, name := range []string{"same.txt", "sub/deep/same.bin"} {
		if !sameFile(t, filepath.Join(first, name), filepath.Join(second, name)) {
			t.Errorf("%s did not change but was copied again instead of linked", name)
		}
	}
	for _, name := range []string{"changed.txt", "mode.txt", "touched.txt"} {
		if sameFile(t, filepath.Join(first, name), filepath.Join(second, name)) {
			t.Errorf("%s changed but is a link to the old version", name)
		}
	}
	if got := read(t, filepath.Join(second, "changed.txt")); got != "after!" {
		t.Errorf("changed.txt in the new snapshot: %q", got)
	}
	if got := read(t, filepath.Join(first, "changed.txt")); got != "before" {
		t.Errorf("changed.txt in the old snapshot was altered: %q", got)
	}
	if info, _ := os.Stat(filepath.Join(second, "mode.txt")); info.Mode().Perm() != 0600 {
		t.Errorf("mode.txt in the new snapshot has mode %o", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Join(first, "mode.txt")); info.Mode().Perm() != 0644 {
		t.Errorf("mode.txt in the old snapshot now has mode %o", info.Mode().Perm())
	}

	if exists(filepath.Join(second, "gone.txt")) {
		t.Error("a deleted file is in the new snapshot")
	}
	if got := read(t, filepath.Join(first, "gone.txt")); got != "will be deleted" {
		t.Errorf("the deleted file in the old snapshot: %q", got)
	}
	if got := read(t, filepath.Join(second, "new.txt")); got != "new" {
		t.Errorf("new.txt: %q", got)
	}

	if meta.Files != 6 || meta.Errors != 0 {
		t.Errorf("second snapshot: %+v", meta)
	}
	if want := int64(len("stays the same") + len("also the same")); meta.BytesLinked != want {
		t.Errorf("second snapshot linked %d bytes, want %d", meta.BytesLinked, want)
	}
	if want := int64(len("after!") + len("mode changes") + len("touched") + len("new")); meta.BytesCopied != want {
		t.Errorf("second snapshot copied %d bytes, want %d", meta.BytesCopied, want)
	}

	// the record in the snapshot says the same, and the listing reads it
	var stored Meta
	if err := json.Unmarshal([]byte(read(t, filepath.Join(second, metaName))), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Source != source || stored.Files != meta.Files || stored.BytesCopied != meta.BytesCopied ||
		stored.BytesLinked != meta.BytesLinked || !stored.Started.Equal(day(13)) || stored.Finished.IsZero() {
		t.Errorf("record in the snapshot: %+v", stored)
	}
	list := List(cfg.Destination, source)
	if len(list) != 2 || list[0].ID != filepath.Base(second) || list[1].ID != filepath.Base(first) {
		t.Fatalf("listing: %+v", list)
	}
	if list[0].Files != 6 || list[0].BytesLinked != meta.BytesLinked || list[0].BytesCopied != meta.BytesCopied {
		t.Errorf("listing of the new snapshot: %+v", list[0])
	}

	// nothing is left of the copy under way
	entries, _ := os.ReadDir(filepath.Dir(second))
	if len(entries) != 2 {
		t.Errorf("the destination holds %d entries, want the 2 snapshots", len(entries))
	}
	filepath.WalkDir(second, func(path string, entry os.DirEntry, err error) error {
		if strings.HasPrefix(entry.Name(), ".cosmos-copy-") {
			t.Errorf("temporary file left behind: %s", path)
		}
		return nil
	})
}

func TestOwnerChangeMakesACopy(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to change the owner of a file")
	}
	cfg, source := fixture(t)
	file := filepath.Join(source, "owned.txt")
	write(t, file, "content", 0644)
	if err := os.Chown(filepath.Join(source), 4242, 4343); err != nil {
		t.Fatal(err)
	}

	first, _ := snap(t, cfg, source, day(12))
	for _, path := range []string{first, filepath.Join(first, "owned.txt")} {
		info, _ := os.Lstat(path)
		uid, gid := owner(info)
		want := [2]int{0, 0}
		if path == first {
			want = [2]int{4242, 4343}
		}
		if uid != want[0] || gid != want[1] {
			t.Errorf("%s belongs to %d:%d, want %d:%d", path, uid, gid, want[0], want[1])
		}
	}

	info, _ := os.Stat(file)
	if err := os.Chown(file, 1234, 2345); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(file, info.ModTime(), info.ModTime())

	second, _ := snap(t, cfg, source, day(13))
	if sameFile(t, filepath.Join(first, "owned.txt"), filepath.Join(second, "owned.txt")) {
		t.Error("a file with a new owner was linked to the old version, which has the old owner")
	}
	copied, _ := os.Lstat(filepath.Join(second, "owned.txt"))
	if uid, gid := owner(copied); uid != 1234 || gid != 2345 {
		t.Errorf("the copy belongs to %d:%d, want 1234:2345", uid, gid)
	}
	old, _ := os.Lstat(filepath.Join(first, "owned.txt"))
	if uid, gid := owner(old); uid != 0 || gid != 0 {
		t.Errorf("the old snapshot's file now belongs to %d:%d", uid, gid)
	}
}

func TestSymlinksStaySymlinksAndAreNotFollowed(t *testing.T) {
	cfg, source := fixture(t)
	outside := filepath.Join(filepath.Dir(source), "outside")
	write(t, filepath.Join(outside, "secret.txt"), "not part of the folder", 0600)
	write(t, filepath.Join(source, "real.txt"), "real", 0644)

	links := map[string]string{
		"to-file":    "real.txt",
		"to-outside": outside,
		"dangling":   "/does/not/exist",
		"sub/up":     "../real.txt",
	}
	os.Mkdir(filepath.Join(source, "sub"), 0755)
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(source, name)); err != nil {
			t.Fatal(err)
		}
	}

	// things that are not files to keep
	if err := syscall.Mkfifo(filepath.Join(source, "a-fifo"), 0644); err != nil {
		t.Fatal(err)
	}
	// a short path: socket names are limited to about a hundred bytes
	socketDir, err := os.MkdirTemp(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(socketDir, "sock"))
	if err == nil {
		defer listener.Close()
		if err := os.Rename(filepath.Join(socketDir, "sock"), filepath.Join(source, "a-socket")); err != nil {
			t.Fatal(err)
		}
	}

	for round, now := range []time.Time{day(12), day(13)} {
		dir, meta := snap(t, cfg, source, now)
		for name, target := range links {
			info, err := os.Lstat(filepath.Join(dir, name))
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Errorf("round %d: %s is not a symbolic link in the snapshot (%v)", round, name, err)
				continue
			}
			if got, _ := os.Readlink(filepath.Join(dir, name)); got != target {
				t.Errorf("round %d: %s points to %q, want %q", round, name, got, target)
			}
		}
		filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if entry != nil && entry.Name() == "secret.txt" {
				t.Errorf("round %d: a symbolic link was followed: %s", round, path)
			}
			return nil
		})
		for _, name := range []string{"a-fifo", "a-socket"} {
			if exists(filepath.Join(dir, name)) {
				t.Errorf("round %d: %s was copied", round, name)
			}
		}
		// real.txt and the four links
		if meta.Files != 5 || meta.Errors != 0 {
			t.Errorf("round %d: %+v", round, meta)
		}
	}
	if got := read(t, filepath.Join(outside, "secret.txt")); got != "not part of the folder" {
		t.Errorf("the folder a link points to was touched: %q", got)
	}
}

// A folder that others can write to (the data of an app) may change under
// the walk. Something put in the place of an entry between the moment it was
// looked at and the moment it is opened must not lead the copy elsewhere.
func TestEntryReplacedDuringTheWalk(t *testing.T) {
	swap := func(t *testing.T, path string, target string) {
		t.Helper()
		if err := os.Rename(path, path+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name    string
		replace string                              // the entry that is replaced when the walk reaches it
		target  func(source, outside string) string // by a link to this
	}{
		{"a folder, by a link out of the tree", "sub", func(source, outside string) string { return outside }},
		{"a folder, by a relative link out of the tree", "sub", func(source, outside string) string { return "../outside" }},
		{"a folder, by a link to another folder of the tree", "sub", func(source, outside string) string { return "other" }},
		{"a deeper folder, by a link out of the tree", "other/deep", func(source, outside string) string { return outside }},
		{"a file, by a link out of the tree", "plain.txt", func(source, outside string) string { return filepath.Join(outside, "secret.txt") }},
		{"a file, by a link to another file of the tree", "plain.txt", func(source, outside string) string { return "other/inside.txt" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, source := fixture(t)
			outside := filepath.Join(filepath.Dir(source), "outside")
			write(t, filepath.Join(outside, "secret.txt"), "a secret of the host", 0600)
			write(t, filepath.Join(source, "plain.txt"), "plain", 0644)
			write(t, filepath.Join(source, "sub", "file.txt"), "in sub", 0644)
			write(t, filepath.Join(source, "other", "inside.txt"), "in other", 0644)
			write(t, filepath.Join(source, "other", "deep", "file.txt"), "deep", 0644)
			write(t, filepath.Join(source, "zz-last.txt"), "after the replaced entry", 0644)

			done := false
			betweenLookAndOpen = func(rel string) {
				if rel == c.replace && !done {
					done = true
					swap(t, filepath.Join(source, c.replace), c.target(source, outside))
				}
			}
			defer func() { betweenLookAndOpen = nil }()

			dir, meta := snap(t, cfg, source, day(12))
			if !done {
				t.Fatal("the entry was never reached")
			}
			if meta.Errors != 1 || len(meta.ErrorMessages) != 1 || !strings.HasPrefix(meta.ErrorMessages[0], c.replace+": ") {
				t.Errorf("record: %+v", meta)
			}
			// nothing was read through the link, and the entry is simply missing
			if exists(filepath.Join(dir, c.replace)) {
				t.Errorf("%s is in the snapshot", c.replace)
			}
			inside := 0
			filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
				if entry == nil || entry.IsDir() {
					return nil
				}
				if entry.Name() == "secret.txt" || strings.Contains(read(t, path), "a secret of the host") {
					t.Errorf("a file from outside the tree is in the snapshot: %s", path)
				}
				if entry.Name() == "inside.txt" {
					inside++
				}
				return nil
			})
			if inside != 1 {
				t.Errorf("other/inside.txt is in the snapshot %d times", inside)
			}
			// the walk went on with the rest
			if got := read(t, filepath.Join(dir, "zz-last.txt")); got != "after the replaced entry" {
				t.Errorf("what came after the replaced entry: %q", got)
			}
		})
	}

	// the folder to back up itself, replaced after it was looked at
	base := t.TempDir()
	first, second := filepath.Join(base, "first"), filepath.Join(base, "second")
	os.Mkdir(first, 0755)
	os.Mkdir(second, 0755)
	looked, _ := os.Lstat(first)
	if top, err := openTree(second, looked); err == nil {
		top.Close()
		t.Error("a folder other than the one that was checked was opened")
	}
	if top, err := openTree(first, looked); err != nil {
		t.Errorf("the folder that was checked: %v", err)
	} else {
		top.Close()
	}
}

// The tree that is written may be within reach of others too. A folder of
// it swapped for a link while the copy is under way must not send what
// Cosmos writes, as root, somewhere else.
func TestTargetReplacedDuringTheWalk(t *testing.T) {
	prepare := func(t *testing.T) (cfg utils.SnapshotsConfig, source string, outside string, untouched func()) {
		cfg, source = fixture(t)
		outside = filepath.Join(filepath.Dir(source), "outside")
		write(t, filepath.Join(outside, "theirs.txt"), "not to be touched", 0644)
		os.Chmod(outside, 0755)
		write(t, filepath.Join(source, "sub", "a.txt"), "a", 0640)
		write(t, filepath.Join(source, "sub", "b.txt"), "b", 0640)
		write(t, filepath.Join(source, "sub", "deeper", "c.txt"), "c", 0640)
		os.Symlink("a.txt", filepath.Join(source, "sub", "link"))
		os.Chmod(filepath.Join(source, "sub"), 0700)
		before := describe(t, outside)
		return cfg, source, outside, func() {
			t.Helper()
			if after := describe(t, outside); !reflect.DeepEqual(after, before) {
				t.Errorf("something was written outside the tree:\nbefore %v\nafter  %v", before, after)
			}
		}
	}
	swapOnce := func(t *testing.T, at string, path func() string, target string) {
		done := false
		betweenLookAndOpen = func(rel string) {
			if rel != at || done {
				return
			}
			done = true
			if err := os.Rename(path(), path()+".moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path()); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			betweenLookAndOpen = nil
			if !done {
				t.Error("the moment to swap the folder never came")
			}
		})
	}
	partial := func(cfg utils.SnapshotsConfig, source string) string {
		matches, _ := filepath.Glob(filepath.Join(cfg.Destination, Slug(source), "*"+partialExt))
		if len(matches) != 1 {
			return filepath.Join(cfg.Destination, "no-unfinished-snapshot")
		}
		return matches[0]
	}

	t.Run("a first snapshot: files are copied", func(t *testing.T) {
		cfg, source, outside, untouched := prepare(t)
		swapOnce(t, "sub/b.txt", func() string { return filepath.Join(partial(cfg, source), "sub") }, outside)
		id, _, err := snapshotSource(cfg, source, day(12))
		untouched()
		// the folder that was written to is no longer where it should be: the snapshot is not kept
		if err == nil {
			t.Errorf("a snapshot %s was kept although its tree was tampered with", id)
		}
		if list := List(cfg.Destination, source); len(list) != 0 {
			t.Errorf("snapshots listed: %+v", list)
		}
	})

	t.Run("a later snapshot: files are linked", func(t *testing.T) {
		cfg, source, outside, untouched := prepare(t)
		first, _ := snap(t, cfg, source, day(12))
		swapOnce(t, "sub/b.txt", func() string { return filepath.Join(partial(cfg, source), "sub") }, outside)
		id, _, err := snapshotSource(cfg, source, day(13))
		untouched()
		if err == nil {
			t.Errorf("a snapshot %s was kept although its tree was tampered with", id)
		}
		if list := List(cfg.Destination, source); len(list) != 1 || list[0].ID != filepath.Base(first) {
			t.Errorf("snapshots listed: %+v", list)
		}
		if got := read(t, filepath.Join(first, "sub", "b.txt")); got != "b" {
			t.Errorf("the earlier snapshot was changed: %q", got)
		}
	})

	t.Run("a restore", func(t *testing.T) {
		cfg, source, outside, untouched := prepare(t)
		useConfig(t, cfg)
		first, _ := snap(t, cfg, source, day(12))
		id := filepath.Base(first)
		write(t, filepath.Join(source, "sub", "a.txt"), "changed since", 0640)
		swapOnce(t, "sub/b.txt", func() string { return filepath.Join(source+".restoring-"+id, "sub") }, outside)
		err := Restore(source, id)
		untouched()
		if err == nil {
			t.Error("the restore reported success although its copy was tampered with")
		}
		// nothing was swapped in: the folder is as it was
		if got := read(t, filepath.Join(source, "sub", "a.txt")); got != "changed since" {
			t.Errorf("the folder after the failed restore: %q", got)
		}
		if kept, _ := filepath.Glob(source + ".before-restore-*"); len(kept) != 0 {
			t.Errorf("the folder was moved aside: %v", kept)
		}
	})

	t.Run("a restore, the copy itself replaced", func(t *testing.T) {
		cfg, source, outside, untouched := prepare(t)
		useConfig(t, cfg)
		first, _ := snap(t, cfg, source, day(12))
		id := filepath.Base(first)
		swapOnce(t, "sub", func() string { return source + ".restoring-" + id }, outside)
		err := Restore(source, id)
		untouched()
		if err == nil {
			t.Error("the restore reported success although its copy was replaced")
		}
		if got := read(t, filepath.Join(source, "sub", "a.txt")); got != "a" {
			t.Errorf("the folder after the restore (%v): %q", err, got)
		}
		if exists(filepath.Join(source, "theirs.txt")) {
			t.Error("the folder that was swapped in was put in place of the restored one")
		}
	})

	// the same with a link that stays next to the folder: it is not followed
	// to give another folder the restored one's mode, nor put in its place
	t.Run("a restore, the copy replaced by a link to a folder next to it", func(t *testing.T) {
		cfg, source, outside, _ := prepare(t)
		useConfig(t, cfg)
		os.Chmod(outside, 0700)
		os.Chmod(source, 0755)
		first, _ := snap(t, cfg, source, day(12))
		id := filepath.Base(first)
		write(t, filepath.Join(source, "sub", "a.txt"), "changed since", 0640)
		before := describe(t, outside)
		swapOnce(t, "sub", func() string { return source + ".restoring-" + id }, "outside")
		err := Restore(source, id)
		if after := describe(t, outside); !reflect.DeepEqual(after, before) {
			t.Errorf("the folder next to it was changed:\nbefore %v\nafter  %v", before, after)
		}
		if err == nil {
			t.Error("the restore reported success although its copy was replaced")
		}
		if info, errStat := os.Lstat(source); errStat != nil || !info.IsDir() {
			t.Errorf("the folder is no longer a folder: %v", errStat)
		}
		if got := read(t, filepath.Join(source, "sub", "a.txt")); got != "changed since" {
			t.Errorf("the folder after the failed restore: %q", got)
		}
	})
}

func TestModeAndTimeArePreserved(t *testing.T) {
	cfg, source := fixture(t)
	old := time.Date(2020, 2, 29, 12, 34, 56, 123456789, time.Local)
	older := time.Date(2019, 1, 2, 3, 4, 5, 0, time.Local)

	files := map[string]os.FileMode{
		"plain.txt":       0644,
		"private.key":     0600,
		"script.sh":       0755,
		"readonly.txt":    0444,
		"dir/setuid-tool": 0755 | os.ModeSetuid,
	}
	for name, mode := range files {
		write(t, filepath.Join(source, name), "content of "+name, 0600)
		if err := os.Chmod(filepath.Join(source, name), mode); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(filepath.Join(source, name), old, old)
	}
	dirs := map[string]os.FileMode{
		"dir":     0750,
		"shared":  0775 | os.ModeSetgid,
		"private": 0700,
		"sticky":  0777 | os.ModeSticky,
	}
	for name, mode := range dirs {
		os.MkdirAll(filepath.Join(source, name), 0755)
		if err := os.Chmod(filepath.Join(source, name), mode); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(filepath.Join(source, name), older, older)
	}
	os.Chmod(source, 0751)
	os.Chtimes(source, older, older)

	check := func(dir string) {
		t.Helper()
		for name, mode := range files {
			info, err := os.Lstat(filepath.Join(dir, name))
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if info.Mode() != mode {
				t.Errorf("%s has mode %v, want %v", name, info.Mode(), mode)
			}
			if !info.ModTime().Equal(old) {
				t.Errorf("%s has time %v, want %v", name, info.ModTime(), old)
			}
		}
		for name, mode := range dirs {
			info, err := os.Lstat(filepath.Join(dir, name))
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if info.Mode() != mode|os.ModeDir {
				t.Errorf("%s has mode %v, want %v", name, info.Mode(), mode|os.ModeDir)
			}
			if !info.ModTime().Equal(older) {
				t.Errorf("%s has time %v, want %v", name, info.ModTime(), older)
			}
		}
		// the snapshot itself has the mode of the folder; its time moves when the record is written
		if info, _ := os.Lstat(dir); info.Mode().Perm() != 0751 {
			t.Errorf("the snapshot has mode %o, want 751", info.Mode().Perm())
		}
	}

	first, _ := snap(t, cfg, source, day(12))
	check(first)
	second, meta := snap(t, cfg, source, day(13))
	check(second)
	if meta.BytesCopied != 0 {
		t.Errorf("nothing changed, yet %d bytes were copied again", meta.BytesCopied)
	}

	// the folder holding the snapshots is for its owner only
	if info, _ := os.Lstat(filepath.Dir(first)); info.Mode().Perm() != 0700 {
		t.Errorf("%s has mode %o, want 700", filepath.Dir(first), info.Mode().Perm())
	}
}

func TestDestinationInsideTheSourceIsNotCopiedIntoItself(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "data")
	destination := filepath.Join(source, "backups", "snapshots")
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	write(t, filepath.Join(source, "backups", "other.txt"), "next to the destination", 0644)
	cfg := utils.SnapshotsConfig{Enabled: true, Destination: destination, Sources: []string{source}}
	if err := ValidateConfig(&cfg); err != nil {
		t.Fatalf("a destination inside a source must be accepted: %v", err)
	}

	for round, now := range []time.Time{day(12), day(13), day(14)} {
		dir, meta := snap(t, cfg, source, now)
		if exists(filepath.Join(dir, "backups", "snapshots")) {
			t.Fatalf("round %d: the destination was copied into its own snapshot", round)
		}
		if got := read(t, filepath.Join(dir, "backups", "other.txt")); got != "next to the destination" {
			t.Errorf("round %d: what lies next to the destination is missing: %q", round, got)
		}
		if meta.Files != 2 {
			t.Errorf("round %d: %d files, want 2", round, meta.Files)
		}
	}

	// the same folder reached under another name is recognised too
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	real, _ := os.Stat(destination)
	viaAlias, _ := os.Stat(filepath.Join(alias, "backups", "snapshots"))
	w := &walker{skipPath: destination, skipInfo: real}
	if !w.isSkipped(filepath.Join(alias, "backups", "snapshots"), viaAlias) {
		t.Error("the destination is not recognised under another name")
	}
	if other, _ := os.Stat(filepath.Join(alias, "backups")); w.isSkipped(filepath.Join(alias, "backups"), other) {
		t.Error("a folder that is not the destination is skipped")
	}
}

func TestPartialLeftoversAreIgnoredAndCleaned(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	first, _ := snap(t, cfg, source, day(12))
	dir := filepath.Dir(first)

	// an interrupted run, newer than the finished snapshot
	leftover := filepath.Join(dir, "2026-11-12_050000"+partialExt)
	write(t, filepath.Join(leftover, "file.txt"), "half a copy", 0644)
	write(t, filepath.Join(leftover, "sub", "more.txt"), "x", 0644)
	// things that are nobody's snapshot: never listed, never removed
	write(t, filepath.Join(dir, "notes.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "my-folder", "keep.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "2026-11-12_050000.partial.bak", "keep.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "2026-11-12_0500", "keep.txt"), "mine", 0644)
	write(t, filepath.Join(dir, "2026-13-45_999999", "keep.txt"), "mine", 0644) // looks like a date, is none
	write(t, filepath.Join(dir, "2026-11-12_070000"), "a file, not a folder", 0644)

	list := List(cfg.Destination, source)
	if len(list) != 1 || list[0].ID != filepath.Base(first) {
		t.Fatalf("listing with leftovers: %+v", list)
	}

	second, meta := snap(t, cfg, source, day(13))
	if exists(leftover) {
		t.Error("the unfinished snapshot is still there after the next run")
	}
	// the unfinished one was not used as the previous snapshot
	if !sameFile(t, filepath.Join(first, "file.txt"), filepath.Join(second, "file.txt")) || meta.BytesCopied != 0 {
		t.Error("the new snapshot is not linked to the last finished one")
	}
	for _, kept := range []string{"notes.txt", "my-folder/keep.txt", "2026-11-12_050000.partial.bak/keep.txt", "2026-11-12_0500/keep.txt", "2026-13-45_999999/keep.txt", "2026-11-12_070000"} {
		if !exists(filepath.Join(dir, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
	if list := List(cfg.Destination, source); len(list) != 2 {
		t.Errorf("listing after the second run: %+v", list)
	}
}

func TestSnapshotFailsWithoutTouchingAnything(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)

	// a folder that is not there, a file, a link to a folder
	file := filepath.Join(filepath.Dir(source), "a-file")
	write(t, file, "x", 0644)
	link := filepath.Join(filepath.Dir(source), "a-link")
	os.Symlink(source, link)
	for _, bad := range []string{filepath.Join(filepath.Dir(source), "missing"), file, link} {
		if id, _, err := snapshotSource(cfg, bad, day(12)); err == nil {
			t.Errorf("%s was backed up as %s", bad, id)
		}
		if exists(filepath.Join(cfg.Destination, Slug(bad))) {
			t.Errorf("a folder was made in the destination for %s", bad)
		}
	}

	// a destination that is gone is not created again: its disk may be missing
	gone := cfg
	gone.Destination = filepath.Join(filepath.Dir(source), "not-mounted")
	if id, _, err := snapshotSource(gone, source, day(12)); err == nil {
		t.Errorf("a snapshot %s was made in a destination that does not exist", id)
	}
	if exists(gone.Destination) {
		t.Error("the missing destination was created by the run")
	}
}

func TestTwoSnapshotsInTheSameSecond(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	first, _ := snap(t, cfg, source, day(12))
	second, meta := snap(t, cfg, source, day(12))
	if filepath.Base(first) != "2026-11-12_030000" || filepath.Base(second) != "2026-11-12_030001" || !exists(second) {
		t.Errorf("two runs in one second: %s and %s", first, second)
	}
	// the second one was linked to the first before the retention removed the
	// first: of one day, only the newest snapshot stays
	if meta.BytesCopied != 0 || meta.BytesLinked != int64(len("content")) {
		t.Errorf("the second snapshot: %+v", meta)
	}
	if exists(first) {
		t.Error("the older snapshot of the same day was kept")
	}
	if got := read(t, filepath.Join(second, "file.txt")); got != "content" {
		t.Errorf("the file in the snapshot that stays: %q", got)
	}
}

func TestSnapshotNamesOnlyGoForward(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	newest, _ := snap(t, cfg, source, day(14))

	// the clock was set back two days: the new snapshot still comes after the
	// newest one, so it is neither taken for an old one and removed, nor does
	// it push the newer one out
	back, meta := snap(t, cfg, source, day(12))
	if filepath.Base(back) <= filepath.Base(newest) {
		t.Errorf("a snapshot named %s was made after %s", filepath.Base(back), filepath.Base(newest))
	}
	if !exists(back) || !validID(filepath.Base(back)) {
		t.Errorf("the new snapshot %s is gone or misnamed", back)
	}
	if meta.BytesLinked != int64(len("content")) || !meta.Started.Equal(day(12)) {
		t.Errorf("the new snapshot: %+v", meta)
	}
	if list := List(cfg.Destination, source); len(list) == 0 || list[0].ID != filepath.Base(back) {
		t.Errorf("listing: %+v", list)
	}
}

func TestReservedNameIsReportedNotCopied(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, metaName), "the folder's own file of that name", 0644)
	write(t, filepath.Join(source, "sub", metaName), "deeper it is an ordinary file", 0644)

	dir, meta := snap(t, cfg, source, day(12))
	if meta.Errors != 1 || len(meta.ErrorMessages) != 1 || !strings.HasPrefix(meta.ErrorMessages[0], metaName+": ") {
		t.Errorf("record: %+v", meta)
	}
	if got := read(t, filepath.Join(dir, "sub", metaName)); got != "deeper it is an ordinary file" {
		t.Errorf("the file below the top was not copied: %q", got)
	}
	if strings.Contains(read(t, filepath.Join(dir, metaName)), "own file") {
		t.Error("the snapshot's record was replaced by the folder's file")
	}
}

func TestRunAll(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	missing := filepath.Join(filepath.Dir(source), "missing")
	cfg.Sources = append(cfg.Sources, missing)

	notices := []string{}
	realNotify := notifyFailure
	notifyFailure = func(summary string) { notices = append(notices, summary) }
	defer func() {
		notifyFailure = realNotify
		utils.LoadBaseMainConfig(utils.Config{})
	}()

	// off, or incomplete: nothing runs
	for _, off := range []utils.SnapshotsConfig{
		{},
		{Enabled: false, Destination: cfg.Destination, Sources: cfg.Sources},
		{Enabled: true, Sources: cfg.Sources},
		{Enabled: true, Destination: cfg.Destination},
	} {
		utils.LoadBaseMainConfig(utils.Config{Snapshots: off})
		if err := RunAll(); err != ErrNotConfigured {
			t.Errorf("RunAll with %+v: %v", off, err)
		}
		if err := Start(); err != ErrNotConfigured {
			t.Errorf("Start with %+v: %v", off, err)
		}
	}
	if entries, _ := os.ReadDir(cfg.Destination); len(entries) != 0 || len(notices) != 0 {
		t.Fatalf("something ran while the module was off: %v, %v", entries, notices)
	}

	// on, but edited by hand into something unusable: nothing runs, and the
	// admins are told when the schedule finds out (a request gets the answer)
	for _, broken := range []utils.SnapshotsConfig{
		{Enabled: true, Destination: "relative", Sources: cfg.Sources},
		{Enabled: true, Destination: cfg.Destination, Sources: []string{cfg.Destination + "/inside"}},
		{Enabled: true, Destination: cfg.Destination, Sources: cfg.Sources, Time: "9:99"},
		{Enabled: true, Destination: cfg.Destination, Sources: cfg.Sources, KeepDaily: -5},
		{Enabled: true, Destination: cfg.Destination, Sources: []string{"/"}},
	} {
		utils.LoadBaseMainConfig(utils.Config{Snapshots: broken})
		notices = nil
		if err := Start(); err == nil || !strings.Contains(err.Error(), errUnusable.Error()) {
			t.Errorf("Start with %+v: %v", broken, err)
		}
		if len(notices) != 0 {
			t.Errorf("Start with %+v sent a notice: %q", broken, notices)
		}
		if err := RunAll(); err == nil || !strings.Contains(err.Error(), errUnusable.Error()) {
			t.Errorf("RunAll with %+v: %v", broken, err)
		}
		if len(notices) != 1 || !strings.Contains(notices[0], "cannot be used") {
			t.Errorf("RunAll with %+v: notices %q", broken, notices)
		}
	}
	notices = nil
	if entries, _ := os.ReadDir(cfg.Destination); len(entries) != 0 {
		t.Fatalf("something ran with unusable settings: %v", entries)
	}
	if runMu.TryLock() {
		runMu.Unlock()
	} else {
		t.Fatal("a run that did not start kept the lock")
	}

	utils.LoadBaseMainConfig(utils.Config{Snapshots: cfg})

	// while something else runs
	runMu.Lock()
	if err := RunAll(); err != ErrBusy {
		t.Errorf("RunAll while busy: %v", err)
	}
	if err := Start(); err != ErrBusy {
		t.Errorf("Start while busy: %v", err)
	}
	runMu.Unlock()

	// one folder works, the other does not exist: one notice for the run
	if err := RunAll(); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], missing) || strings.Contains(notices[0], source+":") {
		t.Errorf("notices: %q", notices)
	}
	st := GetStatus()
	if st.Running || st.Current != "" || st.StartedAt != nil {
		t.Errorf("status after the run: %+v", st)
	}
	if good := st.LastResults[source]; good.Snapshot == "" || good.Error != "" || good.Errors != 0 {
		t.Errorf("result of the folder that exists: %+v", good)
	}
	if bad := st.LastResults[missing]; bad.Snapshot != "" || bad.Error == "" {
		t.Errorf("result of the folder that does not exist: %+v", bad)
	}
	if len(List(cfg.Destination, source)) != 1 || len(List(cfg.Destination, missing)) != 0 {
		t.Error("unexpected snapshots after the run")
	}
	newest := func() string {
		list := List(cfg.Destination, source)
		if len(list) == 0 {
			t.Fatal("no snapshot left")
		}
		return list[0].ID
	}
	last := newest()

	// a clean run: no notice, and the folder that was dropped is forgotten
	cfg.Sources = []string{source}
	utils.LoadBaseMainConfig(utils.Config{Snapshots: cfg})
	if err := RunAll(); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Errorf("a clean run sent a notice: %q", notices)
	}
	if _, kept := GetStatus().LastResults[missing]; kept {
		t.Error("the result of a folder that is no longer configured is still reported")
	}
	// (the runs of this test happen on one day, so each replaces the one before)
	if id := newest(); id <= last {
		t.Errorf("the second run made no snapshot: newest is %s, was %s", id, last)
	}
	last = newest()

	// files that could not be backed up: one notice, and the snapshot is kept
	write(t, filepath.Join(source, metaName), "reserved", 0644)
	if err := RunAll(); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 2 || !strings.Contains(notices[1], source) {
		t.Errorf("notices after a run with errors: %q", notices)
	}
	if result := GetStatus().LastResults[source]; result.Snapshot != newest() || result.Snapshot <= last || result.Errors != 1 || result.Error == "" {
		t.Errorf("result of a run with errors: %+v", result)
	}
	last = newest()

	// Start does the same in the background and returns at once
	os.Remove(filepath.Join(source, metaName))
	if err := Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500 && GetStatus().Running; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if GetStatus().Running || newest() <= last {
		t.Errorf("after Start: running=%v, newest snapshot %s, was %s", GetStatus().Running, newest(), last)
	}
}
