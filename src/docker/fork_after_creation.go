package docker

// Fork addition (plan 45): what happens once an installation is done. Set by
// src/appsignin (the e-mail and the notification with the new apps' sign-in
// details), which imports this package and so cannot be called from here
// directly. Called by CreateServiceRoute after the start check, for a
// creation only: an edit, a repair or a re-deploy (?replace=1) is not one.
// It must return at once (it works off the request) and never fail the
// creation.

import "net/http"

// ForkAfterCreation receives the request (who installed it), the
// definition that was created and the start check's result.
var ForkAfterCreation = func(req *http.Request, request DockerServiceCreateRequest, check StartCheck) {}

// forkAfterCreation is the hook of CreateServiceRoute.
func forkAfterCreation(req *http.Request, request DockerServiceCreateRequest, check StartCheck) {
	if req.URL.Query().Get("replace") == "1" {
		return
	}
	defer func() { recover() }()
	ForkAfterCreation(req, request, check)
}
