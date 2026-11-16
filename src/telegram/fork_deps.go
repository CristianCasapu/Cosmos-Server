package telegram

// Fork addition: the "Server update installed" message says when the
// release was an automatic dependency update, from the dependency summary
// of the signed meta.json of the running release (read and bounded by
// forkrelease.ParseForkDependencies; set by the server before Start).

import (
	"strconv"
	"sync/atomic"

	"github.com/azukaar/cosmos-server/src/forkrelease"
)

var dependencyUpdate atomic.Pointer[forkrelease.ForkDependencies]

// SetDependencyUpdate sets the dependency summary of the running release;
// nil when it was not a dependency update.
func SetDependencyUpdate(d *forkrelease.ForkDependencies) {
	dependencyUpdate.Store(d)
}

// withDependencyUpdate adds "It was an automatic dependency update: 14
// minor, 1 major (react 19.0.0)." to the details of a start message, when
// the running release was one.
func withDependencyUpdate(details func(c *composer)) func(c *composer) {
	d := dependencyUpdate.Load()
	if d == nil || (d.Minor == 0 && d.Major == 0) {
		return details
	}
	minor, major, majors := d.Minor, d.Major, append([]string(nil), d.Majors...)
	if len(majors) > 3 {
		majors = majors[:3]
	}
	return func(c *composer) {
		if details != nil {
			details(c)
		}
		c.line().fixed("It was an automatic dependency update: ")
		if minor > 0 {
			c.fixed(strconv.Itoa(minor) + " minor")
		}
		if major > 0 {
			if minor > 0 {
				c.fixed(", ")
			}
			c.fixed(strconv.Itoa(major) + " major")
			if len(majors) > 0 {
				c.fixed(" (")
				for i, name := range majors {
					if i > 0 {
						c.fixed(", ")
					}
					c.value(name, maxValue)
				}
				if more := major - len(majors); more > 0 {
					c.fixed(" and " + strconv.Itoa(more) + " more")
				}
				c.fixed(")")
			}
		}
		c.fixed(".")
	}
}
