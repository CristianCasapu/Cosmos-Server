package anyimage

// Registry credentials: only those the server already has, in the Docker
// configuration of the user Cosmos runs as (what `docker login` writes, and
// what the pull uses: docker/docker.go). Read per request, never logged,
// never in an answer.

import (
	"strings"

	dockerConfig "github.com/docker/cli/cli/config"
)

const hubAuthKey = "https://index.docker.io/v1/"

// authKey is the key the Docker configuration keeps a registry's credentials under.
func authKey(registry string) string {
	if registry == hubRegistry || registry == "registry-1.docker.io" || registry == "index.docker.io" {
		return hubAuthKey
	}
	return registry
}

// credentialsFor answers the user and password for a registry (an identity
// token alone is not used: its refresh flow is left out), "" when the server has none. Replaced by the tests.
var credentialsFor = func(registry string) (string, string) {
	file, err := dockerConfig.Load(dockerConfig.Dir())
	if err != nil {
		return "", ""
	}
	for _, key := range []string{authKey(registry), "https://" + registry} {
		auth, err := file.GetAuthConfig(key)
		if err != nil {
			continue
		}
		if auth.Username != "" && auth.Password != "" {
			return auth.Username, auth.Password
		}
	}
	return "", ""
}

// loggedIn tells whether the server has credentials for a registry host.
var loggedIn = func(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	user, _ := credentialsFor(host)
	return user != ""
}
