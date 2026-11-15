package utils

// Fork addition: a configuration file that goes missing does not reopen the
// setup wizard.
//
// Upstream creates a fresh default configuration whenever the file is not
// there, at start and on every later read, and a fresh configuration is a new
// install: the setup wizard answers anyone, and its first visitor becomes the
// administrator. A file can go missing without anyone meaning a new install
// (a restore or a clean-up by hand, a folder on a disk that did not mount).
// So:
//   - while the server runs on an installed configuration, a missing file is
//     reported to the administrators and the settings in memory are used
//     (the next save writes the file again);
//   - at start, when the folder still holds the databases of an install, the
//     server refuses to start and says what to do, instead of starting as a
//     new install.
// A real first install (an empty folder) is unchanged.
//
// And a configuration that cannot be written (a full disk) is reported
// instead of ending the server: upstream stopped the process, which then
// failed the same way at every start.

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var installedConfigLoaded atomic.Bool

// configFatal ends the process (a variable for the tests).
var configFatal = Fatal

// noticeConfigLoaded is the hook of LoadBaseMainConfig.
func noticeConfigLoaded(config Config) {
	if !config.NewInstall {
		installedConfigLoaded.Store(true)
	}
}

// installDatabases are the files only a finished install leaves in the
// configuration folder. Not auth.db: the setup wizard's clean-up removes the
// configuration and opens a new, empty user store, so auth.db without a
// configuration is what a setup in progress looks like. The monitoring
// database is made only once the wizard has finished.
var installDatabases = []string{"database.db"}

var (
	missingConfigMu       sync.Mutex
	missingConfigReported time.Time
)

// refuseDefaultConfig is the hook of ReadConfigFromFile, called before a
// default configuration would be created for a missing file. It returns the
// configuration to use instead and true, or false when creating the default
// is right (the file exists, or this is a first install).
func refuseDefaultConfig(configFile string) (Config, bool) {
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		return Config{}, false
	}
	if installedConfigLoaded.Load() {
		missingConfigMu.Lock()
		report := time.Since(missingConfigReported) > 10*time.Minute
		if report {
			missingConfigReported = time.Now()
		}
		missingConfigMu.Unlock()
		if report {
			MajorError("The configuration file "+configFile+" is missing. Cosmos keeps running on the settings it has in memory and writes the file again at the next save; restore it from a backup if you can.", nil)
		}
		return GetBaseMainConfig(), true
	}
	for _, name := range installDatabases {
		if _, err := os.Stat(CONFIGFOLDER + name); err == nil {
			configFatal("The configuration file "+configFile+" is missing, but the folder "+CONFIGFOLDER+" holds the monitoring database of a finished install ("+name+"). Cosmos does not start as a new install over them: restore the configuration file, or, for a new install, move the folder's content away first.", nil)
		}
	}
	return Config{}, false
}

// configWriteFailed is the hook of SaveConfigTofile for a failed write.
func configWriteFailed(err error) {
	MajorError("Writing the configuration file failed: the change is in effect until the next restart, but not saved. Check the free space of the configuration folder.", err)
}
