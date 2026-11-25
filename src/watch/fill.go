package watch

// A disk filling fast: the space used on each mounted data disk is sampled
// every fillEvery, kept for a day (a small ring per mount, remembered across
// restarts), and its trend computed (least squares). When, at that rate, the
// disk is full within the settings' days, a warning; when the trend is over
// (it no longer grows, or would take twice as long), a closing reply. Tiny
// and read-only file systems are never watched. A trend needs at least
// fillMinSpan of samples, and growth worth the name, so that one large
// download does not count.

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// the fill watch's timing: variables only for the end-to-end build with
// the tag watch_fastclock (fastclock_e2e.go), never set otherwise
var (
	fillEvery   = 30 * time.Minute
	fillWindow  = 24 * time.Hour
	fillMinSpan = 6 * time.Hour
)

// FillTestMounts: mounts the fill watch also reads. Empty in every build but
// the end-to-end one (the tag watch_fastclock, fastclock_e2e.go).
var FillTestMounts []string

// fillGoneAfter: how long a mount must be absent from listings that worked
// before it is forgotten (review 7, N2)
const fillGoneAfter = 24 * time.Hour

const (
	fillMinPoints = 6
	maxMounts     = 16
	maxSamples    = 50
	tinyDisk      = 1 << 30 // file systems smaller than this are not watched
)

// Usage is one mounted file system as read now.
type Usage struct {
	Mount    string
	Total    uint64
	Free     uint64 // available to users
	ReadOnly bool
	// Failed: the mount is listed but its space could not be read (an
	// error, a time-out): nothing is concluded (plan 48, review 6)
	Failed bool
}

// trend is the growth of the used space in bytes per second over the
// samples, and the span they cover.
func trend(samples []fillSample) (perSecond float64, span time.Duration, ok bool) {
	if len(samples) < fillMinPoints {
		return 0, 0, false
	}
	span = samples[len(samples)-1].At.Sub(samples[0].At)
	if span < fillMinSpan {
		return 0, span, false
	}
	t0 := samples[0].At
	var sx, sy, sxx, sxy float64
	n := float64(len(samples))
	for _, s := range samples {
		x := s.At.Sub(t0).Seconds()
		sx += x
		sy += s.Used
		sxx += x * x
		sxy += x * s.Used
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, span, false
	}
	return (n*sxy - sx*sy) / den, span, true
}

// checkFill adds the samples read now and writes what started and ended.
// complete: the mounts were listed; a mount is forgotten (and an open
// warning ends with it) only when such a listing no longer holds it. A
// mount whose space could not be read keeps what is known of it.
func checkFill(st *store, usages []Usage, complete bool, days int, now time.Time) {
	sort.Slice(usages, func(i, j int) bool { return usages[i].Mount < usages[j].Mount })
	st.with(func(data *stateData) bool {
		watched := map[string]bool{}
		for _, u := range usages {
			if ring, known := data.Fill[u.Mount]; known && u.Mount != "" && (u.Failed || u.ReadOnly) {
				// listed, not read (or gone read-only): nothing concluded,
				// the samples kept (review 7, N2)
				watched[u.Mount] = true
				ring.Unread, ring.Missing = true, time.Time{}
				continue
			}
			if u.Failed || u.ReadOnly || u.Total < tinyDisk || u.Mount == "" {
				continue
			}
			ring, known := data.Fill[u.Mount]
			if !known {
				if len(data.Fill) >= maxMounts {
					continue
				}
				ring = &fillRing{}
				data.Fill[u.Mount] = ring
			}
			watched[u.Mount] = true
			ring.Unread, ring.Missing = false, time.Time{}
			used := float64(u.Total - min(u.Free, u.Total))
			kept := ring.Samples[:0]
			for _, s := range ring.Samples {
				if now.Sub(s.At) <= fillWindow && !s.At.After(now) {
					kept = append(kept, s)
				}
			}
			ring.Samples = append(kept, fillSample{At: now, Used: used})
			if len(ring.Samples) > maxSamples {
				ring.Samples = ring.Samples[len(ring.Samples)-maxSamples:]
			}

			rate, span, ok := trend(ring.Samples)
			grown := rate * span.Seconds()
			growing := ok && rate > 0 && grown >= math.Max(float64(u.Total)*0.005, float64(tinyDisk))
			left := time.Duration(math.MaxInt64)
			if growing {
				if seconds := float64(u.Free) / rate; seconds < float64(math.MaxInt64/int64(time.Second)) {
					left = time.Duration(seconds) * time.Second
				}
			}
			limit := time.Duration(days) * 24 * time.Hour
			switch {
			case !ring.Open && growing && left < limit:
				ring.Open = true
				notify("warning", fillInfo("disk.filling", u, grown, span, left))
			case ring.Open && ok && (!growing || left > 2*limit):
				ring.Open = false
				resolveThread(fillThread(u.Mount))
				notify("success", fillInfo("disk.filling.over", u, 0, 0, 0))
			}
		}
		// a mount absent from listings that worked is unknown; after a day
		// of them it is forgotten, and an open warning ends with it ("No
		// longer followed"): a decision that long is the owner's (review 7, N2)
		for mount, ring := range data.Fill {
			if !complete || watched[mount] {
				continue
			}
			if ring.Missing.IsZero() {
				ring.Missing = now
			}
			if now.Sub(ring.Missing) >= fillGoneAfter {
				if ring.Open {
					resolveThread(fillThread(mount))
				}
				delete(data.Fill, mount)
				noteFillGone(mount)
			}
		}
		return true
	})
}

func fillThread(mount string) string { return "fill:" + mount }

func fillInfo(kind string, u Usage, grown float64, span time.Duration, left time.Duration) *utils.NotificationInfo {
	thread := fillThread(u.Mount)
	info := &utils.NotificationInfo{
		Kind: kind, Source: "storage", Thread: thread,
		Title: utils.NText("notif."+kind+".title", "mount", u.Mount),
		What: utils.NText("notif."+kind+".what", "mount", u.Mount, "grown", sizeWords(grown),
			"hours", strconv.Itoa(int(span.Hours()+0.5)), "left", durationWords(left), "free", sizeWords(float64(u.Free))),
		Subject: &utils.NotificationSubject{Type: "disk", Name: u.Mount, Link: "/cosmos-ui/storage"},
	}
	if kind == "disk.filling" {
		info.Means = utils.NText("notif.disk.filling.means")
	} else {
		info.Resolves = thread
	}
	return info
}
