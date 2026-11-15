package anyimage

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/references.json holds the cases shared with the panel's rules
// (client/src/utils/anyImage.test.mjs reads the same file).
type referenceCase struct {
	Text     string `json:"text"`
	LoggedIn string `json:"loggedIn,omitempty"`
	Want     string `json:"want,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

func loadReferenceCases(t *testing.T) []referenceCase {
	data, err := os.ReadFile("testdata/references.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []referenceCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("only %d cases", len(cases))
	}
	return cases
}

func TestParseReferences(t *testing.T) {
	for _, c := range loadReferenceCases(t) {
		ref, err := Parse(c.Text, func(host string) bool { return c.LoggedIn != "" && host == c.LoggedIn })
		if c.Problem != "" {
			if err == nil || codeOf(err) != c.Problem {
				t.Errorf("%q: want %s, got %v (%+v)", c.Text, c.Problem, err, ref)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.Text, err)
			continue
		}
		if got := ref.Registry + " " + ref.Repository + " " + ref.Tag; got != c.Want {
			t.Errorf("%q: want %q, got %q", c.Text, c.Want, got)
		}
	}
}

func TestReferenceStringAndPage(t *testing.T) {
	ref, _ := Parse("postgres", nil)
	if ref.String() != "postgres:latest" || ref.Name() != "postgres" || ref.Page() != "https://hub.docker.com/_/postgres" {
		t.Fatalf("%s %s %s", ref.String(), ref.Name(), ref.Page())
	}
	ref, _ = Parse("valkey/valkey:9", nil)
	if ref.String() != "valkey/valkey:9" || ref.Page() != "https://hub.docker.com/r/valkey/valkey" || ref.registryHost() != "registry-1.docker.io" {
		t.Fatalf("%s %s", ref.String(), ref.Page())
	}
	ref, _ = Parse("ghcr.io/owner/app:1.2", nil)
	if ref.String() != "ghcr.io/owner/app:1.2" || ref.registryHost() != "ghcr.io" {
		t.Fatal(ref.String())
	}
}
