//go:build watch_fastclock

package watch

// Plan 48, review 6: the end-to-end run (scripts/fork-telegram-messages-e2e.sh)
// is built with the tag watch_fastclock so that a disk filling fast opens
// and ends within minutes. A release build never passes the tag.

import "time"

func init() {
	fillEvery = 20 * time.Second
	fillWindow = 4 * time.Minute
	fillMinSpan = time.Minute
}
