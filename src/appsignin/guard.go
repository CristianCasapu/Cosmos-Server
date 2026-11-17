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

func guardRoutes(routes []utils.ProxyRouteConfig, who string) []string {
	changed := []string{}
	seen := map[string]string{}
	for i := range routes {
		r := &routes[i]
		if r.Mode != "SERVAPP" || (r.AuthEnabled && r.AdminOnly) {
			continue
		}
		name := routeContainer(*r)
		if name == "" || !validContainerName(name) {
			continue
		}
		image, ok := seen[name]
		if !ok {
			image = imageOf(name)
			seen[name] = image
		}
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
