package snapshots

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A restore that fails takes back only the folder it created. When that
// name leads to another folder by then, nothing is removed and the admin is
// told which name to check.
func TestFailedRestoreLeavesAFolderThatTookTheCopysName(t *testing.T) {
	cfg, source := fixture(t)
	useConfig(t, cfg)
	write(t, filepath.Join(source, "sub", "a.txt"), "a", 0640)
	write(t, filepath.Join(source, "sub", "b.txt"), "b", 0640)
	first, _ := snap(t, cfg, source, day(12))
	id := filepath.Base(first)

	other := filepath.Join(filepath.Dir(source), "other")
	write(t, filepath.Join(other, "keep.txt"), "not to be removed", 0644)
	staging := source + ".restoring-" + id
	done := false
	betweenLookAndOpen = func(rel string) {
		if rel != "sub/b.txt" || done {
			return
		}
		done = true
		if err := os.Rename(staging, staging+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, staging); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { betweenLookAndOpen = nil }()

	err := Restore(source, id)
	if !done {
		t.Fatal("the moment to swap the folder never came")
	}
	if err == nil {
		t.Fatal("the restore reported success although its copy was replaced")
	}
	if got := read(t, filepath.Join(staging, "keep.txt")); got != "not to be removed" {
		t.Errorf("the folder that took the copy's name: %q", got)
	}
	if !strings.Contains(err.Error(), filepath.Base(staging)) || !strings.Contains(err.Error(), "left as it is") {
		t.Errorf("the message does not say which folder was left: %v", err)
	}
}

func TestShownName(t *testing.T) {
	cases := map[string]string{
		"plain/name.txt":       "plain/name.txt",
		"café/ünïcode":         "café/ünïcode",
		"line\nbreak":          "line?break",
		"cr\rtab\tdel\x7f":     "cr?tab?del?",
		"\x1b[31mred":          "?[31mred",
		"c1\u0085next\u2028ls": "c1?next?ls",
		"bad\xffbyte":          "bad?byte",
	}
	for name, want := range cases {
		if got := shownName(name); got != want {
			t.Errorf("shownName(%q) = %q, want %q", name, got, want)
		}
	}
	long := strings.Repeat("é", 400) + "/end.txt"
	got := shownName(long)
	if len(got) > maxShownName+len("…") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "/end.txt") || !utf8Valid(got) {
		t.Errorf("a long name is shown as %q (%d bytes)", got, len(got))
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\x00") == s }

// The names of files that could not be backed up reach the log, the admins'
// notice, the status the API answers with and the snapshot's record: without
// control characters, and not longer than a few hundred bytes.
func TestNamesOfFailedEntriesAreShownSafely(t *testing.T) {
	cfg, source := fixture(t)
	odd := "odd\nline\x1b[2Jname.txt"
	deep := source
	for i := 0; i < 12; i++ {
		deep = filepath.Join(deep, strings.Repeat("d", 200))
	}
	write(t, filepath.Join(source, odd), "x", 0644)
	write(t, filepath.Join(deep, "far.txt"), "x", 0644)
	write(t, filepath.Join(source, "fine.txt"), "x", 0644)
	betweenLookAndOpen = func(rel string) {
		if rel == odd || filepath.Base(rel) == "far.txt" {
			os.Remove(filepath.Join(source, rel)) // gone before it is opened: it cannot be backed up
		}
	}
	defer func() { betweenLookAndOpen = nil }()

	var notices []string
	notifyFailure = func(summary string) { notices = append(notices, summary) }
	defer func() { notifyFailure = func(string) {} }()
	runMu.Lock()
	runAll(cfg) // releases the lock

	result := GetStatus().LastResults[source]
	if result.Errors != 2 || result.Snapshot == "" {
		t.Fatalf("the run: %+v", result)
	}
	record, err := os.ReadFile(filepath.Join(cfg.Destination, Slug(source), result.Snapshot, metaName))
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := readMeta(filepath.Join(cfg.Destination, Slug(source), result.Snapshot))
	if len(meta.ErrorMessages) != 2 || len(notices) != 1 {
		t.Fatalf("messages %q, notices %q", meta.ErrorMessages, notices)
	}
	texts := append([]string{result.Error, notices[0], string(record)}, meta.ErrorMessages...)
	for _, text := range texts {
		for _, r := range text {
			if r == '\x1b' || (r < 0x20 && r != '\n') {
				t.Errorf("a control character is shown: %q", text)
				break
			}
		}
	}
	for _, message := range append([]string{result.Error}, meta.ErrorMessages...) {
		if strings.Contains(message, "\n") {
			t.Errorf("a line break is shown: %q", message)
		}
		if len(message) > maxShownName+200 {
			t.Errorf("a message of %d bytes: %.80q…", len(message), message)
		}
	}
	if !strings.Contains(strings.Join(meta.ErrorMessages, " "), "odd?line?[2Jname.txt") {
		t.Errorf("the odd name is not recognisable: %q", meta.ErrorMessages)
	}
}

// A tree deeper than maxDepth is copied down to that depth; what lies below
// is counted as one problem.
func TestTheWalkStopsAtTheDepthLimit(t *testing.T) {
	cfg, source := fixture(t)
	deepest := source
	for i := 0; i < maxDepth+5; i++ {
		deepest = filepath.Join(deepest, "d")
		if i == maxDepth-1 {
			write(t, filepath.Join(deepest, "last.txt"), "kept", 0644)
		}
	}
	write(t, filepath.Join(deepest, "too-deep.txt"), "not kept", 0644)

	dir, meta := snap(t, cfg, source, day(12))
	if meta.Errors != 1 || len(meta.ErrorMessages) != 1 || !strings.Contains(meta.ErrorMessages[0], "folders deep") {
		t.Fatalf("errors %d, messages %q", meta.Errors, meta.ErrorMessages)
	}
	at := dir
	for i := 0; i < maxDepth; i++ {
		at = filepath.Join(at, "d")
	}
	if got := read(t, filepath.Join(at, "last.txt")); got != "kept" {
		t.Errorf("the deepest file allowed: %q", got)
	}
	if exists(filepath.Join(at, "d")) {
		t.Error("a folder below the limit was copied")
	}
}

// A folder with more entries than are listed at a time is copied whole.
func TestAFolderWithManyEntriesIsCopiedWhole(t *testing.T) {
	cfg, source := fixture(t)
	count := 2*listBatch + 7
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(source, "f"+strconv.Itoa(i)), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	dir, meta := snap(t, cfg, source, day(12))
	entries, err := os.ReadDir(dir)
	if err != nil || meta.Files != int64(count) || meta.Errors != 0 || len(entries) != count+1 { // and the record
		t.Errorf("files %d, errors %d, entries %d, want %d (%v)", meta.Files, meta.Errors, len(entries), count, err)
	}
}

// A file that grows while it is copied is copied at the size it had when it
// was opened; the next snapshot has the rest.
func TestAFileThatGrowsIsCopiedAtItsSizeWhenOpened(t *testing.T) {
	cfg, source := fixture(t)
	path := filepath.Join(source, "growing.log")
	first := strings.Repeat("a", 3<<20)
	write(t, path, first, 0644)
	grown := false
	whileOpen = func(name string) {
		if name != "growing.log" || grown {
			return
		}
		grown = true
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		file.WriteString(strings.Repeat("b", 2<<20))
		file.Close()
	}
	defer func() { whileOpen = nil }()

	dir, meta := snap(t, cfg, source, day(12))
	if !grown {
		t.Fatal("the file was never opened")
	}
	if got := read(t, filepath.Join(dir, "growing.log")); got != first {
		t.Errorf("the copy holds %d bytes, want the %d the file had when it was opened", len(got), len(first))
	}
	if meta.BytesCopied != int64(len(first)) || meta.Errors != 0 {
		t.Errorf("copied %d bytes, %d errors", meta.BytesCopied, meta.Errors)
	}
	dir, _ = snap(t, cfg, source, day(13))
	if got := read(t, filepath.Join(dir, "growing.log")); len(got) != 5<<20 {
		t.Errorf("the next snapshot holds %d bytes, want %d", len(got), 5<<20)
	}
}

// A sparse file stays sparse in the snapshot and in what is restored from it,
// with the same content.
func TestSparseFilesStaySparse(t *testing.T) {
	cfg, source := fixture(t)
	useConfig(t, cfg)
	path := filepath.Join(source, "disk.img")
	const size = 256 << 20
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteAt([]byte("head"), 0)
	file.WriteAt([]byte("middle"), size/2+12345)
	file.Truncate(size)
	file.Close()
	if used := blocksUsed(t, path); used >= size/2 {
		t.Skipf("the file system here does not keep holes (%d bytes used)", used)
	}
	check := func(what string, copy string) {
		t.Helper()
		info, err := os.Stat(copy)
		if err != nil || info.Size() != size {
			t.Fatalf("%s: %v, size %v", what, err, info)
		}
		if used := blocksUsed(t, copy); used > 4<<20 {
			t.Errorf("%s takes %d bytes on the disk", what, used)
		}
		data := read(t, copy)
		if data[:4] != "head" || data[size/2+12345:size/2+12351] != "middle" || strings.Count(data, "\x00") != size-10 {
			t.Errorf("%s does not hold the same content", what)
		}
	}
	dir, _ := snap(t, cfg, source, day(12))
	check("the snapshot", filepath.Join(dir, "disk.img"))
	if err := Restore(source, filepath.Base(dir)); err != nil {
		t.Fatal(err)
	}
	check("the restored file", path)
}

func blocksUsed(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Blocks * 512
}

// The snapshots are only kept where no other user can change them.
func TestDestinationMustBeForCosmosOnly(t *testing.T) {
	t.Run("a destination others can write to is refused", func(t *testing.T) {
		cfg, source := fixture(t)
		write(t, filepath.Join(source, "a.txt"), "a", 0644)
		os.Chmod(cfg.Destination, 0777)
		_, _, err := snapshotSource(cfg, source, day(12))
		if err == nil || !strings.Contains(err.Error(), "chmod go-w") {
			t.Errorf("snapshot into a destination with mode 777: %v", err)
		}
		if entries, _ := os.ReadDir(cfg.Destination); len(entries) != 0 {
			t.Errorf("something was written there: %v", entries)
		}
	})

	t.Run("the folder of a source that others can reach is closed again", func(t *testing.T) {
		cfg, source := fixture(t)
		write(t, filepath.Join(source, "a.txt"), "a", 0644)
		store := filepath.Join(cfg.Destination, Slug(source))
		os.Mkdir(store, 0755)
		os.Chmod(store, 0757)
		dir, _ := snap(t, cfg, source, day(12))
		if info, _ := os.Stat(store); info.Mode().Perm() != 0700 {
			t.Errorf("the folder of the source has mode %o, want 700", info.Mode().Perm())
		}
		if got := read(t, filepath.Join(dir, "a.txt")); got != "a" {
			t.Errorf("the snapshot holds %q", got)
		}
	})

	t.Run("a restore from a folder others can change is refused", func(t *testing.T) {
		cfg, source := fixture(t)
		useConfig(t, cfg)
		write(t, filepath.Join(source, "a.txt"), "a", 0644)
		dir, _ := snap(t, cfg, source, day(12))
		write(t, filepath.Join(source, "a.txt"), "changed", 0644)
		os.Chmod(filepath.Dir(dir), 0770)
		if err := Restore(source, filepath.Base(dir)); err == nil || !strings.Contains(err.Error(), "other users") {
			t.Errorf("restore from a folder with mode 770: %v", err)
		}
		os.Chmod(filepath.Dir(dir), 0700)
		os.Chmod(cfg.Destination, 0775)
		if err := Restore(source, filepath.Base(dir)); err == nil || !strings.Contains(err.Error(), "other users") {
			t.Errorf("restore from a destination with mode 775: %v", err)
		}
		if got := read(t, filepath.Join(source, "a.txt")); got != "changed" {
			t.Errorf("the folder was replaced: %q", got)
		}
	})

	t.Run("folders of another user are refused", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("only root can give a folder to another user")
		}
		cfg, source := fixture(t)
		write(t, filepath.Join(source, "a.txt"), "a", 0644)
		os.Chown(cfg.Destination, 1234, 1234)
		if _, _, err := snapshotSource(cfg, source, day(12)); err == nil || !strings.Contains(err.Error(), "belongs to user 1234") {
			t.Errorf("snapshot into a destination of another user: %v", err)
		}
		os.Chown(cfg.Destination, 0, 0)
		store := filepath.Join(cfg.Destination, Slug(source))
		os.Mkdir(store, 0700)
		os.Chown(store, 1234, 1234)
		if _, _, err := snapshotSource(cfg, source, day(12)); err == nil || !strings.Contains(err.Error(), "belongs to user 1234") {
			t.Errorf("snapshot into a folder of another user: %v", err)
		}
		if entries, _ := os.ReadDir(store); len(entries) != 0 {
			t.Errorf("something was written there: %v", entries)
		}
	})
}
