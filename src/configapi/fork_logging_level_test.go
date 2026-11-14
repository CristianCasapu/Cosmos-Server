package configapi

// Fork addition: tests of fork_logging_level.go.

import (
	"net/http"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestTheLogLevelIsOneOfTheKnownOnes(t *testing.T) {
	restarted := hostPowersServer(t)
	settings := readSettingsAs(t, asAdmin)
	for _, level := range []string{"debug", "VERBOSE", "TRACE", " INFO"} {
		settings.LoggingLevel = utils.LoggingLevel(level)
		if code := saveSettingsAs(t, restarted, asAdmin, settings); code != http.StatusBadRequest {
			t.Errorf("log level %q: %d, want 400", level, code)
		}
	}
	settings.LoggingLevel = "ERROR"
	if code := saveSettingsAs(t, restarted, asAdmin, settings); code != http.StatusOK {
		t.Errorf("log level ERROR: %d, want 200", code)
	}
}
