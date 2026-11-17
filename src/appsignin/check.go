package appsignin

// Before a creation (docker.ForkBeforeCreation), for every door. Nothing here
// refuses what an ordinary compose import, Market install, new-container form,
// image update or start-problem repair sends (second review R1): the server
// writes the right answer itself where one exists.
//
//   - cosmos-fork-signin: the server writes the label it rebuilds from the
//     request (the image's first-run entry, or the guess by the variables'
//     names shared with the panel through testdata/guesses.json; none for a
//     recipe's image), replacing whatever came. A repair that adds a variable,
//     a hand edit, a compose file or a template can never trip it, nor smuggle
//     one in. A first-run label keeps only its proposed username, checked.
//   - cosmos-fork-manager-of: kept only on a web manager's image naming a
//     container of the request or one it links to; otherwise removed.
//   - The routes of web managers, of tools that sign in with their database's
//     accounts and of web pages without a sign-in (noAuth), in any service of
//     the request, are for administrators behind Cosmos' sign-in (minor 1).
//   - A web manager is never on Docker's default bridge: one without a network
//     of its own gets a new one (any door). The one refusal left: a manager's
//     network that exists already and holds containers of other apps (a
//     manager must never share its network with strangers). A network made for
//     it earlier (a repair, an edit: ?replace=1) holds only this request's
//     containers and is accepted (R1a); the panel's names carry a random part,
//     so an ordinary installation never meets an existing one.

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	doctype "github.com/docker/docker/api/types"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// networkMembers answers whether a network exists, and the names of the
// containers on it; replaced by the tests.
var networkMembers = func(name string) (bool, []string) {
	if err := docker.Connect(); err != nil {
		return true, []string{"?"} // cannot tell: treated as holding strangers
	}
	n, err := docker.DockerClient.NetworkInspect(context.Background(), name, doctype.NetworkInspectOptions{})
	if err != nil {
		return false, nil
	}
	out := []string{}
	for _, c := range n.Containers {
		out = append(out, strings.TrimPrefix(c.Name, "/"))
	}
	return true, out
}

// randomPart is the random part of a network's name made here.
var randomPart = func() string {
	const set = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = set[int(b[i])%len(set)]
	}
	return string(b)
}

// rebuiltLabel is the sign-in label the review writes for a service, from the
// request alone; "" for none. A first-run label keeps its proposed username.
func rebuiltLabel(service docker.ContainerCreateRequestContainer) string {
	image := service.Image
	if recipe := anyimage.RecipeForImage(image); recipe != nil && recipe.Login != nil {
		return ""
	}
	if first := anyimage.FirstRunForImage(image); first != nil {
		user := ""
		if info, ok := parseLabel(service.Labels[LabelKey]); ok && identifier.MatchString(info.first) {
			user = info.first
		}
		parts := []string{"where=web"}
		if user != "" {
			parts = append(parts, "first="+user)
		}
		return strings.Join(append(parts, "note="+first.Note, "from=review"), ";")
	}
	names := []string{}
	for _, line := range service.Environment {
		if i := strings.Index(line, "="); i > 0 {
			names = append(names, line[:i])
		}
	}
	user, email, password, ok := GuessFromNames(names)
	if !ok {
		return ""
	}
	parts := []string{"where=web"}
	if user != "" {
		parts = append(parts, "user="+user)
	}
	if email != "" {
		parts = append(parts, "email="+email)
	}
	return strings.Join(append(parts, "password="+password, "from=guess"), ";")
}

// protectedImage: a web manager, a tool that signs in with its database's
// accounts, or an image whose recipe says it has no sign-in of its own.
func protectedImage(image string) bool {
	recipe := anyimage.RecipeForImage(image)
	return recipe != nil && (recipe.Manager != nil || hasNote(recipe, "noAuth") || (recipe.Login != nil && recipe.Login.Via == "database"))
}

func beforeCreation(req *http.Request, request *docker.DockerServiceCreateRequest) error {
	images := map[string]string{} // container name -> image, for routes of other services
	for key, service := range request.Services {
		name := service.Name
		if name == "" {
			name = key
		}
		images[strings.TrimPrefix(name, "/")] = service.Image
	}
	keys := make([]string, 0, len(request.Services))
	for key := range request.Services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		service := request.Services[key]
		name := strings.TrimPrefix(service.Name, "/")
		if name == "" {
			name = key
		}
		labels := map[string]string{}
		for k, v := range service.Labels {
			labels[k] = v
		}
		service.Labels = labels

		if want := rebuiltLabel(service); want != "" {
			service.Labels[LabelKey] = want
		} else {
			delete(service.Labels, LabelKey)
		}

		isManager := anyimage.RecipeForImage(service.Image) != nil && anyimage.RecipeForImage(service.Image).Manager != nil
		if of, ok := service.Labels[ManagerLabel]; ok {
			linked := false
			for _, l := range service.Links {
				linked = linked || l == of
			}
			if !isManager || !validContainerName(of) || !(images[of] != "" || linked) {
				delete(service.Labels, ManagerLabel)
			}
		}

		// administrators only, behind the sign-in, for a route to such a container, whichever service carries it
		for i, route := range service.Routes {
			target := routeContainer(route)
			if target == "" {
				target = name
			}
			image := images[target]
			if image == "" && target == name {
				image = service.Image
			}
			if image != "" && protectedImage(image) {
				service.Routes[i].AuthEnabled = true
				service.Routes[i].AdminOnly = true
			}
		}

		if isManager {
			mode := service.NetworkMode
			if mode == "" || mode == "bridge" || mode == "default" {
				// a network of its own, made for it (any door)
				fresh := name + "-" + randomPart()
				if request.Networks == nil {
					request.Networks = map[string]docker.ContainerCreateRequestNetwork{}
				}
				request.Networks[fresh] = docker.ContainerCreateRequestNetwork{Driver: "bridge"}
				if service.Networks == nil {
					service.Networks = map[string]docker.ContainerCreateRequestServiceNetwork{}
				}
				service.Networks[fresh] = docker.ContainerCreateRequestServiceNetwork{}
				service.NetworkMode = fresh
			}
			if service.Labels[ManagerLabel] != "" {
				nets := []string{service.NetworkMode}
				for n := range service.Networks {
					nets = append(nets, n)
				}
				for _, n := range nets {
					if strings.HasPrefix(n, "container:") || strings.HasPrefix(n, "service:") || n == "host" {
						continue
					}
					exists, members := networkMembers(n)
					if !exists {
						continue
					}
					for _, m := range members {
						if images[m] == "" {
							return errors.New("The web manager " + name + " would share the network " + n + " with another app (" + m + "): a manager gets a network of its own. Install it again from the database's page.")
						}
					}
				}
			}
		}
		request.Services[key] = service
	}
	return nil
}

// routeContainer is the container a route's target names, "" when it names none.
func routeContainer(route utils.ProxyRouteConfig) string {
	if route.Mode != "SERVAPP" && route.Mode != "" {
		return ""
	}
	target := route.Target
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func hasNote(recipe *anyimage.Recipe, note string) bool {
	for _, n := range recipe.Notes {
		if n == note {
			return true
		}
	}
	return false
}
