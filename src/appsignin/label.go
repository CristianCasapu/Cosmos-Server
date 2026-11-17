package appsignin

// The label `cosmos-fork-signin` the review of an installation sets: which of
// the container's variables are its sign-in, for an image Cosmos has no
// recipe for (a Market app, a guess by the names), and the username proposed
// for an app that makes its first account in its own page. Names of
// variables and a username only, never a value of a secret:
//
//	where=web;user=ADMIN_USER;password=ADMIN_PASSWORD;from=guess
//	where=web;first=owner;note=firstRunPage
//
// It can only point at variables of the same container, which whoever may
// read the card may already read whole (GET /api/servapps/{id}/).

import (
	"regexp"
	"strings"
)

type labelInfo struct {
	where, user, email, password, database string
	first, firstNote                       string
	guess                                  bool
}

var (
	labelWhere = map[string]bool{"database": true, "web": true, "s3": true, "amqp": true, "http": true}
	labelNotes = map[string]bool{"firstRunPage": true, "logPassword": true}
	labelUser  = regexp.MustCompile(`^[^;=\s\x00-\x1f\x7f]{1,64}$`)
)

// parseLabel reads the label strictly: an unknown key, a bad name or a value
// out of bounds makes the whole label count as absent.
func parseLabel(value string) (labelInfo, bool) {
	info := labelInfo{}
	if value == "" || len(value) > maxLabel {
		return info, false
	}
	parts := strings.Split(value, ";")
	if len(parts) > 8 {
		return info, false
	}
	seen := map[string]bool{}
	for _, part := range parts {
		eq := strings.Index(part, "=")
		if eq <= 0 {
			return labelInfo{}, false
		}
		key, val := part[:eq], part[eq+1:]
		if seen[key] {
			return labelInfo{}, false
		}
		seen[key] = true
		switch key {
		case "where":
			if !labelWhere[val] {
				return labelInfo{}, false
			}
			info.where = val
		case "user", "email", "password", "database":
			if !envName.MatchString(val) {
				return labelInfo{}, false
			}
			switch key {
			case "user":
				info.user = val
			case "email":
				info.email = val
			case "password":
				info.password = val
			case "database":
				info.database = val
			}
		case "first":
			if !labelUser.MatchString(val) {
				return labelInfo{}, false
			}
			info.first = val
		case "note":
			if !labelNotes[val] {
				return labelInfo{}, false
			}
			info.firstNote = val
		case "from":
			if val != "guess" && val != "market" && val != "review" {
				return labelInfo{}, false
			}
			info.guess = val == "guess"
		default:
			return labelInfo{}, false
		}
	}
	return info, true
}
