package configapi

// Fork addition (plan 49): every interface of an app, known as data.
//
// The home page, ServApps and the app's page choose the link an app's tile
// opens among its URLs (client/src/utils/appInterfaces.js). For that each URL
// needs a role: the app's administration page, its page for users, its API,
// or something else. The administrator can set it on the URL form
// (route.ForkInterface); otherwise it is proposed here, in one place, from
// what the fork knows about the app, in this order:
//
//  1. the recipe of the container's image (src/anyimage/recipes.json): the
//     URL leads to the recipe's web port (a console: RustFS, RabbitMQ, a
//     database manager) or to its service port (an API: RustFS' S3);
//  2. the known apps (src/behind/apps.json) whose web port the URL leads to,
//     with the role of that page from the table below;
//  3. the URL's path (/admin, /console, /api …);
//  4. words of the URL's name and description (console, admin, api, s3 …);
//  5. else "a page for users", said as a guess.
//
// GET /api/dashboard carries the result for the routes the caller may see.
// A plain user gets nothing about containers: the image and the published
// ports go to administrators only.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/behind"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Where a role comes from.
const (
	roleFromSetting = "set"
	roleFromRecipe  = "recipe"
	roleFromApp     = "app"
	roleFromPath    = "path"
	roleFromWords   = "words"
	roleFromNothing = "none"
)

// knownAppRole is the role of a known app's web page and, for an app whose
// administration lives on a path of the same page, that path. Each from the
// app's own documentation.
type knownAppRole struct {
	Role      string
	AdminPath string
}

var knownAppRoles = map[string]knownAppRole{
	// one page, which is where the app is run: administration
	"qbittorrent":  {Role: utils.InterfaceAdmin},
	"transmission": {Role: utils.InterfaceAdmin},
	"deluge":       {Role: utils.InterfaceAdmin},
	"sonarr":       {Role: utils.InterfaceAdmin},
	"radarr":       {Role: utils.InterfaceAdmin},
	"codeserver":   {Role: utils.InterfaceAdmin},
	"portainer":    {Role: utils.InterfaceAdmin},
	"uptimekuma":   {Role: utils.InterfaceAdmin},
	"syncthing":    {Role: utils.InterfaceAdmin},
	"pihole":       {Role: utils.InterfaceAdmin}, // https://docs.pi-hole.net/ (the web interface is /admin)
	"adguard":      {Role: utils.InterfaceAdmin},
	"phpmyadmin":   {Role: utils.InterfaceAdmin},
	"adminer":      {Role: utils.InterfaceAdmin},
	"pgadmin":      {Role: utils.InterfaceAdmin},
	// a page for users, with the administration on a path of its own
	"jellyfin":      {Role: utils.InterfaceUser, AdminPath: "/web/#/dashboard"},            // https://jellyfin.org/docs/general/server/settings
	"emby":          {Role: utils.InterfaceUser, AdminPath: "/web/index.html#!/dashboard"}, // https://emby.media/support/articles/Server-Dashboard.html
	"homeassistant": {Role: utils.InterfaceUser, AdminPath: "/config"},                     // https://www.home-assistant.io/docs/configuration/
	"nextcloud":     {Role: utils.InterfaceUser, AdminPath: "/settings/admin"},             // https://docs.nextcloud.com/server/latest/admin_manual/
	"vaultwarden":   {Role: utils.InterfaceUser, AdminPath: "/admin"},                      // https://github.com/dani-garcia/vaultwarden/wiki/Enabling-admin-page
	"gitea":         {Role: utils.InterfaceUser, AdminPath: "/-/admin"},                    // https://docs.gitea.com/ (site administration, 1.21 and later)
	"forgejo":       {Role: utils.InterfaceUser, AdminPath: "/-/admin"},                    // https://forgejo.org/docs/latest/admin/
	"paperless":     {Role: utils.InterfaceUser},
	"grafana":       {Role: utils.InterfaceUser},
	"vite":          {Role: utils.InterfaceOther},
	"webpack":       {Role: utils.InterfaceOther},
}

// recipeWebRole is the role of a recipe's web port by the recipe's kind: the
// web page of a database manager, a store, a broker or a cache is its console.
func recipeWebRole(kind string) string {
	switch kind {
	case "web":
		return utils.InterfaceUser
	case "":
		return ""
	}
	return utils.InterfaceAdmin
}

var (
	adminPath = regexp.MustCompile(`(?i)^/(admin|administrator|console|dashboard|manage|manager|settings|config)(/|$)`)
	apiPath   = regexp.MustCompile(`(?i)^/(api|v[0-9]+|s3|graphql|rpc|xmlrpc|webdav|dav)(/|$)`)
	adminWord = regexp.MustCompile(`(?i)(^|[^a-z])(console|admin|administration|dashboard|manager|management|panel|webui)([^a-z]|$)`)
	apiWord   = regexp.MustCompile(`(?i)(^|[^a-z])(api|s3|rpc|webdav)([^a-z]|$)`)
)

// targetPort is the port of a route's target ("http://rustfs:9001" → 9001),
// the scheme's own when none is written, 0 when it cannot be read.
func targetPort(target string) int {
	scheme, rest, found := strings.Cut(strings.TrimSpace(target), "://")
	if !found {
		return 0
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, ':'); i >= 0 && !strings.HasSuffix(rest, "]") {
		port, err := strconv.Atoi(rest[i+1:])
		if err != nil || port < 1 || port > 65535 {
			return 0
		}
		return port
	}
	switch strings.ToLower(scheme) {
	case "http":
		return 80
	case "https":
		return 443
	}
	return 0
}

// RoleProposal is the role proposed for a URL, with where it comes from and,
// for a known app, the path of its administration on the same page.
type RoleProposal struct {
	Role      string
	Source    string
	AdminPath string
}

// proposeRole is the rule above. image is the container's ("" for a URL
// that does not lead to a container or a caller who may not know it).
func proposeRole(route utils.ProxyRouteConfig, image string) RoleProposal {
	port := targetPort(route.Target)
	if image != "" && route.Mode == "SERVAPP" {
		if recipe := anyimage.RecipeForImage(image); recipe != nil && port != 0 {
			if recipe.Web != nil && recipe.Web.Port == port {
				if role := recipeWebRole(recipe.Kind); role != "" {
					return RoleProposal{Role: role, Source: roleFromRecipe}
				}
			}
			if recipe.Service != nil && recipe.Service.Port == port {
				return RoleProposal{Role: utils.InterfaceAPI, Source: roleFromRecipe}
			}
		}
		if app := behind.AppFor(image); app != nil && (port == 0 || app.Port == 0 || app.Port == port) {
			if known, ok := knownAppRoles[app.ID]; ok {
				return RoleProposal{Role: known.Role, Source: roleFromApp, AdminPath: known.AdminPath}
			}
		}
	}
	if route.UsePathPrefix {
		if adminPath.MatchString(route.PathPrefix) {
			return RoleProposal{Role: utils.InterfaceAdmin, Source: roleFromPath}
		}
		if apiPath.MatchString(route.PathPrefix) {
			return RoleProposal{Role: utils.InterfaceAPI, Source: roleFromPath}
		}
	}
	words := route.Name + " " + route.Description
	if adminWord.MatchString(words) {
		return RoleProposal{Role: utils.InterfaceAdmin, Source: roleFromWords}
	}
	if apiWord.MatchString(words) {
		return RoleProposal{Role: utils.InterfaceAPI, Source: roleFromWords}
	}
	return RoleProposal{Role: utils.InterfaceUser, Source: roleFromNothing}
}

// ForkDashboardPort is a published port of the app's container (administrators only).
type ForkDashboardPort struct {
	Private  int    `json:"private"`
	Public   int    `json:"public"`
	Protocol string `json:"protocol"`
	IP       string `json:"ip,omitempty"`
}

// ForkDashboardFields are the fork's additions to a route of GET /api/dashboard.
type ForkDashboardFields struct {
	AuthEnabled bool `json:"AuthEnabled,omitempty"` // behind Cosmos' sign-in
	// what the URL is to its app: as set on the URL form, else proposed
	ForkRole         string `json:"ForkRole,omitempty"`
	ForkRoleSource   string `json:"ForkRoleSource,omitempty"`
	ForkRoleProposed string `json:"ForkRoleProposed,omitempty"` // what it would be without the setting (for the URL form)
	ForkLabel        string `json:"ForkLabel,omitempty"`
	ForkHomeOnly     bool   `json:"ForkHomeOnly,omitempty"`
	ForkAdminPath    string `json:"ForkAdminPath,omitempty"`
	// the app's names for use at home that copy this URL
	ForkHomeHosts []string `json:"ForkHomeHosts,omitempty"`
	// administrators only: the container, its image and its published ports
	ForkContainer string              `json:"ForkContainer,omitempty"`
	ForkImage     string              `json:"ForkImage,omitempty"`
	ForkPorts     []ForkDashboardPort `json:"ForkPorts,omitempty"`
}

// forkDashboardRoutes is the hook of DashboardApiGet: it fills the fork's
// fields of the routes the caller gets. containers is what Docker listed.
func forkDashboardRoutes(out []DashboardRoute, config utils.Config, containers []types.Container, isAdmin bool, homeNames []utils.HomeName) []DashboardRoute {
	byName := map[string]utils.ProxyRouteConfig{}
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if _, seen := byName[route.Name]; !seen {
			byName[route.Name] = route
		}
	}
	containerOf := func(name string) *types.Container {
		if name == "" {
			return nil
		}
		for i := range containers {
			for _, n := range containers[i].Names {
				if n == "/"+name || n == name {
					return &containers[i]
				}
			}
		}
		return nil
	}
	for i := range out {
		route, ok := byName[out[i].Name]
		if !ok {
			continue
		}
		fields := ForkDashboardFields{AuthEnabled: route.AuthEnabled}
		image := ""
		var c *types.Container
		if route.Mode == "SERVAPP" {
			c = containerOf(extractContainerName(route.Target))
			if c != nil {
				image = c.Image
			}
		}
		proposal := proposeRole(route, image)
		fields.ForkRole, fields.ForkRoleSource, fields.ForkAdminPath = proposal.Role, proposal.Source, proposal.AdminPath
		fields.ForkRoleProposed = proposal.Role
		if setting := route.ForkInterface; setting != nil && utils.RouteInterfaceProblem(setting) == "" {
			if setting.Role != "" {
				fields.ForkRole, fields.ForkRoleSource = setting.Role, roleFromSetting
			}
			fields.ForkLabel, fields.ForkHomeOnly = setting.Label, setting.HomeOnly
		}
		if fields.ForkRole != utils.InterfaceUser {
			fields.ForkAdminPath = "" // the administration path is of a page for users
		}
		for _, name := range homeNames {
			if name.Route == route.Name && len(fields.ForkHomeHosts) < 4 {
				fields.ForkHomeHosts = append(fields.ForkHomeHosts, name.Host)
			}
		}
		if isAdmin && c != nil {
			fields.ForkContainer = strings.TrimPrefix(firstName(c.Names), "/")
			fields.ForkImage = c.Image
			fields.ForkPorts = dashboardPorts(c)
		}
		out[i].ForkDashboardFields = fields
	}
	return out
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// dashboardPorts are a container's ports published on the host, once each
// (Docker lists a port once per address family), at most 32.
func dashboardPorts(c *types.Container) []ForkDashboardPort {
	seen := map[string]bool{}
	out := []ForkDashboardPort{}
	for _, p := range c.Ports {
		if p.PublicPort == 0 {
			continue
		}
		protocol := strings.ToLower(p.Type)
		if protocol == "" {
			protocol = "tcp"
		}
		ip := p.IP
		if ip == "0.0.0.0" || ip == "::" {
			ip = ""
		}
		key := strconv.Itoa(int(p.PrivatePort)) + "/" + strconv.Itoa(int(p.PublicPort)) + "/" + protocol + "/" + ip
		if seen[key] || len(out) >= 32 {
			continue
		}
		seen[key] = true
		out = append(out, ForkDashboardPort{Private: int(p.PrivatePort), Public: int(p.PublicPort), Protocol: protocol, IP: ip})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Private < out[b].Private })
	return out
}
