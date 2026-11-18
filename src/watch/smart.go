package watch

// A disk whose S.M.A.R.T. data says it is failing: its own verdict (a
// pre-fail attribute at or under its threshold, an NVMe critical warning),
// or counts that only grow on a dying disk growing since the last check
// (reallocated, pending and uncorrectable sectors, NVMe media errors). The
// notification names the disk, its model, the last characters of its serial
// and where it is mounted; it ends (kind smart.restored, a reply) when the
// same disk reads healthy again, or when it is gone (replaced).
//
// A spun-down SATA disk is not woken: the reader asks its power state
// first (ATA CHECK POWER MODE, which never spins a disk up) and skips it
// when it sleeps; it is read at a later check when it is awake anyway.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxDisks     = 32
	smartEvery   = time.Hour
	goneAfter    = 3                  // checks a disk with an open problem may be missing before it counts as replaced
	calmAfter    = 7 * 24 * time.Hour // a failing disk reads healthy again after this long without a new sign
	failedReads  = 3                  // failed reads among the last readWindow before a disk that was read is "cannot be read"
	readWindow   = 6
	maxReasonLen = 200
)

// Disk is what is read of one disk. Counts are -1 when the disk does not
// report them.
type Disk struct {
	Device string // /dev/sdb
	Model  string
	Serial string // as the system lists it (lsblk), the key of the disk
	WWN    string
	Mounts []string
	Asleep bool // not read: it sleeps
	// Unreadable: the system lists it, its S.M.A.R.T. data could not be
	// read. Unwatchable: its power state cannot be asked, so it is not read
	// (that could wake it).
	Unreadable  bool
	Unwatchable bool
	// PreFail names the pre-fail attributes at or under their threshold.
	PreFail       []string
	Reallocated   int64
	Pending       int64
	Uncorrectable int64
	// NVMe
	CriticalWarning uint8
	MediaErrors     int64
}

// key is what a disk is known by across checks: its serial, else its device.
func (d Disk) key() string {
	switch {
	case d.Serial != "":
		return "serial:" + d.Serial
	case d.WWN != "":
		return "wwn:" + d.WWN
	}
	return "device:" + d.Device
}

// judge says why a disk is failing, compared with what it reported before
// (had: there is an earlier reading). An empty answer: it reads healthy.
func judge(before smartMemo, had bool, d Disk) []string {
	reasons := []string{}
	if len(d.PreFail) > 0 {
		reasons = append(reasons, "its own check says it is failing ("+strings.Join(d.PreFail, ", ")+")")
	}
	if d.CriticalWarning != 0 {
		reasons = append(reasons, "it raises a critical warning (code "+strconv.Itoa(int(d.CriticalWarning))+")")
	}
	grew := func(name string, now int64, then int64) {
		switch {
		case now <= 0:
		case had && then >= 0 && now > then:
			reasons = append(reasons, strconv.FormatInt(now, 10)+" "+name+" ("+strconv.FormatInt(now-then, 10)+" more since the last check)")
		case name == "pending sectors" || name == "uncorrectable sectors":
			// these are not normal on a healthy disk, even once
			reasons = append(reasons, strconv.FormatInt(now, 10)+" "+name)
		}
	}
	grew("reallocated sectors", d.Reallocated, before.Reallocated)
	grew("pending sectors", d.Pending, before.Pending)
	grew("uncorrectable sectors", d.Uncorrectable, before.Uncorrectable)
	grew("media errors", d.MediaErrors, before.MediaErrors)
	return reasons
}

// checkSMART compares the disks read now with what was remembered, and
// writes what started and what ended.
func checkSMART(st *store, disks []Disk, now time.Time) {
	if len(disks) > maxDisks {
		disks = disks[:maxDisks]
	}
	st.with(func(data *stateData) bool {
		seen := map[string]bool{}
		for _, d := range disks {
			key := d.key()
			seen[key] = true
			before, had := data.SMART[key]
			if d.Asleep {
				if had {
					before.Missing = 0
					data.SMART[key] = before
				}
				continue
			}
			if d.Unwatchable && before.Read {
				// review T2: a disk that was read and no longer answers even
				// the power check is a disk that cannot be read
				d.Unwatchable, d.Unreadable = false, true
			}
			if d.Unwatchable {
				// a disk whose power state could never be asked is not read
				if !before.Unwatchable {
					before.Unwatchable, before.Device, before.Seen = true, d.Device, now
					notify("info", smartInfo("smart.unwatchable", d, ""))
				}
				before.Missing = 0
				data.SMART[key] = before
				continue
			}
			if d.Unreadable {
				// review R4/T2: listed by the system but not readable: never
				// "healthy again"; failed reads counted among the last few
				// checks (a disk failing two reads in three is told too)
				before.Missing = 0
				if before.Read {
					before.History = appendRead(before.History, false)
					if failures(before.History) >= failedReads && !before.Unreadable {
						before.Unreadable = true
						notify("error", smartInfo("smart.unreadable", Disk{Device: d.Device, Model: before.Model, Serial: d.Serial, WWN: d.WWN, Mounts: d.Mounts}, ""))
					}
					data.SMART[key] = before
				}
				continue
			}
			reasons := judge(before, had, d)
			memo := smartMemo{Device: d.Device, Model: d.Model, Serial: d.Serial, Reallocated: d.Reallocated, Pending: d.Pending,
				Uncorrectable: d.Uncorrectable, MediaErrors: d.MediaErrors, Open: before.Open, LastBad: before.LastBad, Seen: now, Read: true,
				History: appendRead(before.History, true), Unreadable: before.Unreadable}
			if memo.Unreadable && failures(memo.History) == 0 {
				// read well for the whole window: the "cannot be read" is over
				memo.Unreadable = false
				resolveThread(smartThread(key) + ":unreadable")
				notify("success", smartInfo("smart.readable", d, ""))
			}
			if len(reasons) > 0 {
				memo.LastBad = now
			}
			switch {
			case len(reasons) > 0 && !before.Open:
				memo.Open = true
				reason := strings.Join(reasons, "; ")
				if len(reason) > maxReasonLen {
					reason = reason[:maxReasonLen]
				}
				notify("error", smartInfo("smart.failing", d, reason))
			case len(reasons) == 0 && before.Open && now.Sub(memo.LastBad) >= calmAfter:
				memo.Open = false
				resolveThread(smartThread(key))
				notify("success", smartInfo("smart.restored", d, ""))
			}
			data.SMART[key] = memo
		}
		// a disk with an open problem that is gone for a few checks was replaced
		for key, memo := range data.SMART {
			if seen[key] {
				continue
			}
			memo.Missing++
			if memo.Open && memo.Missing >= goneAfter {
				// review 4, A2: a failing disk that is gone is not healthy:
				// "no longer present", and its history is kept (it is
				// judged on it again should it come back)
				memo.Open = false
				resolveThread(smartThread(key))
				notify("warning", smartInfo("smart.gone", Disk{Device: memo.Device, Model: memo.Model, Serial: memo.Serial}, ""))
				data.SMART[key] = memo
				continue
			}
			if !memo.Open && now.Sub(memo.Seen) > 30*24*time.Hour {
				delete(data.SMART, key)
				continue
			}
			data.SMART[key] = memo
		}
		return true
	})
}

// resolveThread ends a thread. A variable for the tests.
var resolveThread = func(thread string) { utils.ResolveThread(thread) }

func smartThread(key string) string { return "smart:" + key }

// serialEnd is the last characters of a serial: enough to find the disk,
// not the whole number.
func serialEnd(serial string) string {
	serial = strings.TrimSpace(serial)
	if len(serial) > 4 {
		return "…" + serial[len(serial)-4:]
	}
	if serial == "" {
		return "-"
	}
	return serial
}

func smartInfo(kind string, d Disk, reason string) *utils.NotificationInfo {
	mounts := append([]string(nil), d.Mounts...)
	sort.Strings(mounts)
	if len(mounts) > 4 {
		mounts = append(mounts[:4], "…")
	}
	where := strings.Join(mounts, ", ")
	if where == "" {
		where = "nothing"
	}
	model := d.Model
	if model == "" {
		model = "unknown model"
	}
	thread := smartThread(d.key())
	info := &utils.NotificationInfo{
		Kind: kind, Source: "storage", Thread: thread,
		Title:   utils.NText("notif."+kind+".title", "disk", d.Device),
		What:    utils.NText("notif."+kind+".what", "disk", d.Device, "model", model, "serial", serialEnd(d.Serial), "mounts", where, "reason", reason),
		Subject: &utils.NotificationSubject{Type: "disk", Name: d.Device, Link: "/cosmos-ui/storage"},
	}
	switch kind {
	case "smart.failing":
		info.Means = utils.NText("notif.smart.failing.means")
	case "smart.unreadable":
		info.Thread = thread + ":unreadable"
		info.Means = utils.NText("notif.smart.unreadable.means")
	case "smart.readable":
		info.Thread, info.Resolves = thread+":unreadable", thread+":unreadable"
	case "smart.unwatchable":
		info.Thread = thread + ":unwatchable"
	default:
		info.Resolves = thread
	}
	return info
}

// appendRead adds the outcome of a read to the last readWindow ones.
func appendRead(history []bool, ok bool) []bool {
	history = append(history, ok)
	if len(history) > readWindow {
		history = history[len(history)-readWindow:]
	}
	return history
}

func failures(history []bool) int {
	n := 0
	for _, ok := range history {
		if !ok {
			n++
		}
	}
	return n
}
