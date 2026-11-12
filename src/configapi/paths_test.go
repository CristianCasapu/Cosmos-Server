package configapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestCleanHostDirectory(t *testing.T) {
	good := map[string]string{
		"":                     "",
		"/storage/backups":     "/storage/backups",
		"/storage//backups/":   "/storage/backups",
		"/storage/./backups":   "/storage/backups",
		"/with space/and.dots": "/with space/and.dots",
	}
	for value, want := range good {
		got, err := cleanHostDirectory("Dir", value)
		if err != nil || got != want {
			t.Errorf("cleanHostDirectory(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"relative/path", "./here", "/a/../etc", "/a/..", "/tmp/x\n/y", "/tmp/x\x00", " /tmp/x", "/tmp/x "} {
		if got, err := cleanHostDirectory("Dir", value); err == nil {
			t.Errorf("cleanHostDirectory(%q) = %q, want an error", value, got)
		}
	}
}

func TestPrepareConfigDirectories(t *testing.T) {
	base := t.TempDir()
	current := utils.Config{}

	// missing folder, nested: created, and stored in its plain form
	request := utils.Config{BackupOutputDir: base + "//new/deeper/"}
	if err := prepareConfigDirectories(&request, current); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "new", "deeper")
	if request.BackupOutputDir != want {
		t.Errorf("stored %q, want %q", request.BackupOutputDir, want)
	}
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s was not created: %v", want, err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("created with mode %o, want 700", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(want); len(entries) != 0 {
		t.Errorf("the write test left files behind: %v", entries)
	}

	// something that cannot be a folder: refused, with the reason and without the path twice
	file := filepath.Join(base, "a-file")
	os.WriteFile(file, []byte("x"), 0600)
	for _, dir := range []string{file, filepath.Join(file, "below")} {
		request = utils.Config{BackupOutputDir: dir}
		err := prepareConfigDirectories(&request, current)
		if err == nil {
			t.Errorf("%s was accepted", dir)
		} else if strings.Count(err.Error(), dir) != 1 {
			t.Errorf("message should name the path once: %s", err)
		}
	}

	// unchanged value: left alone even if the folder is gone (a save of other settings must not fail)
	gone := filepath.Join(base, "gone")
	request = utils.Config{BackupOutputDir: gone}
	if err := prepareConfigDirectories(&request, utils.Config{BackupOutputDir: gone}); err != nil {
		t.Errorf("unchanged directory was checked: %v", err)
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("unchanged directory was created")
	}

	// invalid values are refused before anything is created
	for _, dir := range []string{"backups", "/", base + "/../x"} {
		request = utils.Config{BackupOutputDir: dir}
		if err := prepareConfigDirectories(&request, current); err == nil {
			t.Errorf("%q was accepted", dir)
		}
	}

	// the data path is only checked for form, never created
	request = utils.Config{}
	request.DockerConfig.DefaultDataPath = base + "/data//"
	if err := prepareConfigDirectories(&request, current); err != nil {
		t.Fatal(err)
	}
	if request.DockerConfig.DefaultDataPath != base+"/data" {
		t.Errorf("data path stored as %q", request.DockerConfig.DefaultDataPath)
	}
	if _, err := os.Stat(base + "/data"); err == nil {
		t.Error("the data path was created")
	}
	request.DockerConfig.DefaultDataPath = "data"
	if err := prepareConfigDirectories(&request, current); err == nil {
		t.Error("a relative data path was accepted")
	}
}
