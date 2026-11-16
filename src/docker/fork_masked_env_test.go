package docker

import (
	"reflect"
	"testing"
)

// Review B1: a role with the resources permission but without credentials-read
// applies a repair (its view of the variables is masked): every other variable
// keeps its real value, the new one is set.
func TestAMaskedValueKeepsTheCurrentOne(t *testing.T) {
	current := []string{"POSTGRES_PASSWORD=real-secret-1", "API_TOKEN=real-token-2", "TZ=UTC", "EMPTY="}
	sent := []string{"POSTGRES_PASSWORD=***", "API_TOKEN=***", "TZ=***", "EMPTY=***", "POSTGRESQL_PASSWORD=Generated123456789012345"}
	got, err := keepMaskedEnv(current, sent, false)
	want := []string{"POSTGRES_PASSWORD=real-secret-1", "API_TOKEN=real-token-2", "TZ=UTC", "EMPTY=", "POSTGRESQL_PASSWORD=Generated123456789012345"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	// a variable removed by the caller stays removed; a changed one is changed
	got, _ = keepMaskedEnv(current, []string{"TZ=Europe/Bucharest", "API_TOKEN=***"}, false)
	if !reflect.DeepEqual(got, []string{"TZ=Europe/Bucharest", "API_TOKEN=real-token-2"}) {
		t.Fatalf("%v", got)
	}
	// a mask for a variable the container does not have cannot be kept
	if _, err := keepMaskedEnv(current, []string{"NEW_SECRET=***"}, false); err != ErrMaskedUnknown {
		t.Fatalf("%v", err)
	}
	// a caller who reads credentials got the real values: taken as sent
	got, _ = keepMaskedEnv(current, []string{"TZ=***"}, true)
	if !reflect.DeepEqual(got, []string{"TZ=***"}) {
		t.Fatalf("%v", got)
	}
}
