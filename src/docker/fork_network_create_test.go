package docker

import "testing"

func TestNetworkPayloadChecked(t *testing.T) {
	good := []createNetworkPayload{
		{Name: "media-net", Driver: "bridge"},
		{Name: " media ", Driver: "", Subnet: "172.30.0.0/24"},
		{Name: "lan", Driver: "macvlan", ParentInterface: "eno1", Subnet: "192.168.1.0/24"},
	}
	for _, p := range good {
		p := p
		if msg := checkNetworkPayload(&p); msg != "" {
			t.Errorf("%+v refused: %s", p, msg)
		}
	}
	bad := []createNetworkPayload{
		{Name: "", Driver: "bridge"},
		{Name: "bad name", Driver: "bridge"},
		{Name: "n", Driver: "host"},
		{Name: "n", Driver: "overlay"},
		{Name: "n", Driver: "macvlan"},
		{Name: "n", Driver: "macvlan", ParentInterface: "eth0; rm"},
		{Name: "n", Driver: "bridge", ParentInterface: "eth0"},
		{Name: "n", Driver: "bridge", Subnet: "172.30.0.1/24"},
		{Name: "n", Driver: "bridge", Subnet: "not a subnet"},
	}
	for _, p := range bad {
		p := p
		if msg := checkNetworkPayload(&p); msg == "" {
			t.Errorf("%+v accepted", p)
		}
	}
}
