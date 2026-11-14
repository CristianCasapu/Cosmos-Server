// Package maildetect is a fork addition: it proposes SMTP settings for an
// email address, for the email settings form. It knows a table of providers
// (providers.json, no network needed), asks DNS for the domain's MX and SRV
// records, and checks each candidate with one connection that goes as far as
// the greeting, EHLO and the TLS handshake. It never has a password and
// never signs in anywhere. See api.go for the bounds of the route.
package maildetect

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed providers.json
var providersJSON []byte

// Server is one way to reach a provider's SMTP service.
type Server struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // "tls", "starttls" or "none"
}

// Provider is one entry of the built-in table.
type Provider struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Relay     bool     `json:"relay"`
	Domains   []string `json:"domains"`
	MX        []string `json:"mx"`
	Hosts     []string `json:"hosts"`
	Servers   []Server `json:"servers"`
	User      string   `json:"user"`
	UserValue string   `json:"userValue"`
	Notes     []string `json:"notes"`
	URL       string   `json:"url"`
}

var providers = loadProviders(providersJSON)

func loadProviders(raw []byte) []Provider {
	var file struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		// the file is part of the program: a broken one is a build mistake,
		// caught by the tests; the server then simply knows no provider
		return nil
	}
	return file.Providers
}

// Providers returns the built-in table.
func Providers() []Provider {
	return providers
}

// providerByDomain finds the provider of a mailbox domain.
func providerByDomain(domain string) *Provider {
	for i := range providers {
		for _, d := range providers[i].Domains {
			if d == domain {
				return &providers[i]
			}
		}
	}
	return nil
}

// providerByMX finds the provider that receives mail for a hosted domain,
// from the name of its MX host.
func providerByMX(host string) *Provider {
	for i := range providers {
		for _, suffix := range providers[i].MX {
			if endsWithName(host, suffix) {
				return &providers[i]
			}
		}
	}
	return nil
}

// endsWithName reports whether host is name or a name below it.
func endsWithName(host, name string) bool {
	return host == name || strings.HasSuffix(host, "."+name)
}
