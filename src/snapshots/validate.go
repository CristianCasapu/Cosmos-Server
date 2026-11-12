package snapshots

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxSources    = 50
	maxPathLength = 1024
	maxSlugLength = 200 // a folder name can be 255 bytes long; ".partial" must fit too
	maxKeep       = 365
)

// cleanDirectory checks that value can name a folder of the host and returns
// it in its plain form (no doubled or trailing slashes).
func cleanDirectory(label string, value string) (string, error) {
	if len(value) > maxPathLength {
		return "", fmt.Errorf("%s: the path is too long", label)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s: the path contains characters that are not allowed", label)
		}
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s: the path starts or ends with a space", label)
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%s: enter an absolute path, starting with /", label)
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s: the path must not contain \"..\"", label)
		}
	}
	clean := filepath.Clean(value)
	if clean == "/" {
		return "", fmt.Errorf("%s: choose a folder, not the root of the disk", label)
	}
	return clean, nil
}

// inside tells whether dir lies inside parent (and is not parent itself).
// Both are clean absolute paths.
func inside(dir string, parent string) bool {
	return strings.HasPrefix(dir, strings.TrimSuffix(parent, "/")+"/")
}

// configFolder is the folder Cosmos keeps its own files in, as a clean
// absolute path; "" when it cannot be told.
func configFolder() string {
	folder, err := filepath.Abs(utils.CONFIGFOLDER)
	if err != nil || utils.CONFIGFOLDER == "" {
		return ""
	}
	return filepath.Clean(folder)
}

// parseTime reads "HH:MM" (24 hours); ok is false for anything else.
func parseTime(value string) (hour int, minute int, ok bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if value[i] < '0' || value[i] > '9' {
			return 0, 0, false
		}
	}
	hour, _ = strconv.Atoi(value[:2])
	minute, _ = strconv.Atoi(value[3:])
	if hour > 23 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// ValidateConfig checks the snapshot settings and stores the paths in their
// plain form. It is called when the settings are saved, so that a
// configuration that cannot work, or that would be dangerous, is refused
// with the reason; and again before every run.
func ValidateConfig(cfg *utils.SnapshotsConfig) error {
	const label = "Incremental backup"

	destination := ""
	if cfg.Destination != "" {
		var err error
		destination, err = cleanDirectory(label+", destination", cfg.Destination)
		if err != nil {
			return err
		}
		// Cosmos resets the modes of everything in its own folder at every
		// start, which would change the snapshots.
		if own := configFolder(); own != "" && !utils.IsInsideContainer && (destination == own || inside(destination, own)) {
			return fmt.Errorf("%s: the destination cannot be Cosmos' own folder (%s) or a folder in it", label, own)
		}
	}

	if len(cfg.Sources) > maxSources {
		return fmt.Errorf("%s: at most %d folders can be backed up", label, maxSources)
	}
	// a new list: the one in cfg may be shared with the running configuration
	sources := make([]string, 0, len(cfg.Sources))
	for _, value := range cfg.Sources {
		if value == "" {
			return fmt.Errorf("%s: one of the folders to back up is empty", label)
		}
		source, err := cleanDirectory(label+", folder "+strconv.Quote(value), value)
		if err != nil {
			return err
		}
		for _, other := range sources {
			if other == source {
				return fmt.Errorf("%s: %s is listed twice", label, source)
			}
		}
		if len(Slug(source)) > maxSlugLength {
			return fmt.Errorf("%s: the path %s is too long to be backed up", label, source)
		}
		if destination != "" {
			if source == destination {
				return fmt.Errorf("%s: the destination %s cannot be one of the folders that are backed up", label, destination)
			}
			if inside(source, destination) {
				return fmt.Errorf("%s: %s lies inside the destination and cannot be backed up", label, source)
			}
		}
		sources = append(sources, source)
	}

	if cfg.Time != "" {
		if _, _, ok := parseTime(cfg.Time); !ok {
			return fmt.Errorf("%s: enter the time as HH:MM, for example 03:00", label)
		}
	}
	switch cfg.Frequency {
	case "", "6h", "12h", "daily", "weekly":
	default:
		return fmt.Errorf("%s: the frequency must be 6h, 12h, daily or weekly", label)
	}
	if cfg.Weekday < 0 || cfg.Weekday > 6 {
		return fmt.Errorf("%s: the day of the week must be between 0 (Sunday) and 6 (Saturday)", label)
	}
	for _, keep := range []int{cfg.KeepDaily, cfg.KeepWeekly, cfg.KeepMonthly} {
		if keep < 0 || keep > maxKeep {
			return fmt.Errorf("%s: the number of snapshots to keep must be between 0 and %d", label, maxKeep)
		}
	}

	if cfg.Enabled {
		if destination == "" {
			return errors.New(label + ": choose the folder the snapshots are stored in")
		}
		if len(sources) == 0 {
			return errors.New(label + ": add at least one folder to back up")
		}
	}

	cfg.Destination = destination
	cfg.Sources = sources
	return nil
}
