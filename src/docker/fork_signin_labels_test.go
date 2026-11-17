package docker

// Plan 45, second review item 4: the fork's sign-in labels are never taken
// from an image, through the real rule (only the image read is stubbed).

import (
	"testing"

	doctype "github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

func TestTheSignInLabelsOfAnImageAreNotTaken(t *testing.T) {
	old := imageLabelsOf
	t.Cleanup(func() {
		imageLabelsOf = old
		imageLabelCacheMu.Lock()
		imageLabelCache = map[string]map[string]string{}
		imageLabelCacheMu.Unlock()
	})
	imageLabelsOf = func(string) (map[string]string, error) {
		return map[string]string{"cosmos-fork-signin": "where=web;user=API_KEY", "cosmos-fork-manager-of": "db"}, nil
	}
	container := doctype.ContainerJSON{
		ContainerJSONBase: &doctype.ContainerJSONBase{Image: "sha256:" + "a1b2"},
		Config: &conttype.Config{Labels: map[string]string{"cosmos-fork-signin": "where=web;user=API_KEY", "cosmos-fork-manager-of": "db"}},
	}
	for _, key := range []string{"cosmos-fork-signin", "cosmos-fork-manager-of"} {
		if got := ForkAdminLabel(container, key); got != "" {
			t.Errorf("%s from the image was taken: %q", key, got)
		}
	}
	// the administrator's own (a value the image does not carry) is taken
	container.Config.Labels["cosmos-fork-manager-of"] = "otherdb"
	if got := ForkAdminLabel(container, "cosmos-fork-manager-of"); got != "otherdb" {
		t.Errorf("the administrator's label: %q", got)
	}
	// and they are dropped before a recreate, like the other labels of the list
	dropImageLabels(&container)
	if _, still := container.Config.Labels["cosmos-fork-signin"]; still {
		t.Error("the image's sign-in label was handed back on a recreate")
	}
}
