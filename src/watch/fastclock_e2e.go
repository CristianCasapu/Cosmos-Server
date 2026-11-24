//go:build watch_fastclock

package watch

// Plan 48, review 6: the end-to-end run (scripts/fork-telegram-messages-e2e.sh)
// is built with the tag watch_fastclock so that a disk filling fast opens
// and ends within minutes. A release build never passes the tag.

//
// Review 7: its test disk is a size-limited tmpfs, named in
// COSMOS_FILL_TEST_MOUNTS, that the fill watch also reads. This file is in a
// package, not named on the command line, so its build constraint holds
// even for build.sh's `go build src/*.go` (a file of package main named
// there would be compiled whatever its tag).

import (
	"os"
	"strings"
	"time"
)

func init() {
	fillEvery = 20 * time.Second
	fillWindow = 4 * time.Minute
	fillMinSpan = time.Minute
	for _, mount := range strings.Split(os.Getenv("COSMOS_FILL_TEST_MOUNTS"), ",") {
		if mount = strings.TrimSpace(mount); strings.HasPrefix(mount, "/") {
			FillTestMounts = append(FillTestMounts, mount)
		}
	}
}
