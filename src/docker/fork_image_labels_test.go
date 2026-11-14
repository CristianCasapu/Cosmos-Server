package docker

// Fork addition: tests of fork_image_labels.go.

import (
	"errors"
	"os"
	"strings"
	"testing"

	doctype "github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"

	"github.com/azukaar/cosmos-server/src/utils"
)

// fakeImages stands in for the image store: id -> labels; an id that is not
// there fails to be read.
func fakeImages(t *testing.T, images map[string]map[string]string) {
	t.Helper()
	previous := imageLabelsOf
	imageLabelsOf = func(imageID string) (map[string]string, error) {
		labels, ok := images[imageID]
		if !ok {
			return nil, errors.New("no such image")
		}
		return labels, nil
	}
	imageLabelCacheMu.Lock()
	imageLabelCache = map[string]map[string]string{}
	imageLabelCacheMu.Unlock()
	t.Cleanup(func() {
		imageLabelsOf = previous
		imageLabelCacheMu.Lock()
		imageLabelCache = map[string]map[string]string{}
		imageLabelCacheMu.Unlock()
	})
}

func containerOf(imageID string, labels map[string]string) doctype.ContainerJSON {
	return doctype.ContainerJSON{
		ContainerJSONBase: &doctype.ContainerJSONBase{Name: "/app", Image: imageID, HostConfig: &conttype.HostConfig{NetworkMode: "bridge"}},
		Config:            &conttype.Config{Labels: labels},
	}
}

func TestLabelFromImage(t *testing.T) {
	fakeImages(t, map[string]map[string]string{
		"sha256:evil":  {"cosmos-force-network-mode": "host", "cosmos-auto-update": "true"},
		"sha256:plain": {"org.opencontainers.image.title": "app"},
	})
	cases := []struct {
		image, key, value string
		want              bool
	}{
		{"sha256:evil", "cosmos-force-network-mode", "host", true},
		{"sha256:evil", "cosmos-force-network-mode", "bridge", false}, // the admin changed it
		{"sha256:plain", "cosmos-force-network-mode", "host", false},  // the image has none
		{"sha256:gone", "cosmos-force-network-mode", "host", true},    // cannot be read: not obeyed
		{"", "cosmos-force-network-mode", "host", false},              // no image to compare with
		{"sha256:evil", "cosmos-auto-update", "", false},
	}
	for _, c := range cases {
		if got := labelFromImage(c.image, c.key, c.value); got != c.want {
			t.Errorf("labelFromImage(%s, %s=%s) = %v, want %v", c.image, c.key, c.value, got, c.want)
		}
	}
}

// An image that asks for the host's network is recreated in the network the
// administrator gave it; the administrator's own choices stay.
func TestDropImageLabelsBeforeRecreate(t *testing.T) {
	fakeImages(t, map[string]map[string]string{
		"sha256:evil": {"cosmos-force-network-mode": "host", "cosmos-auto-update": "true", "cosmos-network-name": "other_app", "cosmos-lazy": "true", "traefik.enable": "true"},
	})
	c := containerOf("sha256:evil", map[string]string{
		"cosmos-force-network-mode": "host",
		"cosmos-auto-update":        "true",
		"cosmos-network-name":       "mine",
		"cosmos-lazy":               "true",
		"traefik.enable":            "true",
	})
	dropImageLabels(&c)
	for _, gone := range []string{"cosmos-force-network-mode", "cosmos-auto-update"} {
		if _, ok := c.Config.Labels[gone]; ok {
			t.Errorf("%s from the image was kept", gone)
		}
	}
	for key, want := range map[string]string{"cosmos-network-name": "mine", "cosmos-lazy": "true", "traefik.enable": "true"} {
		if c.Config.Labels[key] != want {
			t.Errorf("%s = %q, want %q (kept)", key, c.Config.Labels[key], want)
		}
	}
}

// EditContainer drops them before it applies the network mode: the hook must
// stay in front of that code.
func TestEditContainerDropsImageLabelsFirst(t *testing.T) {
	source, err := os.ReadFile("docker.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	drop := strings.Index(text, "dropImageLabels(&newConfig)")
	apply := strings.Index(text, `newConfig.HostConfig.NetworkMode = container.NetworkMode(GetLabel(newConfig, "cosmos-force-network-mode"))`)
	if drop < 0 || apply < 0 || drop > apply {
		t.Fatalf("dropImageLabels must run in EditContainer before the network mode label is applied (drop at %d, apply at %d)", drop, apply)
	}
	for file, hook := range map[string]string{
		"network.go":        `adminLabel(containerConfig, "cosmos-network-name")`,
		"remove_service.go": "labelFromImage(c.ImageID, DeploymentLabel, deployment)",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), hook) {
			t.Errorf("%s: the hook %s is missing", file, hook)
		}
	}
}

func TestAutoUpdateFromImageIsNotObeyed(t *testing.T) {
	previous := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Cleanup(func() { utils.CONFIGFOLDER = previous })
	fakeImages(t, map[string]map[string]string{
		"sha256:evil":  {"cosmos-auto-update": "true"},
		"sha256:plain": {},
	})
	if HasAutoUpdateOn(containerOf("sha256:evil", map[string]string{"cosmos-auto-update": "true"})) {
		t.Error("automatic updates switched on by the image's own label")
	}
	if !HasAutoUpdateOn(containerOf("sha256:plain", map[string]string{"cosmos-auto-update": "true"})) {
		t.Error("automatic updates switched on by the administrator are not obeyed")
	}
	if HasAutoUpdateOn(containerOf("sha256:plain", map[string]string{"cosmos-auto-update": "false"})) {
		t.Error("automatic updates on although switched off")
	}
}

func TestAdminLabel(t *testing.T) {
	fakeImages(t, map[string]map[string]string{"sha256:evil": {"cosmos-network-name": "other_app"}})
	if got := adminLabel(containerOf("sha256:evil", map[string]string{"cosmos-network-name": "other_app"}), "cosmos-network-name"); got != "" {
		t.Errorf("network name from the image obeyed: %q", got)
	}
	if got := adminLabel(containerOf("sha256:evil", map[string]string{"cosmos-network-name": "mine"}), "cosmos-network-name"); got != "mine" {
		t.Errorf("the administrator's network name: %q", got)
	}
	if got := adminLabel(doctype.ContainerJSON{}, "cosmos-network-name"); got != "" {
		t.Errorf("an empty container: %q", got)
	}
}
