package docker

import (
	"errors"
	"testing"

	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

func TestStopTimeout(t *testing.T) {
	if stopTimeout(0) != nil {
		t.Error("no grace period must leave Docker's default, not a timeout of 0")
	}
	if stopTimeout(-1) != nil {
		t.Error("a negative grace period must leave Docker's default")
	}
	if got := stopTimeout(30); got == nil || *got != 30 {
		t.Errorf("a grace period of 30 must be kept, got %v", got)
	}
}

func TestHealStopTimeout(t *testing.T) {
	zero, thirty := 0, 30

	c := types.ContainerJSON{Config: &conttype.Config{StopTimeout: &zero}}
	healStopTimeout(&c)
	if c.Config.StopTimeout != nil {
		t.Error("a stop timeout of 0 must be dropped")
	}

	c = types.ContainerJSON{Config: &conttype.Config{StopTimeout: &thirty}}
	healStopTimeout(&c)
	if c.Config.StopTimeout == nil || *c.Config.StopTimeout != 30 {
		t.Error("a real stop timeout must be kept")
	}

	healStopTimeout(nil)
	healStopTimeout(&types.ContainerJSON{})
}

func TestContainerGone(t *testing.T) {
	gone := []string{
		"Error response from daemon: removal of container acc46e88 is already in progress",
		"Error response from daemon: No such container: cosmos-uibuild",
	}
	for _, message := range gone {
		if !containerGone(errors.New(message)) {
			t.Errorf("%q must count as a removed container", message)
		}
	}
	if containerGone(errors.New("Error response from daemon: port is already allocated")) {
		t.Error("an unrelated error must not count as a removed container")
	}
	if containerGone(nil) {
		t.Error("no error is not a removed container")
	}
}
