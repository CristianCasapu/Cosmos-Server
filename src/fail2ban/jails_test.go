package fail2ban

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/security"
)

// The files the installer puts on the host live in fork/fail2ban. This
// package and those files must agree on the jails, their thresholds, the
// names of the files and the kinds the filters count.
const forkFiles = "../../fork/fail2ban"

func readForkFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(forkFiles, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// sections reads "[name]" sections with "key = value" lines.
func sections(content string) map[string]map[string]string {
	result := map[string]map[string]string{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			result[current] = map[string]string{}
			continue
		}
		if key, value, found := strings.Cut(line, "="); found && current != "" && !strings.HasPrefix(line, "#") {
			result[current][strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return result
}

func TestJailFileAndThisPackageAgree(t *testing.T) {
	file := sections(readForkFile(t, "jail.d/"+installedFile))

	for _, known := range jails {
		section, found := file[known.name]
		if !found {
			t.Errorf("the jail file has no [%s]", known.name)
			continue
		}
		for key, want := range map[string]int{"maxretry": known.defaults.MaxRetry, "findtime": known.defaults.FindTime, "bantime": known.defaults.BanTime} {
			if got, err := strconv.Atoi(section[key]); err != nil || got != want {
				t.Errorf("[%s] %s = %q in the file, %d here", known.name, key, section[key], want)
			}
		}
		if !known.defaults.valid() {
			t.Errorf("the defaults of %s are outside the bounds the panel accepts", known.name)
		}
		if section["enabled"] != "true" {
			t.Errorf("[%s] is not enabled in the jail file", known.name)
		}
		// every jail of the fork blocks through the fork's own table
		if section["banaction"] != "%(cosmos_banaction)s" || section["chain"] != "%(cosmos_chain)s" {
			t.Errorf("[%s] does not use the fork's ban action: %q, chain %q", known.name, section["banaction"], section["chain"])
		}
	}
	for name := range file {
		if _, ours := jailByName(name); !ours && name != "DEFAULT" {
			t.Errorf("the jail file has [%s], which this package does not know", name)
		}
	}

	defaults := file["DEFAULT"]
	action := defaults["cosmos_banaction"]
	for _, part := range []string{"nftables[", "type=custom", "table=f2b-cosmos", "chain_hook=prerouting", "chain_priority=-300", "blocktype=drop"} {
		if !strings.Contains(action, part) {
			t.Errorf("the ban action %q lacks %q", action, part)
		}
	}
	// never banned by default: this machine and the networks at home
	for _, network := range []string{"127.0.0.0/8", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10"} {
		if !strings.Contains(" "+defaults["ignoreip"]+" ", " "+network+" ") {
			t.Errorf("the default never-ban list lacks %s: %q", network, defaults["ignoreip"])
		}
	}
	for _, entry := range strings.Fields(defaults["ignoreip"]) {
		if _, ok := parsePrefix(entry); !ok {
			t.Errorf("the default never-ban list has %q, which is not a network", entry)
		}
	}
}

var filterKinds = regexp.MustCompile(`(?m)^failregex = \^\\s\*cosmos-security kind=\(\?:([a-z.\\|]+)\) ip=<ADDR> sev=\[a-z\]\+ n=\\d\+\$$`)

func TestFiltersCountKindsThatExistAndAreIncidents(t *testing.T) {
	counted := map[string]string{}
	for _, known := range jails {
		if !strings.HasPrefix(known.name, "cosmos-") {
			continue
		}
		content := readForkFile(t, "filter.d/"+known.name+".conf")
		match := filterKinds.FindStringSubmatch(content)
		if match == nil {
			t.Errorf("%s: the failregex does not have the shape this test knows (anchored, kinds listed, <ADDR>)", known.name)
			continue
		}
		if !strings.Contains(content, "datepattern = {^LN-BEG}%%Y-%%m-%%dT%%H:%%M:%%S%%z") {
			t.Errorf("%s: the anchored date pattern is missing", known.name)
		}
		for _, escaped := range strings.Split(match[1], "|") {
			kind := strings.ReplaceAll(escaped, `\.`, ".")
			if strings.Contains(kind, `\`) || strings.Count(escaped, `\.`) != strings.Count(kind, ".") {
				t.Errorf("%s: a dot in %q is not escaped", known.name, escaped)
			}
			found, ok := security.KindOf(kind)
			if !ok {
				t.Errorf("%s counts %q, which the security record does not have", known.name, kind)
				continue
			}
			// only incidents with an address are written to the log
			if found.Class != security.ClassIncident {
				t.Errorf("%s counts %q, which is not an incident and is never in the log", known.name, kind)
			}
			if other, twice := counted[kind]; twice {
				t.Errorf("%q is counted by %s and by %s", kind, other, known.name)
			}
			counted[kind] = known.name
		}
	}
	// what this package records about Fail2ban's own bans must not be
	// counted by a filter: a ban would count towards the next one
	for kind, filter := range counted {
		if strings.HasPrefix(kind, "firewall.") {
			t.Errorf("%s counts %q", filter, kind)
		}
	}
	if len(counted) < 10 {
		t.Errorf("only %d kinds are counted: %v", len(counted), counted)
	}
}

func TestScriptsKnowTheSameFiles(t *testing.T) {
	install := readForkFile(t, "install.sh")
	uninstall := readForkFile(t, "uninstall.sh")
	for _, name := range []string{installedFile, "cosmos-50-host.local"} {
		if !strings.Contains(install, name) || !strings.Contains(uninstall, name) {
			t.Errorf("%s is not named by both scripts", name)
		}
	}
	// the installer leaves the panel's file alone; the uninstaller removes it
	if !strings.Contains(uninstall, "jail.d/"+panelFile) {
		t.Errorf("uninstall.sh does not remove %s", panelFile)
	}
	if !(installedFile < "cosmos-50-host.local" && "cosmos-50-host.local" < panelFile) {
		t.Error("the files are not read in the order jails, host, panel")
	}
	for _, known := range jails {
		if strings.HasPrefix(known.name, "cosmos-") {
			for script, content := range map[string]string{"install.sh": install, "uninstall.sh": uninstall} {
				if !strings.Contains(content, known.name) {
					t.Errorf("%s does not name %s", script, known.name)
				}
			}
		}
	}
	for script, content := range map[string]string{"install.sh": install, "uninstall.sh": uninstall} {
		if !strings.Contains(content, "\nset -eu\n") {
			t.Errorf("%s does not stop at the first error (set -eu)", script)
		}
		for _, forbidden := range []string{"curl ", "wget ", "eval ", "| sh", "|sh", "bash -c"} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s contains %q", script, forbidden)
			}
		}
	}
}
