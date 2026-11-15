package main

// Fork addition: the Turnstile check is where it must be (fork_turnstile.go).

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/turnstile"
)

func TestTurnstileIsWiredAfterTheSignInShield(t *testing.T) {
	raw, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	shield := strings.Index(source, "SecureAPI(srapiStrict, false, false, true)")
	hook := strings.Index(source, "forkTurnstile(srapiStrict)")
	if shield < 0 || hook < 0 || hook < shield {
		t.Errorf("the check must be added to the sign-in router after its shield and token check (shield at %d, hook at %d)", shield, hook)
	}
	if !strings.Contains(source, "registerTurnstileRoutes(srapiAdmin)") {
		t.Error("the settings routes are not on the admin router")
	}
	raw, _ = os.ReadFile("system.go")
	if !strings.Contains(string(raw), `"turnstile": forkTurnstilePublic(req)`) {
		t.Error("the sign-in pages are not told about the widget")
	}
	if reflect.ValueOf(turnstile.IsLocal).Pointer() != reflect.ValueOf(proxy.IsLocalVisitorRequest).Pointer() {
		t.Error("the local network rule is not the fork's")
	}
}
