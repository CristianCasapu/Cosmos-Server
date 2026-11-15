package anyimage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutesNeedThePermissionAndPOST(t *testing.T) {
	for _, route := range []http.HandlerFunc{SearchRoute, DescribeRoute} {
		// no session: refused by CheckPermissions
		res := httptest.NewRecorder()
		route(res, httptest.NewRequest(http.MethodPost, "/cosmos/api/images/search", strings.NewReader(`{"query":"x"}`)))
		if res.Code < 400 {
			t.Fatalf("anonymous request answered %d", res.Code)
		}
	}
}

// The handlers' own checks, past the permission (the request carries what
// CheckPermissions reads on a test server).
func TestDecodeBounds(t *testing.T) {
	cases := map[string]struct {
		method, body string
		code         int
	}{
		"GET":           {http.MethodGet, ``, http.StatusMethodNotAllowed},
		"unknown field": {http.MethodPost, `{"query":"x","more":1}`, http.StatusBadRequest},
		"too large":     {http.MethodPost, `{"query":"` + strings.Repeat("a", 2000) + `"}`, http.StatusBadRequest},
		"not JSON":      {http.MethodPost, `query=x`, http.StatusBadRequest},
	}
	for name, c := range cases {
		res := httptest.NewRecorder()
		var body struct {
			Query string `json:"query"`
		}
		if decode(res, httptest.NewRequest(c.method, "/", strings.NewReader(c.body)), &body) || res.Code != c.code {
			t.Errorf("%s: %d", name, res.Code)
		}
	}
}

func TestDescribeOfARefusedReference(t *testing.T) {
	loggedIn = func(string) bool { return false }
	for text, want := range map[string]string{"192.168.1.2:5000/app": "internal", "https://x/y": "invalid", "localhost/app": "internal"} {
		desc := Describe(context.Background(), text)
		if desc.Problem != want || desc.Declared != nil {
			t.Errorf("%s: %+v", text, desc)
		}
	}
}

func TestDescribeCombinesTagsAndRecipe(t *testing.T) {
	f := newFakeRegistry(t, nil)
	serverPlatform = func() Platform { return amd64 }
	loggedIn = func(host string) bool { return false }
	// the fake registry is on 127.0.0.1: allowed for the test by name
	desc := Describe(context.Background(), f.host()+"/team/app:18")
	if desc.Problem != "internal" {
		t.Fatalf("an IP literal registry without a login: %q", desc.Problem)
	}
	loggedIn = func(host string) bool { return host == f.host() }
	desc = Describe(context.Background(), f.host()+"/team/app:18")
	if desc.Problem != "" || desc.Tag != "18" || !desc.TagTyped || desc.Tags.Suggested != "18.1" || desc.Tags.Why != "newest" || desc.Declared == nil {
		t.Fatalf("%+v", desc)
	}
	// without a tag, the proposal is described (here 18.1, which the fake registry does not have)
	desc = Describe(context.Background(), f.host()+"/team/app")
	if desc.Tag != "18.1" || desc.TagTyped || desc.Problem != "notFound" || len(desc.Tags.List) == 0 {
		t.Fatalf("%+v", desc)
	}
	out, _ := json.Marshal(desc)
	if strings.Contains(string(out), fakeToken) {
		t.Fatal("the token is in the answer")
	}
}
