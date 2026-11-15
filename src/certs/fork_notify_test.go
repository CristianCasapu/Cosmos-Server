package certs

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestCertificateNoticesSayWhichDomainAndWhere(t *testing.T) {
	info := failureInfo(utils.ForkZoneCertKey("example.org"), "Zones: couldn't get the TLS certificate of example.org", nil, certState{LastCode: FailDNSCredentials})
	if info.Subject.Name != "example.org" || info.Thread != "cert:example.org" || info.Subject.Link != cardLink {
		t.Fatalf("%+v", info)
	}
	if got := utils.RenderNotificationText(*info.What); got != "Cosmos could not obtain a certificate for example.org: The DNS provider refused the credentials" {
		t.Errorf("%q", got)
	}
	cert := Certificate{Domain: "example.org", NotAfter: time.Date(2026, 11, 27, 0, 0, 0, 0, time.Local)}
	exp := expiringInfo(cert, "12 days left", "it was not renewed yet")
	if got := utils.RenderNotificationText(*exp.What); got != "The certificate of example.org ends on 2026-11-27 (12 days left) and has not been renewed: it was not renewed yet." {
		t.Errorf("%q", got)
	}
	if exp.Thread != info.Thread {
		t.Error("a renewal must end both")
	}
	for _, i := range []*utils.NotificationInfo{info, exp, untrustedInfo("a.example.org"), RenewedInfo("example.org", []string{"example.org", "*.example.org"})} {
		for _, text := range []*utils.NotificationText{i.What, i.Means, i.Doing} {
			if text != nil && (utils.NotificationEnglish(text.Key) == "" || strings.Contains(utils.RenderNotificationText(*text), "{{")) {
				t.Errorf("%s: %s %q", i.Kind, text.Key, utils.RenderNotificationText(*text))
			}
		}
	}
}

func TestAFailedOrderCarriesItsDomain(t *testing.T) {
	env := setup(t, nil)
	NoticeFailure(utils.ForkZoneCertKey("example.org"), "Zones: couldn't get the TLS certificate of example.org", nil)
	if len(env.infos) != 1 || env.infos[0] == nil || env.infos[0].Subject.Name != "example.org" {
		t.Fatalf("%+v", env.infos)
	}
}
