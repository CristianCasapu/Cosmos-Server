package constellation

import "testing"

func TestUnitDeviceHostname(t *testing.T) {
	cases := []struct{ device, domain, want string }{
		{"node-2", "example.com", "node-2.example.com"},
		{"Node_2", "Example.com", "node-2.example.com"},
		{"_edge_", "lab.example.com", "edge.lab.example.com"},
		{"node2", "", ""},
		{"___", "example.com", ""},
	}
	for _, c := range cases {
		if got := DeviceHostname(c.device, c.domain); got != c.want {
			t.Errorf("DeviceHostname(%q, %q) = %q, want %q", c.device, c.domain, got, c.want)
		}
	}
}

func TestUnitPreviewJoinHostname(t *testing.T) {
	got, err := PreviewJoinHostname([]byte("cstln_device_name: node_3\ncstln_cluster_domain: example.com\n"))
	if err != nil || got != "node-3.example.com" {
		t.Errorf("got %q, %v", got, err)
	}

	// files from a cluster without a domain: the caller has to give a hostname
	got, err = PreviewJoinHostname([]byte("cstln_device_name: node_3\n"))
	if err != nil || got != "" {
		t.Errorf("got %q, %v, want an empty hostname", got, err)
	}

	if _, err := PreviewJoinHostname([]byte("pki: {}\n")); err == nil {
		t.Error("a file without a device name is not a constellation file")
	}
}
