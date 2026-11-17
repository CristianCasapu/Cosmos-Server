package appsignin

// Before a creation (docker.ForkBeforeCreation), for every door:
//
//   - The label cosmos-fork-signin must be the one the review writes for that
//     service (review F1a). The review always writes its own and removes one
//     that came with a compose file or a Market template, so a request from
//     the panel always passes; the server rebuilds the label from the request
//     (the image's first-run entry, or the guess by the variables' names, the
//     rules the panel shares with guess.go) and refuses any other. A recipe's
//     image takes no label at all. How the two sides agree is tested with the
//     shared cases of testdata/guesses.json.
//   - The label cosmos-fork-manager-of only on the image of a web manager, and
//     naming a container of the same request or one it links to.
//   - The routes of a web manager and of an app without a sign-in of its own
//     (a recipe's noAuth, a tool that signs in with its database's accounts)
//     are for administrators behind Cosmos' sign-in, whatever was sent (review
//     minor 1).
//   - A web manager's networks are made for it: one that exists already is not
//     joined (review minor 2), and a manager is never on Docker's default
//     bridge (review F2): its network mode is one of its own networks.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	doctype "github.com/docker/docker/api/types"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/docker"
)

// networkExists answers whether a network exists already; replaced by the tests.
var networkExists = func(name string) bool {
	if err := docker.Connect(); err != nil {
		return true // cannot tell: refused rather than joined
	}
	_, err := docker.DockerClient.NetworkInspect(context.Background(), name, doctype.NetworkInspectOptions{})
	return err == nil
}

// expectedLabel is the label the review writes for a service, "" for none.
func expectedLabel(service docker.ContainerCreateRequestContainer, label string) (string, bool) {
	image := service.Image
	if recipe := anyimage.RecipeForImage(image); recipe != nil && recipe.Login != nil {
		return "", true
	}
	if first := anyimage.FirstRunForImage(image); first != nil {
		info, ok := parseLabel(label)
		if !ok || info.firstNote != first.Note || info.where != "web" || info.user != "" || info.email != "" || info.password != "" || info.database != "" {
			return "", false
		}
		// the proposed username is the administrator's (it is checked again where it is shown)
		return label, true
	}
	names := []string{}
	for _, line := range service.Environment {
		if i := strings.Index(line, "="); i > 0 {
			names = append(names, line[:i])
		}
	}
	user, email, password, ok := GuessFromNames(names)
	if !ok {
		return "", true
	}
	parts := []string{"where=web"}
	if user != "" {
		parts = append(parts, "user="+user)
	}
	if email != "" {
		parts = append(parts, "email="+email)
	}
	parts = append(parts, "password="+password, "from=guess")
	return strings.Join(parts, ";"), true
}

func beforeCreation(req *http.Request, request *docker.DockerServiceCreateRequest) error {
	names := map[string]bool{}
	for key, service := range request.Services {
		name := service.Name
		if name == "" {
			name = key
		}
		names[strings.TrimPrefix(name, "/")] = true
	}
	keys := make([]string, 0, len(request.Services))
	for key := range request.Services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		service := request.Services[key]
		name := service.Name
		if name == "" {
			name = key
		}
		recipe := anyimage.RecipeForImage(service.Image)

		if label, ok := service.Labels[LabelKey]; ok {
			want, known := expectedLabel(service, label)
			if !known || label != want {
				return errors.New("The label " + LabelKey + " of " + name + " was not written by Cosmos' review of this installation: remove it from the definition.")
			}
		}

		isManager := recipe != nil && recipe.Manager != nil
		if of, ok := service.Labels[ManagerLabel]; ok {
			linked := false
			for _, l := range service.Links {
				linked = linked || l == of
			}
			if !isManager || !validContainerName(of) || !(names[of] || linked) {
				return errors.New("The label " + ManagerLabel + " of " + name + " was not written by Cosmos' review of this installation: remove it from the definition.")
			}
		}

		if recipe != nil && (isManager || hasNote(recipe, "noAuth") || (recipe.Login != nil && recipe.Login.Via == "database")) {
			for i := range service.Routes {
				service.Routes[i].AuthEnabled = true
				service.Routes[i].AdminOnly = true
			}
		}

		if isManager && service.Labels[ManagerLabel] != "" {
			own := map[string]bool{}
			for net := range service.Networks {
				if _, declared := request.Networks[net]; !declared || networkExists(net) {
					return errors.New("The web manager " + name + " must get a network of its own, made for it: " + net + " is not new.")
				}
				own[net] = true
			}
			if !own[service.NetworkMode] {
				return errors.New("The web manager " + name + " must not be on Docker's default network: its network mode must be one of its own networks.")
			}
		}
		request.Services[key] = service
	}
	return nil
}

func hasNote(recipe *anyimage.Recipe, note string) bool {
	for _, n := range recipe.Notes {
		if n == note {
			return true
		}
	}
	return false
}
