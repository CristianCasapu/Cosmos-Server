package certs

import (
	"strconv"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Orders happen between restarts now: the list the notices read their
// reason from must not grow without end, and its last entry is the newest.
func TestRecordedErrorsAreBounded(t *testing.T) {
	saved := utils.LetsEncryptErrors
	defer func() { utils.LetsEncryptErrors = saved }()
	utils.LetsEncryptErrors = []string{}
	for i := 0; i < 3*keptErrors; i++ {
		recordError("example.org: failure " + strconv.Itoa(i))
	}
	if len(utils.LetsEncryptErrors) != keptErrors {
		t.Fatalf("kept %d errors, want %d", len(utils.LetsEncryptErrors), keptErrors)
	}
	if last := utils.LetsEncryptErrors[keptErrors-1]; last != "example.org: failure "+strconv.Itoa(3*keptErrors-1) {
		t.Fatalf("the newest error is not last: %q", last)
	}
}
