package behind

// Fork addition (plan 44): the data of the known apps is what the research
// read from the real images.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

var placeholder = regexp.MustCompile(`\{[a-z]+\}`)
var knownPlaceholders = map[string]bool{"{mainurl}": true, "{each}": true, "{i}": true, "{url}": true, "{urls}": true, "{host}": true, "{hosts}": true, "{name}": true, "{names}": true, "{port}": true, "{proxy}": true}

func TestEveryKnownAppIsSourcedAndConsistent(t *testing.T) {
	if len(Apps()) < 15 {
		t.Fatalf("only %d known apps", len(Apps()))
	}
	ids, images := map[string]bool{}, map[string]string{}
	for _, app := range Apps() {
		if app.ID == "" || ids[app.ID] || app.Name == "" || app.Port < 1 || app.Checked == "" || app.Version == "" {
			t.Errorf("%q: id, name, port, checked and version are needed (and the id once)", app.ID)
		}
		ids[app.ID] = true
		if len(app.Source) == 0 {
			t.Errorf("%s cites no documentation", app.ID)
		}
		for _, s := range app.Source {
			if !strings.HasPrefix(s, "https://") {
				t.Errorf("%s: source %q", app.ID, s)
			}
		}
		for _, image := range app.Images {
			if imageKey(image) != image {
				t.Errorf("%s: image %q is not written as matched (%q)", app.ID, image, imageKey(image))
			}
			if other, taken := images[image]; taken {
				t.Errorf("%s: image %s is %s's too", app.ID, image, other)
			}
			images[image] = app.ID
		}
		if app.AppOwnHost && app.NoOwnHost {
			t.Errorf("%s: appOwnHost and noOwnHost", app.ID)
		}
		texts := []string{}
		for _, e := range app.Env {
			if !envName.MatchString(e.Name) || (e.When != "always" && e.When != "refused" && e.When != "ifSet" && e.When != "advice") || e.Why == "" {
				t.Errorf("%s: variable %+v", app.ID, e)
			}
			if forbidden.MatchString(e.Name + "=" + e.Value) {
				t.Errorf("%s: %s switches a protection off", app.ID, e.Name)
			}
			texts = append(texts, e.Value, e.Contains)
		}
		for _, f := range app.File {
			if (f.Path == "" && !f.Command) || len(f.Lines) == 0 || (f.When != "refused" && f.When != "advice") || f.Why == "" {
				t.Errorf("%s: file %+v", app.ID, f)
			}
			for _, image := range f.Images {
				if !containsString(app.Images, image) {
					t.Errorf("%s: file for image %s, which is not the app's", app.ID, image)
				}
			}
			for _, l := range f.Lines {
				if forbidden.MatchString(l) {
					t.Errorf("%s: line %q switches a protection off", app.ID, l)
				}
			}
			texts = append(texts, f.Lines...)
		}
		for _, text := range texts {
			for _, p := range placeholder.FindAllString(text, -1) {
				if !knownPlaceholders[p] {
					t.Errorf("%s: unknown placeholder %s", app.ID, p)
				}
			}
		}
		for _, s := range app.Signatures {
			if s.Example == "" || s.Reason == "" {
				t.Errorf("%s: a signature without its real example or reason", app.ID)
			}
			if s.body != nil && !s.body.MatchString(s.Example) {
				t.Errorf("%s: %q does not match its own example %q", app.ID, s.Body, s.Example)
			}
			if s.log != nil && !s.log.MatchString(s.Example) {
				t.Errorf("%s: %q does not match its own example %q", app.ID, s.Log, s.Example)
			}
		}
	}
	for _, s := range generic {
		if s.body != nil && !s.body.MatchString(s.Example) || s.log != nil && !s.log.MatchString(s.Example) {
			t.Errorf("generic %s does not match its example", s.Reason)
		}
	}
	// the owner's apps
	for _, id := range []string{"qbittorrent", "sonarr", "radarr", "jellyfin", "emby"} {
		if !ids[id] {
			t.Errorf("%s is not known", id)
		}
	}
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// what the data must never propose: an app's own protection switched off
// (and nothing that can let visitors in without a sign-in because of their address: sign-in exemptions
// for local networks, and Sonarr's/Radarr's TrustedNetworks, which makes their "disabled for local
// addresses" admit everyone at home: see plan 44)
var forbidden = regexp.MustCompile(`(?i)(TrustedNetworks|AuthenticationRequired|DisabledForLocal|trusted_networks|AuthSubnetWhitelist|LocalHostAuth|bypass_auth|EnableAutoLogin|HostHeaderValidation\s*=\s*false|CSRFProtection\s*=\s*false|ALLOWED_HOSTS\s*=\s*\*|whitelist-enabled.*false|allowedHosts:\s*(true|'all'|"all")|DisableHostCheck|disable-host-check|trusted_proxies.*0\.0\.0\.0/0)`)

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestImagesAreMatchedWithoutRegistryAndTag(t *testing.T) {
	for image, want := range map[string]string{
		"lscr.io/linuxserver/qbittorrent:latest":                "qbittorrent",
		"linuxserver/qbittorrent:5.3.0":                         "qbittorrent",
		"ghcr.io/hotio/qbittorrent:release":                     "qbittorrent",
		"qbittorrentofficial/qbittorrent-nox@sha256:0123456789": "qbittorrent",
		"localhost:5000/linuxserver/sonarr:4":                   "sonarr",
		"docker.io/library/adminer:latest":                      "adminer",
		"adminer":                                               "adminer",
		"example/qbittorrent:1":                                 "",
		"":                                                      "",
	} {
		got := ""
		if app := AppFor(image); app != nil {
			got = app.ID
		}
		if got != want {
			t.Errorf("%q is %q, want %q", image, got, want)
		}
	}
}

func TestRealRefusalsAreRecognised(t *testing.T) {
	// answers of the real images, as the research read them
	for _, c := range []struct {
		image, text, reason, app string
		status                   int
	}{
		{"lscr.io/linuxserver/qbittorrent:latest", "Unauthorized", "hostPort", "qbittorrent", 401},
		{"lscr.io/linuxserver/transmission:latest", "421: Misdirected Request Transmission received your request, but the hostname was unrecognized. If you're editing settings.json, see the 'rpc_host_whitelist' and 'rpc_host_whitelist_enabled' entries.", "hostNotAllowed", "transmission", 421},
		{"lscr.io/linuxserver/sonarr:latest", "Bad Request - Invalid Hostname HTTP Error 400. The request hostname is invalid.", "hostNotAllowed", "sonarr", 400},
		{"node:22-alpine", `Blocked request. This host ("vite.example.org") is not allowed. To allow this host, add "vite.example.org" to ` + "`server.allowedHosts`" + ` in vite.config.js.`, "hostNotAllowed", "vite", 403},
		{"node:22-alpine", "Invalid Host header", "hostNotAllowed", "webpack", 403},
		{"example/django-app:1", "DisallowedHost at / Invalid HTTP_HOST header: 'app.example.org:2053'.", "hostNotAllowed", "", 400},
	} {
		sig, _, app := matchSignature(AppFor(c.image), Attempt{Status: c.status, First: c.text}, c.text, nil)
		id := ""
		if app != nil {
			id = app.ID
		}
		if sig == nil || sig.Reason != c.reason || id != c.app {
			t.Errorf("%s: %v / %q, want %s / %q", c.image, sig, id, c.reason, c.app)
		}
	}
	// a log line, for an app whose answer says nothing (Home Assistant: an empty 400)
	sig, line, _ := matchSignature(nil, Attempt{Status: 400}, "", []string{"2026-11-16 08:00:00.000 ERROR (MainThread) [homeassistant.components.http.forwarded] A request from a reverse proxy was received from 172.30.0.1, but your HTTP integration is not set-up for reverse proxies"})
	if sig == nil || sig.Reason != "untrustedProxy" || !strings.Contains(line, "reverse proxy") {
		t.Errorf("Home Assistant's log: %v %q", sig, line)
	}
}

func TestHostileNamesNeverReachALineOrACommand(t *testing.T) {
	hostile := []string{"a.example.org;rm -rf /", "a.example.org' && id '", "$(id).example.org", "`id`.example.org", "a.example.org\nX: y", "{b:[a-z]+}.example.org", "a.example.org --value=x", "a b.example.org"}
	onConfig := func(hosts ...string) utils.Config {
		c := utils.Config{}
		c.HTTPConfig.HTTPSPort = "2053"
		for i, h := range hosts {
			c.HTTPConfig.ProxyConfig.Routes = append(c.HTTPConfig.ProxyConfig.Routes, utils.ProxyRouteConfig{Name: "r" + string(rune('a'+i)), Mode: "SERVAPP", UseHost: true, Host: h, Target: "http://app:80"})
		}
		return c
	}
	for _, bad := range hostile {
		for _, own := range []string{bad, "good.example.org"} {
			config := onConfig(own, bad)
			utils.LoadBaseMainConfig(config)
			public := proxy.PublicAddress{Name: strings.ToLower(own), Port: "2053", Host: own + ":2053"}
			f := fillFor(config, config.HTTPConfig.ProxyConfig.Routes[0], "app", public, "https", "192.0.2.1")
			for _, app := range Apps() {
				a := app
				out := []Repair{}
				for _, when := range []string{"always", "refused", "advice", "ifSet"} {
					out = append(out, appRepairs(&a, docker.URLCheckFacts{Env: map[string]string{}}, f, when)...)
					out = append(out, filesOf(&a, f, when)...)
				}
				for _, r := range out {
					text := r.Value + strings.Join(r.Lines, "\n")
					for _, piece := range []string{";", "$(", "`", "&&", "\nX:", "{b:", "--value=x", "a b."} {
						if strings.Contains(text, piece) {
							t.Errorf("%s with %q: %q reached %q", app.ID, bad, piece, text)
						}
					}
					if r.Kind == "command" {
						for _, l := range r.Lines {
							if strings.Count(l, "'")%2 != 0 {
								t.Errorf("%s: unbalanced quoting in %q", app.ID, l)
							}
						}
					}
				}
			}
		}
	}
	utils.LoadBaseMainConfig(utils.Config{})
	if got := shellQuote("it's"); got != `'it'"'"'s'` {
		t.Errorf("shellQuote: %s", got)
	}
}
