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
//   - those who get the container's details (administrators with resources-read): its Docker networks (another
//     container reaches the app there); to those who may edit URLs only:
//     where the guide comes from and what it would be without the setting.
//
// Callers who do not get the tile's words (an API token without
// configuration-read) get none of this.
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

	"github.com/azukaar/cosmos-server/src/anyimage"
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
	// administrators only (it is configuration): what Cosmos itself limits on this URL
	ForkLimits *ForkRouteLimits `json:"ForkLimits,omitempty"`
	// administrators only: the port of the recipe's service (an API), for the URL form's proposal
	ForkServicePort int `json:"ForkServicePort,omitempty"`
}

// ForkRouteLimits is what Cosmos' proxy limits on a URL, as it applies it:
// the shield's per-visitor limits with the defaults and the strictness
// factor already applied (src/proxy/shield.go budgetViolation), per rolling
// hour; zero means no limit.
type ForkRouteLimits struct {
	Shield                   bool    `json:"Shield"`
	ShieldRequestsPerHour    int     `json:"ShieldRequestsPerHour,omitempty"`
	ShieldBytesPerHour       int64   `json:"ShieldBytesPerHour,omitempty"`
	ShieldTimePerHourSeconds float64 `json:"ShieldTimePerHourSeconds,omitempty"`
	ShieldSimultaneous       int     `json:"ShieldSimultaneous,omitempty"`
	ShieldGlobalSimultaneous int     `json:"ShieldGlobalSimultaneous,omitempty"`
	ThrottlePerMinute        int     `json:"ThrottlePerMinute,omitempty"`
	MaxBandwidth             int64   `json:"MaxBandwidth,omitempty"` // bytes per second sent back to a visitor
	TimeoutMs                int64   `json:"TimeoutMs,omitempty"`
	BlockBots                bool    `json:"BlockBots,omitempty"`
	BlockAPIAbuse            bool    `json:"BlockAPIAbuse,omitempty"`
}

// routeLimits reads a URL's limits as the proxy applies them.
func routeLimits(route utils.ProxyRouteConfig) *ForkRouteLimits {
	limits := &ForkRouteLimits{
		ThrottlePerMinute: route.ThrottlePerMinute,
		MaxBandwidth:      route.MaxBandwith,
		TimeoutMs:         int64(route.Timeout),
		BlockBots:         route.BlockCommonBots,
		BlockAPIAbuse:     route.BlockAPIAbuse,
	}
	if route.SmartShield.Enabled {
		// the proxy's own arithmetic (src/proxy/shield.go judges requests with the same function)
		policy := utils.ApplySmartShieldDefaults(route.SmartShield)
		effective := utils.ShieldLimits(policy)
		limits.Shield = true
		limits.ShieldRequestsPerHour = effective.Requests
		limits.ShieldBytesPerHour = effective.Bytes
		limits.ShieldTimePerHourSeconds = effective.TimeMs / 1000
		limits.ShieldSimultaneous = effective.SimultaneousHTTP
		limits.ShieldGlobalSimultaneous = policy.MaxGlobalSimultaneous
	}
	return limits
}

// ForkHostOwnCA reports whether the certificate the server presents for a
// name is signed by Cosmos' own authority. Set by the main package (it holds
// the certificates); nil in tests that do not set it.
var ForkHostOwnCA func(host string) bool

// guideFields is the hook of forkDashboardRoutes.
func guideFields(route utils.ProxyRouteConfig, image string, c *types.Container, containerDetails bool, editsURLs bool, homeHosts []string) ForkGuideFields {
	fields := ForkGuideFields{}
	setting := ""
	if route.ForkInterface != nil {
		setting = route.ForkInterface.Guide
	}
	if editsURLs { // like the role's proposal: for those who may edit URLs (the form)
		fields.ForkGuideProposed, _ = guides.For("", image)
	}
	fields.ForkGuide, fields.ForkGuideSource = guides.For(setting, image)
	if !editsURLs {
		fields.ForkGuideSource = ""
	}
	if fields.ForkGuide != "" {
		fields.ForkGuideRegion = guides.Region(fields.ForkGuide, image)
	}
	host := ""
	if route.UseHost && route.Host != "" {
		host = route.Host
		fields.ForkShielded = utils.IsShieldedHost(host)
	}
	if ForkHostOwnCA != nil && fields.ForkGuide != "" { // only for a guide, and remembered for a minute
		names := append([]string{}, homeHosts...)
		if host != "" && !fields.ForkShielded { // a shielded name shows Cloudflare's certificate to its visitors
			names = append([]string{host}, names...)
		}
		for _, name := range names {
			if len(fields.ForkOwnCAHosts) < 5 && hostOwnCA(hostName(name)) {
				fields.ForkOwnCAHosts = append(fields.ForkOwnCAHosts, name)
			}
		}
	}
	if containerDetails && fields.ForkGuide != "" {
		if recipe := anyimage.RecipeForImage(image); recipe != nil && recipe.Service != nil {
			fields.ForkServicePort = recipe.Service.Port
		}
	}
	if containerDetails && c != nil && c.NetworkSettings != nil {
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

// forkContainerImage is the image a container was created from, as its
// configuration names it (Config.Image: "rustfs/rustfs@sha256:…" or the tag
// that was used), "" when Docker does not answer. A variable for the tests.
var forkContainerImage = func(containerID string) string {
	if docker.DockerClient == nil || containerID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inspected, err := docker.DockerClient.ContainerInspect(ctx, containerID)
	if err != nil || inspected.Config == nil {
		return ""
	}
	return inspected.Config.Image
}

// how long a failed lookup is remembered before it is tried again
const imageLookupRetry = time.Minute

type imageName struct {
	name   string
	failed time.Time // zero: a name that was found
}

var imageNames = struct {
	sync.Mutex
	byKey map[string]imageName
}{byKey: map[string]imageName{}}

// resolvedImage is the image a container runs, as a name. Docker's list
// gives "b81d16a90ee2" for a container created from "repo@sha256:…"; the
// container's own configuration says what it was created from. A name found
// is kept (at most 256); a failed lookup (Docker busy) is tried again after
// a minute, and meanwhile the ID is used.
func resolvedImage(c *types.Container) string {
	if c == nil {
		return ""
	}
	image := c.Image
	if !imageID.MatchString(image) {
		return image
	}
	key := c.ID + "/" + c.ImageID
	imageNames.Lock()
	known, ok := imageNames.byKey[key]
	imageNames.Unlock()
	if ok && (known.failed.IsZero() || time.Since(known.failed) < imageLookupRetry) {
		if known.failed.IsZero() {
			return known.name
		}
		return image
	}
	entry := imageName{failed: time.Now()}
	if name := strings.TrimSpace(forkContainerImage(c.ID)); name != "" && !imageID.MatchString(name) {
		entry = imageName{name: name}
	}
	imageNames.Lock()
	if len(imageNames.byKey) >= 256 {
		imageNames.byKey = map[string]imageName{}
	}
	imageNames.byKey[key] = entry
	imageNames.Unlock()
	if entry.failed.IsZero() {
		return entry.name
	}
	return image
}

// ---------------------------------------------------------------------------
// whether a name's certificate is Cosmos' own, remembered for a minute

var ownCACache = struct {
	sync.Mutex
	at    map[string]time.Time
	value map[string]bool
}{at: map[string]time.Time{}, value: map[string]bool{}}

func hostOwnCA(host string) bool {
	ownCACache.Lock()
	at, ok := ownCACache.at[host]
	value := ownCACache.value[host]
	ownCACache.Unlock()
	if ok && time.Since(at) < time.Minute {
		return value
	}
	value = ForkHostOwnCA(host)
	ownCACache.Lock()
	if len(ownCACache.at) >= 256 {
		ownCACache.at, ownCACache.value = map[string]time.Time{}, map[string]bool{}
	}
	ownCACache.at[host], ownCACache.value[host] = time.Now(), value
	ownCACache.Unlock()
	return value
}

// forkGuideSettingProblem says what is wrong with a URL's guide setting. A
// kind this build does not know is refused when it is set, and kept (shown as
// no guide) when the stored configuration already holds it for that URL, so
// that a configuration from a newer build still saves as a whole.
func forkGuideSettingProblem(route utils.ProxyRouteConfig) string {
	if route.ForkInterface == nil || guides.ValidSetting(route.ForkInterface.Guide) {
		return ""
	}
	for _, stored := range utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes {
		if stored.Name == route.Name && stored.ForkInterface != nil && stored.ForkInterface.Guide == route.ForkInterface.Guide {
			return ""
		}
	}
	return "The guide of the address must be one of: none, " + strings.Join(guides.Kinds(), ", ")
}
