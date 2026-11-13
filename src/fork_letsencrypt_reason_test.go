package main

import (
	"os"
	"strings"
	"testing"
)

func TestLetsEncryptFailureGivesTheLastReason(t *testing.T) {
	err := letsEncryptFailure([]string{
		"acme: error: 429 :: too many failed authorizations",
		"acme: error: 403 :: urn:ietf:params:acme:error:unauthorized ::\n  Invalid response from http://example.com/.well-known/acme-challenge/x: 404",
	})
	if err == nil {
		t.Fatal("a failed order must give its reason")
	}
	want := "acme: error: 403 :: urn:ietf:params:acme:error:unauthorized :: Invalid response from http://example.com/.well-known/acme-challenge/x: 404"
	if err.Error() != want {
		t.Errorf("the reason must be the last one, on one line, got %q", err.Error())
	}
}

func TestLetsEncryptFailureWithoutReason(t *testing.T) {
	if letsEncryptFailure(nil) != nil || letsEncryptFailure([]string{"", "  "}) != nil {
		t.Error("no recorded reason must give no error, not an empty one")
	}
}

func TestLetsEncryptFailureIsBounded(t *testing.T) {
	err := letsEncryptFailure([]string{"a" + strings.Repeat("é", 5000)})
	if err == nil || len(err.Error()) > maxLetsEncryptReason+len("…") {
		t.Fatalf("a long reason must be cut, got %d bytes", len(err.Error()))
	}
	if !strings.HasSuffix(err.Error(), "…") || strings.ContainsRune(err.Error(), '�') {
		t.Errorf("a cut reason must end cleanly, got %q", err.Error()[len(err.Error())-8:])
	}
}

// The notice of a failed order must carry the reason: an upstream merge could
// put the bare notice back without any other test noticing.
func TestFailedOrderNoticeCarriesTheReason(t *testing.T) {
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	from := strings.Index(text, "func issueLocalCerts(")
	to := strings.Index(text, "func localCertsCoverAll(")
	if from < 0 || to < from {
		t.Fatal("issueLocalCerts not found")
	}
	if !strings.Contains(text[from:to], "letsEncryptFailure(utils.LetsEncryptErrors)") {
		t.Error("the notice of a failed Let's Encrypt order must say why")
	}
}
