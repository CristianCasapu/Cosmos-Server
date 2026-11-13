package utils

// Fork addition: secrets in an address do not go into the log or the events.
//
// The request log writes the address of every request, and an event is kept
// for every request and for every refused one, each with its address.
// Invitation and password links carry their key in the address
// (/cosmos-ui/register?...&key=...), an API token can be given as ?token=,
// and applications behind routes put codes and tokens there too. Whoever may
// read the log or the events (a read permission is enough) could collect
// them. The value of a parameter whose name says it is a secret is replaced
// by *** before the address is written anywhere; everything else stays.

import (
	"net/url"
	"strings"
)

// names of query parameters whose values are not written down
var secretQueryNames = map[string]bool{
	"token": true, "key": true, "registerkey": true, "code": true, "password": true, "passwd": true, "pass": true,
	"secret": true, "client_secret": true, "access_token": true, "id_token": true, "refresh_token": true,
	"apikey": true, "api_key": true, "auth": true, "authorization": true, "jwt": true, "session": true,
	"signature": true, "sig": true, "otp": true,
}

// RedactQuery returns a raw query with the values of secret parameters
// replaced by ***. Order and encoding of everything else are kept.
func RedactQuery(rawQuery string) string {
	if rawQuery == "" {
		return rawQuery
	}
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		name, _, hasValue := strings.Cut(part, "=")
		if !hasValue {
			continue
		}
		plain, err := url.QueryUnescape(name)
		if err != nil {
			plain = name
		}
		if secretQueryNames[strings.ToLower(plain)] {
			parts[i] = name + "=***"
		}
	}
	return strings.Join(parts, "&")
}

// RedactedURL returns a copy of an address that can be written down.
func RedactedURL(address *url.URL) *url.URL {
	if address == nil {
		return nil
	}
	redacted := *address
	redacted.RawQuery = RedactQuery(address.RawQuery)
	redacted.User = nil
	return &redacted
}

// RedactedRequestURI does the same for the request line's address.
func RedactedRequestURI(requestURI string) string {
	path, query, hasQuery := strings.Cut(requestURI, "?")
	if !hasQuery {
		return requestURI
	}
	return path + "?" + RedactQuery(query)
}
