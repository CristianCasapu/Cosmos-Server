package docker

import "testing"

func TestVolumeHostPath(t *testing.T) {
	// Docker's data root moved to /storage/docker: the reported folder wins
	if got := volumeHostPath("jellyfin-config", "/storage/docker/volumes/jellyfin-config/_data"); got != "/storage/docker/volumes/jellyfin-config/_data" {
		t.Errorf("volume on a moved data root: got %q", got)
	}
	// nothing reported: Docker's default location, as before
	if got := volumeHostPath("jellyfin-config", ""); got != "/var/lib/docker/volumes/jellyfin-config/_data" {
		t.Errorf("volume without a reported folder: got %q", got)
	}

	// a backup of the volume's real folder has to find it, the old guess did not
	backup := "/storage/docker/volumes/jellyfin-config/_data"
	if !arePathsRelated(backup, volumeHostPath("jellyfin-config", "/storage/docker/volumes/jellyfin-config/_data")) {
		t.Error("the backup path and the volume's real folder must be related")
	}
	if arePathsRelated(backup, "/var/lib/docker/volumes/jellyfin-config/_data") {
		t.Error("the old hard-coded path is not expected to match a moved data root")
	}
}
