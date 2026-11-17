package configapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"

	"github.com/azukaar/cosmos-server/src/utils"
)

func withImageNames(t *testing.T, names map[string][]string) *int {
	t.Helper()
	calls := 0
	previous := forkImageNames
	forkImageNames = func(id string) []string { calls++; return names[id] }
	imageNames.Lock()
	imageNames.byID = map[string]string{}
	imageNames.Unlock()
	t.Cleanup(func() { forkImageNames = previous })
	return &calls
}

func TestResolvedImageOfADigestPinnedContainer(t *testing.T) {
	digest := "rustfs/rustfs@sha256:" + strings.Repeat("b8", 32)
	calls := withImageNames(t, map[string][]string{"sha256:" + strings.Repeat("b8", 32): {digest}})
	c := &types.Container{Image: "b81d16a90ee2", ImageID: "sha256:" + strings.Repeat("b8", 32)}
	if got := resolvedImage(c); got != digest {
		t.Errorf("resolved: %q", got)
	}
	resolvedImage(c)
	if *calls != 1 {
		t.Errorf("the image was looked up %d times, once is enough", *calls)
	}
	if got := resolvedImage(&types.Container{Image: "minio/minio:latest"}); got != "minio/minio:latest" {
		t.Errorf("a named image: %q", got)
	}
	if got := resolvedImage(&types.Container{Image: "0123456789ab", ImageID: "sha256:00"}); got != "0123456789ab" {
		t.Errorf("an ID nobody knows stays as it is: %q", got)
	}
}

func TestDashboardCarriesTheGuide(t *testing.T) {
	withImageNames(t, map[string][]string{"sha256:" + strings.Repeat("c1", 32): {"rustfs/rustfs@sha256:" + strings.Repeat("c1", 32)}})
	previous := ForkHostOwnCA
	ForkHostOwnCA = func(host string) bool { return strings.HasSuffix(host, ".local") }
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
		{Names: []string{"/rustfs"}, Image: "c1c1c1c1c1c1", ImageID: "sha256:" + strings.Repeat("c1", 32),
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
