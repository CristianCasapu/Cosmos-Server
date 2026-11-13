package utils

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Fork addition: tests of fork_redact_url.go.

func TestRedactQuery(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"page=2&sort=name":                    "page=2&sort=name",
		"t=1&nickname=alice&key=SECRETKEY":    "t=1&nickname=alice&key=***",
		"token=cosmos_abcdef":                 "token=***",
		"Token=cosmos_abcdef&x=1":             "Token=***&x=1",
		"code=OAUTHCODE&state=xyz":            "code=***&state=xyz",
		"a=1&access_token=AT&b=2&password=PW": "a=1&access_token=***&b=2&password=***",
		"key":                                 "key",
		"%6bey=ENCODEDNAME":                   "%6bey=***",
		"monkey=banana&keyboard=qwerty":       "monkey=banana&keyboard=qwerty",
		"key=":                                "key=***",
	}
	for in, want := range cases {
		if got := RedactQuery(in); got != want {
			t.Errorf("RedactQuery(%q) = %q, want %q", in, got, want)
		}
	}
	u, _ := url.Parse("https://user:pw@cosmos.example.com/cosmos-ui/register?t=1&nickname=alice&key=SECRETKEY")
	if got := RedactedURL(u).String(); strings.Contains(got, "SECRETKEY") || strings.Contains(got, "pw@") || !strings.Contains(got, "nickname=alice") {
		t.Errorf("RedactedURL: %s", got)
	}
	if u.RawQuery != "t=1&nickname=alice&key=SECRETKEY" {
		t.Error("RedactedURL changed the address it was given")
	}
	if got := RedactedRequestURI("/cosmos/api/terminal/bash?token=cosmos_abc"); got != "/cosmos/api/terminal/bash?token=***" {
		t.Errorf("RedactedRequestURI: %s", got)
	}
}

// The request log and the event of a refused request are where an address
// is written down.
func TestSecretsOfAnAddressAreNotWrittenDown(t *testing.T) {
	prev := GetBaseMainConfig()
	config := Config{LoggingLevel: "INFO"}
	config.HTTPConfig.Hostname = "cosmos.example.com"
	LoadBaseMainConfig(config)
	var out bytes.Buffer
	prevWriter := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
		LoadBaseMainConfig(prev)
	})

	handler := Logger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos-ui/register?t=1&nickname=alice&key=INVITATIONKEY", nil)
	req.RequestURI = "/cosmos-ui/register?t=1&nickname=alice&key=INVITATIONKEY"
	handler.ServeHTTP(httptest.NewRecorder(), req)
	req = httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/terminal/bash?token=cosmos_THETOKEN", nil)
	req.RequestURI = "/cosmos/api/terminal/bash?token=cosmos_THETOKEN"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	logged := out.String()
	if !strings.Contains(logged, "/cosmos-ui/register") {
		t.Fatalf("the request log did not write the requests: %q", logged)
	}
	if strings.Contains(logged, "INVITATIONKEY") || strings.Contains(logged, "cosmos_THETOKEN") {
		t.Errorf("the request log holds a secret of the address: %q", logged)
	}
	if !strings.Contains(logged, "nickname=alice") {
		t.Errorf("the rest of the address should stay readable: %q", logged)
	}
}
