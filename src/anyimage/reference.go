// Package anyimage lets an administrator install any container image: it
// searches Docker Hub, reads what an image declares (ports, volumes,
// variables …) from its registry without pulling it, lists its tags, and
// carries recipes for the images people install most. Fork addition (plan 40).
//
// Everything that reaches out goes through guard.go (no address on the local
// network unless the server is logged in to that registry) and client.go
// (https only, bounded answers, bounded redirects).
package anyimage

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

// Reference is an image reference split into its parts.
type Reference struct {
	// Registry is the host[:port] the image is pulled from; "docker.io" for Docker Hub.
	Registry string `json:"registry"`
	// Repository is the path on the registry ("library/postgres").
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest,omitempty"`
}

const hubRegistry = "docker.io"

// The grammar of github.com/distribution/reference (the OCI distribution
// specification's names), lower-case only.
var (
	pathComponent = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*$`)
	tagPattern    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	domainLabel   = regexp.MustCompile(`^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])$`)
	portPattern   = regexp.MustCompile(`^[0-9]{1,5}$`)
	numericLabel  = regexp.MustCompile(`^[0-9]+$`)
)

// Reasons a reference is refused, as codes the panel turns into sentences.
var (
	ErrInvalid  = errors.New("invalid")
	ErrInternal = errors.New("internal")
)

// String is the reference as Docker takes it ("postgres:18", "ghcr.io/a/b:1").
func (r Reference) String() string {
	name := r.Repository
	if r.Registry != hubRegistry {
		name = r.Registry + "/" + r.Repository
	} else if strings.HasPrefix(name, "library/") {
		name = strings.TrimPrefix(name, "library/")
	}
	if r.Digest != "" {
		if r.Tag != "" {
			return name + ":" + r.Tag + "@" + r.Digest
		}
		return name + "@" + r.Digest
	}
	return name + ":" + r.Tag
}

// Name is the reference without tag and digest.
func (r Reference) Name() string {
	return strings.Split(Reference{Registry: r.Registry, Repository: r.Repository, Tag: "x"}.String(), ":x")[0]
}

// isDomain tells whether the first component of a name is a registry host:
// it contains a dot or a port, or it is "localhost" (the reference grammar's rule).
func isDomain(component string) bool {
	return strings.ContainsAny(component, ".:") || component == "localhost"
}

// validHost checks host[:port] and tells whether it is a name only an inside
// network can resolve (an IP literal, "localhost", a name without a dot).
func validHost(hostPort string) (internal bool, ok bool) {
	host := hostPort
	if strings.HasPrefix(host, "[") {
		end := strings.Index(host, "]")
		if end < 0 {
			return false, false
		}
		if _, err := netip.ParseAddr(host[1:end]); err != nil {
			return false, false
		}
		rest := host[end+1:]
		if rest != "" && (!strings.HasPrefix(rest, ":") || !validPort(rest[1:])) {
			return false, false
		}
		return true, true
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		if !validPort(host[i+1:]) {
			return false, false
		}
		host = host[:i]
	}
	if host == "" || len(host) > 253 {
		return false, false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true, true
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) > 63 || !domainLabel.MatchString(label) {
			return false, false
		}
	}
	if len(labels) < 2 || strings.EqualFold(host, "localhost") {
		return true, true
	}
	// a name that ends in a number is an address written oddly ("10.0.0.1." or "1.2.3")
	if numericLabel.MatchString(labels[len(labels)-1]) {
		return true, true
	}
	return false, true
}

func validPort(text string) bool {
	if !portPattern.MatchString(text) {
		return false
	}
	n := 0
	for _, c := range text {
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 65535
}

// Parse reads a reference strictly. loggedIn tells whether the server has
// credentials for a registry host (its Docker config): only those may be an
// IP address, "localhost" or a name without a dot.
func Parse(text string, loggedIn func(host string) bool) (Reference, error) {
	if len(text) == 0 || len(text) > 255 {
		return Reference{}, ErrInvalid
	}
	for _, c := range text {
		if c <= ' ' || c == 0x7f || c > 0x7e {
			return Reference{}, ErrInvalid
		}
	}
	if strings.Contains(text, "://") || strings.HasPrefix(text, "/") || strings.HasSuffix(text, "/") {
		return Reference{}, ErrInvalid
	}
	ref := Reference{}
	rest := text
	if at := strings.Index(rest, "@"); at >= 0 {
		ref.Digest = rest[at+1:]
		rest = rest[:at]
		if !digestPattern.MatchString(ref.Digest) {
			return Reference{}, ErrInvalid
		}
	}
	// the tag is after the last colon that comes after the last slash
	slash := strings.LastIndex(rest, "/")
	if colon := strings.LastIndex(rest, ":"); colon > slash {
		ref.Tag = rest[colon+1:]
		rest = rest[:colon]
		if !tagPattern.MatchString(ref.Tag) {
			return Reference{}, ErrInvalid
		}
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 1 && isDomain(parts[0]) {
		internal, ok := validHost(parts[0])
		if !ok {
			return Reference{}, ErrInvalid
		}
		ref.Registry = strings.ToLower(parts[0])
		parts = parts[1:]
		if internal && (loggedIn == nil || !loggedIn(ref.Registry)) {
			return Reference{}, ErrInternal
		}
	} else {
		ref.Registry = hubRegistry
	}
	if ref.Registry == "index.docker.io" || ref.Registry == "registry-1.docker.io" || ref.Registry == "registry.hub.docker.com" {
		ref.Registry = hubRegistry
	}
	if len(parts) == 0 || len(parts) > 8 {
		return Reference{}, ErrInvalid
	}
	for _, part := range parts {
		if !pathComponent.MatchString(part) {
			return Reference{}, ErrInvalid
		}
	}
	if ref.Registry == hubRegistry && len(parts) == 1 {
		parts = []string{"library", parts[0]}
	}
	ref.Repository = strings.Join(parts, "/")
	if len(ref.Repository) > 200 {
		return Reference{}, ErrInvalid
	}
	if ref.Tag == "" && ref.Digest == "" {
		ref.Tag = "latest"
	}
	return ref, nil
}

// registryHost is the host the distribution API is asked on.
func (r Reference) registryHost() string {
	if r.Registry == hubRegistry {
		return "registry-1.docker.io"
	}
	return r.Registry
}

// Page is the address of the image's page for people, shown as a link.
func (r Reference) Page() string {
	switch {
	case r.Registry == hubRegistry && strings.HasPrefix(r.Repository, "library/"):
		return "https://hub.docker.com/_/" + strings.TrimPrefix(r.Repository, "library/")
	case r.Registry == hubRegistry:
		return "https://hub.docker.com/r/" + r.Repository
	case r.Registry == "ghcr.io":
		parts := strings.SplitN(r.Repository, "/", 2)
		if len(parts) == 2 {
			return "https://github.com/" + parts[0] + "/" + strings.Split(parts[1], "/")[0] + "/pkgs/container/" + strings.ReplaceAll(parts[1], "/", "%2F")
		}
		return "https://github.com/" + r.Repository
	case r.Registry == "quay.io":
		return "https://quay.io/repository/" + r.Repository
	}
	return "https://" + r.Registry + "/" + r.Repository
}
