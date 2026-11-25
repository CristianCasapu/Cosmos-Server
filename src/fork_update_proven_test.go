package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestAProvenUpdateIsToldWithoutTouchingTheProof(t *testing.T) {
	var got []utils.Notification
	previous := writeProvenNotice
	writeProvenNotice = func(n utils.Notification) { got = append(got, n) }
	defer func() { writeProvenNotice = previous }()

	noteUpdateProven(forkrelease.TrialRun{Version: "0.24.0-fork.36", Run: 2}, 95*time.Second)
	if len(got) != 1 || got[0].Fork.Kind != "update.proven" || got[0].Fork.What.Vars["version"] != "0.24.0-fork.36" || got[0].Fork.What.Vars["run"] != "2" {
		t.Fatalf("%+v", got)
	}
	// a writer that panics cannot reach the proof's goroutine
	writeProvenNotice = func(utils.Notification) { panic("database gone") }
	noteUpdateProven(forkrelease.TrialRun{Version: "x"}, time.Second)

	// the notice is called only after the proof returned true, and proveUpdateTrial is not changed for it
	source, _ := os.ReadFile("fork_update_trial.go")
	text := string(source)
	if !strings.Contains(text, "if proveUpdateTrial(folder, run, serverTrialChecks(), time.Sleep) {\n\t\t\tnoteUpdateProven(run, time.Since(started))") {
		t.Error("startUpdateTrial does not tell the proof after it succeeded")
	}
	body := text[strings.Index(text, "func proveUpdateTrial("):]
	body = body[:strings.Index(body, "\n}\n")]
	if strings.Contains(body, "noteUpdateProven") || strings.Contains(body, "WriteNotification") {
		t.Error("the proof itself writes a notification")
	}
}
