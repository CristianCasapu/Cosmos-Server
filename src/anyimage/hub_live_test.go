package anyimage

// The real Docker Hub, once, opt-in (the query "postgres" is all that is sent;
// the describe counts as one pull against the anonymous limit):
//
//   ANYIMAGE_LIVE_HUB=1 go test ./src/anyimage/ -run HubLive -v

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHubLive(t *testing.T) {
	if os.Getenv("ANYIMAGE_LIVE_HUB") != "1" {
		t.Skip("set ANYIMAGE_LIVE_HUB=1 to ask the real Docker Hub")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results, err := search(ctx, "postgres")
	if err != nil || len(results) < 5 || results[0].Name != "postgres" || !results[0].Official || results[0].Recipe != "postgres" {
		t.Fatalf("%v %+v", err, results)
	}
	for i := 2; i < len(results); i++ {
		if !results[i].Official && results[i-1].Pulls < results[i].Pulls && !results[i-1].Official {
			t.Errorf("not by downloads: %s before %s", results[i-1].Name, results[i].Name)
		}
	}
	serverPlatform = func() Platform { return Platform{OS: "linux", Arch: "amd64"} }
	desc := Describe(ctx, "postgres", "user:live")
	t.Logf("postgres: tag %s (%s), %d tags, platforms %v, volumes %v, size %d", desc.Tag, desc.Tags.Why, len(desc.Tags.List), desc.Platforms, desc.Declared.Volumes, desc.Size)
	if desc.Problem != "" || desc.Tags.Why != "pinMajor" || desc.Declared == nil || len(desc.Platforms) < 3 {
		t.Fatalf("%+v", desc)
	}
	if !strings.HasPrefix(desc.Declared.Volumes[0], "/var/lib/postgresql") {
		t.Fatal(desc.Declared.Volumes)
	}
	desc = Describe(ctx, "ghcr.io/home-assistant/home-assistant:stable", "user:live")
	t.Logf("ghcr: %q market %q platforms %v", desc.Problem, desc.Market, desc.Platforms)
	if desc.Problem != "" || desc.Market != "Home Assistant" || desc.Declared == nil {
		t.Fatalf("GHCR anonymous token: %+v", desc.Problem)
	}
}
