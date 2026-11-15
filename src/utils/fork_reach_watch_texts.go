package utils

// Fork addition: the kinds and sentences of the reachability watch
// (src/reachwatch), added to the catalogue of fork_notification_texts.go.
// The same keys with the same English are in the panel's translation file
// (TestNotificationTextsAreInThePanel covers them).

// ReachWatchProblemCauses are the outcomes that count as "cannot be reached"
// (each has its sentences below); ReachWatchUnknownCauses those after which
// nothing can be concluded.
var (
	ReachWatchProblemCauses = []string{"dnsNotFound", "dnsNotCloudflare", "edgeCert", "connect", "refused", "unreachable",
		"timeout", "tlsHandshake", "tlsCertificate", "unexpected", "originDNS", "originRefused", "cloudflare", "foreign"}
	ReachWatchUnknownCauses = []string{"dnsLocal", "offline", "noAnswer", "challenged", "edgeBlocked"}
	// the causes after which the router module is asked to check its forwards
	ReachWatchPathCauses = []string{"connect", "refused", "timeout", "foreign"}
)

var reachWatchTexts = map[string]string{
	"notif.reach.down.title":     "Cosmos cannot be reached from the internet",
	"notif.reach.restored.title": "Cosmos can be reached from the internet again",

	// what happened: since when, why, and what Cosmos does (this is what Telegram tells)
	"notif.reach.down.connect.what":          "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare cannot connect to this server on port {{port}} (error 522). The router no longer forwards the port, or your provider or a firewall blocks it. Cosmos keeps testing every few minutes.",
	"notif.reach.down.connect.asked.what":    "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare cannot connect to this server on port {{port}} (error 522). The router no longer forwards the port, or your provider or a firewall blocks it. Cosmos asked the router to forward the port again and keeps testing every few minutes.",
	"notif.reach.down.refused.what":          "Since {{since}} Cosmos cannot be reached from the internet: the connection from Cloudflare to port {{port}} is refused before it reaches Cosmos (error 521). The router has no forward for the port, or a firewall refuses Cloudflare's addresses. Cosmos keeps testing every few minutes.",
	"notif.reach.down.refused.asked.what":    "Since {{since}} Cosmos cannot be reached from the internet: the connection from Cloudflare to port {{port}} is refused before it reaches Cosmos (error 521). The router has no forward for the port, or a firewall refuses Cloudflare's addresses. Cosmos asked the router to forward the port again and keeps testing every few minutes.",
	"notif.reach.down.timeout.what":          "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare connects to port {{port}} but gets no answer (error 524). The port may lead to another machine. Cosmos keeps testing every few minutes.",
	"notif.reach.down.timeout.asked.what":    "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare connects to port {{port}} but gets no answer (error 524). The port may lead to another machine. Cosmos asked the router to forward the port again and keeps testing every few minutes.",
	"notif.reach.down.foreign.what":          "Since {{since}} {{name}} answers from the internet, but not from this server: the router sends port {{port}} to another machine, or the name's record points at someone else. Cosmos keeps testing every few minutes.",
	"notif.reach.down.foreign.asked.what":    "Since {{since}} {{name}} answers from the internet, but not from this server: the router sends port {{port}} to another machine, or the name's record points at someone else. Cosmos asked the router to forward the port to this server again and keeps testing every few minutes.",
	"notif.reach.down.unreachable.what":      "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare finds no way to the address in the record of {{name}} (error 523). The public address probably changed and the record did not follow. Cosmos keeps testing every few minutes.",
	"notif.reach.down.tlsHandshake.what":     "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare and this server could not set up an encrypted connection on port {{port}} (error 525). Cosmos keeps testing every few minutes.",
	"notif.reach.down.tlsCertificate.what":   "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare does not accept this server's certificate for {{name}} (error 526): it has ended, does not cover the name, or is not trusted while Cloudflare is set to Full (strict). Cosmos keeps testing every few minutes.",
	"notif.reach.down.unexpected.what":       "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare gets an answer on port {{port}} that it cannot use (error 520). Cosmos keeps testing every few minutes.",
	"notif.reach.down.originDNS.what":        "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare cannot resolve where {{name}} leads (error 530). Cosmos keeps testing every few minutes.",
	"notif.reach.down.originRefused.what":    "Since {{since}} Cosmos cannot be reached from the internet: Cosmos itself refuses Cloudflare's connections for {{name}}, because they do not show the certificate of Authenticated Origin Pulls. Cosmos keeps testing every few minutes.",
	"notif.reach.down.dnsNotFound.what":      "Since {{since}} {{name}} cannot be reached from the internet: the name does not exist in DNS any more. Cosmos keeps testing every few minutes.",
	"notif.reach.down.dnsNotCloudflare.what": "Since {{since}} {{name}} cannot be reached from the internet: the name no longer leads to Cloudflare's proxy (the proxy was switched off at Cloudflare, or the record changed), and Cosmos answers it only through Cloudflare. Cosmos keeps testing every few minutes.",
	"notif.reach.down.edgeCert.what":         "Since {{since}} visitors of {{name}} get a certificate warning: Cloudflare's certificate for the name is not valid. Cosmos keeps testing every few minutes.",
	"notif.reach.down.cloudflare.what":       "Since {{since}} Cosmos cannot be reached from the internet: Cloudflare answers with an error of its own ({{code}}), not with this server's answer. This is most likely a problem at Cloudflare. Cosmos keeps testing every few minutes.",

	// what it means
	"notif.reach.down.connect.means":          "Nobody outside your network reaches the panel or the apps served through Cloudflare. Cosmos itself is running: the connection is lost between Cloudflare and this server, at the router, at your provider or at a firewall.",
	"notif.reach.down.refused.means":          "Nobody outside your network reaches the panel or the apps served through Cloudflare. Cosmos itself is running and listening: something in front of it, usually the router without the forward, turns the connection away.",
	"notif.reach.down.timeout.means":          "Something accepts connections on the port, but it is not Cosmos, or it hangs. Visitors wait and get an error.",
	"notif.reach.down.foreign.means":          "Visitors of this name do not reach this server. Cosmos never received the test request, although an answer came back.",
	"notif.reach.down.unreachable.means":      "Cloudflare tries the address written in the record and finds no route to it. The address this server has on the internet today is probably another one.",
	"notif.reach.down.tlsHandshake.means":     "Cloudflare reaches the port, but what answers there does not speak HTTPS the way Cloudflare expects.",
	"notif.reach.down.tlsCertificate.means":   "Cloudflare checks the certificate of this server before it passes visitors on, and the check fails. Visitors get Cloudflare's error page.",
	"notif.reach.down.unexpected.means":       "The connection is made, but the answer is empty or broken: another program on the port, or the connection is cut.",
	"notif.reach.down.originDNS.means":        "The record of the name points at another name that does not resolve.",
	"notif.reach.down.originRefused.means":    "Authenticated Origin Pulls are enforced here, but Cloudflare does not present the certificate (it was switched off at Cloudflare, or another certificate is in use there).",
	"notif.reach.down.dnsNotFound.means":      "Visitors' browsers cannot find the name at all.",
	"notif.reach.down.dnsNotCloudflare.means": "Visitors connect straight to the server's address, and Cosmos refuses them because the name is set to be served through Cloudflare.",
	"notif.reach.down.edgeCert.means":         "Browsers do not trust the connection. Cloudflare's free certificate covers the domain and names one level below it, not deeper ones.",
	"notif.reach.down.cloudflare.means":       "There is nothing wrong that Cosmos can see at home. Visitors probably get Cloudflare's error page until Cloudflare repairs it.",

	// what Cosmos does
	"notif.reach.doing.next":     "Cosmos tests again by itself at {{at}} and tells you as soon as the server can be reached again.",
	"notif.reach.restored.means": "A test request went out through Cloudflare and came back in to this server: visitors get through again.",
	"notif.reach.restored.doing": "Cosmos keeps testing every {{minutes}} minutes.",
	"notif.reach.restored.what":  "Since {{at}} Cosmos can be reached from the internet again. It could not be reached for {{duration}}, from {{since}}.",

	// what you can do
	"notif.step.reach.check":            "Test again now, through Cloudflare",
	"notif.step.reach.router":           "Look at the router's forwards and at the internet access settings",
	"notif.step.reach.cloudflareCard":   "Open the Cloudflare card under Domains",
	"notif.step.reach.connect":          "On the router's own page, check that port {{port}} is forwarded to this server. If it is, ask your provider whether they block incoming connections, and check that no firewall drops Cloudflare's addresses.",
	"notif.step.reach.refused":          "On the router's own page, check that port {{port}} is forwarded to this server, and that no firewall rejects Cloudflare's addresses.",
	"notif.step.reach.timeout":          "Check on the router that port {{port}} goes to this server and not to another machine.",
	"notif.step.reach.foreign":          "Check on the router that port {{port}} goes to this server, and at Cloudflare that the record of {{name}} holds this server's public address.",
	"notif.step.reach.unreachable":      "At Cloudflare, put this server's current public address into the record of {{name}}, or let Cosmos keep the record current (Domains).",
	"notif.step.reach.tlsHandshake":     "Check that port {{port}} on the router leads to Cosmos' HTTPS port.",
	"notif.step.reach.tlsCertificate":   "Open Domains and look at the certificate of {{name}}; or set Cloudflare's SSL/TLS mode to Full instead of Full (strict) while the certificate is not a public one.",
	"notif.step.reach.unexpected":       "Check that port {{port}} on the router leads to Cosmos and not to another program.",
	"notif.step.reach.originDNS":        "At Cloudflare, correct the record of {{name}} so that it points at this server's address.",
	"notif.step.reach.originRefused":    "At Cloudflare, check SSL/TLS > Origin Server > Authenticated Origin Pulls, or stop enforcing them on the Cloudflare card.",
	"notif.step.reach.dnsNotFound":      "At Cloudflare (or your DNS provider), create the record of {{name}} again.",
	"notif.step.reach.dnsNotCloudflare": "At Cloudflare, switch the proxy (the orange cloud) on for {{name}} again, or serve the name directly on the Cloudflare card.",
	"notif.step.reach.edgeCert":         "Give the app a name directly under the domain, or order an advanced certificate at Cloudflare.",
	"notif.step.reach.cloudflare":       "Look at www.cloudflarestatus.com: when Cloudflare has an incident, wait.",

	// the cause in a few words (facts, the settings card, the history)
	"notif.reach.cause.seen":             "Reached from the internet",
	"notif.reach.cause.connect":          "Cloudflare cannot connect (error 522)",
	"notif.reach.cause.refused":          "Connection refused before Cosmos (error 521)",
	"notif.reach.cause.timeout":          "No answer on the port (error 524)",
	"notif.reach.cause.foreign":          "Another server answers",
	"notif.reach.cause.unreachable":      "The record's address cannot be reached (error 523)",
	"notif.reach.cause.tlsHandshake":     "No encrypted connection (error 525)",
	"notif.reach.cause.tlsCertificate":   "Certificate not accepted by Cloudflare (error 526)",
	"notif.reach.cause.unexpected":       "An answer Cloudflare cannot use (error 520)",
	"notif.reach.cause.originDNS":        "The record leads nowhere (error 530)",
	"notif.reach.cause.originRefused":    "Cosmos refuses Cloudflare (origin pulls)",
	"notif.reach.cause.dnsNotFound":      "The name does not exist",
	"notif.reach.cause.dnsNotCloudflare": "The name no longer goes through Cloudflare",
	"notif.reach.cause.edgeCert":         "Cloudflare's certificate is not valid",
	"notif.reach.cause.cloudflare":       "An error at Cloudflare",
	"notif.reach.cause.dnsLocal":         "A local DNS answers: cannot be tested from here",
	"notif.reach.cause.offline":          "This server cannot reach the internet: nothing can be concluded",
	"notif.reach.cause.noAnswer":         "Cloudflare did not answer in time: nothing can be concluded",
	"notif.reach.cause.challenged":       "Cloudflare asked the server's own request for a challenge: nothing can be concluded",
	"notif.reach.cause.edgeBlocked":      "Cloudflare blocked the server's own request (a rule at Cloudflare): visitors are not concerned",

	// facts
	"notif.fact.reach.names":  "Names",
	"notif.fact.reach.cause":  "Cause",
	"notif.fact.reach.answer": "Cloudflare's answer",
	"notif.fact.reach.since":  "Since",
	"notif.fact.reach.router": "The router module says",
	"notif.fact.reach.tries":  "Tests in a row that failed",

	// the state now (details view)
	"notif.state.reach.down":    "Right now Cosmos still cannot be reached from the internet (last test {{at}}).",
	"notif.state.reach.ok":      "Right now Cosmos can be reached from the internet: seen from outside at {{at}}.",
	"notif.state.reach.unknown": "The last test could not run ({{at}}): nothing can be concluded right now.",
	"notif.state.reach.off":     "The watch from the internet is switched off now.",
	"notif.state.reach.none":    "No name is tested from the internet now.",
	"notif.state.col.name":      "Name",
	"notif.state.col.state":     "State",
	"notif.state.col.lastTest":  "Last test",
}

func init() {
	notificationKinds["reach.down"] = notificationKind{"cloudflare", "notif.reach.down.title"}
	notificationKinds["reach.restored"] = notificationKind{"cloudflare", "notif.reach.restored.title"}
	for key, text := range reachWatchTexts {
		notificationTexts[key] = text
	}
}
