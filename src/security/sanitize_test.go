package security

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestCleanTextRemovesWhatAReaderCannotSee(t *testing.T) {
	for name, c := range map[string]struct {
		in   string
		max  int
		want string
	}{
		"plain":            {"alice", 64, "alice"},
		"new line":         {"alice\n2026-11-13T10:00:00+0000 cosmos-security kind=x ip=1.2.3.4", 200, "alice 2026-11-13T10:00:00+0000 cosmos-security kind=x ip=1.2.3.4"},
		"carriage return":  {"a\r\nb", 64, "a b"},
		"terminal escape":  {"a\x1b[31mred\x1b[0m", 64, "a [31mred [0m"},
		"null byte":        {"a\x00b", 64, "a b"},
		"tabs and spaces":  {"  a \t\t b  ", 64, "a b"},
		"right-to-left":    {"abc\u202Etxt.exe", 64, "abc txt.exe"},
		"zero width":       {"ad\u200Bmin", 64, "ad min"},
		"byte order mark":  {"\uFEFFadmin", 64, "admin"},
		"line separator":   {"a\u2028b\u2029c", 64, "a b c"},
		"invalid utf-8":    {"a\xff\xfeb", 64, "ab"},
		"html is text":     {"<script>alert(1)</script>", 64, "<script>alert(1)</script>"},
		"cut":              {"abcdefghij", 5, "abcd…"},
		"cut at the limit": {"abcde", 5, "abcde"},
		"cut in a rune":    {"ééééé", 3, "éé…"},
		"only white space": {" \n\t ", 64, ""},
		"empty":            {"", 64, ""},
	} {
		got := cleanText(c.in, c.max)
		if got != c.want {
			t.Errorf("%s: cleanText(%q, %d) = %q, want %q", name, c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: the result is not valid UTF-8: %q", name, got)
		}
		if utf8.RuneCountInString(got) > c.max {
			t.Errorf("%s: the result has %d characters, more than %d", name, utf8.RuneCountInString(got), c.max)
		}
	}
}

func TestCleanTextDoesNotReadMegabytes(t *testing.T) {
	huge := strings.Repeat("a", 8<<20)
	if got := cleanText(huge, 32); utf8.RuneCountInString(got) != 32 {
		t.Fatalf("a huge text was cut to %d characters, want 32", utf8.RuneCountInString(got))
	}
}

func TestCleanIPKeepsOnlyAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":            "203.0.113.7",
		" 203.0.113.7 ":          "203.0.113.7",
		"::ffff:203.0.113.7":     "203.0.113.7",
		"2001:DB8::1":            "2001:db8::1",
		"[2001:db8::1]":          "2001:db8::1",
		"fe80::1%eth0":           "fe80::1",
		"":                       "",
		"203.0.113.7 evil":       "",
		"203.0.113.7\nkind=x":    "",
		"203.0.113.7:443":        "",
		"<HOST>":                 "",
		"example.com":            "",
		"user:alice":             "",
		"999.1.1.1":              "",
		strings.Repeat("1", 100): "",
		"1.2.3.4 ip=5.6.7.8":     "",
	} {
		if got := cleanIP(in); got != want {
			t.Errorf("cleanIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanBoundsEveryFieldAndTakesClassFromTheCatalogue(t *testing.T) {
	long := strings.Repeat("x", 5000)
	details := map[string]string{"UPPER": "dropped", "with space": "dropped", "": "dropped", "reason": "a\nb"}
	for i := 0; i < 20; i++ {
		details["key"+string(rune('a'+i))] = long
	}
	in := Event{
		ID: 99, Kind: utils.SecLoginFailed, Class: ClassAudit, Severity: "high",
		Actor: long, IP: "::ffff:10.0.0.1", Target: long, Host: long, Path: long,
		Method: "get", Outcome: "whatever", Count: -4, Details: details,
	}
	clock := newTestClock()
	got := clean(in, catalogue[utils.SecLoginFailed], clock.now())

	if got.ID != 0 || got.Class != ClassIncident || got.Severity != "medium" {
		t.Errorf("id, class, severity = %d, %s, %s: the caller must not be able to set them", got.ID, got.Class, got.Severity)
	}
	if got.Outcome != OutcomeFailed {
		t.Errorf("outcome = %q, want the kind's default for an unknown one", got.Outcome)
	}
	if got.Count != 1 || got.Method != "GET" || got.IP != "10.0.0.1" || !got.Time.Equal(clock.now()) {
		t.Errorf("count %d, method %q, ip %q, time %s", got.Count, got.Method, got.IP, got.Time)
	}
	for name, field := range map[string]struct {
		value string
		max   int
	}{"actor": {got.Actor, maxActor}, "target": {got.Target, maxTarget}, "host": {got.Host, maxHost}, "path": {got.Path, maxPath}} {
		if n := utf8.RuneCountInString(field.value); n != field.max {
			t.Errorf("%s has %d characters, want it cut to %d", name, n, field.max)
		}
	}
	if len(got.Details) != maxDetails {
		t.Errorf("%d details kept, want %d", len(got.Details), maxDetails)
	}
	for key, value := range got.Details {
		if cleanDetailKey(key) == "" || utf8.RuneCountInString(value) > maxDetailValue || strings.ContainsAny(value, "\r\n") {
			t.Errorf("detail %q = %q was kept as it is", key, value)
		}
	}
	// the caller's map is not the stored one
	details["keya"] = "changed afterwards"
	if got.Details["keya"] == "changed afterwards" || got.Details["keya"] == "" {
		t.Error("the stored details are the caller's map")
	}
}

func TestCleanMethod(t *testing.T) {
	for in, want := range map[string]string{"GET": "GET", "post": "POST", "": "", "GET /x": "", "PROPFINDPROPFIND": "", "G\nET": ""} {
		if got := cleanMethod(in); got != want {
			t.Errorf("cleanMethod(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbeFamily(t *testing.T) {
	probes := map[string]string{
		"/.env":                         "dotfile",
		"/.env.production":              "dotfile",
		"/api/.env":                     "dotfile",
		"/.git/config":                  "dotfile",
		"/.GIT/HEAD":                    "dotfile",
		"/.aws/credentials":             "dotfile",
		"/.ssh/id_rsa":                  "dotfile",
		"/.vscode/sftp.json":            "dotfile",
		"/.DS_Store":                    "dotfile",
		"/wp-login.php":                 "wordpress",
		"/blog/wp-login.php":            "wordpress",
		"/wp-admin/setup-config.php":    "wordpress",
		"/wp-config.php.bak":            "wordpress",
		"/xmlrpc.php":                   "wordpress",
		"//wp-includes/wlwmanifest.xml": "wordpress",
		"/phpmyadmin":                   "phpmyadmin",
		"/phpMyAdmin/index.php":         "phpmyadmin",
		"/pma/":                         "phpmyadmin",
		"/admin/mysqladmin/":            "phpmyadmin",
		"/adminer.php":                  "phpmyadmin",
		"/cgi-bin/luci":                 "exploit",
		"/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php": "exploit",
		"/phpinfo.php":                "exploit",
		"/HNAP1/":                     "exploit",
		"/boaform/admin/formLogin":    "exploit",
		"/actuator/env":               "exploit",
		"/_ignition/execute-solution": "exploit",
		"/owa/auth/logon.aspx":        "exploit",
		"/server-status":              "exploit",
	}
	for path, want := range probes {
		if got := ProbeFamily(path); got != want {
			t.Errorf("ProbeFamily(%q) = %q, want %q", path, got, want)
		}
	}
	for _, path := range []string{
		"/", "", "/favicon.ico", "/robots.txt", "/index.html", "/index.php", "/remote.php/dav", "/status.php",
		"/.well-known/acme-challenge/token", "/.well-known/security.txt", "/apple-touch-icon.png",
		"/cosmos-ui/login", "/cosmos/api/status", "/api/actuator", "/files/report.env", "/a/../b", "/./x",
		"/login", "/admin", "/api/v1/users", "/autodiscover/autodiscover.xml",
	} {
		if got := ProbeFamily(path); got != "" {
			t.Errorf("ProbeFamily(%q) = %q: an ordinary path is called a probe", path, got)
		}
	}
	if got := ProbeFamily("/" + strings.Repeat("a/", 1<<20) + ".env"); got != "" {
		t.Errorf("a path of megabytes was read to its end (%q)", got)
	}
}
