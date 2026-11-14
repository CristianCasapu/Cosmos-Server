// Package certs is a fork addition: everything this server decides about
// the certificates it orders by itself. Which certificate authority, with
// which account and key, through which DNS provider; when a certificate is
// due, how long to wait after a failure and how many orders a day are
// allowed; what a failure means in plain words; whether the challenge of a
// domain can be answered in another domain; and what the panel shows about
// all of it (routes under /cosmos/api/certificates).
//
// Upstream's code keeps deciding which hostnames go on which certificate
// and which certificate a handshake gets; it asks this package through the
// function variables of utils/fork_certs.go.
package certs

import (
	"crypto/x509"
	"net/url"
	"os"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The only certificate authorities a released server talks to.
const (
	letsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStaging    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// The two variables that point the orders at a test authority. Both are
// needed; they are read once, when the process starts.
const (
	envTestDirectory = "FORK_ACME_TEST_DIRECTORY"
	envTestCA        = "FORK_ACME_TEST_CA"
	envTestTick      = "FORK_ACME_TEST_TICK"
)

// CA is a certificate authority orders go to.
type CA struct {
	// Kind is "production", "staging" or "test". Only "production" gives
	// certificates browsers trust.
	Kind      string
	Name      string
	Directory string
	// roots, when set, is the only thing the ACME client trusts for the
	// directory's own HTTPS certificate (the test authority).
	roots *x509.CertPool
}

// testCA is the test authority read from the environment at start, nil
// when there is none (always, on a real server).
var testCA *CA

func init() {
	ca, problem := readTestCA(os.Getenv, os.ReadFile)
	testCA = ca
	if problem != "" {
		// said once, at start; the orders go to Let's Encrypt
		utils.Warn("Certificates: " + envTestDirectory + " is ignored: " + problem)
	}
}

// readTestCA applies the conditions under which a test directory is used:
// an https address, and a file of PEM certificates that is the only thing
// trusted for it. Anything less and the variable is ignored.
func readTestCA(getenv func(string) string, readFile func(string) ([]byte, error)) (*CA, string) {
	directory := strings.TrimSpace(getenv(envTestDirectory))
	bundle := strings.TrimSpace(getenv(envTestCA))
	if directory == "" {
		return nil, ""
	}
	if bundle == "" {
		return nil, envTestCA + " (the file with the test authority's own certificate) is not set"
	}
	parsed, err := url.Parse(directory)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, "it is not an https address"
	}
	content, err := readFile(bundle)
	if err != nil {
		return nil, "the file named by " + envTestCA + " cannot be read"
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, "the file named by " + envTestCA + " holds no certificate"
	}
	return &CA{Kind: "test", Name: "Test authority", Directory: directory, roots: pool}, ""
}

// Current is the certificate authority orders go to now.
func Current() CA {
	if testCA != nil {
		return *testCA
	}
	if os.Getenv("ACME_STAGING") == "true" {
		return CA{Kind: "staging", Name: "Let's Encrypt (staging)", Directory: letsEncryptStaging}
	}
	return CA{Kind: "production", Name: "Let's Encrypt", Directory: letsEncryptProduction}
}

// accountFile is where the account registered at a CA is kept. Production
// keeps the file upstream wrote, so an existing account is reused; every
// other directory has a file of its own, so that switching to staging and
// back does not register again each time.
func (ca CA) accountFile() string {
	if ca.Kind == "production" {
		return "acme-account.json"
	}
	return "acme-account-" + ca.Kind + ".json"
}
