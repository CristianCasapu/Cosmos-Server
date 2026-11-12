package utils

import (
	"errors"
	"os"
	"testing"
)

// fakeEtc stands for the files of one or more machines: path -> content for
// regular files, path -> target for symbolic links.
type fakeEtc struct {
	files map[string]string
	links map[string]string
}

func (f fakeEtc) readFile(path string) ([]byte, error) {
	if content, ok := f.files[path]; ok {
		return []byte(content), nil
	}
	return nil, os.ErrNotExist
}

func (f fakeEtc) readlink(path string) (string, error) {
	if target, ok := f.links[path]; ok {
		return target, nil
	}
	if _, ok := f.files[path]; ok {
		return "", errors.New("readlink " + path + ": invalid argument") // a regular file
	}
	return "", os.ErrNotExist
}

func TestHostTimezone(t *testing.T) {
	cases := []struct {
		name  string
		etc   fakeEtc
		roots []string
		want  string
	}{
		{
			name:  "Debian 13: no /etc/timezone, /etc/localtime is a link",
			etc:   fakeEtc{links: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Europe/Bucharest"}},
			roots: []string{""},
			want:  "Europe/Bucharest",
		},
		{
			name:  "relative link, as systemd writes it",
			etc:   fakeEtc{links: map[string]string{"/etc/localtime": "../usr/share/zoneinfo/America/Argentina/Buenos_Aires"}},
			roots: []string{""},
			want:  "America/Argentina/Buenos_Aires",
		},
		{
			name: "/etc/timezone comes first",
			etc: fakeEtc{
				files: map[string]string{"/etc/timezone": "Europe/Paris\n"},
				links: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Europe/Bucharest"},
			},
			roots: []string{""},
			want:  "Europe/Paris",
		},
		{
			name:  "comments and blank lines in /etc/timezone",
			etc:   fakeEtc{files: map[string]string{"/etc/timezone": "# set by the installer\n\n  Asia/Tokyo  \n"}},
			roots: []string{""},
			want:  "Asia/Tokyo",
		},
		{
			name: "an unusable /etc/timezone falls through to the link",
			etc: fakeEtc{
				files: map[string]string{"/etc/timezone": "../../etc/passwd\n"},
				links: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Etc/GMT+2"},
			},
			roots: []string{""},
			want:  "Etc/GMT+2",
		},
		{
			name:  "posix/ and right/ copies of the database name the same zone",
			etc:   fakeEtc{links: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/posix/Europe/Berlin"}},
			roots: []string{""},
			want:  "Europe/Berlin",
		},
		{
			name:  "/etc/localtime is a copy, not a link: unknown",
			etc:   fakeEtc{files: map[string]string{"/etc/localtime": "TZif2..."}},
			roots: []string{""},
			want:  "",
		},
		{
			name:  "a link to somewhere else: unknown",
			etc:   fakeEtc{links: map[string]string{"/etc/localtime": "/var/db/timezone/zone"}},
			roots: []string{""},
			want:  "",
		},
		{
			name:  "nothing there",
			etc:   fakeEtc{},
			roots: []string{""},
			want:  "",
		},
		{
			name: "in a container the host's files win over the container's",
			etc: fakeEtc{
				files: map[string]string{"/etc/timezone": "Etc/UTC\n"},
				links: map[string]string{"/mnt/host/etc/localtime": "/usr/share/zoneinfo/Europe/Bucharest"},
			},
			roots: []string{"/mnt/host", ""},
			want:  "Europe/Bucharest",
		},
		{
			name:  "in a container without the host mounted: the container's own",
			etc:   fakeEtc{files: map[string]string{"/etc/timezone": "Etc/UTC\n"}},
			roots: []string{"/mnt/host", ""},
			want:  "Etc/UTC",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostTimezone(c.roots, c.etc.readFile, c.etc.readlink); got != c.want {
				t.Errorf("hostTimezone = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveTimezone(t *testing.T) {
	host := func(zone string) func() string { return func() string { return zone } }

	cases := []struct {
		name      string
		setting   string
		processTZ string
		host      string
		want      string
	}{
		{"the setting wins", "Europe/Paris", "America/New_York", "Europe/Bucharest", "Europe/Paris"},
		{"the setting is trimmed", "  Europe/Paris ", "", "", "Europe/Paris"},
		{"then the TZ of the process, as upstream", "", "America/New_York", "Europe/Bucharest", "America/New_York"},
		{"then the host's zone", "", "", "Europe/Bucharest", "Europe/Bucharest"},
		{"a blank setting is no setting", "   ", "", "Europe/Bucharest", "Europe/Bucharest"},
		{"nothing known: empty, the caller drops the variable as upstream does", "", "", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveTimezone(c.setting, c.processTZ, host(c.host)); got != c.want {
				t.Errorf("resolveTimezone = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveTimezoneReadsHostOnlyWhenNeeded(t *testing.T) {
	called := false
	host := func() string { called = true; return "Europe/Bucharest" }

	resolveTimezone("Europe/Paris", "", host)
	resolveTimezone("", "America/New_York", host)
	if called {
		t.Error("the host's files were read although the setting or TZ gave the zone")
	}
}

func TestValidateTimezone(t *testing.T) {
	valid := []string{"", "UTC", "Europe/Bucharest", "America/Argentina/Buenos_Aires", "Etc/GMT+2"}
	for _, name := range valid {
		if err := ValidateTimezone(name); err != nil {
			t.Errorf("ValidateTimezone(%q) = %v, want it accepted", name, err)
		}
	}

	invalid := []string{
		"Local", "local", "Europe/Nowhere", "Bucharest", "Europe/", "/Europe/Bucharest",
		"../../etc/passwd", "Europe/../../etc/passwd", "Europe/Bucharest ", " Europe/Bucharest",
		"Europe/Bucharest\nFOO=bar", "Europe\\Bucharest", "$(reboot)", "UTC;id",
	}
	for _, name := range invalid {
		if err := ValidateTimezone(name); err == nil {
			t.Errorf("ValidateTimezone(%q) accepted", name)
		}
	}
}
