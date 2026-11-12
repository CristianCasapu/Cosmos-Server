package main

import (
	"io/ioutil"
	"hash/fnv"
	"runtime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Masterminds/semver"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/azukaar/cosmos-server/src/storage"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/cron"
	

	"github.com/jasonlvhit/gocron"
)

type Version struct {
	Version string `json:"version"`
}

type UpdateCheck struct {
	Latest string `json:"latest"`
}

func GetCosmosVersion() string {
	ex, err := os.Executable()
	if err != nil {
			panic(err)
	}
	exPath := filepath.Dir(ex)

	pjs, errPR := os.Open(exPath + "/meta.json")
	if errPR != nil {
		utils.Error("checkVersion", errPR)
		return ""
	}

	packageJson, _ := ioutil.ReadAll(pjs)

	utils.Debug("checkVersion" + string(packageJson))

	var version Version
	errJ := json.Unmarshal(packageJson, &version)
	if errJ != nil {
		utils.Error("checkVersion", errJ)
		return ""
	}

	return version.Version

}

func checkVersion() {
	utils.NewVersionAvailable = false

	myVersion := GetCosmosVersion()
	if myVersion == "" {
		utils.Error("checkVersion - Could not get version", nil)
		return
	}

	response, err := http.Get(fmt.Sprintf(
		"https://cosmos-cloud.io/update-check/cosmos/server?c=%d&version=%s",
		time.Now().Unix(),
		neturl.QueryEscape(myVersion),
	))
	if err != nil {
		utils.Error("checkVersion", err)
		return
	}

	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		utils.Error("checkVersion", err)
		return
	}

	var check UpdateCheck
	if errJ := json.Unmarshal(body, &check); errJ != nil {
		utils.Error("checkVersion", errJ)
		return
	}

	if check.Latest == "" {
		utils.Error("checkVersion - No latest version in response", nil)
		return
	}

	current, errc := semver.NewVersion(myVersion)
	if errc != nil {
		utils.Error("checkVersion " + myVersion, errc)
		return
	}

	latest, errl := semver.NewVersion(check.Latest)
	if errl != nil {
		utils.Error("checkVersion " + check.Latest, errl)
		return
	}

	// never notify about a prerelease/unstable build unless the user opted into beta updates
	if latest.Prerelease() != "" && !utils.GetMainConfig().BetaUpdates {
		utils.Log("Latest version " + check.Latest + " is a prerelease, ignoring (not on beta updates)")
		return
	}

	// only notify on a major or minor bump, ignore patches
	if current.Compare(latest) == -1 && (current.Major() != latest.Major() || current.Minor() != latest.Minor()) {
		utils.Log("New version available: " + check.Latest)
		utils.NewVersionAvailable = true
	} else {
		utils.Log("No new version available")
	}
}

// ServerUpdateCheck is the outcome of comparing the running server against the release feed.
type ServerUpdateCheck struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Containerized   bool   `json:"containerized"`
}

// findServerUpdate queries the release feed and compares it against the running version.
// It never installs anything.
func findServerUpdate(useBeta bool) (ServerUpdateCheck, *VersionInfo, error) {
	check := ServerUpdateCheck{
		CurrentVersion: GetCosmosVersion(),
		Containerized:  utils.IsInsideContainer,
	}

	updates, err := GetLatestVersion(useBeta)
	if err != nil {
		return check, nil, err
	}
	if updates == nil {
		return check, nil, nil
	}

	check.LatestVersion = updates.Version

	// Fork addition: fork releases are ordered by their build number.
	cp, err := CompareVersions(check.CurrentVersion, updates.Version)
	if err != nil {
		return check, nil, err
	}

	check.UpdateAvailable = cp == -1
	return check, updates, nil
}

// applyServerUpdate downloads the release next to the binary and exits so the launcher installs it.
func applyServerUpdate(updates *VersionInfo, useBeta bool) error {
	url := updates.AMDURL
	if runtime.GOARCH == "arm64" {
		url = updates.ARMURL
	}

	utils.Log("Downloading update from " + url)

	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	currentFolder := filepath.Dir(execPath)

	dlPath := currentFolder + "/cosmos-update.zip"
	betaFile := currentFolder + "/.BETA"

	if err := utils.DownloadFileToLocation(dlPath, url); err != nil {
		return err
	}

	if useBeta && !utils.FileExists(betaFile) {
		utils.Log("Saving BETA file")
		if err := ioutil.WriteFile(betaFile, []byte("BETA"), 0600); err != nil {
			return err
		}
	} else if !useBeta && utils.FileExists(betaFile) {
		utils.Log("Removing BETA file")
		if err := os.Remove(betaFile); err != nil {
			return err
		}
	}

	utils.PlanRestart()
	cron.WaitForAllJobs() // wait for all jobs to finish

	utils.Log("Update downloaded, restarting server")
	storage.StopAllRCloneProcess(true)
	utils.RevertHostDNS()
	os.Exit(0)
	return nil
}

// checkUpdatesAvailable is the scheduled check: containers always, the server binary only
// when auto-update is enabled and we are not containerized.
func checkUpdatesAvailable() {
	utils.UpdateAvailable = docker.CheckUpdatesAvailable()

	if utils.IsInsideContainer || !utils.GetMainConfig().AutoUpdate {
		return
	}

	useBeta := utils.GetMainConfig().BetaUpdates

	check, updates, err := findServerUpdate(useBeta)
	if err != nil {
		utils.Error("checkUpdatesAvailable", err)
		return
	}

	if !check.UpdateAvailable {
		utils.Log("No new version available")
		return
	}

	utils.Log("New version available: " + check.LatestVersion)

	if err := applyServerUpdate(updates, useBeta); err != nil {
		utils.Error("checkUpdatesAvailable", err)
	}
}

func checkCerts() {
	config := utils.GetMainConfig()
	HTTPConfig := config.HTTPConfig

	if !utils.ServerHTTPSDisabled(HTTPConfig) {
		utils.Log("Checking certificates for renewal")
		if !CertificateIsExpiredSoon(HTTPConfig.TLSValidUntil) {
			utils.Log("Certificates are not valid anymore, renewing")
			RestartHTTPServer()
		} else if HTTPConfig.HTTPSCertificateMode == utils.HTTPSCertModeList["LETSENCRYPT"] && len(utils.LocalCertsToIssue(config, utils.LocalCertHostnamesNow(config), false)) > 0 {
			utils.Log("Certificates need a refresh, renewing")
			RestartHTTPServer()
		} else if utils.IsZoneIssuer() && len(utils.ZoneCertsToIssue(config, false)) > 0 {
			utils.Log("Zone certificates need a refresh, renewing")
			RestartHTTPServer()
		}
	} else if config.ConstellationConfig.Enabled && utils.IsZoneIssuer() && len(utils.ZoneCertsToIssue(config, false)) > 0 {
		// an HTTP-only leader serves no certificate but still issues the ones of its cluster
		utils.Log("Zone certificates need a refresh, renewing")
		utils.RefreshZoneCerts()
	}
}

func runSnapRAIDSync(snap utils.SnapRAIDConfig) {
	err := storage.RunSnapRAIDSync(snap)
	if err != nil {
		utils.Error("runSnapRAIDSync", err)
	}
}

func runSnapRAIDScrub(snap utils.SnapRAIDConfig) {
	err := storage.RunSnapRAIDScrub(snap)
	if err != nil {
		utils.Error("runSnapRAIDScrub", err)
	}
}

func CRON() {
	go func() {
		// TODO: change to new CRON executor, wth customizable maintenance schedules
		
		s := gocron.NewScheduler()
		// Fork addition: on the schedule set in the settings (upstream: Every(2).Hours()).
		s.Every(1).Minute().Do(func() {
			if backupDue(time.Now()) {
				go RunBackup()
			}
		})
		s.Every(1).Hours().Do(utils.CleanBannedIPs)
		s.Every(1).Hours().Do(proxy.CleanUp)
		s.Every(1).Hours().Do(proxy.CleanUpSocket)
		s.Every(1).Hours().Do(docker.CleanupExitedDeploymentContainers)
		// Fork addition: at the configured time (upstream: always At("2:00")),
		// asked every minute so a changed time needs no restart.
		s.Every(1).Minute().Do(func() {
			if !maintenanceDue(time.Now()) {
				return
			}
			utils.RunDatabaseRetention()
			imageCleanUp()
			checkCerts()
			checkUpdatesAvailable()
		})
		
		hostname, _ := os.Hostname()
		h := fnv.New32a()
		h.Write([]byte(hostname))
		randomHour := int(h.Sum32()%23) + 1
		s.Every(1).Day().At(fmt.Sprintf("%02d:45", randomHour)).Do(utils.ProcessLicence)
		// Fork addition: the new-version notice comes from this fork's releases
		// (upstream: checkVersion, once a day, against its own version service).
		startUpdateChecks()

		s.Start()
	}()
}