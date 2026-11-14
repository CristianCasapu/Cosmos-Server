package configapi

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestCreateTokenChecks(t *testing.T) {
	ok := CreateAPITokenRequest{Name: "ha", IPWhitelist: []string{" 192.168.1.0/24 ", "", "10.0.0.5"}, ExpiryDays: 90}
	if err := checkCreateToken(&ok); err != nil {
		t.Fatalf("a valid token refused: %v", err)
	}
	if strings.Join(ok.IPWhitelist, ",") != "192.168.1.0/24,10.0.0.5" {
		t.Errorf("the list is not stored trimmed: %q", ok.IPWhitelist)
	}
	bad := []CreateAPITokenRequest{
		{Name: "a", IPWhitelist: []string{"192.168.1.0/24 x"}},
		{Name: "a", IPWhitelist: []string{"my laptop"}},
		{Name: "a", ExpiryDays: -1},
		{Name: "a", ExpiryDays: 100000},
		{Name: "a", Description: strings.Repeat("x", 300)},
		{Name: "a", Description: "two\nlines"},
		{Name: "a", Permissions: []utils.Permission{utils.PERM_LOGIN}},
		{Name: "a", Permissions: []utils.Permission{999}},
	}
	many := CreateAPITokenRequest{Name: "a"}
	for i := 0; i < 40; i++ {
		many.IPWhitelist = append(many.IPWhitelist, "10.0.0.1")
	}
	bad = append(bad, many)
	for i, r := range bad {
		if err := checkCreateToken(&r); err == nil {
			t.Errorf("case %d accepted: %+v", i, r)
		}
	}
}

func TestUpdateTokenChecks(t *testing.T) {
	desc := "fine"
	if err := checkUpdateToken(&UpdateAPITokenRequest{Description: &desc, IPWhitelist: []string{"2001:db8::/32"}}); err != nil {
		t.Fatalf("valid update refused: %v", err)
	}
	if err := checkUpdateToken(&UpdateAPITokenRequest{IPWhitelist: []string{"nope"}}); err == nil {
		t.Fatal("an invalid address was accepted on update")
	}
	if err := checkUpdateToken(&UpdateAPITokenRequest{}); err != nil {
		t.Fatalf("an empty update refused: %v", err)
	}
}
