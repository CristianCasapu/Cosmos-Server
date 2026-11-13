package security

import "strings"

// Paths that scanners ask for and that mean nothing on this server. The list
// is short on purpose: every entry is either a file no web server should
// hand out or the entry point of software Cosmos is not, so a visitor who
// asks for one is not a user who mistyped. It only ever applies to requests
// that Cosmos itself answers with "not found"; what is proxied to an app is
// the app's business.
//
// Sources: Fail2ban's botsearch filters (webmail, phpMyAdmin and WordPress
// entry points, cgi-bin) and the paths every mass scanner starts with
// (secrets and version-control folders, well-known exploit endpoints).
var (
	// whole path segments, lower case
	probeSegments = map[string]string{
		"wp-login.php":    "wordpress",
		"wp-admin":        "wordpress",
		"wp-includes":     "wordpress",
		"wp-content":      "wordpress",
		"xmlrpc.php":      "wordpress",
		"wlwmanifest.xml": "wordpress",

		"phpmyadmin":  "phpmyadmin",
		"pma":         "phpmyadmin",
		"myadmin":     "phpmyadmin",
		"mysqladmin":  "phpmyadmin",
		"adminer.php": "phpmyadmin",

		"cgi-bin":        "exploit",
		"eval-stdin.php": "exploit", // PHPUnit, CVE-2017-9841
		"phpinfo.php":    "exploit",
		"hnap1":          "exploit", // home routers
		"boaform":        "exploit",
		"gponform":       "exploit",
	}
	// first segments, lower case
	probeFirstSegments = map[string]string{
		"actuator":      "exploit", // Spring Boot
		"_ignition":     "exploit", // Laravel, CVE-2021-3129
		"server-status": "exploit", // Apache
		"owa":           "exploit", // Exchange
		"ecp":           "exploit",
	}
)

// maxProbePath is how much of a path is looked at.
const maxProbePath = 512

// ProbeFamily tells whether a path is one that scanners ask for, and which
// family it belongs to: "dotfile" (a hidden file or folder: /.env,
// /.git/config, /.aws/credentials), "wordpress", "phpmyadmin" or "exploit".
// It returns "" for any other path. /.well-known/ is not a probe.
func ProbeFamily(path string) string {
	if len(path) > maxProbePath {
		path = path[:maxProbePath]
	}
	path = strings.ToLower(path)

	first := true
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." && segment != ".well-known" {
			return "dotfile"
		}
		if strings.HasPrefix(segment, "wp-config") {
			return "wordpress"
		}
		if family, ok := probeSegments[segment]; ok {
			return family
		}
		if first {
			if family, ok := probeFirstSegments[segment]; ok {
				return family
			}
		}
		first = false
	}
	return ""
}
