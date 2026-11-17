package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func disk(serial string) Disk {
	return Disk{Device: "/dev/sdb", Model: "WDC WD40EFRX", Serial: serial, Mounts: []string{"/mnt/data"}, Reallocated: 0, Pending: 0, Uncorrectable: 0, MediaErrors: -1}
}

func TestADiskThatStartsFailingAndIsReplaced(t *testing.T) {
	r := record(t)
	st := newStore("")
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)

	healthy := disk("WD-WCC4E1234F2A")
	healthy.Reallocated = 8 // some disks come with a few: not a failure by itself
	checkSMART(st, []Disk{healthy}, now)
	if len(r.written) != 0 {
		t.Fatalf("a healthy disk is told: %s", r.kinds())
	}

	// asleep: not read, nothing said, nothing forgotten
	asleep := healthy
	asleep.Asleep, asleep.Reallocated = true, -1
	checkSMART(st, []Disk{asleep}, now.Add(time.Hour))

	worse := healthy
	worse.Reallocated = 13
	checkSMART(st, []Disk{worse}, now.Add(2*time.Hour))
	if r.kinds() != "smart.failing" {
		t.Fatalf("growing reallocations: %s", r.kinds())
	}
	n := r.written[0]
	what := n.Fork.What.Vars
	if n.Level != "error" || what["serial"] != "…4F2A" || what["mounts"] != "/mnt/data" || !strings.Contains(what["reason"], "13 reallocated sectors (5 more") {
		t.Errorf("%s %+v", n.Level, what)
	}
	if strings.Contains(utils.RenderNotificationText(*n.Fork.What), "WCC4E") {
		t.Error("the whole serial is in the message")
	}
	// still failing: told once
	checkSMART(st, []Disk{worse}, now.Add(3*time.Hour))
	if len(r.written) != 1 {
		t.Fatalf("told again: %s", r.kinds())
	}
	// replaced: another serial on the same device; the old one is gone for a few checks
	fresh := disk("ZA4NEW99")
	for i := 0; i < goneAfter; i++ {
		checkSMART(st, []Disk{fresh}, now.Add(time.Duration(4+i)*time.Hour))
	}
	if r.kinds() != "smart.failing,smart.restored" || r.written[1].Fork.Resolves != r.written[0].Fork.Thread {
		t.Errorf("replacement: %s %+v", r.kinds(), r.written[len(r.written)-1].Fork)
	}
}

func TestTheDiskSaysItIsFailing(t *testing.T) {
	r := record(t)
	st := newStore("")
	now := time.Now()
	d := disk("S1")
	d.PreFail = []string{"Spin_Up_Time"}
	checkSMART(st, []Disk{d}, now)
	pending := disk("S2")
	pending.Pending = 3
	checkSMART(st, []Disk{d, pending}, now.Add(time.Hour))
	nvme := Disk{Device: "/dev/nvme0n1", Serial: "N1", CriticalWarning: 4, MediaErrors: 0, Reallocated: -1, Pending: -1, Uncorrectable: -1}
	checkSMART(st, []Disk{d, pending, nvme}, now.Add(2*time.Hour))
	if r.kinds() != "smart.failing,smart.failing,smart.failing" {
		t.Fatalf("%s", r.kinds())
	}
	// it reads healthy again: not at once, after a week without a new sign; then the closing answers it
	ok := disk("S2")
	checkSMART(st, []Disk{d, ok, nvme}, now.Add(3*time.Hour))
	if strings.HasSuffix(r.kinds(), "smart.restored") {
		t.Fatal("closed at the first healthy reading")
	}
	checkSMART(st, []Disk{d, ok, nvme}, now.Add(8*24*time.Hour))
	if !strings.HasSuffix(r.kinds(), "smart.restored") || r.written[3].Fork.Resolves != "smart:serial:S2" {
		t.Errorf("%s", r.kinds())
	}
}
