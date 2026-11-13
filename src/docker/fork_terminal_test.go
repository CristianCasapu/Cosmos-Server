package docker

import (
	"os"
	"strings"
	"testing"
)

// The container terminal sets the write deadline of its websocket from the
// goroutine that reads it while another one writes. Every write and every
// write deadline must go through the serialised writer.
func TestContainerTerminalWritesAreSerialised(t *testing.T) {
	source, err := os.ReadFile("api_terminal.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "out := utils.NewSerialWebsocket(ws)") {
		t.Fatal("the container terminal must wrap its websocket in the serialised writer")
	}
	for _, direct := range []string{"ws.WriteMessage(", "ws.WriteJSON(", "ws.NextWriter(", "ws.WriteControl(", "ws.SetWriteDeadline("} {
		if strings.Contains(text, direct) {
			t.Errorf("the container terminal must not call %s directly", direct)
		}
	}
}
