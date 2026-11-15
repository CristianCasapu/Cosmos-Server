package utils

import (
	"os"
	"strings"
	"testing"
)

// Fork addition: the trial of an update hears why the server ends.
func TestFatalIsHeardBeforeTheProcessEnds(t *testing.T) {
	heard := []string{}
	OnFatal(func(message string) { heard = append(heard, message) })
	OnFatal(func(string) { panic("a watcher that fails") })
	forkBeforeFatal("Docker connection died, couldn't recover... Restarting")
	if len(heard) != 1 || heard[0] != "Docker connection died, couldn't recover... Restarting" {
		t.Errorf("heard %v", heard)
	}
	raw, err := os.ReadFile("log.go")
	if err != nil {
		t.Fatal(err)
	}
	fatal := string(raw)[strings.Index(string(raw), "func Fatal("):]
	fatal = fatal[:strings.Index(fatal, "\n}\n")]
	if hook, exit := strings.Index(fatal, "forkBeforeFatal(message)"), strings.Index(fatal, "os.Exit(1)"); hook < 0 || hook > exit {
		t.Error("Fatal does not tell before it ends the process")
	}
}
