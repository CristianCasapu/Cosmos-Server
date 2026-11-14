package storage 

import (
	"os"
	"errors"
	// "strings" Fork addition: no longer used once the checks moved to fork_snapraid.go
	"strconv"
	
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/azukaar/cosmos-server/src/cron"
)

func findParent(disks []BlockDevice, path string) (BlockDevice, error) {
	for _, d := range disks {
		if d.MountPoint == path {
			return d, nil
		}
		if len(d.Children) > 0 {
			if p, err := findParent(d.Children, path); err == nil {
				return p, nil
			}
		}
	}
	return BlockDevice{}, errors.New("Not found")
}

// Create a SnapRAID configuration
func CreateSnapRAID(raidOptions utils.SnapRAIDConfig, editRaid string) error {
	// Fork addition: upstream's checks (a list of refused name characters, data and parity
	// found with findParent, which matches "" too) are replaced by fork_snapraid.go's, on the
	// listed data mounts: names, empty rows, counts, one physical disk each, parity size,
	// a rename onto another name, the schedules
	if err := forkCheckSnapRAID(raidOptions, editRaid); err != nil {
		return err
	}

	// Fork addition: saved under ConfigLock, the name checked again against the file (fork_snapraid.go)
	if err := saveSnapRAID(raidOptions, editRaid); err != nil {
		return err
	}

	utils.TriggerEvent(
		"cosmos.settings",
		"Settings updated",
		"success",
		"",
		map[string]interface{}{
			"from": "SnapRAID configuration created",
	})

	InitSnapRAIDConfig()

	return nil
}

func DeleteSnapRAID(name string) error {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()

	os.Remove(utils.CONFIGFOLDER + "snapraid/" + name + ".conf")
	
	
	config := utils.ReadConfigFromFile()
	for i, r := range config.Storage.SnapRAIDs {
		if r.Name == name {
			// remove the snapraid.content and snapraid.parity files, and lock fiels
			for _, d := range r.Data {
				os.Remove(d + "/snapraid.content")
				os.Remove(d + "/snapraid.parity")
				os.Remove(d + "/snapraid.content.lock")
				os.Remove(d + "/snapraid.parity.lock")
			}

			config.Storage.SnapRAIDs = append(config.Storage.SnapRAIDs[:i], config.Storage.SnapRAIDs[i+1:]...)
			utils.SetBaseMainConfig(config)
			utils.Log("Deleted SnapRAID " + name)
			return nil
		}
	}
	return errors.New("SnapRAID not found")
}

func InitSnapRAIDConfig() {
	config := utils.GetMainConfig()
	snaps := config.Storage.SnapRAIDs

	// reset the scheduler
	cron.ResetScheduler("SnapRAID")
	
	// remove the folder if it exists
	if _, err := os.Stat(utils.CONFIGFOLDER + "snapraid"); err == nil {
		os.RemoveAll(utils.CONFIGFOLDER + "snapraid")
	}

	os.MkdirAll(utils.CONFIGFOLDER + "snapraid", 0700)

	for _, raidOptions := range snaps {
		// create or overwrite the file
		file, err := os.OpenFile(utils.CONFIGFOLDER + "snapraid/" + raidOptions.Name + ".conf", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			utils.MajorError("Failed to create SnapRAID config file", err)
			return
		}
		defer file.Close()

		// write the configuration
		for _di, d := range raidOptions.Parity {
			di := strconv.Itoa(_di + 1)
			if _di == 0 {
				file.WriteString("parity " + d + "/snapraid.parity\n")
			} else {
				file.WriteString(di + "-parity " + d + "/snapraid."+di+"-parity\n")
			}
		}

		// file.WriteString("content " + utils.CONFIGFOLDER + "snapraid/" + raidOptions.Name + ".conf\n")
		
		for name, d := range raidOptions.Data {
			file.WriteString("content " + d + "/snapraid.content\n")
			file.WriteString("data " + name + " " + d + "\n")
		}
		
		// Init scheduler
		
		cron.RegisterJob(cron.ConfigJob{
			Scheduler: "SnapRAID",
			Name: "SnapRAID sync " + raidOptions.Name,
			Crontab: raidOptions.SyncCrontab,
			Disabled: !raidOptions.Enabled, // Fork addition: "disable" stops the schedule
			Cancellable: true,
			Job: cron.JobFromCommand("snapraid", "-q", "sync", "-c", utils.CONFIGFOLDER + "snapraid/" + raidOptions.Name + ".conf"),
		})
		
		cron.RegisterJob(cron.ConfigJob{
			Scheduler: "SnapRAID",
			Name: "SnapRAID scrub " + raidOptions.Name,
			Crontab: raidOptions.ScrubCrontab,
			Disabled: !raidOptions.Enabled, // Fork addition: "disable" stops the schedule
			Cancellable: true,
			Job: cron.JobFromCommand("snapraid", "-q", "scrub", "-c", utils.CONFIGFOLDER + "snapraid/" + raidOptions.Name + ".conf"),
		})
	}	
}

func ToggleSnapRAID(name string, enable bool) error {
	toggled := false
	defer func() { if toggled { reinitSnapRAIDInBackground() } }() // Fork addition: the jobs follow the switch, re-registered once the lock is released (fork_snapraid.go)
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()

	config := utils.ReadConfigFromFile()
	for i, r := range config.Storage.SnapRAIDs {
		if r.Name == name {
			config.Storage.SnapRAIDs[i].Enabled = enable
			utils.SetBaseMainConfig(config)
			utils.Log("Toggled SnapRAID " + name)
			toggled = true // Fork addition
			return nil
		}
	}
	return errors.New("SnapRAID not found")
}

func RunSnapRAIDSync(raid utils.SnapRAIDConfig) error {
	utils.Log("[STORAGE] Running SnapRAID sync for " + raid.Name)
	return cron.ManualRunJob("SnapRAID", "SnapRAID sync " + raid.Name) // Fork addition: its error is returned
}

func RunSnapRAIDFix(raid utils.SnapRAIDConfig) error {
	utils.Log("[STORAGE] Running SnapRAID fix for " + raid.Name)
	if err := snapRAIDConfReady(raid.Name); err != nil { return err } // Fork addition: say why it cannot run

	cron.RunOneTimeJob(cron.ConfigJob{
		Scheduler: "SnapRAID",
		Name: "SnapRAID fix " + raid.Name,
		Cancellable: true,
		Job: cron.JobFromCommand("snapraid", "fix", "-c", utils.CONFIGFOLDER + "snapraid/" + raid.Name + ".conf"),
	})

	return nil
}

func RunSnapRAIDScrub(raid utils.SnapRAIDConfig) error  {
	utils.Log("[STORAGE] Running SnapRAID scrub for " + raid.Name)
	return cron.ManualRunJob("SnapRAID", "SnapRAID scrub " + raid.Name) // Fork addition: its error is returned
}

func RunSnapRAIDStatus(raid utils.SnapRAIDConfig) (string, error)  {
	out, err := utils.Exec("snapraid", "status", "-c", utils.CONFIGFOLDER + "snapraid/" + raid.Name + ".conf")

	if err != nil {
		utils.MajorError("Failed to status " + raid.Name, err)
		return "", err
	}

	return out, nil
}