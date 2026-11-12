package docker

// Fork addition: where a named volume is on the host.
//
// Upstream assumed /var/lib/docker/volumes/<name>/_data. Docker's data root can be
// anywhere (upstream issue #478; "data-root" in daemon.json), and then a backup or
// restore of a volume's folder did not find the containers that use it, so they
// were not stopped around the operation.

import (
	"path/filepath"
)

// volumeHostPath returns the folder of a volume mount: the one Docker reports for the
// mount (the volume's mountpoint), or the default location if Docker reports none.
func volumeHostPath(name string, source string) string {
	if source != "" {
		return source
	}
	return filepath.Join("/var/lib/docker/volumes", name, "_data")
}
