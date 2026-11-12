package utils

// Fork addition: the time zone given to new containers.
//
// A container definition can ask for the server's time zone with TZ=auto.
// Upstream answers with the TZ variable of the Cosmos process and drops the
// variable when there is none, which is the normal case for a service started
// by systemd: such containers then run in UTC. The zone is now looked for in
// this order: the Timezone setting, the TZ variable of the process, the zone
// the host is set to (/etc/timezone, or where /etc/localtime points to). When
// none of them gives a zone the variable is dropped, as before.

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

// ValidateTimezone checks a zone an admin entered: empty (no preference) or
// an IANA name this server can load, such as Europe/Bucharest.
func ValidateTimezone(name string) error {
	if name == "" {
		return nil
	}
	if !plausibleZoneName(name) || strings.EqualFold(name, "local") {
		return errors.New("Time zone: enter a name such as Europe/Bucharest, or leave it empty")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return errors.New("Time zone: " + name + " is not a time zone this server knows")
	}
	return nil
}

// plausibleZoneName accepts what a zone name can look like (Europe/Bucharest,
// Etc/GMT+2, America/Argentina/Buenos_Aires, UTC) and nothing that could be a
// path out of the zone database.
func plausibleZoneName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" {
			return false
		}
		for _, c := range part {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			case c == '_', c == '-', c == '+':
			default:
				return false
			}
		}
	}
	return true
}

// hostTimezone reads the zone the host is set to. roots are the places the
// host's files can be found at ("" for the machine itself); the two functions
// stand for os.ReadFile and os.Readlink.
func hostTimezone(roots []string, readFile func(string) ([]byte, error), readlink func(string) (string, error)) string {
	for _, root := range roots {
		if content, err := readFile(root + "/etc/timezone"); err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if plausibleZoneName(line) {
					return line
				}
				break
			}
		}

		if target, err := readlink(root + "/etc/localtime"); err == nil {
			const database = "/usr/share/zoneinfo/"
			if i := strings.Index(target, database); i != -1 {
				zone := target[i+len(database):]
				zone = strings.TrimPrefix(strings.TrimPrefix(zone, "posix/"), "right/")
				if plausibleZoneName(zone) {
					return zone
				}
			}
		}
	}
	return ""
}

// resolveTimezone picks the zone for a new container; "" means none is known.
func resolveTimezone(setting string, processTZ string, host func() string) string {
	if zone := strings.TrimSpace(setting); zone != "" {
		return zone
	}
	if processTZ != "" {
		return processTZ
	}
	return host()
}

func hostTimezoneRoots() []string {
	if IsInsideContainer {
		// the host's files first, then the container's own
		return []string{"/mnt/host", ""}
	}
	return []string{""}
}

// DetectedTimezone is the zone used when the Timezone setting is empty: the
// TZ variable of the process, else the host's zone, else "".
func DetectedTimezone() string {
	return resolveTimezone("", os.Getenv("TZ"), func() string {
		return hostTimezone(hostTimezoneRoots(), os.ReadFile, os.Readlink)
	})
}

// ContainerTimezone is the zone that replaces TZ=auto in a new container; ""
// when there is nothing to replace it with.
func ContainerTimezone() string {
	return resolveTimezone(GetMainConfig().Timezone, os.Getenv("TZ"), func() string {
		return hostTimezone(hostTimezoneRoots(), os.ReadFile, os.Readlink)
	})
}

// DetectedTimezoneFor is DetectedTimezone for those who may read the
// configuration, for the placeholder of the setting; "" for everyone else.
func DetectedTimezoneFor(req *http.Request) string {
	if !HasPermission(req, PERM_CONFIGURATION_READ) {
		return ""
	}
	return DetectedTimezone()
}
