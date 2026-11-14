package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

func withInstallPlanFakes(t *testing.T) {
	t.Helper()
	previousDisks, previousRoot, previousImages, previousMeasure, previousProtected := dataDisks, dockerRootForPlan, checkImagesForPlan, measurePlace, protectedForPlan
	protectedForPlan = func() []string { return []string{"/var/lib/cosmos-config"} }
	dataDisks = func() []string { return []string{"/archive", "/storage"} }
	dockerRootForPlan = func() string { return "/storage/docker" }
	checkImagesForPlan = func(images []string) (docker.ServerPlatform, []docker.ImagePlatforms) {
		answers := []docker.ImagePlatforms{}
		for _, image := range images {
			answers = append(answers, docker.ImagePlatforms{Image: image, Platforms: []string{"linux/arm64"}, Fits: "no"})
		}
		return docker.ServerPlatform{OS: "linux", Arch: "amd64"}, answers
	}
	measurePlace = func(place installPlace, disks []string) installPlace {
		place.Disk = "/"
		for _, disk := range disks {
			if strings.HasPrefix(place.Path, disk) {
				place.Disk = disk
			}
		}
		place.OSDisk = place.Disk == "/"
		place.Free, place.Total, place.Exists = 100, 200, true
		return place
	}
	t.Cleanup(func() {
		dataDisks, dockerRootForPlan, checkImagesForPlan, measurePlace, protectedForPlan = previousDisks, previousRoot, previousImages, previousMeasure, previousProtected
	})
}

func TestTheInstallPlanListsThePlacesForDataAndTheImagesPlatforms(t *testing.T) {
	withInstallPlanFakes(t)
	config := utils.Config{}
	config.DockerConfig.DefaultDataPath = "/cosmos-storage"
	plan := buildInstallPlan([]string{"app:1"}, config)
	kinds := []string{}
	for _, place := range plan.Places {
		kinds = append(kinds, place.Path+"="+place.Kind)
		if (place.Path == "/cosmos-storage" || place.Path == "/") != place.OSDisk {
			t.Errorf("%s: OS disk %v", place.Path, place.OSDisk)
		}
	}
	if strings.Join(kinds, " ") != "/cosmos-storage=data-path /archive=disk /storage=disk /storage/docker=docker-root /=system" {
		t.Errorf("places: %v", kinds)
	}
	if plan.Server.Arch != "amd64" || len(plan.Images) != 1 || plan.Images[0].Fits != "no" {
		t.Errorf("images: %+v %+v", plan.Server, plan.Images)
	}
}

func TestTheInstallPlanRouteIsBoundedAndProtected(t *testing.T) {
	withInstallPlanFakes(t)
	serve := func(method string, body string, auth *utils.AuthContext) (int, string) {
		req := httptest.NewRequest(method, "http://cosmos.example/cosmos/api/install/plan", strings.NewReader(body))
		if auth != nil {
			req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
		}
		w := httptest.NewRecorder()
		InstallPlanRoute(w, req)
		return w.Code, w.Body.String()
	}
	reader := &utils.AuthContext{Nickname: "bob", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}}
	admin := &utils.AuthContext{Nickname: "alice", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES}}

	if code, _ := serve("POST", `{"images":[]}`, nil); code != http.StatusUnauthorized {
		t.Errorf("without a session: %d", code)
	}
	if code, _ := serve("POST", `{"images":[]}`, reader); code == http.StatusOK {
		t.Errorf("without the permission: %d", code)
	}
	if code, _ := serve("GET", ``, admin); code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", code)
	}
	for _, body := range []string{
		`{"images":["a"],"more":1}`,
		`{"images":["a b"]}`,
		`{"images":["1","2","3","4","5","6","7","8","9"]}`,
		`{"images":["` + strings.Repeat("a", 300) + `"]}`,
		`{"images":["` + strings.Repeat("a", 20000) + `"]}`,
	} {
		if code, _ := serve("POST", body, admin); code != http.StatusBadRequest {
			t.Errorf("%.60s: %d", body, code)
		}
	}
	code, answer := serve("POST", `{"images":["jellyfin/jellyfin:10"]}`, admin)
	var parsed struct {
		Data installPlanAnswer `json:"data"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(answer), &parsed) != nil || len(parsed.Data.Places) == 0 || parsed.Data.Images[0].Image != "jellyfin/jellyfin:10" {
		t.Errorf("%d %s", code, answer)
	}
}

func TestTheInstallPlanRouteIsRegistered(t *testing.T) {
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `srapiAdmin.HandleFunc("/api/install/plan", InstallPlanRoute)`) {
		t.Error("the route is not registered on the admin router")
	}
}
