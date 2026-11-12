package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Routes that are deliberately absent from the API documentation.
var undocumentedRoutes = map[string]string{
	"/api/terminal/{route}":                         "websocket",
	"/api/servapps/{containerId}/terminal/{action}": "websocket",
	"/api/listen=jobs":                              "legacy alias of /api/jobs",
}

// The SDKs are generated from the swagger annotations, not from the router: a
// route registered here without a godoc block silently never reaches them.
func TestEveryRouteIsDocumented(t *testing.T) {
	router, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../api-docs/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	param := regexp.MustCompile(`\{[^}]+\}`)
	documented := map[string]bool{}
	for path := range spec.Paths {
		documented[param.ReplaceAllString(path, "{}")] = true
	}

	for _, m := range regexp.MustCompile(`HandleFunc\("(/api/[^"]+)"`).FindAllStringSubmatch(string(router), -1) {
		route := m[1]
		if _, skip := undocumentedRoutes[route]; skip || strings.HasPrefix(route, "/api/storage/raid") {
			continue
		}
		if !documented[param.ReplaceAllString(route, "{}")] {
			t.Errorf("%s has no @Router godoc (run scripts/generate-api.sh after adding one)", route)
		}
	}
}
