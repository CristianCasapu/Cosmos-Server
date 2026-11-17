package appsignin

// Where URLs are saved (utils.ForkGuardRoutes): a URL to a web manager, a tool
// that signs in with its database's accounts, or a web page without a sign-in
// of its own (by the recipe of the container's image) is for administrators
// behind Cosmos' sign-in, whichever form made it and whenever (second review,
// minor). The administrator is told by a notification when Cosmos turned it on.

import (
	"context"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// imageOf answers the image of a container, "" when there is none; replaced by the tests.
var imageOf = func(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := inspect(ctx, name)
	if err != nil {
		return ""
	}
	return c.Image
}

// guardWait bounds the whole look at Docker of one save: the callers hold
// the configuration's lock, and a Docker that hangs must not keep every save
// of the settings waiting (third review). Past it the URLs are saved as they
// came; the next save looks again.
var guardWait = 4 * time.Second

// imagesOf asks Docker for the images of these containers, all within guardWait.
func imagesOf(names []string) (map[string]string, bool) {
	done := make(chan map[string]string, 1)
	ask := imageOf // read here: the goroutine below may outlive this call
	go func() {
		out := map[string]string{}
		for _, name := range names {
			out[name] = ask(name)
		}
		done <- out
	}()
	select {
	case out := <-done:
		return out, true
	case <-time.After(guardWait):
		return nil, false
	}
}

func guardRoutes(routes []utils.ProxyRouteConfig, who string) []string {
	changed := []string{}
	// the containers of the URLs that are not protected yet (nothing is asked for the others)
	names, asked := []string{}, map[string]bool{}
	for i := range routes {
		r := &routes[i]
		if r.Mode != "SERVAPP" || (r.AuthEnabled && r.AdminOnly) {
			continue
		}
		name := routeContainer(*r)
		if name == "" || !validContainerName(name) || asked[name] {
			continue
		}
		asked[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return changed
	}
	seen, ok := imagesOf(names)
	if !ok {
		utils.Warn("URLs: Docker did not answer in time, so the URLs were saved without looking at which of them lead to a database manager; the next save looks again")
		return changed
	}
	for i := range routes {
		r := &routes[i]
		if r.Mode != "SERVAPP" || (r.AuthEnabled && r.AdminOnly) {
			continue
		}
		image := seen[routeContainer(*r)]
		if image == "" || !protectedImage(image) {
			continue
		}
		r.AuthEnabled, r.AdminOnly = true, true
		changed = append(changed, r.Name)
	}
	if len(changed) > 0 {
		utils.Log("URLs set for administrators behind the sign-in (a tool that controls data): " + strings.Join(changed, ", "))
		recipient := who
		if recipient == "" {
			recipient = "admin"
		}
		notify(utils.Notification{Recipient: recipient, Level: "info", Fork: &utils.NotificationInfo{
			Kind:    "route.protected",
			Subject: &utils.NotificationSubject{Type: "route", Name: changed[0], Link: "/cosmos-ui/config-url"},
			Title:   utils.NText("notif.route.protected.title", "names", strings.Join(changed, ", ")),
			What:    utils.NText("notif.route.protected.what", "names", strings.Join(changed, ", ")),
			Means:   utils.NText("notif.route.protected.means"),
		}})
	}
	return changed
}
