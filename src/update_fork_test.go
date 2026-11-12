package main

// Fork addition: ordering of fork releases (see CompareVersions in update.go).

import "testing"

func TestCompareVersionsForkReleases(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               int
	}{
		// same upstream version, next build
		{"0.24.0-fork.7", "v0.24.0-fork.8", -1},
		{"0.24.0-fork.9", "v0.24.0-fork.10", -1},
		{"0.24.0-fork.8", "v0.24.0-fork.8", 0},
		{"0.24.0-fork.8", "v0.24.0-fork.7", 1},
		// newer upstream version
		{"0.23.4-fork.5", "v0.24.0-fork.6", -1},
		// rollback: a higher build on an older upstream version is installed
		{"0.24.1-fork.13", "v0.24.0-fork.14", -1},
		{"0.24.0-fork.14", "v0.24.1-fork.13", 1},
		// the numbering used before: 0.23.4003 was build 3 on upstream 0.23.4
		{"0.23.4003", "v0.24.0-fork.4", -1},
		{"0.23.4003", "v0.23.4-fork.4", 1},
		// not fork releases: plain semantic versions
		{"0.23.4", "v0.23.5", -1},
		{"0.24.0", "v0.23.9", 1},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.current, c.candidate)
		if err != nil {
			t.Errorf("CompareVersions(%q, %q): %v", c.current, c.candidate, err)
			continue
		}
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.current, c.candidate, got, c.want)
		}
	}
}

func TestCompareVersionsRejectsGarbage(t *testing.T) {
	if _, err := CompareVersions("0.24.0-fork.7", "not-a-version"); err == nil {
		t.Error("expected an error for an unparseable version")
	}
}

func TestForkBuild(t *testing.T) {
	cases := []struct {
		version string
		build   int
		ok      bool
	}{
		{"0.24.0-fork.7", 7, true},
		{"v0.24.0-fork.120", 120, true},
		{"0.24.0", 0, false},
		{"0.24.0-fork.", 0, false},
		{"0.24.0-fork.7a", 0, false},
		{"0.24.0-fork.-7", 0, false},
		{"0.24.0-fork.99999999999999999999", 0, false},
		{"0.24.0-unstable031", 0, false},
	}
	for _, c := range cases {
		build, ok := forkBuild(c.version)
		if build != c.build || ok != c.ok {
			t.Errorf("forkBuild(%q) = %d, %v; want %d, %v", c.version, build, ok, c.build, c.ok)
		}
	}
}
