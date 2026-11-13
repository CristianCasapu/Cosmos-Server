package main

import (
	"os"
	"strings"
	"testing"
)

// The host terminal writes to its websocket from two goroutines (the shell's
// output and the keep-alive answers). Every write and every write deadline
// must go through the serialised writer: a direct one brings back the
// "concurrent write to websocket connection" panic that ends the server.
func TestHostTerminalWritesAreSerialised(t *testing.T) {
	source, err := os.ReadFile("terminal.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "out := utils.NewSerialWebsocket(ws)") {
		t.Fatal("the host terminal must wrap its websocket in the serialised writer")
	}
	for _, direct := range []string{"ws.WriteMessage(", "ws.WriteJSON(", "ws.NextWriter(", "ws.WriteControl(", "ws.SetWriteDeadline("} {
		if strings.Contains(text, direct) {
			t.Errorf("the host terminal must not call %s directly", direct)
		}
	}
}
