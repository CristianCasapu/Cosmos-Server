package appsignin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

func check(t *testing.T, request docker.DockerServiceCreateRequest, existing ...string) (docker.DockerServiceCreateRequest, error) {
	t.Helper()
	old := networkExists
	t.Cleanup(func() { networkExists = old })
	networkExists = func(name string) bool {
		for _, e := range existing {
			if e == name {
				return true
			}
		}
		return false
	}
	err := beforeCreation(httptest.NewRequest("POST", "http://cosmos.example.org/", nil), &request)
	return request, err
}

func svc(image string, env []string, labels map[string]string) docker.ContainerCreateRequestContainer {
	return docker.ContainerCreateRequestContainer{Image: image, Environment: env, Labels: labels}
}

func TestOnlyTheLabelTheReviewWritesIsTaken(t *testing.T) {
	guess := []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=x", "SMTP_PASSWORD="}
	ok := []docker.ContainerCreateRequestContainer{
		svc("registry.example.org/a/forms:1", guess, map[string]string{LabelKey: "where=web;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=guess"}),
		svc("registry.example.org/a/forms:1", guess, nil),
		svc("jellyfin/jellyfin:10.11", nil, map[string]string{LabelKey: "where=web;first=owner_name;note=firstRunPage;from=review"}),
		svc("postgres:18", []string{"POSTGRES_PASSWORD=x"}, nil),
	}
	for i, s := range ok {
		if _, err := check(t, docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"s": s}}); err != nil {
			t.Errorf("%d refused: %v", i, err)
		}
	}
	bad := []docker.ContainerCreateRequestContainer{
		// the review's reproduction: secrets named as user, e-mail, database
		svc("registry.example.org/a/forms:1", []string{"ADMIN_PASSWORD=x", "DB_PASSWORD=y", "APP_SECRET_KEY=z"},
			map[string]string{LabelKey: "where=web;user=APP_SECRET_KEY;email=ADMIN_PASSWORD;database=DB_PASSWORD"}),
		svc("registry.example.org/a/forms:1", guess, map[string]string{LabelKey: "where=web;email=SMTP_PASSWORD;password=ADMIN_PASSWORD;from=guess"}),
		svc("postgres:18", nil, map[string]string{LabelKey: "where=database;user=POSTGRES_PASSWORD"}),
		svc("jellyfin/jellyfin:10.11", nil, map[string]string{LabelKey: "where=web;first=x;password=API_KEY;note=firstRunPage"}),
		svc("registry.example.org/a/plain:1", nil, map[string]string{LabelKey: "where=web;password=ANY"}),
		// a manager label on an image that is not a manager, or naming a stranger
		svc("busybox:1.36", nil, map[string]string{ManagerLabel: "db"}),
		svc("adminer:5", nil, map[string]string{ManagerLabel: "elsewhere"}),
	}
	for i, s := range bad {
		if _, err := check(t, docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"s": s}}); err == nil {
			t.Errorf("%d taken", i)
		}
	}
}

func TestManagersAndToolsWithoutSignInAreForAdministrators(t *testing.T) {
	route := utils.ProxyRouteConfig{Name: "r", Mode: "SERVAPP", UseHost: true, Host: "x.example.org", Target: "http://x:80"}
	for _, image := range []string{"adminer:5", "phpmyadmin:5", "redis/redisinsight:2", "redis/redis-stack:7.4.0-v1"} {
		s := svc(image, nil, nil)
		s.Routes = []utils.ProxyRouteConfig{route}
		got, err := check(t, docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"s": s}})
		if err != nil || !got.Services["s"].Routes[0].AdminOnly || !got.Services["s"].Routes[0].AuthEnabled {
			t.Errorf("%s: %v %+v", image, err, got.Services["s"].Routes)
		}
	}
	s := svc("nginx:1", nil, nil)
	s.Routes = []utils.ProxyRouteConfig{route}
	got, _ := check(t, docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"s": s}})
	if got.Services["s"].Routes[0].AdminOnly {
		t.Error("an ordinary web app was changed")
	}
}

func TestAManagerGetsNewNetworksOfItsOwnAndNotTheDefaultBridge(t *testing.T) {
	make := func(mode string, nets ...string) docker.DockerServiceCreateRequest {
		m := svc("dpage/pgadmin4:9", nil, map[string]string{ManagerLabel: "db"})
		m.NetworkMode = mode
		m.Networks = map[string]docker.ContainerCreateRequestServiceNetwork{}
		request := docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"db": svc("postgres:18", nil, nil), "m": m}, Networks: map[string]docker.ContainerCreateRequestNetwork{}}
		for _, n := range nets {
			m.Networks[n] = docker.ContainerCreateRequestServiceNetwork{}
			request.Networks[n] = docker.ContainerCreateRequestNetwork{Driver: "bridge"}
		}
		request.Services["m"] = m
		return request
	}
	if _, err := check(t, make("db-manager-k3x9", "db-manager-k3x9")); err != nil {
		t.Fatalf("a fresh network of its own: %v", err)
	}
	if _, err := check(t, make("db-manager-k3x9", "db-manager-k3x9"), "db-manager-k3x9"); err == nil || !strings.Contains(err.Error(), "not new") {
		t.Fatalf("an existing network: %v", err)
	}
	if _, err := check(t, make("", "db-manager-k3x9")); err == nil || !strings.Contains(err.Error(), "default network") {
		t.Fatalf("on the default bridge: %v", err)
	}
	// a network the request does not create
	request := make("other", "db-manager-k3x9")
	m := request.Services["m"]
	m.Networks["other"] = docker.ContainerCreateRequestServiceNetwork{}
	request.Services["m"] = m
	if _, err := check(t, request); err == nil {
		t.Fatal("a network it does not create")
	}
}
