package watch

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestASnapshotFolderGoesLateAndBack(t *testing.T) {
	r := record(t)
	st := newStore("")
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cfg := utils.SnapshotsConfig{Enabled: true, Destination: "/mnt/backup", Sources: []string{"/mnt/data/photos", "/mnt/data/new"}, Frequency: "daily"}
	last := map[string]time.Time{"/mnt/data/photos": now.Add(-30 * time.Hour)}
	checkSnapshots(st, cfg, last, nil, 0, now)
	if len(r.written) != 0 {
		t.Fatalf("30 hours for a daily schedule is not late (36 is): %s", r.kinds())
	}
	checkSnapshots(st, cfg, last, nil, 0, now.Add(7*time.Hour))
	if r.kinds() != "snapshots.late" || r.written[0].Fork.What.Vars["folder"] != "/mnt/data/photos" {
		t.Fatalf("%s", r.kinds())
	}
	// the folder that never had one counts from when it was first watched
	checkSnapshots(st, cfg, last, nil, 0, now.Add(37*time.Hour))
	if r.kinds() != "snapshots.late,snapshots.late" || r.written[1].Fork.What.Vars["last"] != "never" {
		t.Fatalf("%s", r.kinds())
	}
	last["/mnt/data/photos"] = now.Add(37 * time.Hour)
	checkSnapshots(st, cfg, last, nil, 0, now.Add(38*time.Hour))
	if r.kinds() != "snapshots.late,snapshots.late,snapshots.ontime" || r.written[2].Fork.Resolves != "snapshots.late:/mnt/data/photos" {
		t.Fatalf("%s", r.kinds())
	}
	// the delay follows the schedule, or the settings
	if lateAfter("6h", 0) != 12*time.Hour || lateAfter("weekly", 0) != 252*time.Hour || lateAfter("daily", 5*time.Hour) != 5*time.Hour {
		t.Error("lateAfter")
	}
	// switched off: forgotten
	checkSnapshots(st, utils.SnapshotsConfig{}, nil, nil, 0, now.Add(40*time.Hour))
	if len(st.data.SnapFirst) != 0 {
		t.Error("folders kept after the backup was switched off")
	}
}
