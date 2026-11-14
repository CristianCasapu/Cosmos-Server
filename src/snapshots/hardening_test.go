package snapshots

import (
	"os"
	"path/filepath"
	"strings"
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
