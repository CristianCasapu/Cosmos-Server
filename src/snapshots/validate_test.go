package snapshots

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestValidateConfigAccepts(t *testing.T) {
	cases := []struct {
		name        string
		cfg         utils.SnapshotsConfig
		destination string
		sources     []string
	}{
		{"nothing set", utils.SnapshotsConfig{}, "", []string{}},
		{
			"enabled, paths stored in their plain form",
			utils.SnapshotsConfig{Enabled: true, Destination: "/storage//snapshots/", Sources: []string{"/var/lib/cosmos/", "/storage/./apps", "/with space/and.dots"}},
			"/storage/snapshots", []string{"/var/lib/cosmos", "/storage/apps", "/with space/and.dots"},
		},
		{
			"off, but what is filled in is still checked and cleaned",
			utils.SnapshotsConfig{Destination: "/storage/snapshots/", Sources: []string{"/data/"}},
			"/storage/snapshots", []string{"/data"},
		},
		{
			"the destination inside a folder that is backed up",
			utils.SnapshotsConfig{Enabled: true, Destination: "/storage/apps/snapshots", Sources: []string{"/storage/apps"}},
			"/storage/apps/snapshots", []string{"/storage/apps"},
		},
		{
			"a folder whose name only starts like the destination",
			utils.SnapshotsConfig{Enabled: true, Destination: "/storage/snap", Sources: []string{"/storage/snapshots-of-mine", "/storage"}},
			"/storage/snap", []string{"/storage/snapshots-of-mine", "/storage"},
		},
		{
			"folders inside each other",
			utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/var/lib", "/var/lib/docker/volumes"}},
			"/backups", []string{"/var/lib", "/var/lib/docker/volumes"},
		},
		{
			"every setting at its limit",
			utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/data"}, Frequency: "weekly", Time: "23:59", Weekday: 6, KeepRecent: 365, KeepDaily: 365, KeepWeekly: 365, KeepMonthly: 365},
			"/backups", []string{"/data"},
		},
		{
			"next to Cosmos' own folder, not in it",
			utils.SnapshotsConfig{Enabled: true, Destination: "/var/lib/cosmos-snapshots", Sources: []string{"/var/lib/cosmos"}},
			"/var/lib/cosmos-snapshots", []string{"/var/lib/cosmos"},
		},
	}
	for _, c := range cases {
		cfg := c.cfg
		if err := ValidateConfig(&cfg); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if cfg.Destination != c.destination || !reflect.DeepEqual(cfg.Sources, c.sources) {
			t.Errorf("%s: stored %q and %q, want %q and %q", c.name, cfg.Destination, cfg.Sources, c.destination, c.sources)
		}
	}

	for _, frequency := range []string{"", "6h", "12h", "daily", "weekly"} {
		cfg := utils.SnapshotsConfig{Frequency: frequency, Time: "03:00"}
		if err := ValidateConfig(&cfg); err != nil {
			t.Errorf("frequency %q: %v", frequency, err)
		}
	}

	// the list that was passed in is not written to: it may belong to the running configuration
	shared := []string{"/data//"}
	cfg := utils.SnapshotsConfig{Sources: shared}
	if err := ValidateConfig(&cfg); err != nil || cfg.Sources[0] != "/data" {
		t.Fatalf("cleaning: %q, %v", cfg.Sources, err)
	}
	if shared[0] != "/data//" {
		t.Error("the caller's list was changed in place")
	}
}

func TestValidateConfigRefuses(t *testing.T) {
	good := func() utils.SnapshotsConfig {
		return utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/data"}}
	}
	many := []string{}
	for i := 0; i <= maxSources; i++ {
		many = append(many, fmt.Sprintf("/data/%d", i))
	}

	cases := map[string]func(cfg *utils.SnapshotsConfig){
		"enabled without a destination":              func(cfg *utils.SnapshotsConfig) { cfg.Destination = "" },
		"enabled without a folder":                   func(cfg *utils.SnapshotsConfig) { cfg.Sources = nil },
		"enabled with an empty list":                 func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{} },
		"relative destination":                       func(cfg *utils.SnapshotsConfig) { cfg.Destination = "backups" },
		"destination ./":                             func(cfg *utils.SnapshotsConfig) { cfg.Destination = "./backups" },
		"destination is the root":                    func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/" },
		"destination is the root, disguised":         func(cfg *utils.SnapshotsConfig) { cfg.Destination = "//./" },
		"destination with ..":                        func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/backups/../etc" },
		"destination ending in ..":                   func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/backups/.." },
		"destination with a newline":                 func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/back\nups" },
		"destination with a NUL":                     func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/backups\x00" },
		"destination with an escape":                 func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/back\x1bups" },
		"destination with a DEL":                     func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/back\x7fups" },
		"destination with a space in front":          func(cfg *utils.SnapshotsConfig) { cfg.Destination = " /backups" },
		"destination with a space behind":            func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/backups " },
		"destination too long":                       func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/" + strings.Repeat("a/", maxPathLength) },
		"destination is Cosmos' own folder":          func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/var/lib/cosmos" },
		"destination inside Cosmos' folder":          func(cfg *utils.SnapshotsConfig) { cfg.Destination = "/var/lib/cosmos/snapshots" },
		"relative folder":                            func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"data"} },
		"empty folder":                               func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/data", ""} },
		"the root as a folder":                       func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/"} },
		"folder with ..":                             func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/data/../etc"} },
		"folder with a tab":                          func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/da\tta"} },
		"folder with a carriage return":              func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/data\r"} },
		"the same folder twice":                      func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/data", "/other", "/data"} },
		"the same folder twice, written apart":       func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/data", "/data/"} },
		"the destination as a folder":                func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/data", "/backups"} },
		"the destination as a folder, written apart": func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/backups//"} },
		"a folder inside the destination":            func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/backups/var-lib-cosmos"} },
		"a path too long for a folder name":          func(cfg *utils.SnapshotsConfig) { cfg.Sources = []string{"/" + strings.Repeat("a", maxSlugLength+1)} },
		"too many folders":                           func(cfg *utils.SnapshotsConfig) { cfg.Sources = many },
		"time without a leading zero":                func(cfg *utils.SnapshotsConfig) { cfg.Time = "3:00" },
		"time past midnight":                         func(cfg *utils.SnapshotsConfig) { cfg.Time = "24:00" },
		"time with a minute 60":                      func(cfg *utils.SnapshotsConfig) { cfg.Time = "12:60" },
		"time with seconds":                          func(cfg *utils.SnapshotsConfig) { cfg.Time = "03:00:00" },
		"time in letters":                            func(cfg *utils.SnapshotsConfig) { cfg.Time = "ab:cd" },
		"time with a sign":                           func(cfg *utils.SnapshotsConfig) { cfg.Time = "-1:00" },
		"frequency of the other backup":              func(cfg *utils.SnapshotsConfig) { cfg.Frequency = "2h" },
		"unknown frequency":                          func(cfg *utils.SnapshotsConfig) { cfg.Frequency = "hourly" },
		"weekday below 0":                            func(cfg *utils.SnapshotsConfig) { cfg.Weekday = -1 },
		"weekday above 6":                            func(cfg *utils.SnapshotsConfig) { cfg.Weekday = 7 },
		"negative keep":                              func(cfg *utils.SnapshotsConfig) { cfg.KeepDaily = -1 },
		"negative number of recent ones":             func(cfg *utils.SnapshotsConfig) { cfg.KeepRecent = -1 },
		"keep too many recent ones":                  func(cfg *utils.SnapshotsConfig) { cfg.KeepRecent = 366 },
		"keep too many weeks":                        func(cfg *utils.SnapshotsConfig) { cfg.KeepWeekly = 366 },
		"keep too many months":                       func(cfg *utils.SnapshotsConfig) { cfg.KeepMonthly = 1000 },
	}
	for name, spoil := range cases {
		cfg := good()
		spoil(&cfg)
		err := ValidateConfig(&cfg)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		// the reason is shown to the admin and logged: one line, nothing raw from the input
		for _, r := range err.Error() {
			if r < 0x20 || r == 0x7f {
				t.Errorf("%s: the message contains a control character: %q", name, err.Error())
				break
			}
		}
	}
	cfg := good()
	if err := ValidateConfig(&cfg); err != nil {
		t.Fatalf("the configuration the cases start from is refused: %v", err)
	}

	// the same paths are refused when the module is off: nothing unusable is stored
	for _, off := range []utils.SnapshotsConfig{
		{Destination: "backups"},
		{Sources: []string{"../data"}},
		{Destination: "/backups", Sources: []string{"/backups/inside"}},
	} {
		if err := ValidateConfig(&off); err == nil {
			t.Errorf("accepted while off: %+v", off)
		}
	}
}

func TestParseTime(t *testing.T) {
	valid := map[string][2]int{"03:00": {3, 0}, "00:00": {0, 0}, "23:59": {23, 59}, "04:30": {4, 30}}
	for value, want := range valid {
		hour, minute, ok := parseTime(value)
		if !ok || hour != want[0] || minute != want[1] {
			t.Errorf("parseTime(%q) = %d, %d, %v", value, hour, minute, ok)
		}
	}
	for _, value := range []string{"", "3:00", "24:00", "12:60", "12-30", "ab:cd", "03:00:00", "-1:00", "+2:30", " 3:00"} {
		if _, _, ok := parseTime(value); ok {
			t.Errorf("parseTime(%q) must be rejected", value)
		}
	}
}

func TestScheduled(t *testing.T) {
	// 2026-11-15 is a Sunday, 2026-11-12 a Thursday
	on := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, time.Local) }
	runs := func(cfg utils.SnapshotsConfig, day int) (times []int) {
		for hour := 0; hour < 24; hour++ {
			for minute := 0; minute < 60; minute++ {
				if scheduled(cfg, on(day, hour, minute)) {
					times = append(times, hour*100+minute)
				}
			}
		}
		return times
	}
	with := func(change func(cfg *utils.SnapshotsConfig)) utils.SnapshotsConfig {
		cfg := utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/data"}}
		change(&cfg)
		return cfg
	}

	cases := []struct {
		name string
		cfg  utils.SnapshotsConfig
		day  int
		want []int
	}{
		{"default: daily at 03:00", with(func(cfg *utils.SnapshotsConfig) {}), 12, []int{300}},
		{"daily at 04:05", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency, cfg.Time = "daily", "04:05" }), 12, []int{405}},
		{"every six hours from 03:15", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency, cfg.Time = "6h", "03:15" }), 12, []int{315, 915, 1515, 2115}},
		{"every six hours, default time", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency = "6h" }), 12, []int{300, 900, 1500, 2100}},
		{"every twelve hours from 22:00", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency, cfg.Time = "12h", "22:00" }), 12, []int{1000, 2200}},
		{"weekly, Sunday, on a Sunday", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency, cfg.Time = "weekly", "03:00" }), 15, []int{300}},
		{"weekly, Sunday, on a Thursday", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency, cfg.Time = "weekly", "03:00" }), 12, nil},
		{"weekly, Thursday", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency, cfg.Time, cfg.Weekday = "weekly", "01:30", 4 }), 12, []int{130}},
		{"a broken time falls back to 03:00", with(func(cfg *utils.SnapshotsConfig) { cfg.Time = "25:99" }), 12, []int{300}},
		{"an unknown frequency means daily", with(func(cfg *utils.SnapshotsConfig) { cfg.Frequency = "sometimes" }), 12, []int{300}},
		{"off", with(func(cfg *utils.SnapshotsConfig) { cfg.Enabled = false }), 12, nil},
		{"no destination", with(func(cfg *utils.SnapshotsConfig) { cfg.Destination = "" }), 12, nil},
		{"no folder", with(func(cfg *utils.SnapshotsConfig) { cfg.Sources = nil }), 12, nil},
	}
	for _, c := range cases {
		if got := runs(c.cfg, c.day); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: runs at %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDueOncePerMinute(t *testing.T) {
	useConfig(t, utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/data"}, Frequency: "daily", Time: "04:05"})
	dueMu.Lock()
	lastDue = ""
	dueMu.Unlock()

	now := time.Date(2026, 11, 12, 4, 5, 10, 0, time.Local)
	if Due(now.Add(-time.Minute)) {
		t.Error("due a minute early")
	}
	if !Due(now) {
		t.Fatal("not due at the scheduled time")
	}
	if Due(now.Add(30 * time.Second)) {
		t.Error("due twice in the same minute")
	}
	if Due(now.Add(time.Minute)) {
		t.Error("due a minute late")
	}
	if !Due(now.Add(24 * time.Hour)) {
		t.Error("not due the next day")
	}

	// a changed schedule is followed without a restart
	useConfig(t, utils.SnapshotsConfig{Enabled: true, Destination: "/backups", Sources: []string{"/data"}, Frequency: "daily", Time: "06:00"})
	if Due(now.Add(48 * time.Hour)) {
		t.Error("still due at the old time")
	}
	if !Due(time.Date(2026, 11, 14, 6, 0, 0, 0, time.Local)) {
		t.Error("not due at the new time")
	}

	// switched off
	useConfig(t, utils.SnapshotsConfig{Destination: "/backups", Sources: []string{"/data"}, Time: "06:00"})
	if Due(time.Date(2026, 11, 15, 6, 0, 0, 0, time.Local)) {
		t.Error("due while switched off")
	}
}
