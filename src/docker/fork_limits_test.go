package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	conttype "github.com/docker/docker/api/types/container"
)

// limitsAfter applies a container update with this body to a container that
// has a memory limit of 512 MiB, a swap limit of 1 GiB and two CPUs.
func limitsAfter(t *testing.T, body string) conttype.Resources {
	t.Helper()
	req, err := http.NewRequest("POST", "/cosmos/api/servapps/app/update", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	sent := readSentLimits(req)

	var form ContainerForm
	if err := json.NewDecoder(req.Body).Decode(&form); err != nil {
		t.Fatalf("the body must still be readable after looking for the limits: %v", err)
	}

	resources := conttype.Resources{Memory: 512 << 20, MemorySwap: 1 << 30, NanoCPUs: 2e9}
	if err := applyResourceLimits(&resources, form, sent); err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestUpdateWithoutLimitsKeepsThem(t *testing.T) {
	// what the Volumes, Network, URL and terminal forms send: their own fields only
	for _, body := range []string{
		`{"interactive": 2}`,
		`{"labels": {"cosmos-auto-update": "true"}}`,
		`{"portBindings": {"80/tcp": [{"HostPort": "8080"}]}}`,
		`{"memLimit": null, "cpus": null}`,
	} {
		got := limitsAfter(t, body)
		if got.Memory != 512<<20 || got.MemorySwap != 1<<30 || got.NanoCPUs != 2e9 {
			t.Errorf("%s removed or changed the limits: %+v", body, got)
		}
	}
}

func TestUpdateSetsAndRemovesLimitsItCarries(t *testing.T) {
	got := limitsAfter(t, `{"memLimit": "256m", "cpus": 0.5}`)
	if got.Memory != 256<<20 || got.NanoCPUs != 5e8 {
		t.Errorf("the limits sent must be set, got %+v", got)
	}

	// the Setup form sends an empty limit and 0 when the fields are cleared
	got = limitsAfter(t, `{"memLimit": "", "cpus": 0}`)
	if got.Memory != 0 || got.MemorySwap != 0 || got.NanoCPUs != 0 {
		t.Errorf("limits sent empty must be removed, got %+v", got)
	}

	got = limitsAfter(t, `{"memLimit": "1g"}`)
	if got.Memory != 1<<30 || got.NanoCPUs != 2e9 {
		t.Errorf("only the limit sent may change, got %+v", got)
	}

	got = limitsAfter(t, `{"cpus": 1}`)
	if got.Memory != 512<<20 || got.MemorySwap != 1<<30 || got.NanoCPUs != 1e9 {
		t.Errorf("only the limit sent may change, got %+v", got)
	}
}

func TestInvalidMemoryLimitIsRefused(t *testing.T) {
	resources := conttype.Resources{Memory: 512 << 20}
	err := applyResourceLimits(&resources, ContainerForm{MemLimit: "a lot"}, sentLimits{memory: true})
	if err == nil {
		t.Fatal("a memory limit that is not a size must be refused")
	}
	if resources.Memory != 512<<20 {
		t.Error("a refused limit must not change the container")
	}
}

func TestReadSentLimitsLeavesTheBody(t *testing.T) {
	for _, body := range []string{`{"memLimit": "1g"}`, `not json`, ``} {
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		readSentLimits(req)
		rest, err := io.ReadAll(req.Body)
		if err != nil || string(rest) != body {
			t.Errorf("the body %q must be left for the decoding that follows, got %q (%v)", body, rest, err)
		}
	}

	if sent := readSentLimits(nil); sent.memory || sent.cpus {
		t.Error("no request carries no limits")
	}
}
