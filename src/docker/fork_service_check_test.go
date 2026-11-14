package docker

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestServicePortsTheCreateLoopWouldCrashOnAreRefused(t *testing.T) {
	good := []string{"8080:80", "8080:80/udp", "127.0.0.1:8080:80", "[::1]:8080:80/tcp", "8000-8010:8000-8010", "6881:6881/udp"}
	bad := []string{"8080", "80/tcp", ":80", "10-8:10-8", "9-8:80", "x:80", "8080:y", "70000:80", "8080:80/http", "nothing:8080:80", "1-5000:1-5000"}
	for _, spec := range good {
		if err := checkPortSpec(spec); err != nil {
			t.Errorf("%q refused: %v", spec, err)
		}
	}
	for _, spec := range bad {
		if err := checkPortSpec(spec); err == nil {
			t.Errorf("%q accepted", spec)
		}
	}
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"web": {Ports: []string{"8080:80"}, Devices: []string{"/dev/dri:/dev/dri"}},
	}}
	if err := CheckServiceRequest(request); err != nil {
		t.Errorf("a good request: %v", err)
	}
	request.Services["db"] = ContainerCreateRequestContainer{Devices: []string{"/dev/dri"}}
	if err := CheckServiceRequest(request); err == nil || !strings.Contains(err.Error(), "service db") {
		t.Errorf("a device without ':': %v", err)
	}
}

func TestCreatingAServiceDoesNotReplaceAnExistingContainerUnlessAsked(t *testing.T) {
	previous := containerExists
	containerExists = func(name string) bool { return name == "jellyfin" }
	defer func() { containerExists = previous }()

	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"jellyfin": {Ports: []string{"8096:8096"}},
		"other":    {Name: "radarr"},
	}}
	recorder := httptest.NewRecorder()
	if forkRefuseService(recorder, httptest.NewRequest("POST", "/cosmos/api/docker-service", nil), request, func() {}) {
		t.Fatal("an existing name was accepted")
	}
	if body := recorder.Body.String(); !strings.HasPrefix(body, "[OPERATION FAILED]") || !strings.Contains(body, "jellyfin") || strings.Contains(body, "radarr") {
		t.Errorf("answer: %s", body)
	}
	if !forkRefuseService(httptest.NewRecorder(), httptest.NewRequest("POST", "/cosmos/api/docker-service?replace=1", nil), request, func() {}) {
		t.Error("an edit (replace=1) was refused")
	}
	bad := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"x": {Ports: []string{"8080"}}}}
	recorder = httptest.NewRecorder()
	if forkRefuseService(recorder, httptest.NewRequest("POST", "/cosmos/api/docker-service?replace=1", nil), bad, func() {}) || !strings.Contains(recorder.Body.String(), "port") {
		t.Errorf("a crashing port passed: %s", recorder.Body.String())
	}
}

func TestServiceCheckIsHookedIn(t *testing.T) {
	source, err := os.ReadFile("api_blueprint.go")
	if err != nil {
		t.Fatal(err)
	}
	hook := strings.Index(string(source), "forkRefuseService(w, req, serviceRequest, flusher.Flush)")
	create := strings.Index(string(source), "CreateService(serviceRequest,")
	if hook == -1 || create == -1 || hook > create {
		t.Error("the check is not called before CreateService in CreateServiceRoute")
	}
}
