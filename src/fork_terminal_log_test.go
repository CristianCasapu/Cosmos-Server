package main

import (
	"os"
	"regexp"
	"testing"
)

// Fork addition: what is typed in the host terminal, and what it shows,
// never goes to the log (at DEBUG upstream wrote both, passwords typed at a
// prompt included). The terminal needs a real shell, so this reads the
// source: no log call in terminal.go is given the data read from the
// terminal or from the websocket.
func TestTheHostTerminalDoesNotLogWhatIsTyped(t *testing.T) {
	source, err := os.ReadFile("terminal.go")
	if err != nil {
		t.Fatal(err)
	}
	logged := regexp.MustCompile(`utils\.(Debug|Log|Warn|Error)\([^\n]*string\((buf|message)`)
	if found := logged.FindAll(source, -1); len(found) > 0 {
		t.Errorf("terminal.go logs the terminal's data: %q", found)
	}
}
