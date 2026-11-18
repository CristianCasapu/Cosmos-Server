package docker

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

func containerWith(hc container.HostConfig, labels map[string]string) types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{Name: "/app", HostConfig: &hc},
		Config:            &container.Config{Labels: labels},
	}
}

// An edit that adds a device, a system folder or the host's network needs a tick;
// what the container had is not asked again (review F3).
func TestAnEditNeedsATickForNewSensitiveAccess(t *testing.T) {
	before := containerWith(container.HostConfig{
		Resources: container.Resources{Devices: []container.DeviceMapping{{PathOnHost: "/dev/dri", PathInContainer: "/dev/dri"}}},
	}, nil)
	after := containerWith(container.HostConfig{
		Resources:   container.Resources{Devices: []container.DeviceMapping{{PathOnHost: "/dev/dri", PathInContainer: "/dev/dri"}, {PathOnHost: "/dev/sda", PathInContainer: "/dev/xvda"}}},
		Mounts:      []mount.Mount{{Type: mount.TypeBind, Source: "/", Target: "/host"}, {Type: mount.TypeBind, Source: "/srv/app", Target: "/data"}},
		Binds:       []string{"/var/run/docker.sock:/var/run/docker.sock"},
		NetworkMode: "host",
	}, nil)
	missing := unconfirmedEdit("app", before, after, url.Values{}, nil)
	keys := []string{}
	for _, m := range missing {
		keys = append(keys, m.Key())
	}
	want := "app|bind|/ app|socket|/var/run/docker.sock app|network-host|host app|device|/dev/sda"
	if strings.Join(keys, " ") != want {
		t.Fatalf("%q", keys)
	}
	if left := unconfirmedEdit("app", before, after, url.Values{"confirm": keys}, nil); len(left) != 0 {
		t.Fatalf("all confirmed: %+v", left)
	}
	if left := unconfirmedEdit("app", after, after, url.Values{}, nil); len(left) != 0 {
		t.Fatalf("an edit is not asked again for what the container had: %+v", left)
	}

	recorder := httptest.NewRecorder()
	if !forkRefuseUnconfirmedEdit(recorder, httptest.NewRequest("POST", "/cosmos/api/servapps/app/update", nil), before, after) {
		t.Fatal("not refused")
	}
	var body struct {
		Code string `json:"code"`
		Data struct {
			Sensitive []SensitiveItem `json:"sensitive"`
		} `json:"data"`
	}
	json.Unmarshal(recorder.Body.Bytes(), &body)
	if recorder.Code != 409 || body.Code != "DS010" || len(body.Data.Sensitive) != 4 {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	confirmed := httptest.NewRequest("POST", "/cosmos/api/servapps/app/update?"+url.Values{"confirm": keys}.Encode(), nil)
	if forkRefuseUnconfirmedEdit(httptest.NewRecorder(), confirmed, before, after) {
		t.Fatal("refused although confirmed")
	}
}

// The update route calls the check before it changes the container.
func TestTheUpdateRouteChecksSensitiveAccess(t *testing.T) {
	source, err := os.ReadFile("api_updateContainer.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	check := strings.Index(text, "forkRefuseUnconfirmedEdit(w, req, before, container, where...)")
	edit := strings.Index(text, "_, err = EditContainer(container.ID, container, false)")
	if check < 0 || edit < 0 || check > edit {
		t.Fatal("the update route must refuse unconfirmed sensitive access before EditContainer")
	}
}
