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

// The incremental snapshots (src/snapshots) are checked by the same hook.
func TestPrepareConfigDirectoriesSnapshots(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "data")
	os.Mkdir(source, 0755)

	// switched on with a destination that does not exist: created, owner only, stored in its plain form
	request := utils.Config{}
	request.Snapshots = utils.SnapshotsConfig{Enabled: true, Destination: base + "//snapshots/here/", Sources: []string{source + "/"}}
	if err := prepareConfigDirectories(&request, utils.Config{}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(base, "snapshots", "here")
	if request.Snapshots.Destination != destination || len(request.Snapshots.Sources) != 1 || request.Snapshots.Sources[0] != source {
		t.Errorf("stored %+v", request.Snapshots)
	}
	info, err := os.Stat(destination)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s was not created: %v", destination, err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("created with mode %o, want 700", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(destination); len(entries) != 0 {
		t.Errorf("the write test left files behind: %v", entries)
	}

	// settings that cannot work are refused with a reason that names the module, and nothing is created
	bad := map[string]utils.SnapshotsConfig{
		"relative destination":         {Enabled: true, Destination: "snapshots", Sources: []string{source}},
		"no folder":                    {Enabled: true, Destination: filepath.Join(base, "new-1")},
		"folder inside destination":    {Enabled: true, Destination: filepath.Join(base, "new-2"), Sources: []string{filepath.Join(base, "new-2", "x")}},
		"destination is a folder":      {Enabled: true, Destination: filepath.Join(base, "new-3"), Sources: []string{filepath.Join(base, "new-3")}},
		"traversal":                    {Enabled: true, Destination: filepath.Join(base, "new-4"), Sources: []string{base + "/../etc"}},
		"bad time":                     {Enabled: true, Destination: filepath.Join(base, "new-5"), Sources: []string{source}, Time: "25:00"},
		"bad paths while switched off": {Destination: "snapshots", Sources: []string{"data"}},
	}
	for name, snapshots := range bad {
		request = utils.Config{Snapshots: snapshots}
		err := prepareConfigDirectories(&request, utils.Config{})
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.HasPrefix(err.Error(), "Incremental backup") {
			t.Errorf("%s: the reason does not say what it is about: %s", name, err)
		}
	}
	for i := 1; i <= 5; i++ {
		if _, err := os.Stat(filepath.Join(base, "new-"+string(rune('0'+i)))); err == nil {
			t.Errorf("new-%d was created for settings that were refused", i)
		}
	}

	// a destination that cannot be a folder: refused
	file := filepath.Join(base, "a-file")
	os.WriteFile(file, []byte("x"), 0600)
	request = utils.Config{Snapshots: utils.SnapshotsConfig{Enabled: true, Destination: file, Sources: []string{source}}}
	if err := prepareConfigDirectories(&request, utils.Config{}); err == nil {
		t.Error("a file was accepted as the destination")
	}

	// unchanged settings: a destination that is gone (its disk is not mounted) is not created
	// again, and saving other settings does not fail
	gone := utils.SnapshotsConfig{Enabled: true, Destination: filepath.Join(base, "gone"), Sources: []string{source}}
	request = utils.Config{Snapshots: gone}
	if err := prepareConfigDirectories(&request, utils.Config{Snapshots: gone}); err != nil {
		t.Errorf("unchanged settings were checked again: %v", err)
	}
	if _, err := os.Stat(gone.Destination); err == nil {
		t.Error("an unchanged destination was created")
	}

	// switched off: nothing is created
	off := gone
	off.Enabled = false
	request = utils.Config{Snapshots: off}
	if err := prepareConfigDirectories(&request, utils.Config{}); err != nil {
		t.Errorf("switched off: %v", err)
	}
	if _, err := os.Stat(gone.Destination); err == nil {
		t.Error("the destination was created while the snapshots are off")
	}

	// switched on again, or pointed somewhere else: created
	request = utils.Config{Snapshots: gone}
	if err := prepareConfigDirectories(&request, utils.Config{Snapshots: off}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone.Destination); err != nil {
		t.Error("the destination was not created when the snapshots were switched on")
	}
	moved := gone
	moved.Destination = filepath.Join(base, "moved")
	request = utils.Config{Snapshots: moved}
	if err := prepareConfigDirectories(&request, utils.Config{Snapshots: gone}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved.Destination); err != nil {
		t.Error("the new destination was not created")
	}
}
