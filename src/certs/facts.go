package certs

import (
	"strconv"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Which challenges can work on this network. A certificate authority
// checks HTTP-01 on port 80 and TLS-ALPN-01 on port 443 of the address the
// name points at, and on no other port. A server reached from the internet
// on neither (its ports are others and the router forwards 80 and 443
// elsewhere, or nowhere) can only prove a name through its DNS.

// ExternalPorts answers on which ports the internet reaches Cosmos' HTTPS
// and HTTP ports. Set by the server from the router port forwarding
// (upnp.ExternalHTTPSPort and ExternalHTTPPort: the mapped port, else the
// port asked for, else Cosmos' own); nil means Cosmos' own ports.
var ExternalPorts func() (https int, http int)

// The settings of HTTPConfig.Certificates.HTTPChallenge.
const (
	HTTPChallengeDetect = ""
	HTTPChallengeNo     = "no"
	HTTPChallengeYes    = "yes"
)

// Challenges is what the panel is told about the challenges.
type Challenges struct {
	// HTTP is "possible" or "impossible": can a CA reach this server on
	// port 80 or 443.
	HTTP string `json:"http"`
	// Reason is why: "ports" (worked out from the ports), "admin" (the
	// setting says so).
	Reason string `json:"reason"`
	// Setting is the stored choice: "", "no" or "yes".
	Setting string `json:"setting"`
	// The ports the internet reaches Cosmos on.
	HTTPSPort int `json:"httpsPort"`
	HTTPPort  int `json:"httpPort"`
}

func httpChallenge(config utils.Config) Challenges {
	https, _ := strconv.Atoi(config.HTTPConfig.HTTPSPort)
	http, _ := strconv.Atoi(config.HTTPConfig.HTTPPort)
	if ExternalPorts != nil {
		https, http = ExternalPorts()
	}
	result := Challenges{HTTPSPort: https, HTTPPort: http, Setting: config.HTTPConfig.Certificates.HTTPChallenge}
	switch result.Setting {
	case HTTPChallengeNo:
		result.HTTP, result.Reason = "impossible", "admin"
	case HTTPChallengeYes:
		result.HTTP, result.Reason = "possible", "admin"
	default:
		result.Setting = HTTPChallengeDetect
		result.HTTP, result.Reason = "impossible", "ports"
		if https == 443 || http == 80 {
			result.HTTP = "possible"
		}
	}
	return result
}

// noHTTPChallenge is utils.ForkNoHTTPChallenge.
func noHTTPChallenge(config utils.Config) bool {
	return httpChallenge(config).HTTP == "impossible"
}
