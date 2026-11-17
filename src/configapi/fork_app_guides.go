package configapi

// Fork addition (plan 50): what the "How to use it" guide of an app needs to
// know about the server, carried by GET /api/dashboard next to the roles of
// plan 49 (same permission, same filtering: a URL the caller does not get
// carries nothing):
//
//   - ForkGuide: the guide of the URL's app (src/guides), where it comes
//     from, and the one proposed without the URL's setting (for the form);
//   - ForkGuideRegion: the region the guide proposes for the image;
//   - ForkShielded: the URL's name is served through Cloudflare's proxy (a
//     request through it carries at most 100 MB);
//   - ForkOwnCAHosts: the URL's names (its host, its names for use at home)
//     whose certificate is signed by Cosmos' own authority, so that a client
//     must be given that authority's certificate;
//   - administrators only: the container's Docker networks (another
//     container reaches the app there).
//
// Nothing here sends a request anywhere or reads an app's credentials.

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/guides"
	"github.com/azukaar/cosmos-server/src/utils"
)

// ForkGuideFields are the guide's additions to a route of GET /api/dashboard.
type ForkGuideFields struct {
	ForkGuide         string   `json:"ForkGuide,omitempty"`
	ForkGuideSource   string   `json:"ForkGuideSource,omitempty"`
	ForkGuideProposed string   `json:"ForkGuideProposed,omitempty"`
	ForkGuideRegion   string   `json:"ForkGuideRegion,omitempty"`
	ForkShielded      bool     `json:"ForkShielded,omitempty"`
	ForkOwnCAHosts    []string `json:"ForkOwnCAHosts,omitempty"`
	ForkNetworks      []string `json:"ForkNetworks,omitempty"`
}

// ForkHostOwnCA reports whether the certificate the server presents for a
// name is signed by Cosmos' own authority. Set by the main package (it holds
// the certificates); nil in tests that do not set it.
var ForkHostOwnCA func(host string) bool

// guideFields is the hook of forkDashboardRoutes.
func guideFields(route utils.ProxyRouteConfig, image string, c *types.Container, isAdmin bool, homeHosts []string) ForkGuideFields {
	fields := ForkGuideFields{}
	setting := ""
	if route.ForkInterface != nil {
		setting = route.ForkInterface.Guide
	}
	proposed, _ := guides.For("", image)
	fields.ForkGuideProposed = proposed
	fields.ForkGuide, fields.ForkGuideSource = guides.For(setting, image)
	if fields.ForkGuide != "" {
		fields.ForkGuideRegion = guides.Region(fields.ForkGuide, image)
	}
	host := ""
	if route.UseHost && route.Host != "" {
		host = route.Host
		fields.ForkShielded = utils.IsShieldedHost(host)
	}
	if ForkHostOwnCA != nil {
		names := append([]string{}, homeHosts...)
		if host != "" && !fields.ForkShielded { // a shielded name shows Cloudflare's certificate to its visitors
			names = append([]string{host}, names...)
		}
		for _, name := range names {
			if len(fields.ForkOwnCAHosts) < 5 && ForkHostOwnCA(hostName(name)) {
				fields.ForkOwnCAHosts = append(fields.ForkOwnCAHosts, name)
			}
		}
	}
	if isAdmin && c != nil && c.NetworkSettings != nil {
		for name := range c.NetworkSettings.Networks {
			if networkName.MatchString(name) && len(fields.ForkNetworks) < 16 {
				fields.ForkNetworks = append(fields.ForkNetworks, name)
			}
		}
		sort.Strings(fields.ForkNetworks)
	}
	return fields
}

// a name as Docker allows it, and nothing that would need quoting
var networkName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// hostName is a route's host without its port.
func hostName(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i > 0 {
			return host[1:i]
		}
		return host
	}
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[:i], ":") {
		return host[:i]
	}
	return host
}

// ---------------------------------------------------------------------------
// an image pinned by digest is listed by Docker as its ID

var imageID = regexp.MustCompile(`^(sha256:)?[0-9a-f]{12,64}$`)

// forkImageNames lists the names an image ID is known by (its tags, else its
// digests). A variable for the tests.
var forkImageNames = func(id string) []string {
	if docker.DockerClient == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inspected, _, err := docker.DockerClient.ImageInspectWithRaw(ctx, id)
	if err != nil {
		return nil
	}
	return append(append([]string{}, inspected.RepoTags...), inspected.RepoDigests...)
}

var imageNames = struct {
	sync.Mutex
	byID map[string]string
}{byID: map[string]string{}}

// resolvedImage is the image a container runs, as a name: Docker's list
// gives "b81d16a90ee2" for a container created from "repo@sha256:…"; that
// ID is looked up once (the answer is kept, at most 256 of them).
func resolvedImage(c *types.Container) string {
	if c == nil {
		return ""
	}
	image := c.Image
	if !imageID.MatchString(image) {
		return image
	}
	id := c.ImageID
	if id == "" {
		id = image
	}
	imageNames.Lock()
	name, ok := imageNames.byID[id]
	imageNames.Unlock()
	if ok {
		return name
	}
	name = image
	for _, candidate := range forkImageNames(id) {
		if candidate != "" && !strings.HasPrefix(candidate, "<none>") {
			name = candidate
			break
		}
	}
	imageNames.Lock()
	if len(imageNames.byID) >= 256 {
		imageNames.byID = map[string]string{}
	}
	imageNames.byID[id] = name
	imageNames.Unlock()
	return name
}

// forkGuideSettingProblem says what is wrong with a URL's guide setting.
func forkGuideSettingProblem(route utils.ProxyRouteConfig) string {
	if route.ForkInterface == nil || guides.ValidSetting(route.ForkInterface.Guide) {
		return ""
	}
	return "The guide of the address must be one of: none, " + strings.Join(guides.Kinds(), ", ")
}
