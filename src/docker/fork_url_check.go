package docker

// Fork addition (plan 44): what the URL check (src/behind) needs to know of a
// container: its own address (the same rules as the port check: never the
// host's network, never the server's loopback), its image, the values of the
// few variables the knowledge of its app names (never all of them: a secret
// is never read), and the lines its log wrote while it was asked.

import (
	"context"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types/container"
)

// URLCheckFacts is what the URL check knows of a container.
type URLCheckFacts struct {
	ID    string
	IP    string
	Why   string // why there is no address to ask: notRunning, hostNetwork, missing, unreachable
	Image string
	TTY   bool
	// Env holds the values of the variables asked for that the container has
	Env map[string]string
}

// URLCheckFactsOf inspects a container. envNames are the variables whose
// values are wanted (at most 16, and never one whose name says it is secret).
var URLCheckFactsOf = func(ctx context.Context, name string, envNames []string) URLCheckFacts {
	if err := Connect(); err != nil {
		return URLCheckFacts{Why: "unreachable"}
	}
	c, err := DockerClient.ContainerInspect(ctx, name)
	if err != nil {
		return URLCheckFacts{Why: "missing"}
	}
	facts := URLCheckFacts{ID: c.ID, Env: map[string]string{}}
	facts.IP, facts.Why = probeAddressOf(c)
	if c.Config != nil {
		facts.Image = c.Config.Image
		facts.TTY = c.Config.Tty
		wanted := map[string]bool{}
		for i, n := range envNames {
			if i < 16 && !secretName.MatchString(n) {
				wanted[n] = true
			}
		}
		for _, kv := range c.Config.Env {
			if i := strings.IndexByte(kv, '='); i > 0 && wanted[kv[:i]] {
				facts.Env[kv[:i]] = kv[i+1:]
			}
		}
	}
	return facts
}

// URLCheckLogSince is what the container's log said since a moment, cleaned
// and bounded (the end of at most 64 KiB, 40 lines), its secret values masked.
var URLCheckLogSince = func(ctx context.Context, id string, tty bool, since time.Time) []string {
	if err := Connect(); err != nil {
		return nil
	}
	reader, err := DockerClient.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Since: since.UTC().Format(time.RFC3339Nano), Tail: "200"})
	if err != nil {
		return nil
	}
	defer reader.Close()
	text, _ := demuxTail(reader, tty)
	secrets := []string{}
	if c, err := DockerClient.ContainerInspect(ctx, id); err == nil && c.Config != nil {
		secrets = secretValues(c.Config.Env)
	}
	return splitLog(maskText(string(text), secrets))
}

// KnownRouteSetting is set by src/behind: whether the known app of an image
// is to be told its own address as Host, and the app's name ("" when unknown).
var KnownRouteSetting func(image string) (appOwnHost bool, app string)

// forkKnownRoute gives a new URL of a compose (the Market, Install any image)
// what its app is known to need, said in the install's log: a known app that
// only answers its own address (qBittorrent) is told it. A URL that says
// otherwise for itself is left as it is.
func forkKnownRoute(spec DockerServiceCreateRequest, route utils.ProxyRouteConfig, onLog func(string)) utils.ProxyRouteConfig {
	if KnownRouteSetting == nil || route.Mode != "SERVAPP" || route.ForkAppOwnHost || route.SpoofHostname || route.OverwriteHostHeader != "" {
		return route
	}
	for _, svc := range spec.Services {
		for _, r := range svc.Routes {
			if r.Name != route.Name {
				continue
			}
			if own, app := KnownRouteSetting(svc.Image); own {
				route.ForkAppOwnHost = true
				if onLog != nil {
					onLog(utils.DoSuccess("%s", "URL "+route.Name+" tells "+app+" its own address (it only answers that one)\n"))
				}
			}
			return route
		}
	}
	return route
}
