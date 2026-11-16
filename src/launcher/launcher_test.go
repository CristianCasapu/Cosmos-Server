package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/azukaar/cosmos-server/src/forkrelease"
)

func writeZip(t *testing.T, dir string, names ...string) string {
	t.Helper()
	p := filepath.Join(dir, "u.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, n := range names {
		e, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		e.Write([]byte("x"))
	}
	w.Close()
	f.Close()
	return p
}

func TestUnzipRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"cosmos/../../evil", "../evil", "/etc/evil"} {
		tmp := t.TempDir()
		dest := filepath.Join(tmp, "dest")
		os.Mkdir(dest, 0o755)
		z := writeZip(t, tmp, "cosmos/ok.txt", bad)
		if err := unzip(z, dest); err == nil {
			t.Errorf("entry %q accepted, want rejection", bad)
		}
		if _, err := os.Stat(filepath.Join(tmp, "evil")); err == nil {
			t.Errorf("entry %q escaped to parent dir", bad)
		}
	}
}

func TestUnzipHappyPath(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "dest")
	os.Mkdir(dest, 0o755)
	z := writeZip(t, tmp, "cosmos/a.txt", "cosmos/sub/b.txt", "cosmos/cosmos-launcher")
	if err := unzip(z, dest); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a.txt", "sub/b.txt", "cosmos-launcher.updated"} {
		if _, err := os.Stat(filepath.Join(dest, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
}

// Fork addition: the files the launcher keeps before it unzips are exactly
// the files unzip writes (forkrelease.InstallTargets copies its mapping).
func TestTheKeptFilesAreWhatUnzipWrites(t *testing.T) {
	type entry struct {
		name string
		dir  bool
	}
	zips := map[string][]entry{
		"a release": {{"cosmos-cloud-0.24.0-fork.10/", true}, {"cosmos-cloud-0.24.0-fork.10/cosmos", false},
			{"cosmos-cloud-0.24.0-fork.10/cosmos-launcher", false}, {"cosmos-cloud-0.24.0-fork.10/start.sh", false},
			{"cosmos-cloud-0.24.0-fork.10/meta.json", false}, {"cosmos-cloud-0.24.0-fork.10/static/", true},
			{"cosmos-cloud-0.24.0-fork.10/static/assets/index.js", false}, {"cosmos-cloud-0.24.0-fork.10/images/", true}},
		"an arm64 release": {{"cosmos-cloud-0.24.0-fork.10-arm64/", true}, {"cosmos-cloud-0.24.0-fork.10-arm64/cosmos-launcher-arm64", false},
			{"cosmos-cloud-0.24.0-fork.10-arm64/cosmos", false}},
		"no folder entry first": {{"pkg/cosmos", false}, {"pkg/sub/deeper/file", false}, {"pkg/sub/start.sh", false},
			{"pkg/sub/cosmos-launcher", false}},
		"a file first, without folder":     {{"meta.json", false}, {"cosmos", false}, {"static/a.js", false}},
		"two roots":                        {{"one/a", false}, {"two/b", false}, {"two/cosmos-launcher-arm64", false}},
		"a folder named like the launcher": {{"pkg/", true}, {"pkg/cosmos-launcher/", true}, {"pkg/x", false}},
		"the same file twice":              {{"pkg/a", false}, {"pkg/a", false}},
	}
	for name, entries := range zips {
		tmp := t.TempDir()
		dest := filepath.Join(tmp, "dest")
		os.Mkdir(dest, 0o755)
		p := filepath.Join(tmp, "u.zip")
		f, _ := os.Create(p)
		w := zip.NewWriter(f)
		for _, e := range entries {
			if e.dir {
				w.Create(e.name)
				continue
			}
			x, _ := w.Create(e.name)
			x.Write([]byte("x"))
		}
		w.Close()
		f.Close()

		files, dirs, err := forkrelease.InstallTargets(p, dest)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := unzip(p, dest); err != nil {
			t.Fatalf("%s: unzip: %v", name, err)
		}
		written, made := map[string]bool{}, map[string]bool{}
		filepath.Walk(dest, func(path string, info os.FileInfo, err error) error {
			rel, _ := filepath.Rel(dest, path)
			if info.IsDir() {
				made[rel] = true
			} else {
				written[rel] = true
			}
			return nil
		})
		planned, plannedDirs := map[string]bool{}, map[string]bool{".": true}
		for _, file := range files {
			planned[file] = true
			for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
				plannedDirs[dir] = true
				if dir == "." {
					break
				}
			}
		}
		for _, dir := range dirs {
			plannedDirs[dir] = true
		}
		if fmt.Sprint(sortedKeys(written)) != fmt.Sprint(sortedKeys(planned)) {
			t.Errorf("%s: unzip wrote %v, the backup planned %v", name, sortedKeys(written), sortedKeys(planned))
		}
		if fmt.Sprint(sortedKeys(made)) != fmt.Sprint(sortedKeys(plannedDirs)) {
			t.Errorf("%s: unzip made the folders %v, the backup planned %v", name, sortedKeys(made), sortedKeys(plannedDirs))
		}
	}

	// what unzip refuses, the plan refuses
	for _, bad := range []string{"cosmos/../../evil", "../evil", "/etc/evil"} {
		tmp := t.TempDir()
		dest := filepath.Join(tmp, "dest")
		os.Mkdir(dest, 0o755)
		z := writeZip(t, tmp, "cosmos/ok.txt", bad)
		if _, _, err := forkrelease.InstallTargets(z, dest); err == nil {
			t.Errorf("the plan accepts %q", bad)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
