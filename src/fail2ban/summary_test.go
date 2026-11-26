package fail2ban

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSummaryAsksOnlyForAPing(t *testing.T) {
	folder := useJailDir(t, true)
	stamp := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(folder, installedFile), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	f := useFake(t, true, realAnswers(t))
	summary := GetSummary()
	if !summary.Installed || !summary.Running || !summary.ConfigInstalled {
		t.Errorf("summary %+v, want installed, running, the fork's jails", summary)
	}
	if !summary.ConfigSince.Equal(stamp) {
		t.Errorf("since %v, want the jail file's time %v", summary.ConfigSince, stamp)
	}
	if calls := f.called(); len(calls) != 1 || f.count("ping") != 1 {
		t.Errorf("calls %v, want one ping", calls)
	}
}

func TestSummaryReusesAFreshStatus(t *testing.T) {
	useJailDir(t, true)
	f := useFake(t, true, realAnswers(t))
	GetStatus("")
	before := len(f.called())
	if summary := GetSummary(); !summary.Running {
		t.Errorf("summary %+v, want running", summary)
	}
	if after := len(f.called()); after != before {
		t.Errorf("the summary ran the client %d more times, want none: the status of a moment ago says it all", after-before)
	}
}

func TestSummaryOfAServerWithoutFail2ban(t *testing.T) {
	useJailDir(t, false)
	f := useFake(t, false, func([]string) (string, error) { return "", errors.New("not there") })
	summary := GetSummary()
	if summary.Installed || summary.Running || summary.ConfigInstalled || !summary.ConfigSince.IsZero() {
		t.Errorf("summary %+v, want nothing", summary)
	}
	if calls := f.called(); len(calls) != 0 {
		t.Errorf("calls %v, want none without the client", calls)
	}
}

func TestSummaryOfAStoppedFail2ban(t *testing.T) {
	useJailDir(t, true)
	useFake(t, true, func([]string) (string, error) { return "", errClient })
	if summary := GetSummary(); !summary.Installed || summary.Running || !summary.ConfigInstalled {
		t.Errorf("summary %+v, want installed, not running, configured", summary)
	}
}
