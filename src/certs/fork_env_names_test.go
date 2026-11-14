package certs

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: every variable a DNS provider of the catalogue declares can
// still be set (utils/fork_env_names.go refuses the dangerous ones).
func TestEveryProviderVariableIsASafeName(t *testing.T) {
	count := 0
	for code, provider := range Providers() {
		for _, list := range [][]Variable{provider.Credentials, provider.Optional} {
			for _, v := range list {
				count++
				if !utils.SafeEnvName(v.Name) {
					t.Errorf("%s: %s is refused", code, v.Name)
				}
			}
		}
	}
	if count < 1000 {
		t.Errorf("only %d variables in the catalogue", count)
	}
}
