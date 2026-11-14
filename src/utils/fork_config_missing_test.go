package utils

// Fork addition: tests of fork_config_missing.go.

import (
	"os"
	"strings"
	"testing"
)

func configFolderForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir() + "/"
	prevFolder, prevLoaded, prevBase := CONFIGFOLDER, installedConfigLoaded.Load(), GetBaseMainConfig()
	CONFIGFOLDER = dir
	t.Setenv("CONFIG_FILE", "")
	t.Cleanup(func() {
		CONFIGFOLDER = prevFolder
		installedConfigLoaded.Store(prevLoaded)
		LoadBaseMainConfig(prevBase)
		installedConfigLoaded.Store(prevLoaded)
	})
	return dir
}

func TestMissingConfigWhileRunningKeepsTheSettings(t *testing.T) {
	dir := configFolderForTest(t)
	running := Config{}
	running.HTTPConfig.Hostname = "cosmos.example.com"
	LoadBaseMainConfig(running)

	got := ReadConfigFromFile()
	if got.NewInstall || got.HTTPConfig.Hostname != "cosmos.example.com" {
		t.Errorf("a missing file while running gave NewInstall=%v hostname=%q: the setup wizard would open", got.NewInstall, got.HTTPConfig.Hostname)
	}
	if _, err := os.Stat(dir + "cosmos.config.json"); !os.IsNotExist(err) {
		t.Error("a default configuration was written over the missing one")
	}
}

func TestMissingConfigBesideInstallDatabasesRefusesToStart(t *testing.T) {
	dir := configFolderForTest(t)
	installedConfigLoaded.Store(false)
	if err := os.WriteFile(dir+"auth.db", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	var said string
	previous := configFatal
	configFatal = func(message string, err error) { said = message; panic("fatal") }
	t.Cleanup(func() { configFatal = previous })

	func() {
		defer func() { recover() }()
		refuseDefaultConfig(dir + "cosmos.config.json")
	}()
	if !strings.Contains(said, "does not start as a new install") {
		t.Errorf("start over install databases without a configuration: %q", said)
	}
}

func TestFirstInstallStillCreatesTheDefault(t *testing.T) {
	dir := configFolderForTest(t)
	installedConfigLoaded.Store(false)
	if _, refused := refuseDefaultConfig(dir + "cosmos.config.json"); refused {
		t.Fatal("an empty folder must give a new install")
	}
	if got := ReadConfigFromFile(); !got.NewInstall {
		t.Error("a first install did not start the setup")
	}
}

func TestFailedConfigWriteKeepsRunning(t *testing.T) {
	dir := configFolderForTest(t)
	file := dir + "cosmos.config.json"
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	// the temporary file cannot be made, and neither can the file be written in place
	if err := os.Mkdir(file+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file+".tmp/x", nil, 0600); err != nil {
		t.Fatal(err)
	}
	os.Chmod(file, 0400)
	if os.Geteuid() == 0 {
		// root writes a read-only file anyway: make it a folder instead
		os.Remove(file)
		os.Mkdir(file, 0700)
		os.WriteFile(file+"/x", nil, 0600)
	}
	SaveConfigTofile(Config{}) // must return, not end the process
}
