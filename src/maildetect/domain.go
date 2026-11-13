package maildetect

import (
	"errors"
	"strings"

	"golang.org/x/net/idna"
)

const (
	maxAddressLength = 254
	maxDomainLength  = 253
)

var errNotAnAddress = errors.New("enter an email address such as name@example.com")

// DomainOf returns the mail domain of an email address, or of a bare
// domain, in the form it is looked up: lower case, ASCII. Everything before
// the last "@" is dropped: detection never uses the mailbox name.
func DomainOf(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || len(input) > maxAddressLength {
		return "", errNotAnAddress
	}
	domain := input
	if at := strings.LastIndex(input, "@"); at >= 0 {
		if at == 0 {
			return "", errNotAnAddress
		}
		domain = input[at+1:]
	}
	domain = strings.TrimSuffix(domain, ".")
	if ascii, err := idna.Lookup.ToASCII(domain); err == nil {
		domain = ascii
	}
	domain = strings.ToLower(domain)
	if !validHostName(domain) {
		return "", errNotAnAddress
	}
	return domain, nil
}

// cleanHostName brings a name from a DNS answer to the checked form, or
// returns "" for one that is not a plain host name.
func cleanHostName(name string) string {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if !validHostName(name) {
		return ""
	}
	return name
}

// validHostName accepts a DNS name of at least two labels made of letters,
// digits and hyphens. An IP address, a single label, "localhost" and
// anything with other characters are refused.
func validHostName(name string) bool {
	if name == "" || len(name) > maxDomainLength {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	if last == "localhost" || strings.Trim(last, "0123456789") == "" {
		return false
	}
	return true
}
