package main

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "bufio"
    "strings"
    "syscall"

    "github.com/azukaar/cosmos-server/src/forkrelease" // Fork addition
    "github.com/azukaar/cosmos-server/src/utils"
)

type ReleaseAsset struct {
    Name        string `json:"name"`
    DownloadURL string `json:"browser_download_url"`
}

func (a *ReleaseAsset) UnmarshalJSON(data []byte) error {
    var raw struct {
        Name               string `json:"name"`
        BrowserDownloadURL string `json:"browser_download_url"`
        DownloadURL        string `json:"download_url"`
    }
    if err := json.Unmarshal(data, &raw); err != nil {
        return err
    }
    a.Name = raw.Name
    a.DownloadURL = raw.BrowserDownloadURL
    if a.DownloadURL == "" {
        a.DownloadURL = raw.DownloadURL
    }
    return nil
}

type Release struct {
    TagName string         `json:"tag_name"`
    PreRelease bool        `json:"prerelease"`
    Assets  []ReleaseAsset `json:"assets"`
}

type VersionInfo struct {
    Version     string
    AMDURL      string
    ARMURL      string
    AMDMD5      string
    ARMMD5      string
}

func parseMD5File(content string) string {
	// Read first line only (there should only be one anyway)
	scanner := bufio.NewScanner(strings.NewReader(content))
	if scanner.Scan() {
			// Split by whitespace and take first part (the hash)
			parts := strings.Fields(scanner.Text())
			if len(parts) > 0 {
					return parts[0]
			}
	}
	return ""
}

// Fork addition: the GitHub repository whose releases this build updates from
// (upstream: azukaar/cosmos-server). The default is the fork itself, so that a
// build made by hand never replaces itself with an upstream release; release
// builds also set it with -ldflags "-X main.updateRepo=owner/name". This file
// is compiled into the launcher too, which picks up the same value.
var updateRepo = "CristianCasapu/Cosmos-Server"

// Fork addition: fork releases are named "<upstream version>-fork.<build>",
// e.g. 0.24.0-fork.7. The build number counts every fork release and never
// restarts, so it alone orders two fork releases: a higher build is newer even
// when it is based on an older upstream version (that is how a release is
// rolled back). Anything else is compared as a plain semantic version.
func forkBuild(version string) (int, bool) {
    i := strings.LastIndex(version, "-fork.")
    if i < 0 {
        return 0, false
    }
    digits := version[i+len("-fork."):]
    if digits == "" || len(digits) > 9 {
        return 0, false
    }
    build := 0
    for _, c := range digits {
        if c < '0' || c > '9' {
            return 0, false
        }
        build = build*10 + int(c-'0')
    }
    return build, true
}

// CompareVersions returns -1 when candidate is newer than current, 1 when it
// is older and 0 when they are the same release.
func CompareVersions(current string, candidate string) (int, error) {
    currentBuild, currentIsFork := forkBuild(current)
    candidateBuild, candidateIsFork := forkBuild(candidate)
    if currentIsFork && candidateIsFork {
        switch {
        case currentBuild < candidateBuild:
            return -1, nil
        case currentBuild > candidateBuild:
            return 1, nil
        }
        return 0, nil
    }
    return utils.CompareSemver(current, candidate)
}

func GetLatestVersion(includeBeta bool) (*VersionInfo, error) {
    // Fetch releases from GitHub API

    updateURL := "https://api.github.com/repos/" + updateRepo + "/releases"
    if utils.IsPro() {
        updateURL = "https://api.cosmos-cloud.io/proupdates"
    }

    // Fork addition: the fork's release list is read from GitHub only, with time and size limits, see forkrelease/fetch.go (upstream: http.Get and io.ReadAll)
    body, err := forkrelease.GetFeed(updateURL, utils.IsPro(), forkrelease.MaxReleaseList)
    if err != nil {
        return nil, fmt.Errorf("failed to fetch releases: %v", err)
    }

    var releases []Release
    if utils.IsPro() {
        var wrapper struct {
            Releases []Release `json:"releases"`
        }
        if err := json.Unmarshal(body, &wrapper); err != nil {
            return nil, fmt.Errorf("failed to parse JSON: %v", err)
        }
        releases = wrapper.Releases
    } else {
        if err := json.Unmarshal(body, &releases); err != nil {
            return nil, fmt.Errorf("failed to parse JSON: %v", err)
        }
    }

    // Find latest release that matches criteria
    // Fork addition: take the newest release by version instead of the first
    // one listed, so the choice does not depend on the order GitHub returns.
    var latestRelease *Release
    for i := range releases {
        release := &releases[i]
        if !includeBeta && release.PreRelease {
            continue
        }
        if latestRelease == nil {
            latestRelease = release
            continue
        }
        if cmp, err := CompareVersions(latestRelease.TagName, release.TagName); err == nil && cmp == -1 {
            latestRelease = release
        }
    }

    if latestRelease == nil {
        return nil, nil
    }

    // Initialize version info
    info := &VersionInfo{
        Version: latestRelease.TagName,
    }

    // Parse assets to find URLs and MD5s
    for _, asset := range latestRelease.Assets {
        name := strings.ToLower(asset.Name)
        
        // Match AMD64 binary
        if strings.Contains(name, "amd64") && !strings.HasSuffix(name, ".md5") && !strings.Contains(name, "terraform") {
            info.AMDURL = asset.DownloadURL
        }
        // Match ARM64 binary
        if strings.Contains(name, "arm64") && !strings.HasSuffix(name, ".md5") && !strings.Contains(name, "terraform") {
            info.ARMURL = asset.DownloadURL
        }
        // Match MD5 files
        if strings.HasSuffix(name, ".md5") {
            // Fetch MD5 content
            // Fork addition: as above (upstream: http.Get and io.ReadAll)
            md5Content, err := forkrelease.GetFeed(asset.DownloadURL, utils.IsPro(), forkrelease.MaxChecksumFile)
            if err != nil {
                continue
            }
            
            md5String := strings.TrimSpace(string(md5Content))
            
            if strings.Contains(name, "amd64") && !strings.Contains(name, "terraform") {
                info.AMDMD5 = parseMD5File(md5String)
            }
            if strings.Contains(name, "arm64") && !strings.Contains(name, "terraform") {
                info.ARMMD5 = parseMD5File(md5String)
            }
        }
    }

    return info, nil
}

func cleanUpUpdateFiles() {
    execPath, err := os.Executable()
    if err != nil {
        return
    }
    
    currentFolder := filepath.Dir(execPath)

    dlPath := currentFolder + "/cosmos-update.zip"

    if _, err := os.Stat(dlPath); err == nil {
        os.Remove(dlPath)
    }

    // if cosmos-laumcher.updated exists, rename it to cosmos-launcher
    updatedPath := currentFolder + "/cosmos-launcher.updated"
    if _, err := os.Stat(updatedPath); err == nil {
        // get old permissions
        var perms os.FileMode
        if info, err := os.Stat(currentFolder + "/cosmos-launcher"); err == nil {
            perms = info.Mode()
        } else {
            fmt.Println("Update: Failed to get old permissions:", err)
        }
        // get old owner
        var owner int
        if info, err := os.Stat(currentFolder + "/cosmos-launcher"); err == nil {
            owner = int(info.Sys().(*syscall.Stat_t).Uid)
        } else {
            fmt.Println("Update: Failed to get old owner:", err)
        }

        err := os.Rename(updatedPath, currentFolder + "/cosmos-launcher")
        if err != nil {
            fmt.Println("Update: Failed to rename cosmos-launcher.updated:", err)
        }

        // set old permissions
        if perms != 0 {
            err = os.Chmod(currentFolder + "/cosmos-launcher", perms)
            if err != nil {
                fmt.Println("Update: Failed to set old permissions:", err)
            }
        }
        // set old owner
        if owner != 0 {
            err = os.Chown(currentFolder + "/cosmos-launcher", owner, -1)
            if err != nil {
                fmt.Println("Update: Failed to set old owner:", err)
            }
        }
    }
}