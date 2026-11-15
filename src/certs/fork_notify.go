package certs

// What the administrators are told about certificates, in words: which
// domain, why, until when the current one lasts, what Cosmos does and where
// to look. A renewal ends the notices of a failure (thread "cert:<domain>").

import (
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

const cardLink = "/cosmos-ui/config-general/https"

// DomainOfKey is the domain a certificate's key is about.
func DomainOfKey(key string) string {
	return strings.TrimPrefix(strings.TrimPrefix(key, "zone:"), "local:")
}

// Thread is what a domain's certificate notices are grouped and resolved by.
func Thread(domain string) string { return "cert:" + domain }

func cardStep() utils.NotificationStep {
	return utils.NotificationStep{NotificationText: *utils.NText("notif.step.cert.card"), Link: cardLink}
}

func failureInfo(key string, message string, reason error, known certState) *utils.NotificationInfo {
	domain := DomainOfKey(key)
	why := FailureSentence(known.LastCode)
	raw := message
	if reason != nil {
		raw += ": " + reason.Error()
		if known.LastCode == "" {
			why = reason.Error()
		}
	}
	return &utils.NotificationInfo{
		Kind:    "cert.order.failed",
		Thread:  Thread(domain),
		Subject: &utils.NotificationSubject{Type: "domain", Name: domain, Link: cardLink},
		Title:   utils.NText("notif.cert.order.failed.title", "domain", domain),
		What:    utils.NText("notif.cert.order.failed.what", "domain", domain, "reason", why),
		Means:   utils.NText("notif.cert.means.generic", "domain", domain),
		Doing:   utils.NText("notif.cert.doing.retry"),
		Steps: []utils.NotificationStep{cardStep(), {NotificationText: *utils.NText("notif.step.cert.dns", "domain", domain)},
			{NotificationText: *utils.NText("notif.step.cert.renew")}},
		Raw:   raw,
		Facts: []utils.NotificationFact{{Key: "notif.fact.domain", Value: domain}, {Key: "notif.fact.reason", Value: why}},
	}
}

func expiringInfo(cert Certificate, left string, why string) *utils.NotificationInfo {
	until := cert.NotAfter.Local().Format("2006-01-02")
	what := utils.NText("notif.cert.expiring.what", "domain", cert.Domain, "until", until, "left", left, "why", why)
	if cert.Expired {
		what = utils.NText("notif.cert.expired.what", "domain", cert.Domain, "until", until, "why", why)
	}
	return &utils.NotificationInfo{
		Kind:    "cert.expiring",
		Thread:  Thread(cert.Domain),
		Subject: &utils.NotificationSubject{Type: "domain", Name: cert.Domain, Link: cardLink},
		Title:   utils.NText("notif.cert.expiring.title", "domain", cert.Domain),
		What:    what,
		Means:   utils.NText("notif.cert.expiring.means", "domain", cert.Domain),
		Doing:   utils.NText("notif.cert.expiring.doing"),
		Steps: []utils.NotificationStep{cardStep(), {NotificationText: *utils.NText("notif.step.cert.dns", "domain", cert.Domain)},
			{NotificationText: *utils.NText("notif.step.cert.renew")}},
		Facts: []utils.NotificationFact{{Key: "notif.fact.domain", Value: cert.Domain}, {Key: "notif.fact.until", Value: until}, {Key: "notif.fact.reason", Value: why}},
	}
}

func untrustedInfo(hosts string) *utils.NotificationInfo {
	return &utils.NotificationInfo{
		Kind:    "cert.untrusted",
		Thread:  "cert.untrusted",
		Subject: &utils.NotificationSubject{Type: "domain", Name: hosts, Link: cardLink},
		What:    utils.NText("notif.cert.untrusted.what", "hosts", hosts),
		Means:   utils.NText("notif.cert.untrusted.means"),
		Doing:   utils.NText("notif.doing.nothing"),
		Steps:   []utils.NotificationStep{cardStep()},
	}
}

// RenewedInfo is the info of upstream's "certificate renewed"; it resolves
// the notices of a failure or an end for that domain.
func RenewedInfo(domain string, names []string) *utils.NotificationInfo {
	utils.ResolveThread(Thread(domain))
	list := strings.Join(names, ", ")
	return &utils.NotificationInfo{
		Kind:    "cert.renewed",
		Thread:  "cert.renewed:" + domain,
		Subject: &utils.NotificationSubject{Type: "domain", Name: domain, Link: cardLink},
		What:    utils.NText("notif.cert.renewed.what", "domains", list),
		Means:   utils.NText("notif.cert.renewed.means"),
		Steps:   []utils.NotificationStep{cardStep()},
		Facts:   []utils.NotificationFact{{Key: "notif.fact.domain", Value: list}},
	}
}
