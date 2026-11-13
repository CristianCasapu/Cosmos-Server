package snapshots

// A folder that has a disk mounted somewhere inside it is not moved aside by
// a restore and not deleted by the clean-up of kept folders.
//
// Both look at the folder itself (mountPoint). That is not enough: a folder
// with a mount point further down can be renamed, and the mount moves with
// it. A restore would leave the live disk under the kept folder, and
// deleting the kept folder would then empty that disk. So both refuse as
// soon as anything is mounted below the folder, and say what.

import (
	"os"
	"path/filepath"
)

// mountBelow returns a mount point that lies inside dir, "" when there is
// none. A variable so tests can pretend.
var mountBelow = findMountBelow

func findMountBelow(dir string) string {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	real := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		real = resolved
	}
	return mountBelowIn(mountPoints(string(data)), dir, real)
}

// mountBelowIn looks for a mount point inside dir, which the mount table may
// know under its real path. Both are clean absolute paths.
func mountBelowIn(points []string, dir string, real string) string {
	for _, point := range points {
		point = filepath.Clean(point)
		if inside(point, dir) || inside(point, real) {
			return point
		}
	}
	return ""
}
