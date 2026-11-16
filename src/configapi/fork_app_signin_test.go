package configapi

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestTheSignInDefaultsAreMaskedForReadersWithoutCredentials(t *testing.T) {
	config := utils.Config{AppSignIn: utils.ForkAppSignInConfig{User: "owner", Email: "owner@example.org", Length: 32}}
	shown := config
	maskAppSignIn(&shown, true)
	if shown.AppSignIn != config.AppSignIn {
		t.Fatalf("a reader with credentials-read: %+v", shown.AppSignIn)
	}
	masked := config
	maskAppSignIn(&masked, false)
	if masked.AppSignIn.User != "***" || masked.AppSignIn.Email != "***" || masked.AppSignIn.Length != 32 {
		t.Fatalf("masked: %+v", masked.AppSignIn)
	}
	empty := utils.Config{}
	maskAppSignIn(&empty, false)
	if empty.AppSignIn.User != "" || empty.AppSignIn.Email != "" {
		t.Fatalf("an empty value is not masked: %+v", empty.AppSignIn)
	}
}

func TestASaveKeepsMaskedDefaultsAndChecksChangedOnes(t *testing.T) {
	stored := utils.Config{AppSignIn: utils.ForkAppSignInConfig{User: "owner", Email: "owner@example.org"}}
	request := utils.Config{AppSignIn: utils.ForkAppSignInConfig{User: "***", Email: "***", MailOff: true}}
	if err := keepAppSignIn(&request, stored); err != nil {
		t.Fatal(err)
	}
	if request.AppSignIn.User != "owner" || request.AppSignIn.Email != "owner@example.org" || !request.AppSignIn.MailOff {
		t.Fatalf("kept: %+v", request.AppSignIn)
	}
	for _, bad := range []utils.ForkAppSignInConfig{
		{Email: "not an address"}, {Email: "a@b"}, {Email: "x@example.org\r\nBcc: y@example.org"},
		{User: strings.Repeat("a", 65)}, {User: "bad\x01name"}, {User: " spaced"}, {Length: 8}, {Length: 65},
	} {
		request := utils.Config{AppSignIn: bad}
		if err := keepAppSignIn(&request, utils.Config{}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// a value the file already had is not checked again (a file edited by hand never blocks a save)
	odd := utils.Config{AppSignIn: utils.ForkAppSignInConfig{Length: 3}}
	again := odd
	if err := keepAppSignIn(&again, odd); err != nil {
		t.Fatalf("unchanged: %v", err)
	}
	good := utils.Config{AppSignIn: utils.ForkAppSignInConfig{User: "Owner Name", Email: "owner@example.org", Length: 40}}
	if err := keepAppSignIn(&good, utils.Config{}); err != nil {
		t.Fatalf("good: %v", err)
	}
}
