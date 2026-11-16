package forkrelease

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Fork addition: tests of the dependency status (parse and fetch) and of
// the dependency summary of a release's meta.json.

const depsTestRepo = "example-owner/Cosmos-Server"

const goodStatus = `{ "schema": 1, "run": 123456789, "at": "2026-11-21T16:40:00Z",
  "outcome": "updated", "release": "0.24.0-fork.31", "quietSince": "2026-10-24",
  "changes": [{"eco": "npm", "name": "react", "from": "18.3.1", "to": "19.0.0", "kind": "major", "ageDays": 12},
              {"eco": "go", "name": "golang.org/x/net", "from": "v0.30.0", "to": "v0.31.0", "kind": "minor"},
              {"eco": "npm", "name": "@mui/material", "from": "5.15.0", "to": "5.15.1", "kind": "patch"}],
  "dropped": [{"eco": "npm", "name": "vite", "from": "5.0.0", "to": "6.0.0", "reason": "full checks"}],
  "tried":   [{"eco": "npm", "name": "dayjs", "from": "1.0.0", "to": "2.0.0", "reason": "quick checks: build failed"}],
  "left":    [{"eco": "action", "name": "actions/checkout", "from": "v7.0.1", "to": "v7.1.0", "reason": "workflow file"}],
  "somethingNew": {"nested": [1, 2, 3]} }`

func TestDepsStatusGood(t *testing.T) {
	st, err := ParseDepsStatus([]byte(goodStatus), depsTestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if st.Run != 123456789 || st.Outcome != "updated" || st.Release != "0.24.0-fork.31" || !st.At.Equal(time.Date(2026, 11, 21, 16, 40, 0, 0, time.UTC)) {
		t.Errorf("%+v", st)
	}
	if st.QuietSince.Format("2006-01-02") != "2026-10-24" {
		t.Errorf("quietSince %v", st.QuietSince)
	}
	if len(st.Changes) != 3 || st.Changes[0].Kind != "major" || st.Changes[0].AgeDays != 12 || len(st.Dropped) != 1 || st.Tried[0].Reason != "quick checks: build failed" || len(st.Left) != 1 {
		t.Errorf("lists %+v", st)
	}
	if st.RunLink != "https://github.com/"+depsTestRepo+"/actions/runs/123456789" || st.IssueLink != "" {
		t.Errorf("links %q %q", st.RunLink, st.IssueLink)
	}
	minor, major, majors := st.Summary()
	if got := DependencySummary(minor, major, majors); got != "2 minor, 1 major (react 19.0.0)" {
		t.Errorf("summary %q", got)
	}
}

func TestDepsStatusFailedWithIssue(t *testing.T) {
	st, err := ParseDepsStatus([]byte(`{"schema":1,"run":7,"at":"2026-11-21T16:40:00+02:00","outcome":"failed","failedStep":"checks","issue":42,
		"failure":"line one\r\nline two\u0007\u001b[31m red\u202e evil"}`), depsTestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if st.IssueLink != "https://github.com/"+depsTestRepo+"/issues/42" || st.FailedStep != "checks" {
		t.Errorf("%+v", st)
	}
	if st.Failure != "line one\nline two  [31m red  evil" {
		t.Errorf("failure %q", st.Failure)
	}
	if st.At.Location() != time.UTC || st.At.Hour() != 14 {
		t.Errorf("at %v", st.At)
	}
}

func TestDepsStatusHostile(t *testing.T) {
	change := func(fields string) string {
		return `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"updated","release":"0.24.0-fork.31","changes":[` + fields + `]}`
	}
	entries := func(n int, list string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"eco":"npm","name":"p%d","from":"1.0.0","to":"1.0.1","kind":"patch","reason":"r"}`, i)
		}
		return `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing","` + list + `":[` + strings.Join(parts, ",") + `]}`
	}
	cases := map[string]string{
		"empty":            ``,
		"not JSON":         `<html>`,
		"array":            `[1,2,3]`,
		"null":             `null`,
		"no schema":        `{"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"schema 2":         `{"schema":2,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"schema string":    `{"schema":"1","run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"run string":       `{"schema":1,"run":"1","at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"run float":        `{"schema":1,"run":1.5,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"run zero":         `{"schema":1,"run":0,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"run negative":     `{"schema":1,"run":-4,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"run huge":         `{"schema":1,"run":99999999999999999999999,"at":"2026-11-21T16:40:00Z","outcome":"nothing"}`,
		"no time":          `{"schema":1,"run":1,"outcome":"nothing"}`,
		"bad time":         `{"schema":1,"run":1,"at":"yesterday","outcome":"nothing"}`,
		"time object":      `{"schema":1,"run":1,"at":{"x":1},"outcome":"nothing"}`,
		"unknown outcome":  `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"hacked"}`,
		"outcome number":   `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":3}`,
		"update no rel":    `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"updated"}`,
		"upstream release": `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"updated","release":"0.25.0"}`,
		"v release":        `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"updated","release":"v0.24.0-fork.3"}`,
		"evil release":     `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"updated","release":"0.24.0-fork.3/../../x"}`,
		"bad quiet":        `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing","quietSince":"last month"}`,
		"bad step":         `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"failed","failedStep":"rm -rf"}`,
		"issue zero":       `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"failed","issue":0}`,
		"issue string":     `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"failed","issue":"javascript:alert(1)"}`,
		"failure number":   `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"failed","failure":12}`,
		"changes object":   `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing","changes":{"a":1}}`,
		"unknown eco":      change(`{"eco":"pip","name":"x","from":"1","to":"2","kind":"minor"}`),
		"unknown kind":     change(`{"eco":"npm","name":"x","from":"1","to":"2","kind":"huge"}`),
		"no kind":          change(`{"eco":"npm","name":"x","from":"1","to":"2"}`),
		"name space":       change(`{"eco":"npm","name":"x y","from":"1","to":"2","kind":"minor"}`),
		"name link":        change(`{"eco":"npm","name":"javascript:alert(1)","from":"1","to":"2","kind":"minor"}`),
		"name html":        change(`{"eco":"npm","name":"<b>x</b>","from":"1","to":"2","kind":"minor"}`),
		"name long":        change(`{"eco":"npm","name":"` + strings.Repeat("a", 215) + `","from":"1","to":"2","kind":"minor"}`),
		"name empty":       change(`{"eco":"npm","name":"","from":"1","to":"2","kind":"minor"}`),
		"name control":     change(`{"eco":"npm","name":"a\u0000b","from":"1","to":"2","kind":"minor"}`),
		"version long":     change(`{"eco":"npm","name":"x","from":"1","to":"` + strings.Repeat("1", 65) + `","kind":"minor"}`),
		"version slash":    change(`{"eco":"npm","name":"x","from":"1","to":"2/3","kind":"minor"}`),
		"version missing":  change(`{"eco":"npm","name":"x","to":"2","kind":"minor"}`),
		"age negative":     change(`{"eco":"npm","name":"x","from":"1","to":"2","kind":"minor","ageDays":-1}`),
		"age string":       change(`{"eco":"npm","name":"x","from":"1","to":"2","kind":"minor","ageDays":"1"}`),
		"entry string":     change(`"react"`),
		"too many changes": entries(301, "changes"),
		"too many dropped": entries(51, "dropped"),
		"too many tried":   entries(51, "tried"),
		"too many left":    entries(51, "left"),
		"deep":             `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing","x":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}`,
		"huge":             `{"schema":1,"run":1,"at":"2026-11-21T16:40:00Z","outcome":"nothing","x":"` + strings.Repeat("a", MaxDepsStatus) + `"}`,
		"not utf8":         "{\"schema\":1,\"run\":1,\"at\":\"2026-11-21T16:40:00Z\",\"outcome\":\"failed\",\"failure\":\"\xff\xfe\"}",
	}
	for name, body := range cases {
		st, err := ParseDepsStatus([]byte(body), depsTestRepo)
		if err == nil {
			t.Errorf("%s: accepted: %+v", name, st)
		} else if !strings.HasPrefix(err.Error(), ErrDepsStatus.Error()) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// the bounds themselves are accepted
	for _, body := range []string{entries(300, "changes"), entries(50, "dropped"), entries(50, "tried"), entries(50, "left"),
		change(`{"eco":"npm","name":"` + strings.Repeat("a", 214) + `","from":"1","to":"` + strings.Repeat("1", 64) + `","kind":"minor"}`)} {
		if _, err := ParseDepsStatus([]byte(body), depsTestRepo); err != nil {
			t.Errorf("at the bound: %v", err)
		}
	}
	// no repository, no status
	if _, err := ParseDepsStatus([]byte(goodStatus), "../evil"); err == nil {
		t.Error("an invalid repository was accepted")
	}
}

func TestDepsStatusTextIsCleanedAndNeverALink(t *testing.T) {
	long := strings.Repeat("x", 5000)
	body := `{"schema":1,"run":5,"at":"2026-11-21T16:40:00Z","outcome":"failed","failedStep":"release","issue":3,
		"failure":"see javascript:alert(1) and https://evil.example/x <script>` + long + `",
		"tried":[{"eco":"npm","name":"x","from":"1","to":"2","reason":"[click](https://evil.example)\u0000\n` + long + `"}]}`
	st, err := ParseDepsStatus([]byte(body), depsTestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(st.Failure)); n != maxFailure {
		t.Errorf("failure is %d runes", n)
	}
	if n := len([]rune(st.Tried[0].Reason)); n != maxReason || strings.ContainsAny(st.Tried[0].Reason, "\x00\n") {
		t.Errorf("reason %d runes: %q", n, st.Tried[0].Reason[:40])
	}
	// the links are the server's, from the numbers
	if st.RunLink != "https://github.com/"+depsTestRepo+"/actions/runs/5" || st.IssueLink != "https://github.com/"+depsTestRepo+"/issues/3" {
		t.Errorf("links %q %q", st.RunLink, st.IssueLink)
	}
}

func TestDepsStatusAddress(t *testing.T) {
	if got, err := DepsStatusURL(depsTestRepo); err != nil || got != "https://raw.githubusercontent.com/"+depsTestRepo+"/refs/heads/fork-deps-status/status.json" {
		t.Errorf("%q %v", got, err)
	}
	for _, repo := range []string{"", "owner", "owner/", "/name", "owner/name/x", "owner/..", "own er/name", "owner/name?x", "owner/name#x", "-owner/name", "owner/na%2fme"} {
		if _, err := DepsStatusURL(repo); err == nil {
			t.Errorf("%q accepted", repo)
		}
	}
}

func TestDepsStatusFetch(t *testing.T) {
	handler := http.NewServeMux()
	statusPath := "/" + depsTestRepo + "/refs/heads/fork-deps-status/status.json"
	var host string
	handler.HandleFunc(statusPath, func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		w.Write([]byte(goodStatus))
	})
	f, _ := fakeGitHub(t, handler)
	body, err := f.fetchDepsStatus(depsTestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if host != "raw.githubusercontent.com" {
		t.Errorf("asked %s", host)
	}
	if st, err := ParseDepsStatus(body, depsTestRepo); err != nil || st.Release != "0.24.0-fork.31" {
		t.Errorf("%+v %v", st, err)
	}
}

func TestDepsStatusFetchRefusesOversizeRedirectsAndOtherHosts(t *testing.T) {
	statusPath := "/" + depsTestRepo + "/refs/heads/fork-deps-status/status.json"
	for name, handle := range map[string]http.HandlerFunc{
		"oversize": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(strings.Repeat(" ", MaxDepsStatus+1)))
		},
		"oversize, unannounced": func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 70; i++ {
				w.Write([]byte(strings.Repeat(" ", 1024)))
				w.(http.Flusher).Flush()
			}
		},
		"redirect away": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/status.json", http.StatusFound)
		},
		"redirect to http": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://raw.githubusercontent.com/status.json", http.StatusFound)
		},
		"missing": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
	} {
		handler := http.NewServeMux()
		handler.HandleFunc(statusPath, handle)
		f, _ := fakeGitHub(t, handler)
		if body, err := f.fetchDepsStatus(depsTestRepo); err == nil {
			t.Errorf("%s: accepted %d bytes", name, len(body))
		} else if strings.HasPrefix(name, "oversize") && !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v", name, err)
		} else if strings.HasPrefix(name, "redirect") && !errors.Is(err, ErrAddress) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a repository that would lead the address elsewhere is refused before any connection
	f, connections := fakeGitHub(t, http.NotFoundHandler())
	for _, repo := range []string{"evil.example/x/..", "a/b@evil.example", "a/b:443"} {
		if _, err := f.fetchDepsStatus(repo); !errors.Is(err, ErrAddress) {
			t.Errorf("%q: %v", repo, err)
		}
	}
	if connections.Load() != 0 {
		t.Error("a connection was opened")
	}
}

func TestForkDependenciesOfMeta(t *testing.T) {
	d, err := ParseForkDependencies([]byte(`{"version":"0.24.0-fork.31","forkDependencies":{"minor":14,"major":1,"majors":["react 19.0.0"]}}`))
	if err != nil || d == nil || d.Minor != 14 || d.Major != 1 || len(d.Majors) != 1 {
		t.Fatalf("%+v %v", d, err)
	}
	if got := DependencySummary(d.Minor, d.Major, d.Majors); got != "14 minor, 1 major (react 19.0.0)" {
		t.Errorf("%q", got)
	}
	if got := DependencySummary(0, 5, []string{"a 1", "b 2", "c 3", "d 4"}); got != "5 major (a 1, b 2, c 3 and 2 more)" {
		t.Errorf("%q", got)
	}
	if got := DependencySummary(3, 0, nil); got != "3 minor" {
		t.Errorf("%q", got)
	}
	for _, meta := range []string{`{"version":"0.24.0-fork.31"}`, `{"forkDependencies":{"minor":0,"major":0}}`} {
		if d, err := ParseForkDependencies([]byte(meta)); d != nil || err != nil {
			t.Errorf("%s: %+v %v", meta, d, err)
		}
	}
	for _, meta := range []string{
		`not json`,
		`{"forkDependencies":"14 minor"}`,
		`{"forkDependencies":{"minor":-1}}`,
		`{"forkDependencies":{"minor":10001}}`,
		`{"forkDependencies":{"major":"1"}}`,
		`{"forkDependencies":{"major":1,"majors":["` + strings.Repeat("a", 81) + `"]}}`,
		`{"forkDependencies":{"major":1,"majors":["<b>react</b>"]}}`,
		`{"forkDependencies":{"major":1,"majors":["react\n19"]}}`,
		`{"forkDependencies":{"major":1,"majors":[" react"]}}`,
		`{"forkDependencies":{"major":11,"majors":["a","a","a","a","a","a","a","a","a","a","a"]}}`,
		`{"x":"` + strings.Repeat("a", MaxMetaFile) + `"}`,
	} {
		if d, err := ParseForkDependencies([]byte(meta)); err == nil {
			t.Errorf("%.60s: accepted %+v", meta, d)
		}
	}
}
