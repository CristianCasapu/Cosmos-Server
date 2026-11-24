//go:build watch_fastclock

package main

// Plan 48: the end-to-end run (scripts/fork-telegram-messages-e2e.sh) gives
// the fill watch a size-limited tmpfs as its test disk, named in
// COSMOS_FILL_TEST_MOUNTS. A release never has the tag.

import (
	"os"
	"strings"
)

func init() {
	for _, mount := range strings.Split(os.Getenv("COSMOS_FILL_TEST_MOUNTS"), ",") {
		if mount = strings.TrimSpace(mount); strings.HasPrefix(mount, "/") {
			fillTestMounts = append(fillTestMounts, mount)
		}
	}
}
