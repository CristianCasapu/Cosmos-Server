package appsignin

// Second review R1 and item 4, against a real Docker daemon of the test's own
// (never the machine's), through the real creation route (CreateServiceRoute:
// the checks before creation, the creation, the start check):
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/appsignin/ -run RepairLive -v
//
// It needs postgres:18, dpage/pgadmin4:9 and busybox:1.36 in that daemon
// (or a way to pull them) and removes what it made.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	doctype "github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

func liveRepairSetup(t *testing.T) {
	t.Helper()
	if os.Getenv("COSMOS_FORK_LIVE_DOCKER") == "" {
		t.Skip("set COSMOS_FORK_LIVE_DOCKER=1 and DOCKER_HOST to a Docker daemon of your own to run this")
	}
	if os.Getenv("DOCKER_HOST") == "" {
		t.Fatal("DOCKER_HOST must point at a Docker daemon of your own: this test never uses the machine's")
	}
	if err := docker.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "cosmos.config.json"))
	oldAfter := docker.ForkAfterCreation
	docker.ForkAfterCreation = func(*http.Request, docker.DockerServiceCreateRequest, docker.StartCheck) {}
	Start()
	docker.ForkAfterCreation = func(*http.Request, docker.DockerServiceCreateRequest, docker.StartCheck) {}
	t.Cleanup(func() { docker.ForkAfterCreation = oldAfter; liveRepairCleanup() })
	liveRepairCleanup()
}

func liveRepairCleanup() {
	ctx := context.Background()
	list, _ := docker.DockerClient.ContainerList(ctx, conttype.ListOptions{All: true})
	for _, c := range list {
		for _, n := range c.Names {
			if strings.HasPrefix(n, "/credsdb") || strings.HasPrefix(n, "/credsform") {
				docker.DockerClient.ContainerRemove(ctx, c.ID, conttype.RemoveOptions{Force: true, RemoveVolumes: true})
			}
		}
	}
	nets, _ := docker.DockerClient.NetworkList(ctx, doctype.NetworkListOptions{})
	for _, n := range nets {
		if strings.HasPrefix(n.Name, "credsdb") || strings.HasPrefix(n.Name, "credsform") {
			docker.DockerClient.NetworkRemove(ctx, n.ID)
		}
	}
}

// create sends a definition to the real creation route and answers its stream.
func create(t *testing.T, request docker.DockerServiceCreateRequest, replace bool) string {
	t.Helper()
	body, _ := json.Marshal(request)
	query := "?anyArch=1"
	if replace {
		query += "&replace=1"
	}
	req := httptest.NewRequest("POST", "http://cosmos.example.org/cosmos/api/docker-service"+query, strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "owner", IsSudoed: true,
		Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES}}))
	w := httptest.NewRecorder()
	docker.CreateServiceRoute(w, req)
	return w.Body.String()
}

func networksOf(t *testing.T, name string) []string {
	t.Helper()
	c, err := docker.DockerClient.ContainerInspect(context.Background(), name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	out := []string{}
	for n := range c.NetworkSettings.Networks {
		out = append(out, n)
	}
	return out
}

func fixtureDefinition(t *testing.T) docker.DockerServiceCreateRequest {
	t.Helper()
	raw, err := os.ReadFile("testdata/panel_definition.json")
	if err != nil {
		t.Fatal(err)
	}
	var request docker.DockerServiceCreateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

// R1a and item 4: the panel's definition of a database with its manager, created without the
// database's password (it fails its start check), then repaired with ?replace=1.
func TestRepairLiveADatabaseWithItsManager(t *testing.T) {
	liveRepairSetup(t)
	request := fixtureDefinition(t)
	db := request.Services["credsdb"]
	kept := []string{}
	password := ""
	for _, line := range db.Environment {
		if strings.HasPrefix(line, "POSTGRES_PASSWORD=") {
			password = line
			continue
		}
		kept = append(kept, line)
	}
	db.Environment = kept
	request.Services["credsdb"] = db
	out := create(t, request, false)
	if !strings.Contains(out, "[OPERATION SUCCEEDED]") || !strings.Contains(out, "[START CHECK]") {
		t.Fatalf("the creation: %s", tail(out))
	}
	if !strings.Contains(out[strings.Index(out, "[START CHECK]"):], `"failing":true`) {
		t.Fatalf("the database without its password was expected to fail its start check: %s", tail(out))
	}
	// the repair: the same definition with the password, created again
	db.Environment = append(db.Environment, password)
	request.Services["credsdb"] = db
	out = create(t, request, true)
	if !strings.Contains(out, "[OPERATION SUCCEEDED]") {
		t.Fatalf("the repair was refused: %s", tail(out))
	}
	for _, name := range []string{"credsdb", "credsdb-pgadmin"} {
		nets := networksOf(t, name)
		if len(nets) != 1 || nets[0] != "credsdb-manager-f1xt3r" {
			t.Errorf("%s: networks %v", name, nets)
		}
	}
	routes := utils.ReadConfigFromFile().HTTPConfig.ProxyConfig.Routes
	for _, r := range routes {
		if r.Name == "credsdb-pgadmin" && !(r.AdminOnly && r.AuthEnabled) {
			t.Errorf("the manager's URL: %+v", r)
		}
	}
}

// R1b: an app whose sign-in is guessed (busybox standing in), whose start check names a
// variable; the repair adds it, which changes the guess: created, with the new label.
func TestRepairLiveAGuessThatChanges(t *testing.T) {
	liveRepairSetup(t)
	app := docker.ContainerCreateRequestContainer{
		Name: "credsform", Image: "busybox:1.36", RestartPolicy: "no",
		Environment: []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=Form-pw-2026x"},
		Labels:      map[string]string{LabelKey: "where=web;user=ADMIN_PASSWORD;from=guess"}, // wrong on purpose: rewritten
	}
	app.Command.UnmarshalJSON([]byte(`["sh","-c","if [ -z \"$ADMIN_USER\" ]; then echo 'ADMIN_USER is required'; exit 1; fi; sleep 3600"]`))
	request := docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"credsform": app}}
	out := create(t, request, false)
	if !strings.Contains(out, "[OPERATION SUCCEEDED]") || !strings.Contains(out, "ADMIN_USER") {
		t.Fatalf("the creation: %s", tail(out))
	}
	c, _ := docker.DockerClient.ContainerInspect(context.Background(), "credsform")
	if c.Config.Labels[LabelKey] != "where=web;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=guess" {
		t.Fatalf("the label: %q", c.Config.Labels[LabelKey])
	}
	app.Environment = append(app.Environment, "ADMIN_USER=owner")
	request.Services["credsform"] = app
	out = create(t, request, true)
	if !strings.Contains(out, "[OPERATION SUCCEEDED]") {
		t.Fatalf("the repair was refused: %s", tail(out))
	}
	time.Sleep(2 * time.Second)
	c, _ = docker.DockerClient.ContainerInspect(context.Background(), "credsform")
	if !c.State.Running || c.Config.Labels[LabelKey] != "where=web;user=ADMIN_USER;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=guess" {
		t.Fatalf("after the repair: running %v, label %q", c.State.Running, c.Config.Labels[LabelKey])
	}
}

func tail(s string) string {
	if len(s) > 1500 {
		return s[len(s)-1500:]
	}
	return s
}
