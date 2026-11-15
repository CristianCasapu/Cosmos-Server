package configapi

// Fork addition: PATCH /api/config logs the container of a SERVAPP route
// after the save. Upstream cut the name out of the target by position and
// panicked on a target without "://" (the save was done, the answer lost).

import "strings"

// servAppTargetName is the host part of a target such as http://name:port,
// "" when there is none.
func servAppTargetName(target string) string {
	_, rest, found := strings.Cut(target, "://")
	if !found {
		return ""
	}
	if i := strings.IndexAny(rest, ":/"); i != -1 {
		rest = rest[:i]
	}
	return rest
}
