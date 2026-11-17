package protection

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Whether the system installs its security updates by itself: Debian's
// unattended-upgrades, switched on in APT's configuration and run by
// systemd's apt-daily-upgrade timer.

// Paths of the automatic updates, variables for the tests.
var (
	upgradeBinary = "/usr/bin/unattended-upgrade"
	aptConfigDir  = "/etc/apt/apt.conf.d"
	upgradeTimer  = "/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer"
)

const (
	// maxAptFiles and maxAptFile bound the scan of APT's configuration.
	maxAptFiles = 100
	maxAptFile  = 64 << 10
)

// apt reads the files of its parts folder whose names are letters, digits,
// "_", "-" and "." and that have no extension or ".conf" (apt.conf(5)).
var aptPartName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// the assignment looked for, on a line of its own
var unattendedLine = regexp.MustCompile(`^APT::Periodic::Unattended-Upgrade\s+"([^"]*)"\s*;`)

// aptSetting is the effective value of APT::Periodic::Unattended-Upgrade.
type aptSetting struct {
	// Value of the last assignment ("" when there is none).
	Value string
	// File holding it, and its modification time.
	File    string
	Changed time.Time
	// Incomplete: more files than are read, or a file larger than is read
	// (it was skipped).
	Incomplete bool
}

// readAptSetting scans the regular files of APT's configuration folder in
// lexical order; the last assignment wins.
func readAptSetting() aptSetting {
	setting := aptSetting{}
	entries, err := os.ReadDir(aptConfigDir) // sorted by name
	if err != nil {
		return setting
	}
	read := 0
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !aptPartName.MatchString(name) {
			continue
		}
		if ext := filepath.Ext(name); ext != "" && ext != ".conf" {
			continue
		}
		if read >= maxAptFiles {
			setting.Incomplete = true
			break
		}
		read++
		path := filepath.Join(aptConfigDir, name)
		value, found, ok, changed := scanAptFile(path)
		if !ok {
			setting.Incomplete = true
			continue
		}
		if found {
			setting.Value, setting.File, setting.Changed = value, path, changed
		}
	}
	return setting
}

// scanAptFile returns the last value assigned in one file. ok is false when
// the file could not be read whole.
func scanAptFile(path string) (value string, found bool, ok bool, changed time.Time) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, false, time.Time{}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", false, false, time.Time{}
	}
	content, err := io.ReadAll(io.LimitReader(file, maxAptFile+1))
	if err != nil || len(content) > maxAptFile {
		return "", false, false, time.Time{}
	}
	value, found = lastAssignment(content)
	return value, found, true, info.ModTime().UTC()
}

// lastAssignment returns the value of the last assignment in content. The
// match is anchored at the start of the (trimmed) line, so comment lines
// (// or #) never match.
func lastAssignment(content []byte) (value string, found bool) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 4096), maxAptFile+1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := unattendedLine.FindStringSubmatch(line); m != nil {
			value, found = m[1], true
		}
	}
	return value, found
}

// exists reports whether something is at path (a link counts, even when
// broken: systemd's wants folder holds links).
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}
