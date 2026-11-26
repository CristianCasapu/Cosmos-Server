package forkrelease

// Fork addition (plan 47): the release notes name exactly the systems
// install.sh accepts. install.sh holds the one list (SUPPORTED_SYSTEMS);
// scripts/fork-release-package.sh turns it into the notes' words.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const packageScript = "../../scripts/fork-release-package.sh"

// supportedList is install.sh's SUPPORTED_SYSTEMS line, as <id>:<version> entries.
func supportedList(t *testing.T, script string) []string {
	t.Helper()
	lines := regexp.MustCompile(`(?m)^SUPPORTED_SYSTEMS="([a-z0-9.: ]*)"$`).FindAllStringSubmatch(script, -1)
	if len(lines) != 1 {
		t.Fatalf("install.sh has %d SUPPORTED_SYSTEMS lines, want 1", len(lines))
	}
	return strings.Fields(lines[0][1])
}

// systemsInWords is what the notes must say for a list: "Debian 12/13 or Ubuntu 24.04/26.04".
func systemsInWords(list []string) string {
	var debian, ubuntu []string
	for _, s := range list {
		id, version, _ := strings.Cut(s, ":")
		switch id {
		case "debian":
			debian = append(debian, version)
		case "ubuntu":
			ubuntu = append(ubuntu, version)
		}
	}
	return "Debian " + strings.Join(debian, "/") + " or Ubuntu " + strings.Join(ubuntu, "/")
}

func systemsOf(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("bash", packageScript, "--systems-of", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%s --systems-of %s: %v\n%s", packageScript, script, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReleaseNotesNameTheSupportedSystems(t *testing.T) {
	data, err := os.ReadFile(installScript)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	list := supportedList(t, script)
	want := systemsInWords(list)
	for _, s := range list {
		if !regexp.MustCompile(`^(debian|ubuntu):[0-9][0-9.]*$`).MatchString(s) {
			t.Errorf("SUPPORTED_SYSTEMS holds %q", s)
		}
	}
	if got := systemsOf(t, installScript); got != want {
		t.Errorf("the release notes would say %q, install.sh accepts %q", got, want)
	}

	// the notes come from the list, not from a list of their own
	pkg, err := os.ReadFile(packageScript)
	if err != nil {
		t.Fatal(err)
	}
	if v := regexp.MustCompile(`(Debian|Ubuntu) [0-9]`).FindString(string(pkg)); v != "" {
		t.Errorf("fork-release-package.sh names systems itself (%q): it must take them from install.sh", v)
	}
	if !strings.Contains(string(pkg), `echo "Install on a $SYSTEMS_WORDS server`) {
		t.Error("the notes' install line does not use the list of install.sh")
	}
	// and a change of the list changes the notes
	changed := strings.Replace(script, `SUPPORTED_SYSTEMS="`, `SUPPORTED_SYSTEMS="ubuntu:22.04 `, 1)
	copyPath := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(copyPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := systemsOf(t, copyPath); got != systemsInWords(append([]string{"ubuntu:22.04"}, list...)) {
		t.Errorf("with 22.04 added to the list, the notes say %q", got)
	}

	// install.sh's own words: every line that names a version of Debian or
	// Ubuntu says exactly what the list says (the header, the messages)
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "SUPPORTED_SYSTEMS=") || !regexp.MustCompile(`(Debian|Ubuntu) [0-9]|[0-9]{2}\.04`).MatchString(line) {
			continue
		}
		if !strings.Contains(line, want) {
			t.Errorf("install.sh names systems other than its list's (%q): %q", want, strings.TrimSpace(line))
		}
	}
	if !strings.Contains(script, "#   2. checks the system: root, "+want) {
		t.Errorf("install.sh's header does not say %q", want)
	}
}
