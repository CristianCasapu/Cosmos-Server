package configapi

// Fork addition: directories named in the settings are checked when the
// settings are saved, instead of failing later when they are first used.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azukaar/cosmos-server/src/snapshots"
	"github.com/azukaar/cosmos-server/src/utils"
)

// cleanHostDirectory checks that value can name a directory on the host and
// returns it in its plain form (no doubled or trailing slashes).
func cleanHostDirectory(label string, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, "\x00\r\n") || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s: the path contains characters that are not allowed", label)
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%s: enter an absolute path, starting with /", label)
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s: the path must not contain \"..\"", label)
		}
	}
	return filepath.Clean(value), nil
}

// ensureDirectory makes sure dir exists and can be written to, creating it
// and any missing parents. created tells whether it had to be created.
func ensureDirectory(label string, dir string) (created bool, err error) {
	real := dir
	if utils.IsInsideContainer {
		real = "/mnt/host" + dir // where the host's files are mounted
	}

	info, err := os.Stat(real)
	switch {
	case err == nil && !info.IsDir():
		return false, fmt.Errorf("%s: %s exists but is not a folder", label, dir)
	case errors.Is(err, os.ErrNotExist):
		// Holds copies of the configuration, which contains secrets: owner only.
		if err := os.MkdirAll(real, 0700); err != nil {
			return false, fmt.Errorf("%s: %s does not exist and could not be created (%s)", label, dir, reason(err))
		}
		created = true
	case err != nil:
		return false, fmt.Errorf("%s: %s cannot be used (%s)", label, dir, reason(err))
	}

	probe, err := os.CreateTemp(real, ".cosmos-write-test-*")
	if err != nil {
		return created, fmt.Errorf("%s: Cosmos cannot write to %s (%s)", label, dir, reason(err))
	}
	probe.Close()
	os.Remove(probe.Name())
	return created, nil
}

// reason is the operating system's explanation without the path, which the
// caller already names.
func reason(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// prepareConfigDirectories validates the directories in a configuration that
// is about to be saved and stores them in their plain form. A directory for
// backups that changed is created if it is missing; the save is refused when
// it cannot be used. The same goes for the incremental snapshots.
func prepareConfigDirectories(request *utils.Config, current utils.Config) error {
	backupDir, err := cleanHostDirectory("Backup output directory", request.BackupOutputDir)
	if err != nil {
		return err
	}
	if backupDir == "/" {
		return errors.New("Backup output directory: choose a folder, not the root of the disk")
	}
	request.BackupOutputDir = backupDir

	dataPath, err := cleanHostDirectory("Default data path", request.DockerConfig.DefaultDataPath)
	if err != nil {
		return err
	}
	request.DockerConfig.DefaultDataPath = dataPath

	// Incremental snapshots (src/snapshots): settings that cannot work, or
	// that would be dangerous, are refused with the reason.
	if err := snapshots.ValidateConfig(&request.Snapshots); err != nil {
		return err
	}

	if backupDir != "" && backupDir != current.BackupOutputDir {
		created, err := ensureDirectory("Backup output directory", backupDir)
		if err != nil {
			return err
		}
		if created {
			utils.Log("Settings: created the backup output directory " + backupDir)
		}
	}

	// The destination of the snapshots is created when they are switched on
	// or pointed somewhere else, not on every save: if its disk is missing
	// later, a run must fail instead of filling the disk underneath.
	if wanted := request.Snapshots; wanted.Enabled && (!current.Snapshots.Enabled || wanted.Destination != current.Snapshots.Destination) {
		created, err := ensureDirectory("Incremental backup, destination", wanted.Destination)
		if err != nil {
			return err
		}
		if created {
			utils.Log("Settings: created the destination of the incremental backup " + wanted.Destination)
		}
	}
	return nil
}
