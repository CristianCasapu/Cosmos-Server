package main

import (
	"encoding/json"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The setup wizard sends {"email": ...} with the administrator account; the field's tag
// used to name the key "omitempty", so the address was dropped.
func TestNewInstallReadsAdminEmail(t *testing.T) {
	var request NewInstallJSON
	body := `{"step":"4","nickname":"admin","password":"x","email":"admin@example.com"}`
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	if request.Email != "admin@example.com" {
		t.Fatalf("email = %q, want admin@example.com", request.Email)
	}
	if err := utils.Validate.Struct(request); err != nil {
		t.Errorf("a valid email must pass validation: %v", err)
	}

	// the other steps send no email
	if err := utils.Validate.Struct(NewInstallJSON{Step: "2"}); err != nil {
		t.Errorf("no email must pass validation: %v", err)
	}

	for _, email := range []string{"not-an-email", "a@b.c\r\nBcc: x@y.z", "<script>@x"} {
		if err := utils.Validate.Struct(NewInstallJSON{Email: email}); err == nil {
			t.Errorf("email %q must be rejected", email)
		}
	}
}
