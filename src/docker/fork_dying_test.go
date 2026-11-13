package docker

import (
	"fmt"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_dying.go.

func resetDying(t *testing.T) {
	t.Helper()
	dyingMu.Lock()
	dyingContainers = map[string]*dyingContainer{}
	dyingMu.Unlock()
}

func TestDyingAContainerThatKeepsFailingIsReportedOnce(t *testing.T) {
	resetDying(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	// two failures are not a pattern yet
	for i := 0; i < 2; i++ {
		if report, _ := containerDied("jellyfin", "die", "1", now.Add(time.Duration(i)*time.Minute)); report {
			t.Fatalf("reported after %d exits", i+1)
		}
	}
	report, code := containerDied("jellyfin", "die", "137", now.Add(2*time.Minute))
	if !report || code != "137" {
		t.Fatalf("the third failure within ten minutes: reported %v, code %q", report, code)
	}

	// it goes on failing every few seconds: nothing more for six hours
	at := now.Add(2 * time.Minute)
	for i := 0; i < 2000; i++ {
		at = at.Add(10 * time.Second)
		if report, _ := containerDied("jellyfin", "die", "1", at); report {
			t.Fatalf("reported again %v after the first report", at.Sub(now))
		}
	}
	// and then once more
	reported := 0
	for i := 0; i < 400; i++ {
		at = at.Add(10 * time.Second)
		if report, _ := containerDied("jellyfin", "die", "1", at); report {
			reported++
		}
	}
	if reported != 1 {
		t.Errorf("reported %d times in the hour after the six quiet ones, want 1", reported)
	}
}

func TestDyingOrdinaryStopsAreNotFailures(t *testing.T) {
	resetDying(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	// exits without an error, however many
	for i := 0; i < 10; i++ {
		if report, _ := containerDied("job", "die", "0", now.Add(time.Duration(i)*time.Second)); report {
			t.Fatal("a container that exits without an error was reported")
		}
	}
	// stops and restarts asked for through Docker: a kill comes first, the exit code is 137 or 143
	for i := 0; i < 10; i++ {
		at := now.Add(time.Duration(i) * 30 * time.Second)
		containerDied("app", "kill", "", at)
		if report, _ := containerDied("app", "die", "143", at.Add(2*time.Second)); report {
			t.Fatal("a container that was stopped on purpose was reported")
		}
	}
	// failures spread over more than the window
	for i := 0; i < 10; i++ {
		if report, _ := containerDied("rare", "die", "1", now.Add(time.Duration(i)*6*time.Minute)); report {
			t.Fatal("a failure every six minutes was reported as keeps stopping")
		}
	}
	// a kill long ago does not excuse a crash now
	containerDied("late", "kill", "", now)
	for i := 0; i < 3; i++ {
		report, _ := containerDied("late", "die", "1", now.Add(time.Hour+time.Duration(i)*time.Second))
		if report != (i == 2) {
			t.Fatalf("exit %d an hour after a kill: reported %v", i+1, report)
		}
	}
}

func TestDyingEventsReachTheNotice(t *testing.T) {
	resetDying(t)
	type seen struct {
		kind   string
		values map[string]string
	}
	got := []seen{}
	utils.SetForkNoticeHandler(func(kind string, values map[string]string) { got = append(got, seen{kind, values}) })
	defer utils.SetForkNoticeHandler(nil)

	for i := 0; i < 3; i++ {
		noteContainerEvent("container", "start", map[string]string{"name": "db"})
		noteContainerEvent("network", "die", map[string]string{"name": "db", "exitCode": "2"}) // not a container
		noteContainerEvent("container", "die", map[string]string{"name": "db", "exitCode": "2"})
	}
	noteContainerEvent("container", "die", map[string]string{"exitCode": "2"}) // no name: nothing to say
	if len(got) != 1 || got[0].kind != utils.NoticeContainerDying || got[0].values["name"] != "db" || got[0].values["code"] != "2" || got[0].values["exits"] != "3" {
		t.Fatalf("notices %+v", got)
	}
}

func TestDyingTheMemoryIsBounded(t *testing.T) {
	resetDying(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5000; i++ {
		containerDied(fmt.Sprintf("c%d", i), "die", "1", now)
	}
	dyingMu.Lock()
	tracked := len(dyingContainers)
	dyingMu.Unlock()
	if tracked > dyingTracked {
		t.Fatalf("%d containers are remembered, the bound is %d", tracked, dyingTracked)
	}
	// later, the ones that went quiet make room
	if report, _ := containerDied("new", "die", "1", now.Add(7*time.Hour)); report {
		t.Fatal("reported at the first exit")
	}
	dyingMu.Lock()
	_, known := dyingContainers["new"]
	dyingMu.Unlock()
	if !known {
		t.Error("a new container is not followed once the old ones went quiet")
	}
}
