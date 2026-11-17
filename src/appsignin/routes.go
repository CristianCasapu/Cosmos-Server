package appsignin

// The routes (plan 45):
//
//	GET  /cosmos/api/app-sign-in/defaults               the installer's defaults (resources)
//	GET  /cosmos/api/servapps/{containerId}/sign-in       what the card shows, no secret (credentials-read)
//	POST /cosmos/api/servapps/{containerId}/sign-in/reveal one secret, recorded (credentials-read)
//
// credentials-read is a permission that needs the password confirmation
// (utils.SudoPermissions): CheckPermissions refuses a session without a
// recent one with HTTP008, and an API token without the permission with
// AT003. That is upstream's bar for reading a container's variables at all
// (GET /api/servapps/{id}/ masks every value below it); the card never asks
// less. The reveal answers one value only, never the whole environment.

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	conttype "github.com/docker/docker/api/types/container"
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// inspect reads a container; replaced by the tests.
var inspect = func(ctx context.Context, name string) (containerInfo, error) {
	if err := docker.Connect(); err != nil {
		return containerInfo{}, err
	}
	c, err := docker.DockerClient.ContainerInspect(ctx, name)
	if err != nil {
		return containerInfo{}, err
	}
	info := containerInfo{Name: c.Name, Labels: map[string]string{}}
	if c.Config != nil {
		info.Image = c.Config.Image
		info.Env = c.Config.Env
		for key, value := range c.Config.Labels {
			info.Labels[key] = value
		}
		// the fork's own labels count only when they are not the image's (fork_image_labels.go)
		for _, key := range []string{LabelKey, ManagerLabel} {
			if value := docker.ForkAdminLabel(c, key); value != "" {
				info.Labels[key] = value
			} else {
				delete(info.Labels, key)
			}
		}
	}
	for _, m := range c.Mounts {
		info.Mounts = append(info.Mounts, mountInfo{Type: string(m.Type), Source: m.Source, Name: m.Name, Destination: m.Destination})
	}
	return info, nil
}

// managersOf lists the containers whose manager label names this one, as the
// administrator set it (never an image's own label); replaced by the tests.
var managersOf = func(ctx context.Context, name string) []string {
	out := []string{}
	if err := docker.Connect(); err != nil {
		return out
	}
	list, err := docker.DockerClient.ContainerList(ctx, conttype.ListOptions{All: true})
	if err != nil {
		return out
	}
	for _, item := range list {
		if item.Labels[ManagerLabel] != name || len(item.Names) == 0 {
			continue
		}
		full, err := docker.DockerClient.ContainerInspect(ctx, item.ID)
		if err == nil && docker.ForkAdminLabel(full, ManagerLabel) == name {
			out = append(out, strings.TrimPrefix(item.Names[0], "/"))
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}

// routesNow and serverURL are the configuration's; replaced by the tests.
var routesNow = func() []utils.ProxyRouteConfig {
	return utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes
}

var serverURL = func(host string) string { return utils.GetServerURL(host) }

// window counts events per key in a sliding minute.
type window struct {
	mu     sync.Mutex
	limit  int
	period time.Duration
	seen   map[string][]time.Time
}

func newWindow(limit int, period time.Duration) *window {
	return &window{limit: limit, period: period, seen: map[string][]time.Time{}}
}

func (w *window) allow(key string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.seen[key][:0]
	for _, t := range w.seen[key] {
		if now.Sub(t) < w.period {
			kept = append(kept, t)
		}
	}
	if len(kept) >= w.limit {
		w.seen[key] = kept
		return false
	}
	w.seen[key] = append(kept, now)
	if len(w.seen) > 1024 { // a bound on the keys: old ones go
		for k, times := range w.seen {
			if len(times) == 0 || now.Sub(times[len(times)-1]) >= w.period {
				delete(w.seen, k)
			}
		}
	}
	return true
}

var (
	revealPerCaller = newWindow(20, time.Minute)
	revealAll       = newWindow(120, time.Minute)
	clock           = time.Now
)

func callerKey(req *http.Request) string {
	ctx := utils.GetAuthContext(req)
	if ctx.APIToken != nil {
		return "token:" + ctx.APIToken.Name
	}
	return "user:" + ctx.Nickname
}

func containerOf(w http.ResponseWriter, req *http.Request) (string, bool) {
	name := strings.TrimPrefix(mux.Vars(req)["containerId"], "/")
	if !validContainerName(name) {
		utils.HTTPError(w, "Invalid container name", http.StatusBadRequest, "SI001")
		return "", false
	}
	return name, true
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
}

// SignInRoute answers GET /cosmos/api/servapps/{containerId}/sign-in.
func SignInRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CREDENTIALS_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	name, ok := containerOf(w, req)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	c, err := inspect(ctx, name)
	if err != nil {
		utils.HTTPError(w, "No such container", http.StatusNotFound, "SI002")
		return
	}
	details := resolve(c, routesNow(), serverURL)
	details.Managers = managersOf(ctx, details.Container)
	clean(&details, details.secretValues) // the last check of this exit (secrets.go); resolve ran it already
	noStore(w)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": details})
}

var secretID = regexp.MustCompile(`^(password|connection)\.[0-3]$`)

// RevealRoute answers POST /cosmos/api/servapps/{containerId}/sign-in/reveal
// {"secret": "password.0", "purpose": "show"|"copy"} with that one value.
func RevealRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CREDENTIALS_READ) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	name, ok := containerOf(w, req)
	if !ok {
		return
	}
	var body struct {
		Secret  string `json:"secret"`
		Purpose string `json:"purpose"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || !secretID.MatchString(body.Secret) || (body.Purpose != "show" && body.Purpose != "copy") {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "SI003")
		return
	}
	now := clock()
	if !revealPerCaller.allow(callerKey(req), now) || !revealAll.allow("all", now) {
		utils.HTTPError(w, "Too many requests: wait a minute", http.StatusTooManyRequests, "SI429")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	c, err := inspect(ctx, name)
	if err != nil {
		utils.HTTPError(w, "No such container", http.StatusNotFound, "SI002")
		return
	}
	details := resolve(c, routesNow(), serverURL)
	value, found := secretValue(details, c, body.Secret)
	if !found {
		utils.HTTPError(w, "This container has no such secret", http.StatusNotFound, "SI004")
		return
	}
	utils.RecordSecurity(req, utils.SecCredentialsRevealed, details.Container, "secret", body.Secret, "purpose", body.Purpose)
	noStore(w)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": map[string]string{"value": value}})
}

// Defaults is what an installation proposes, for the caller.
type Defaults struct {
	User      string `json:"user"`
	Email     string `json:"email"`
	UserFrom  string `json:"userFrom"`  // config, account, ""
	EmailFrom string `json:"emailFrom"` // config, account, ""
	Length    int    `json:"length"`
	Mail      struct {
		On    bool   `json:"on"`
		Ready bool   `json:"ready"`
		Why   string `json:"why,omitempty"` // off, noMail, noAddress, token
	} `json:"mail"`
}

// userRecord gives an account's e-mail; replaced by the tests.
var userRecord = func(nickname string) (string, error) {
	user, err := utils.GetUser(nickname)
	if err != nil {
		return "", err
	}
	return user.Email, nil
}

var configNow = utils.GetMainConfig

func defaultsFor(req *http.Request) Defaults {
	config := configNow()
	ctx := utils.GetAuthContext(req)
	d := Defaults{Length: config.AppSignIn.PasswordLength()}
	// review minor 4: the configured name and e-mail are personal data, given only to those who see them
	// unmasked on the configuration page; anyone else is proposed their own
	if utils.HasPermission(req, utils.PERM_CONFIGURATION_READ) && utils.HasPermission(req, utils.PERM_CREDENTIALS_READ) {
		d.User, d.Email = config.AppSignIn.User, config.AppSignIn.Email
	}
	if d.User != "" {
		d.UserFrom = "config"
	}
	if d.Email != "" {
		d.EmailFrom = "config"
	}
	accountEmail := ""
	if ctx.APIToken == nil && ctx.Nickname != "" {
		if d.User == "" {
			d.User, d.UserFrom = ctx.Nickname, "account"
		}
		if email, err := userRecord(ctx.Nickname); err == nil {
			accountEmail = email
		}
		if d.Email == "" && accountEmail != "" {
			d.Email, d.EmailFrom = accountEmail, "account"
		}
	}
	d.Mail.On = !config.AppSignIn.MailOff
	switch {
	case !d.Mail.On:
		d.Mail.Why = "off"
	case ctx.APIToken != nil:
		d.Mail.Why = "token"
	case !config.EmailConfig.Enabled:
		d.Mail.Why = "noMail"
	case accountEmail == "":
		d.Mail.Why = "noAddress"
	default:
		d.Mail.Ready = true
	}
	return d
}

// DefaultsRoute answers GET /cosmos/api/app-sign-in/defaults.
func DefaultsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	noStore(w)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": defaultsFor(req)})
}
