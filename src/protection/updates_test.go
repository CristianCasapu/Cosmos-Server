package protection

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aptFile(t *testing.T, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(aptConfigDir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAptSettingLastAssignmentWins(t *testing.T) {
	newWorld(t)
	if setting := readAptSetting(); setting.Value != "" || setting.File != "" {
		t.Errorf("nothing set: %+v", setting)
	}
	aptFile(t, "20auto-upgrades", "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n")
	if setting := readAptSetting(); setting.Value != "1" || filepath.Base(setting.File) != "20auto-upgrades" || setting.Changed.IsZero() {
		t.Errorf("Debian's file: %+v", setting)
	}
	// a later file switches it off; a file that sorts before does not count
	aptFile(t, "10early", "APT::Periodic::Unattended-Upgrade \"0\";\n")
	aptFile(t, "99off", "APT::Periodic::Unattended-Upgrade \"0\";\n")
	if setting := readAptSetting(); setting.Value != "0" || filepath.Base(setting.File) != "99off" {
		t.Errorf("switched off later: %+v", setting)
	}
	// in one file, the last line
	aptFile(t, "99off", "APT::Periodic::Unattended-Upgrade \"0\";\n  APT::Periodic::Unattended-Upgrade   \"1\" ;\n")
	if setting := readAptSetting(); setting.Value != "1" {
		t.Errorf("two lines: %+v, want the last", setting)
	}
}

func TestAptSettingSkipsCommentsAndFilesAptDoesNotRead(t *testing.T) {
	newWorld(t)
	aptFile(t, "20auto-upgrades", "APT::Periodic::Unattended-Upgrade \"1\";\n")
	aptFile(t, "90comments", "// APT::Periodic::Unattended-Upgrade \"0\";\n# APT::Periodic::Unattended-Upgrade \"0\";\n  //APT::Periodic::Unattended-Upgrade \"0\";\nX-APT::Periodic::Unattended-Upgrade \"0\";\n")
	for _, ignored := range []string{"95old.bak", "95old.dpkg-old", "95old.disabled", "95 space", "95old~"} {
		aptFile(t, ignored, "APT::Periodic::Unattended-Upgrade \"0\";\n")
	}
	aptFile(t, "96read.conf", "APT::Periodic::Update-Package-Lists \"0\";\n")
	// not a regular file: a folder and a link
	if err := os.Mkdir(filepath.Join(aptConfigDir, "97folder"), 0755); err != nil {
		t.Fatal(err)
	}
	off := filepath.Join(t.TempDir(), "off")
	if err := os.WriteFile(off, []byte("APT::Periodic::Unattended-Upgrade \"0\";\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(off, filepath.Join(aptConfigDir, "98link")); err != nil {
		t.Fatal(err)
	}
	if setting := readAptSetting(); setting.Value != "1" || setting.Incomplete {
		t.Errorf("%+v, want \"1\" from 20auto-upgrades, complete", setting)
	}
	aptFile(t, "99read.conf", "APT::Periodic::Unattended-Upgrade \"0\";\n")
	if setting := readAptSetting(); setting.Value != "0" {
		t.Errorf("a .conf file: %+v, want it read", setting)
	}
}

func TestAptSettingIsBounded(t *testing.T) {
	newWorld(t)
	aptFile(t, "20auto-upgrades", "APT::Periodic::Unattended-Upgrade \"1\";\n")
	// a file over 64 KiB is not read
	aptFile(t, "50large", strings.Repeat("// padding\n", maxAptFile/11+1)+"APT::Periodic::Unattended-Upgrade \"0\";\n")
	if setting := readAptSetting(); setting.Value != "1" || !setting.Incomplete {
		t.Errorf("a large file: %+v, want it skipped and the reading incomplete", setting)
	}
	os.Remove(filepath.Join(aptConfigDir, "50large"))
	// at most 100 files: the 101st is not read
	for i := 0; i < maxAptFiles-1; i++ {
		aptFile(t, fmt.Sprintf("30file%03d", i), "// nothing\n")
	}
	if setting := readAptSetting(); setting.Value != "1" || setting.Incomplete {
		t.Errorf("100 files: %+v, want all read", setting)
	}
	aptFile(t, "99last", "APT::Periodic::Unattended-Upgrade \"0\";\n")
	if setting := readAptSetting(); setting.Value != "1" || !setting.Incomplete {
		t.Errorf("101 files: %+v, want the last not read and the reading incomplete", setting)
	}
}
