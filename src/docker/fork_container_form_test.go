package docker

import (
	"encoding/json"
	"testing"
)

// What the Network and Volumes tabs of a container send must reach the container as
// sent: the address a port is bound to, and the read-only flag of a mount.
func TestContainerFormKeepsHostIPAndReadOnly(t *testing.T) {
	body := `{
		"portBindings": {
			"8080/tcp": [{"HostIp": "127.0.0.1", "HostPort": "8080"}],
			"53/udp": [{"HostIp": "", "HostPort": "53"}, {"HostIp": "::1", "HostPort": "5353"}]
		},
		"Volumes": [
			{"Type": "bind", "Source": "/archive/media", "Target": "/media", "ReadOnly": true},
			{"Type": "volume", "Source": "jellyfin-config", "Target": "/config", "ReadOnly": false}
		]
	}`

	var form ContainerForm
	if err := json.Unmarshal([]byte(body), &form); err != nil {
		t.Fatal(err)
	}

	web := form.PortBindings["8080/tcp"]
	if len(web) != 1 || web[0].HostIP != "127.0.0.1" || web[0].HostPort != "8080" {
		t.Errorf("8080/tcp = %+v, want 127.0.0.1:8080", web)
	}
	dns := form.PortBindings["53/udp"]
	if len(dns) != 2 || dns[0].HostIP != "" || dns[0].HostPort != "53" || dns[1].HostIP != "::1" || dns[1].HostPort != "5353" {
		t.Errorf("53/udp = %+v, want every address:53 and ::1:5353", dns)
	}

	if len(form.Volumes) != 2 || !form.Volumes[0].ReadOnly || form.Volumes[1].ReadOnly {
		t.Errorf("volumes = %+v, want the first read-only and the second not", form.Volumes)
	}
}
