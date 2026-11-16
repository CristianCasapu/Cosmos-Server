package docker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOnlyACreationIsToldNotAnEditOrARepair(t *testing.T) {
	old := ForkAfterCreation
	t.Cleanup(func() { ForkAfterCreation = old })
	calls := 0
	ForkAfterCreation = func(*http.Request, DockerServiceCreateRequest, StartCheck) { calls++ }
	forkAfterCreation(httptest.NewRequest("POST", "http://cosmos.example.org/cosmos/api/docker-service?replace=1", nil), DockerServiceCreateRequest{}, StartCheck{})
	if calls != 0 {
		t.Fatal("an edit (replace=1) was told")
	}
	forkAfterCreation(httptest.NewRequest("POST", "http://cosmos.example.org/cosmos/api/docker-service", nil), DockerServiceCreateRequest{}, StartCheck{})
	if calls != 1 {
		t.Fatal("a creation was not told")
	}
	// a hook that panics never fails the creation
	ForkAfterCreation = func(*http.Request, DockerServiceCreateRequest, StartCheck) { panic("boom") }
	forkAfterCreation(httptest.NewRequest("POST", "http://cosmos.example.org/", nil), DockerServiceCreateRequest{}, StartCheck{})
}
