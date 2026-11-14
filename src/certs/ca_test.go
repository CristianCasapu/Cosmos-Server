package certs

import (
	"errors"
	"strings"
	"testing"
)

func TestTheTestDirectoryNeedsItsOwnAuthorityFile(t *testing.T) {
	env := setup(t, nil)
	bundle, _ := env.ca.issue([]string{"pebble"}, t0, t0.Add(day))
	files := map[string]string{"/shared/ca.pem": bundle, "/shared/empty.pem": "nothing here"}
	read := func(path string) ([]byte, error) {
		if content, ok := files[path]; ok {
			return []byte(content), nil
		}
		return nil, errors.New("no such file")
	}
	with := func(directory string, bundle string) func(string) string {
		return func(name string) string {
			switch name {
			case envTestDirectory:
				return directory
			case envTestCA:
				return bundle
			}
			return ""
		}
	}

	if ca, problem := readTestCA(with("", ""), read); ca != nil || problem != "" {
		t.Errorf("nothing set: %v %q", ca, problem)
	}
	if ca, problem := readTestCA(with("", "/shared/ca.pem"), read); ca != nil || problem != "" {
		t.Errorf("only the file set: %v %q", ca, problem)
	}
	refused := map[string][2]string{
		"the directory alone":        {"https://pebble:14000/dir", ""},
		"a plain http address":       {"http://pebble:14000/dir", "/shared/ca.pem"},
		"not an address":             {"pebble", "/shared/ca.pem"},
		"an address with a password": {"https://user:secret@pebble:14000/dir", "/shared/ca.pem"},
		"a file that is not there":   {"https://pebble:14000/dir", "/shared/missing.pem"},
		"a file without certificate": {"https://pebble:14000/dir", "/shared/empty.pem"},
	}
	for name, values := range refused {
		ca, problem := readTestCA(with(values[0], values[1]), read)
		if ca != nil || problem == "" {
			t.Errorf("%s: the test directory must be ignored, got %v %q", name, ca, problem)
		}
		if strings.Contains(problem, "secret") {
			t.Errorf("%s: the problem quotes the address: %q", name, problem)
		}
	}

	ca, problem := readTestCA(with("https://pebble:14000/dir", "/shared/ca.pem"), read)
	if ca == nil || problem != "" || ca.Kind != "test" || ca.Directory != "https://pebble:14000/dir" || ca.roots == nil {
		t.Fatalf("both set: %+v %q", ca, problem)
	}
	// the client trusts that file for the directory, and nothing else
	client := acmeHTTPClient(*ca)
	if client.Transport.(interface{ CloseIdleConnections() }) == nil {
		t.Fatal("no transport")
	}
}

func TestProductionIsLetsEncryptUnlessTheProcessWasToldOtherwise(t *testing.T) {
	setup(t, nil)
	t.Setenv("ACME_STAGING", "")
	if ca := Current(); ca.Kind != "production" || ca.Directory != "https://acme-v02.api.letsencrypt.org/directory" || ca.roots != nil {
		t.Errorf("by default: %+v", ca)
	}
	t.Setenv("ACME_STAGING", "true")
	if ca := Current(); ca.Kind != "staging" || ca.Directory != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Errorf("with ACME_STAGING: %+v", ca)
	}
	// nothing an admin can store reaches these variables: a domain's
	// settings are given to its provider only when lego declares them
	for _, name := range []string{"ACME_STAGING", envTestDirectory, envTestCA, envTestTick, "LEGO_CA_CERTIFICATES", "LEGO_CA_SERVER_NAME", "SSL_CERT_FILE"} {
		for provider := range Providers() {
			if allowedVariable(provider, name) {
				t.Errorf("a domain using %s could set %s", provider, name)
			}
		}
	}

	// each authority has an account file of its own; production keeps upstream's
	files := map[string]string{}
	for _, ca := range []CA{{Kind: "production"}, {Kind: "staging"}, {Kind: "test"}} {
		files[ca.Kind] = ca.accountFile()
	}
	if files["production"] != "acme-account.json" || files["staging"] == files["production"] || files["test"] == files["staging"] || files["test"] == files["production"] {
		t.Errorf("account files: %v", files)
	}
}
