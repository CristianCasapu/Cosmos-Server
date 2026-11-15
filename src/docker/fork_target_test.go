package docker

import (
	"errors"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func inspected(state types.ContainerState, mode string, networks map[string]string) types.ContainerJSON {
	nets := map[string]*network.EndpointSettings{}
	for name, ip := range networks {
		nets[name] = &network.EndpointSettings{IPAddress: ip, Gateway: "172.17.0.1"}
	}
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{Name: "/app", State: &state, HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(mode)}},
		NetworkSettings:   &types.NetworkSettings{Networks: nets},
	}
}

// The states that have no address, and the address of the ones that run:
// never the network's gateway (this server) and never "".
func TestContainerTargetStates(t *testing.T) {
	cases := []struct {
		name  string
		c     types.ContainerJSON
		ip    string
		state TargetState
	}{
		{"restarting", inspected(types.ContainerState{Running: true, Restarting: true}, "bridge", map[string]string{"bridge": ""}), "", TargetRestarting},
		{"exited", inspected(types.ContainerState{Status: "exited"}, "bridge", nil), "", TargetStopped},
		{"paused", inspected(types.ContainerState{Running: true, Paused: true}, "bridge", map[string]string{"bridge": "172.17.0.5"}), "", TargetStopped},
		{"running, no address yet", inspected(types.ContainerState{Running: true}, "appnet", map[string]string{"appnet": ""}), "", TargetNoAddress},
		{"bridge", inspected(types.ContainerState{Running: true}, "bridge", map[string]string{"bridge": "172.17.0.5"}), "172.17.0.5", TargetRunning},
		{"named network: its own address, not the gateway", inspected(types.ContainerState{Running: true}, "appnet", map[string]string{"other": "10.0.9.2", "appnet": "10.0.8.3"}), "10.0.8.3", TargetRunning},
		{"host network", inspected(types.ContainerState{Running: true}, "host", nil), "127.0.0.1", TargetRunning},
		{"loopback is no address", inspected(types.ContainerState{Running: true}, "bridge", map[string]string{"bridge": "127.0.0.1"}), "", TargetNoAddress},
	}
	for _, c := range cases {
		ip, state := targetOf(c.c, 0)
		if ip != c.ip || state != c.state {
			t.Errorf("%s: %q %q", c.name, ip, state)
		}
	}
}

// A failure is remembered briefly, so a burst asks Docker once; a missing
// container is said as such; ForgetTarget asks again.
func TestContainerTargetCachesFailures(t *testing.T) {
	asked := 0
	prev := inspectForTarget
	inspectForTarget = func(name string) (types.ContainerJSON, error) {
		asked++
		if name == "gone" {
			return types.ContainerJSON{}, errors.New("Error response from daemon: No such container: gone")
		}
		return inspected(types.ContainerState{Running: true, Restarting: true}, "bridge", nil), nil
	}
	defer func() { inspectForTarget = prev }()
	ForgetTarget("pg")
	for i := 0; i < 100; i++ {
		if _, state, err := ContainerTarget("pg"); err == nil || state != TargetRestarting {
			t.Fatalf("%v %v", state, err)
		}
	}
	if asked != 1 {
		t.Fatalf("asked Docker %d times", asked)
	}
	ForgetTarget("pg")
	ContainerTarget("pg")
	if asked != 2 {
		t.Fatalf("asked %d", asked)
	}
	ForgetTarget("gone")
	if _, state, _ := ContainerTarget("gone"); state != TargetMissing {
		t.Fatalf("state %v", state)
	}
	// container:<other> follows the other, a bounded number of times
	inspectForTarget = func(name string) (types.ContainerJSON, error) {
		asked++
		return inspected(types.ContainerState{Running: true}, "container:"+name, nil), nil
	}
	ForgetTarget("loop")
	if _, state, _ := ContainerTarget("loop"); state != TargetMissing {
		t.Fatalf("state %v", state)
	}
}
