package configapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"

	"github.com/azukaar/cosmos-server/src/utils"
)

// withContainerImages answers the lookup of a container's image by its ID;
// an ID that is not in the map fails (Docker busy).
func withContainerImages(t *testing.T, names map[string]string) *int {
	t.Helper()
	calls := 0
	previous := forkContainerImage
	forkContainerImage = func(id string) string { calls++; return names[id] }
	imageNames.Lock()
	imageNames.byKey = map[string]imageName{}
	imageNames.Unlock()
	t.Cleanup(func() { forkContainerImage = previous })
	return &calls
}

func TestResolvedImageOfADigestPinnedContainer(t *testing.T) {
	digest := "rustfs/rustfs@sha256:" + strings.Repeat("b8", 32)
	names := map[string]string{"c1": digest}
	calls := withContainerImages(t, names)
	c := &types.Container{ID: "c1", Image: "b81d16a90ee2", ImageID: "sha256:" + strings.Repeat("b8", 32)}
	if got := resolvedImage(c); got != digest {
		t.Errorf("resolved: %q", got)
	}
	resolvedImage(c)
	if *calls != 1 {
		t.Errorf("the image was looked up %d times, once is enough", *calls)
	}
	if got := resolvedImage(&types.Container{ID: "c9", Image: "minio/minio:latest"}); got != "minio/minio:latest" || *calls != 1 {
		t.Errorf("a named image: %q (%d lookups)", got, *calls)
	}
}

func TestResolvedImageRetriesAFailedLookup(t *testing.T) {
	names := map[string]string{}
	calls := withContainerImages(t, names)
	c := &types.Container{ID: "c2", Image: "0123456789ab", ImageID: "sha256:0123"}
	if got := resolvedImage(c); got != "0123456789ab" {
		t.Errorf("a failed lookup keeps the ID: %q", got)
	}
	resolvedImage(c)
	if *calls != 1 {
		t.Errorf("a failure is remembered for a while (%d lookups)", *calls)
	}
	// Docker answers again: once the failure is older than the retry delay, the name is found
	names["c2"] = "rustfs/rustfs:1.0.5"
	imageNames.Lock()
	entry := imageNames.byKey["c2/sha256:0123"]
	entry.failed = time.Now().Add(-2 * imageLookupRetry)
	imageNames.byKey["c2/sha256:0123"] = entry
	imageNames.Unlock()
	if got := resolvedImage(c); got != "rustfs/rustfs:1.0.5" {
		t.Errorf("after the retry delay: %q", got)
	}
	// an answer that is itself an ID is not a name
	names["c3"] = "sha256:" + strings.Repeat("ab", 32)
	if got := resolvedImage(&types.Container{ID: "c3", Image: "abababababab"}); got != "abababababab" {
		t.Errorf("an ID for a name: %q", got)
	}
}

func TestUnknownGuideKindKeptWhenStored(t *testing.T) {
	stored := utils.Config{}
	route := servapp("x", "http://x:80", "x.local")
	route.ForkInterface = &utils.ForkRouteInterface{Guide: "ftp"}
	stored.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{route}
	withConfig(t, stored)
	if msg := validateRoute(route); msg != "" {
		t.Errorf("a stored kind of a newer build must not block a whole save: %s", msg)
	}
	other := servapp("y", "http://y:80", "y.local")
	other.ForkInterface = &utils.ForkRouteInterface{Guide: "ftp"}
	if validateRoute(other) == "" {
		t.Error("an unknown kind newly set was accepted")
	}
}

func TestDashboardCarriesTheGuide(t *testing.T) {
	withContainerImages(t, map[string]string{"rustfs1": "rustfs/rustfs@sha256:" + strings.Repeat("c1", 32)})
	previous := ForkHostOwnCA
	ForkHostOwnCA = func(host string) bool { return strings.HasSuffix(host, ".local") }
	ownCACache.Lock()
	ownCACache.at, ownCACache.value = map[string]time.Time{}, map[string]bool{}
	ownCACache.Unlock()
	t.Cleanup(func() { ForkHostOwnCA = previous })
	config := utils.Config{}
	config.HTTPConfig.Cloudflare.Shielded = []string{"s3.example.org"}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		servapp("s3", "http://rustfs:9000", "s3.local"),
		servapp("s3-internet", "http://rustfs:9000", "s3.example.org:2053"),
		servapp("rustfs-console", "http://rustfs:9001", "rustfs.local"),
		servapp("db", "http://postgres:5432", "db.local"),
		{Name: "store", Mode: "PROXY", Target: "http://192.168.1.20:9000", UseHost: true, Host: "store.local", ForkInterface: &utils.ForkRouteInterface{Guide: "s3"}},
		{Name: "hidden", Mode: "SERVAPP", Target: "http://rustfs:9000", UseHost: true, Host: "s3b.local", ForkInterface: &utils.ForkRouteInterface{Guide: "none"}},
	}
	withConfig(t, config)
	containers := []types.Container{
		{ID: "rustfs1", Names: []string{"/rustfs"}, Image: "c1c1c1c1c1c1", ImageID: "sha256:" + strings.Repeat("c1", 32),
			NetworkSettings: &types.SummaryNetworkSettings{Networks: map[string]*network.EndpointSettings{"media": {}, "bad name;x": {}}}},
		{Names: []string{"/postgres"}, Image: "postgres:18"},
	}
	for _, admin := range []bool{true, false} {
		caller := plainCaller
		if admin {
			caller = adminCaller
		}
		got := forkDashboardRoutes(dashboardOf(config, admin), containers, caller, nil)
		by := map[string]DashboardRoute{}
		for _, r := range got {
			by[r.Name] = r
		}
		s3 := by["s3"]
		wantSource := ""
		if admin {
			wantSource = "recipe"
		}
		if s3.ForkGuide != "s3" || s3.ForkGuideSource != wantSource || s3.ForkGuideRegion != "us-east-1" || s3.ForkRole != "api" {
			t.Errorf("admin %v: the S3 URL of a digest-pinned RustFS: %+v", admin, s3.ForkGuideFields)
		}
		if len(s3.ForkOwnCAHosts) != 1 || s3.ForkOwnCAHosts[0] != "s3.local" || s3.ForkShielded {
			t.Errorf("admin %v: own CA of s3.local: %+v", admin, s3.ForkGuideFields)
		}
		internet := by["s3-internet"]
		if !internet.ForkShielded || len(internet.ForkOwnCAHosts) != 0 {
			t.Errorf("admin %v: the shielded name: %+v", admin, internet.ForkGuideFields)
		}
		if by["db"].ForkGuide != "" || by["store"].ForkGuide != "s3" || (admin && by["store"].ForkGuideSource != "set") {
			t.Errorf("admin %v: db %+v store %+v", admin, by["db"].ForkGuideFields, by["store"].ForkGuideFields)
		}
		if by["hidden"].ForkGuide != "" || (admin && by["hidden"].ForkGuideProposed != "s3") || (!admin && by["hidden"].ForkGuideProposed != "") {
			t.Errorf("admin %v: a guide switched off: %+v", admin, by["hidden"].ForkGuideFields)
		}
		if admin && !strings.HasPrefix(s3.ForkImage, "rustfs/rustfs@") {
			t.Errorf("an administrator gets the image's name, not its ID: %q", s3.ForkImage)
		}
		networks := strings.Join(s3.ForkNetworks, ",")
		if admin && networks != "media" {
			t.Errorf("an admin gets the plain network names only: %q", networks)
		}
		if !admin {
			body, _ := json.Marshal(got)
			if strings.Contains(string(body), "ForkGuideSource") || strings.Contains(string(body), "ForkGuideProposed") {
				t.Errorf("a plain user got where the guide comes from: %s", body)
			}
			if strings.Contains(string(body), "ForkNetworks") || strings.Contains(string(body), "media") {
				t.Errorf("a plain user got the networks: %s", body)
			}
		}
	}
}

func TestGuideSettingIsChecked(t *testing.T) {
	for setting, ok := range map[string]bool{"": true, "none": true, "s3": true, "ftp": false, "S3": false, "<x>": false} {
		route := servapp("x", "http://x:80", "x.local")
		route.ForkInterface = &utils.ForkRouteInterface{Guide: setting}
		if got := validateRoute(route) == ""; got != ok {
			t.Errorf("guide %q: accepted %v, want %v", setting, got, ok)
		}
	}
}

func TestHostName(t *testing.T) {
	for in, want := range map[string]string{"s3.local:2053": "s3.local", "S3.Example.org": "s3.example.org", "[2001:db8::1]:443": "2001:db8::1", "192.168.1.5": "192.168.1.5"} {
		if got := hostName(in); got != want {
			t.Errorf("hostName(%q) = %q", in, got)
		}
	}
}

func withConfig(t *testing.T, config utils.Config) {
	t.Helper()
	previous := utils.GetBaseMainConfig()
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
}

func TestNoGuideForACallerWithoutTheTileWords(t *testing.T) {
	config := utils.Config{}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{servapp("s3", "http://rustfs:9000", "s3.local")}
	withConfig(t, config)
	got := forkDashboardRoutes(dashboardOf(config, false), []types.Container{{Names: []string{"/rustfs"}, Image: "rustfs/rustfs:1.0.5"}}, dashboardCaller{}, nil)
	if len(got) != 1 || got[0].ForkGuide != "" || got[0].ForkGuideRegion != "" {
		t.Errorf("a token without configuration-read got the guide: %+v", got)
	}
}

func TestLimitsOfAGuidedURL(t *testing.T) {
	config := utils.Config{}
	open := servapp("s3", "http://rustfs:9000", "s3.local")
	open.Timeout = 0
	shielded := servapp("s3-internet", "http://rustfs:9000", "s3.example.org")
	shielded.SmartShield = utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 100, PolicyStrictness: utils.STRICT}
	shielded.ThrottlePerMinute, shielded.MaxBandwith, shielded.Timeout, shielded.BlockCommonBots, shielded.BlockAPIAbuse = 600, 1000000, 14400000, true, true
	defaults := servapp("s3-defaults", "http://rustfs:9000", "s3d.local")
	defaults.SmartShield = utils.SmartShieldPolicy{Enabled: true}
	other := servapp("web", "http://web:80", "web.local")
	other.SmartShield = utils.SmartShieldPolicy{Enabled: true}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{open, shielded, defaults, other}
	withConfig(t, config)
	containers := []types.Container{{ID: "r", Names: []string{"/rustfs"}, Image: "rustfs/rustfs:1.0.5"}, {ID: "w", Names: []string{"/web"}, Image: "nginx:1"}}
	by := map[string]DashboardRoute{}
	for _, r := range forkDashboardRoutes(dashboardOf(config, true), containers, adminCaller, nil) {
		by[r.Name] = r
	}
	if l := by["s3"].ForkLimits; l == nil || l.Shield || l.ThrottlePerMinute != 0 || l.TimeoutMs != 0 || l.BlockAPIAbuse {
		t.Errorf("an open URL: %+v", l)
	}
	if l := by["s3-internet"].ForkLimits; l == nil || !l.Shield || l.ShieldRequestsPerHour != 100 || l.ShieldSimultaneous != utils.GetSmartShieldProfile().Defaults.PerUserSimultaneous*15 ||
		l.ThrottlePerMinute != 600 || l.MaxBandwidth != 1000000 || l.TimeoutMs != 14400000 || !l.BlockBots || !l.BlockAPIAbuse {
		t.Errorf("a limited URL: %+v", l)
	}
	d := utils.GetSmartShieldProfile().Defaults
	if l := by["s3-defaults"].ForkLimits; l == nil || l.ShieldRequestsPerHour != d.PerUserRequestLimit*d.PolicyStrictness || l.ShieldBytesPerHour != d.PerUserByteLimit*int64(d.PolicyStrictness) {
		t.Errorf("the shield's defaults are applied: %+v", l)
	}
	if by["web"].ForkLimits != nil {
		t.Error("a URL without a guide carries no limits")
	}
	if by["s3"].ForkServicePort != 9000 {
		t.Errorf("the recipe's service port: %d", by["s3"].ForkServicePort)
	}
	for _, r := range forkDashboardRoutes(dashboardOf(config, false), containers, plainCaller, nil) {
		if r.ForkLimits != nil || r.ForkServicePort != 0 {
			t.Errorf("a plain user got the configuration's limits: %s", r.Name)
		}
	}
}
