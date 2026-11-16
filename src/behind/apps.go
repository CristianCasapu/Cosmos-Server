// Package behind is the fork's knowledge of apps behind the reverse proxy
// (plan 44): what each known app needs to be told to work behind Cosmos on
// any port, and the check that asks an app, as a visitor would reach it,
// whether it accepts the address it is reached at.
package behind

// The known apps (apps.json): data, matched by the image's name.

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/azukaar/cosmos-server/src/docker"
)

//go:embed apps.json
var appsJSON []byte

// Signature is how an app says it refuses the address it is reached at.
type Signature struct {
	// Reason: the id of the words the panel says (hostPort, hostNotAllowed, untrustedDomain, untrustedProxy, csrfOrigin, wrongAddress)
	Reason string `json:"reason"`
	Status []int  `json:"status,omitempty"`
	Body   string `json:"body,omitempty"` // a regular expression on the start of the answer
	Log    string `json:"log,omitempty"`  // a regular expression on the container's log while it was asked
	// Example: a real answer or log line, as read from the image (documentation of the pattern, tested)
	Example string `json:"example"`
	body    *regexp.Regexp
	log     *regexp.Regexp
}

// EnvNeed is a variable of the app that tells it its address.
type EnvNeed struct {
	Name string `json:"name"`
	// Value proposed: {url}, {urls}, {host}, {hosts}, {name}, {port}, {proxy} are filled in
	Value string `json:"value"`
	// Contains: the variable must contain this (filled in the same way) for the app to work; empty: it only has to be set
	Contains string `json:"contains,omitempty"`
	// When: "always" (the app fails without it: sign-in, links), "refused" (proposed only when the app refuses),
	// "advice" (works without it; links or features need it),
	// "ifSet" (only when the container has the variable and it does not contain what it must)
	When string `json:"when"`
	Why  string `json:"why"`
	// Unless: the need is met when one of these variables is set (another documented way)
	Unless []string `json:"unless,omitempty"`
}

// FileNeed is a line of the app's own configuration file.
type FileNeed struct {
	Path  string   `json:"path"` // inside the container
	Lines []string `json:"lines"`
	// Section: the part of the file the lines go in ("[WebUI]"), "" when none
	Section string `json:"section,omitempty"`
	// Images: only for these images (the file is elsewhere in others), empty: all of the app's
	Images []string `json:"images,omitempty"`
	When   string   `json:"when"` // "refused" or "advice"
	// Command: the lines are commands to run in the container (as User), not lines of a file;
	// a line with {each} is repeated for each name of the app's URLs, {i} counting from From
	Command bool   `json:"command,omitempty"`
	User    string `json:"user,omitempty"`
	From    int    `json:"from,omitempty"`
	Why   string   `json:"why"`
}

// App is one known app.
type App struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Images  []string `json:"images"`
	Port    int      `json:"port"`
	Source  []string `json:"source"`
	Checked string   `json:"checked"`
	Version string   `json:"version"`
	// AppOwnHost: the URL tells the app its own address as Host (the route's ForkAppOwnHost)
	AppOwnHost bool        `json:"appOwnHost,omitempty"`
	// NoOwnHost: never tell the app its own address (its origin check compares Origin with Host only)
	NoOwnHost  bool        `json:"noOwnHost,omitempty"`
	Env        []EnvNeed   `json:"env,omitempty"`
	File       []FileNeed  `json:"file,omitempty"`
	Signatures []Signature `json:"signatures,omitempty"`
	// Works: nothing is needed behind Cosmos (said, so that a check that passes is not doubted)
	Works bool     `json:"works,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

type appsFile struct {
	About string `json:"about"`
	Apps  []App  `json:"apps"`
}

var known = mustLoad(appsJSON)

func init() {
	// a new URL of an install is told what its known app needs (src/docker/fork_url_check.go)
	docker.KnownRouteSetting = func(image string) (bool, string) {
		if app := AppFor(image); app != nil {
			return app.AppOwnHost, app.Name
		}
		return false, ""
	}
}

func mustLoad(data []byte) []App {
	apps, err := load(data)
	if err != nil {
		panic("src/behind/apps.json: " + err.Error())
	}
	return apps
}

func load(data []byte) ([]App, error) {
	var file appsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for i := range file.Apps {
		for j := range file.Apps[i].Signatures {
			s := &file.Apps[i].Signatures[j]
			var err error
			if s.Body != "" {
				if s.body, err = regexp.Compile(s.Body); err != nil {
					return nil, err
				}
			}
			if s.Log != "" {
				if s.log, err = regexp.Compile(s.Log); err != nil {
					return nil, err
				}
			}
		}
	}
	return file.Apps, nil
}

// Apps is the list of known apps.
func Apps() []App { return known }

// imageKey is an image's name without registry, tag and digest:
// "lscr.io/linuxserver/qbittorrent:latest" → "linuxserver/qbittorrent",
// "nextcloud:30" → "nextcloud".
func imageKey(image string) string {
	image = strings.ToLower(strings.TrimSpace(image))
	if i := strings.IndexByte(image, '@'); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndexByte(image, ':'); i > strings.LastIndexByte(image, '/') {
		image = image[:i]
	}
	parts := strings.Split(image, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		parts = parts[1:]
	}
	if len(parts) > 1 && parts[0] == "library" {
		parts = parts[1:]
	}
	return strings.Join(parts, "/")
}

// AppFor is the known app of an image, or nil.
func AppFor(image string) *App {
	key := imageKey(image)
	if key == "" {
		return nil
	}
	for i := range known {
		for _, name := range known[i].Images {
			if name == key {
				return &known[i]
			}
		}
	}
	return nil
}

// fill puts the URL's facts into a value of the data.
func fill(text string, f fillFacts) string {
	return strings.NewReplacer(
		"{url}", f.url, "{urls}", strings.Join(f.urls, ","), "{host}", f.host, "{hosts}", strings.Join(f.hosts, ","),
		"{name}", f.name, "{names}", strings.Join(f.names, ","), "{port}", f.port, "{proxy}", f.proxy, "{mainurl}", f.mainURL(),
	).Replace(text)
}

type fillFacts struct {
	url, host, name, port, proxy string
	image                        string
	urls, hosts, names          []string
}

// mainURL is the address to give an app that takes one: the first public one
// that is not a name of the local network, else the URL's own.
func (f fillFacts) mainURL() string {
	for _, u := range f.urls {
		if !strings.Contains(u, ".local:") && !strings.HasSuffix(u, ".local") {
			return u
		}
	}
	return f.url
}
