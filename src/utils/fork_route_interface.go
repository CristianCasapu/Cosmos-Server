package utils

// Fork addition (plan 49): what a URL is to its app, as the administrator
// set it on the URL form: the app's administration page, its page for users,
// its API, or something else, with a label of the administrator's own and
// whether the address is kept at home on purpose. The panel uses it to pick
// the link an app's tile opens (src/configapi/fork_app_interfaces.go,
// client/src/utils/appInterfaces.js). Upstream ignores the field.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The roles a URL can have.
const (
	InterfaceAdmin = "admin"
	InterfaceUser  = "user"
	InterfaceAPI   = "api"
	InterfaceOther = "other"
)

// RouteInterfaceLabelMax is the longest label, in characters.
const RouteInterfaceLabelMax = 40

// ForkRouteInterface is the route's own setting; nil means "as proposed".
type ForkRouteInterface struct {
	Role     string `yaml:"role,omitempty" json:"Role,omitempty"`
	Label    string `yaml:"label,omitempty" json:"Label,omitempty"`
	HomeOnly bool   `yaml:"home_only,omitempty" json:"HomeOnly,omitempty"`
	// plan 50: the app's "How to use it" guide: "" as proposed, "none", or
	// a kind of src/guides (checked there, on every save: configapi)
	Guide string `yaml:"guide,omitempty" json:"Guide,omitempty"`
}

// ValidInterfaceRole reports whether a role is one of the four ("" is "as proposed").
func ValidInterfaceRole(role string) bool {
	switch role {
	case "", InterfaceAdmin, InterfaceUser, InterfaceAPI, InterfaceOther:
		return true
	}
	return false
}

// RouteInterfaceProblem says what is wrong with a route's setting, "" when
// nothing is. The label is plain text: at most 40 characters, no control
// characters and none of the characters markup is made of.
func RouteInterfaceProblem(setting *ForkRouteInterface) string {
	if setting == nil {
		return ""
	}
	if !ValidInterfaceRole(setting.Role) {
		return "The role of the address must be admin, user, api or other"
	}
	if len(setting.Guide) > 24 || strings.Trim(setting.Guide, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return "The guide of the address must be a short name of lower-case letters"
	}
	label := setting.Label
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > RouteInterfaceLabelMax {
		return "The label of the address is longer than 40 characters"
	}
	if strings.TrimSpace(label) != label {
		return "The label of the address starts or ends with a space"
	}
	for _, r := range label {
		if unicode.IsControl(r) || strings.ContainsRune("<>\"'`\\{}", r) || r == '\u2028' || r == '\u2029' {
			return "The label of the address may hold letters, digits, spaces and simple punctuation only"
		}
	}
	return ""
}

// RouteKey is an opaque key of a route for the panel: the same route gets the
// same key in GET /api/dashboard and GET /api/config, two routes that share a
// name (nothing refuses that) get different ones unless they are the same in
// every part the key is made of. Never an index: lists differ per caller.
func RouteKey(route ProxyRouteConfig) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		route.Name, string(route.Mode), strings.ToLower(strings.TrimSpace(route.Host)), route.PathPrefix, route.Target,
	}, "\x00")))
	return hex.EncodeToString(sum[:6])
}

// WithRouteKeys gives each route of a configuration (a copy for an answer)
// its key.
func WithRouteKeys(config *Config) {
	routes := make([]ProxyRouteConfig, len(config.HTTPConfig.ProxyConfig.Routes))
	for i, route := range config.HTTPConfig.ProxyConfig.Routes {
		route.ForkKey = RouteKey(route)
		routes[i] = route
	}
	config.HTTPConfig.ProxyConfig.Routes = routes
}

// WithoutRouteKeys is a configuration without the keys an answer carried and a
// save sent back: they are never written to the file.
func WithoutRouteKeys(config Config) Config {
	routes := make([]ProxyRouteConfig, len(config.HTTPConfig.ProxyConfig.Routes))
	for i, route := range config.HTTPConfig.ProxyConfig.Routes {
		route.ForkKey = ""
		routes[i] = route
	}
	config.HTTPConfig.ProxyConfig.Routes = routes
	return config
}
