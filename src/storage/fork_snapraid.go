package storage

// Fork addition: a SnapRAID configuration is checked before it is saved, and
// its actions say when they fail.
//
// Upstream accepted a data or parity row without a folder (findParent(disks,
// "") matches the first device that is not mounted, so "data d1 " was written
// and every sync failed later), a parity on the same physical disk as a data
// disk, a rename onto another configuration's name, a name with a space or a
// line break, and any schedule; it wrote the configuration without the
// configuration lock; "disable" changed nothing (the jobs were registered
// without Disabled); and sync, scrub, fix, enable and disable answered OK
// whatever happened.
//
// Now every folder has to be one of the data disks the Storage page lists
// (a whole data mount: not a merge, a network share or the system disk), the
// data disks and the parity disks each on a physical disk of their own, each
// parity at least as large as the largest data disk, the schedules readable
// by the scheduler's own parser, and the name free.

import (
	"errors"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/azukaar/cosmos-server/src/utils"
	robfigcron "github.com/robfig/cron/v3"
)

var (
	snapRAIDNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{2,31}$`)
	snapRAIDDataNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	// the parser gocron uses for CronJob(crontab, true): seconds first
	snapRAIDCronParser = robfigcron.NewParser(robfigcron.Second | robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow | robfigcron.Descriptor)
)

// The configuration, behind variables for the tests.
var (
	existingSnapRAIDs = func() []utils.SnapRAIDConfig { return utils.GetMainConfig().Storage.SnapRAIDs }
	saveSnapRAID      = saveSnapRAIDConfig
)

func snapRAIDScheduleError(crontab string) error {
	if strings.TrimSpace(crontab) == "" {
		return errors.New("it is empty")
	}
	if len(crontab) > 200 {
		return errors.New("it is too long")
	}
	_, err := snapRAIDCronParser.Parse(crontab)
	return err
}

// snapRAIDNameFree refuses a name another configuration has, and an edit of a
// configuration that does not exist.
func snapRAIDNameFree(existing []utils.SnapRAIDConfig, name string, editName string) error {
	found := editName == ""
	for _, r := range existing {
		if r.Name == editName && editName != "" {
			found = true
			continue
		}
		if r.Name == name {
			return refuse(http.StatusConflict, "STO059", "A SnapRAID named \""+name+"\" already exists: choose another name.")
		}
	}
	if !found {
		return refuse(http.StatusNotFound, "STO062", "The SnapRAID \""+cleanText(editName, 40)+"\" does not exist any more: refresh the page.")
	}
	return nil
}

// checkSnapRAIDConfig checks a configuration against the listed mounts.
func checkSnapRAIDConfig(c utils.SnapRAIDConfig, editName string, existing []utils.SnapRAIDConfig, mounts []storageMount) error {
	if !snapRAIDNamePattern.MatchString(c.Name) {
		return refuse(http.StatusBadRequest, "STO050", "The name must be 3 to 32 letters, digits, spaces, \"-\" or \"_\", starting with a letter or a digit.")
	}
	if len(c.Data) < 2 || len(c.Data) > 32 {
		return refuse(http.StatusBadRequest, "STO053", "Choose between 2 and 32 data disks (you chose "+strconv.Itoa(len(c.Data))+").")
	}
	if len(c.Parity) < 1 || len(c.Parity) > 6 {
		return refuse(http.StatusBadRequest, "STO054", "Choose between 1 and 6 parity disks (you chose "+strconv.Itoa(len(c.Parity))+").")
	}

	type disk struct {
		role  string // how the messages name it
		mount storageMount
	}
	lookup := func(role string, path string) (disk, error) {
		if strings.TrimSpace(path) == "" {
			return disk{}, refuse(http.StatusBadRequest, "STO052", "The "+role+" has no folder: choose one of the data disks, or remove the row.")
		}
		m, ok := findListedMount(mounts, path)
		if !ok || !oneField(path) || m.Path != path {
			return disk{}, refuse(http.StatusBadRequest, "STO055", "The "+role+" ("+cleanText(path, 200)+") is not one of the data disks listed on the Storage page: choose the folder a data disk is mounted on.")
		}
		if m.Kind != "data" {
			what := map[string]string{"merge": "a merged folder", "network": "a network share", "system": "the system disk"}[m.Kind]
			return disk{}, refuse(http.StatusBadRequest, "STO055", "The "+role+" ("+path+") is "+what+": SnapRAID needs data disks of their own.")
		}
		return disk{role: role, mount: m}, nil
	}

	var data, parity []disk
	usedPaths := map[string]string{}
	for _, name := range sortedKeys(c.Data) {
		if !snapRAIDDataNamePattern.MatchString(name) {
			return refuse(http.StatusBadRequest, "STO051", "The data disk name \""+cleanText(name, 40)+"\" is not valid: use 1 to 32 letters, digits, \"-\" or \"_\".")
		}
		d, err := lookup("data disk "+name, c.Data[name])
		if err != nil {
			return err
		}
		if other, used := usedPaths[d.mount.Path]; used {
			return refuse(http.StatusBadRequest, "STO063", d.mount.Path+" is chosen twice (as "+other+" and as "+d.role+"): each disk can be used once.")
		}
		usedPaths[d.mount.Path] = d.role
		data = append(data, d)
	}
	for i, path := range c.Parity {
		role := "parity disk " + strconv.Itoa(i+1)
		if other, used := usedPaths[strings.TrimSpace(path)]; used && strings.HasPrefix(other, "data") {
			return refuse(http.StatusBadRequest, "STO058", path+" is a data disk ("+other+"): a parity disk cannot also hold data.")
		}
		d, err := lookup(role, path)
		if err != nil {
			return err
		}
		if other, used := usedPaths[d.mount.Path]; used {
			return refuse(http.StatusBadRequest, "STO063", d.mount.Path+" is chosen twice (as "+other+" and as "+d.role+"): each disk can be used once.")
		}
		usedPaths[d.mount.Path] = d.role
		parity = append(parity, d)
	}

	// each disk on a physical disk of its own
	physical := func(d disk) string {
		if d.mount.Disk != "" {
			return d.mount.Disk
		}
		if d.mount.Device != "" {
			return d.mount.Device
		}
		return d.mount.Path
	}
	all := append(append([]disk{}, data...), parity...)
	for i := range all {
		for j := 0; j < i; j++ {
			if physical(all[i]) == physical(all[j]) {
				return refuse(http.StatusBadRequest, "STO056", all[i].mount.Path+" ("+all[i].role+") and "+all[j].mount.Path+" ("+all[j].role+") are on the same physical disk ("+cleanText(physical(all[i]), 100)+"): one failing disk would lose both. Choose disks on different drives.")
			}
		}
	}

	// every parity at least as large as the largest data disk
	size := func(d disk) uint64 {
		if d.mount.deviceSize > 0 {
			return uint64(d.mount.deviceSize)
		}
		return d.mount.Size
	}
	for _, p := range parity {
		for _, d := range data {
			if size(d) > size(p) {
				return refuse(http.StatusBadRequest, "STO057", p.mount.Path+" ("+p.role+") is smaller than "+d.mount.Path+" ("+d.role+"): a parity disk must be at least as large as the largest data disk.")
			}
		}
	}

	if err := snapRAIDNameFree(existing, c.Name, editName); err != nil {
		return err
	}
	if err := snapRAIDScheduleError(c.SyncCrontab); err != nil {
		return refuse(http.StatusBadRequest, "STO060", "The sync schedule \""+cleanText(c.SyncCrontab, 60)+"\" is not valid ("+cleanText(err.Error(), 120)+"): write it with seconds first, for example \"0 0 2 * * *\" for every day at 02:00.")
	}
	if err := snapRAIDScheduleError(c.ScrubCrontab); err != nil {
		return refuse(http.StatusBadRequest, "STO061", "The scrub schedule \""+cleanText(c.ScrubCrontab, 60)+"\" is not valid ("+cleanText(err.Error(), 120)+"): write it with seconds first, for example \"0 0 4 */2 * *\" for every second day at 04:00.")
	}
	return nil
}

// forkCheckSnapRAID is what CreateSnapRAID checks, with this server's mounts.
func forkCheckSnapRAID(c utils.SnapRAIDConfig, editName string) error {
	view, err := gatherStorage()
	if err != nil {
		return refuse(http.StatusInternalServerError, "STO064", "The disks of this server could not be read ("+cleanText(err.Error(), 300)+"), so nothing was saved. Try again.")
	}
	return checkSnapRAIDConfig(c, editName, existingSnapRAIDs(), view.Mounts)
}

// saveSnapRAIDConfig writes the configuration under the configuration lock,
// with the name checked again against what is on file.
func saveSnapRAIDConfig(c utils.SnapRAIDConfig, editName string) error {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()

	config := utils.ReadConfigFromFile()
	if err := snapRAIDNameFree(config.Storage.SnapRAIDs, c.Name, editName); err != nil {
		return err
	}
	if editName != "" {
		for i, r := range config.Storage.SnapRAIDs {
			if r.Name == editName {
				config.Storage.SnapRAIDs = append(config.Storage.SnapRAIDs[:i], config.Storage.SnapRAIDs[i+1:]...)
				break
			}
		}
	}
	config.Storage.SnapRAIDs = append(config.Storage.SnapRAIDs, c)
	utils.SetBaseMainConfig(config)
	return nil
}

var (
	snapRAIDReinitLock sync.Mutex
	snapRAIDReinits    sync.WaitGroup
)

// reinitSnapRAIDInBackground registers the jobs again after a toggle. In the
// background, one at a time: the scheduler waits for running jobs (a sync can
// take hours) before it takes the new list, and the switch must answer now.
func reinitSnapRAIDInBackground() {
	snapRAIDReinits.Add(1)
	go func() {
		defer snapRAIDReinits.Done()
		snapRAIDReinitLock.Lock()
		defer snapRAIDReinitLock.Unlock()
		InitSnapRAIDConfig()
	}()
}

// snapRAIDConfReady refuses to run an action of a configuration whose file
// was not written.
func snapRAIDConfReady(name string) error {
	if _, err := os.Stat(utils.CONFIGFOLDER + "snapraid/" + name + ".conf"); err != nil {
		return errors.New("its configuration file is missing: save the SnapRAID again")
	}
	return nil
}

// forkSnapRAIDActionFailed answers a sync, scrub, fix, enable or disable that
// did not start.
func forkSnapRAIDActionFailed(w http.ResponseWriter, action string, name string, err error) {
	utils.Error("SnapRAIDRun: "+cleanText(action, 10)+" "+cleanText(name, 40), err)
	if action == "enable" || action == "disable" {
		utils.HTTPError(w, "The SnapRAID \""+cleanText(name, 40)+"\" could not be "+action+"d ("+cleanText(err.Error(), 200)+"). Refresh the page and try again.", http.StatusInternalServerError, "STO071")
		return
	}
	utils.HTTPError(w, "The SnapRAID "+action+" of \""+cleanText(name, 40)+"\" could not be started ("+cleanText(err.Error(), 200)+"). Save the SnapRAID again to rebuild its jobs, then try again.", http.StatusInternalServerError, "STO070")
}
