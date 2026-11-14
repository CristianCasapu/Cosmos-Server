package docker

import (
	"encoding/json"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v2"
)

// ExportDocker writes Cosmos' own container as YAML when Cosmos runs in a
// container. The fork's compose types (an unexported embedded struct, a
// struct with unexported fields) made yaml.v2 panic, and Cosmos with it, at
// every start after the setup.
func TestExportOwnContainerAsYAML(t *testing.T) {
	var service ContainerCreateRequestContainer
	if err := json.Unmarshal([]byte(`{"container_name":"cosmos","image":"azukaar/cosmos-server:latest","command":"sh -c 'echo a b'","entrypoint":["/bin/sh","-c"],"shm_size":"64m","read_only":true,"healthcheck":{"test":"curl -f http://localhost","interval":"1m30s"}}`), &service); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]ContainerCreateRequestContainer{"empty": {}, "filled": service} {
		var out []byte
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panic: %v", name, r)
				}
			}()
			var err error
			out, err = yaml.Marshal(map[string]map[string]ContainerCreateRequestContainer{"services": {"cosmos": c}})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}()
		if name == "filled" {
			text := string(out)
			for _, want := range []string{"command: sh -c 'echo a b'", "- /bin/sh", "shmsize: 64m", "readonly: true"} {
				if !strings.Contains(text, want) {
					t.Errorf("%q missing from:\n%s", want, text)
				}
			}
		}
	}
}
