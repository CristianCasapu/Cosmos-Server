package cloudflare

// Fork addition: the client talks to one host, follows no redirect, reads a
// bounded answer and puts nothing but checked ids into a path.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type scripted func(req *http.Request) *http.Response

func (s scripted) RoundTrip(req *http.Request) (*http.Response, error) { return s(req), nil }

func respond(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func withTransport(t *testing.T, transport http.RoundTripper) {
	previous := apiTransport
	apiTransport = transport
	t.Cleanup(func() { apiTransport = previous })
}

func TestClientFollowsNoRedirect(t *testing.T) {
	calls := []string{}
	withTransport(t, scripted(func(req *http.Request) *http.Response {
		calls = append(calls, req.URL.String())
		if req.Header.Get("Authorization") != "Bearer "+goodToken {
			t.Errorf("the token is missing from the request to %s", req.URL.Host)
		}
		return respond(http.StatusFound, http.Header{"Location": {"https://elsewhere.example.net/collect"}}, "")
	}))

	_, err := NewClient(goodToken).VerifyToken(context.Background())
	if err == nil {
		t.Fatal("a redirect was taken as an answer")
	}
	if len(calls) != 1 || calls[0] != "https://api.cloudflare.com/client/v4/user/tokens/verify" {
		t.Errorf("requests made: %v", calls)
	}
	if strings.Contains(err.Error(), goodToken) {
		t.Error("the error names the token")
	}
}

func TestClientReadsABoundedAnswerAndCleansMessages(t *testing.T) {
	withTransport(t, scripted(func(req *http.Request) *http.Response {
		return respond(200, nil, `{"success":true,"result":"`+strings.Repeat("x", maxAnswer)+`"}`)
	}))
	if _, err := NewClient(goodToken).VerifyToken(context.Background()); err == nil || kindOf(err) != "other" {
		t.Errorf("an answer over the limit: %v", err)
	}

	withTransport(t, scripted(func(req *http.Request) *http.Response {
		return respond(403, nil, `{"success":false,"errors":[{"code":10000,"message":"line one\nline two\u0007 `+strings.Repeat("y", 500)+`"}]}`)
	}))
	_, err := NewClient(goodToken).VerifyToken(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Kind != "forbidden" || apiErr.Code != 10000 {
		t.Fatalf("error: %v", err)
	}
	if strings.ContainsAny(apiErr.Message, "\n\a") || len(apiErr.Message) > 210 {
		t.Errorf("message not cleaned: %q", apiErr.Message)
	}

	// an answer that is not JSON: no part of it is passed on
	withTransport(t, scripted(func(req *http.Request) *http.Response {
		return respond(502, nil, "<html><script>alert(1)</script></html>")
	}))
	_, err = NewClient(goodToken).VerifyToken(context.Background())
	if kindOf(err) != "server" || strings.Contains(err.Error(), "script") {
		t.Errorf("a gateway page: %v", err)
	}
}

func TestOnlyCheckedIdsReachAPath(t *testing.T) {
	withTransport(t, scripted(func(req *http.Request) *http.Response {
		t.Errorf("a request was made: %s", req.URL)
		return respond(200, nil, `{"success":true,"result":[]}`)
	}))
	client := NewClient(goodToken)
	for _, id := range []string{"", "../../user/tokens", "abc", strings.Repeat("g", 32), strings.Repeat("a", 33), strings.Repeat("a", 31) + "/"} {
		if _, _, err := client.ListRecords(context.Background(), id, ""); err == nil {
			t.Errorf("zone id %q was accepted", id)
		}
		if err := client.SetProxied(context.Background(), strings.Repeat("a", 32), id, true, 60); err == nil {
			t.Errorf("record id %q was accepted", id)
		}
		if err := client.CreateRecord(context.Background(), id, Record{}); err == nil {
			t.Errorf("zone id %q was accepted for a new record", id)
		}
		if got := client.CanEdit(context.Background(), id); got != "unknown" {
			t.Errorf("CanEdit(%q) = %s", id, got)
		}
		if _, err := client.SSLMode(context.Background(), id); err == nil {
			t.Errorf("zone id %q was accepted for the SSL mode", id)
		}
	}
}

func TestZonesWithAnIdThatIsNoIdAreDropped(t *testing.T) {
	withTransport(t, scripted(func(req *http.Request) *http.Response {
		return respond(200, nil, `{"success":true,"result":[
			{"id":"../../accounts","name":"evil.example"},
			{"id":"0123456789abcdef0123456789abcdef","name":"Example.ORG."}
		],"result_info":{"page":1,"per_page":50,"count":2,"total_count":2,"total_pages":1}}`)
	}))
	zones, more, err := NewClient(goodToken).ListZones(context.Background(), "")
	if err != nil || more || len(zones) != 1 || zones[0].Name != "example.org" {
		t.Errorf("zones: %+v %v %v", zones, more, err)
	}
}
