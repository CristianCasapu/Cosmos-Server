// Package security is the fork's security record: one table of
// security-relevant events (who, from where, what, with which outcome), a
// log file for Fail2ban, a hook for subscribers and the queries and read API
// the dashboard and the audit panel are built on.
//
// Fork addition: nothing here exists upstream. The rest of the server records
// through utils.RecordSecurity and utils.AuditSecurity (utils/fork_security.go);
// this package is started once, by security.Start.
package security

import (
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Event is one entry of the record.
type Event = utils.SecurityEvent

// The two classes of events.
const (
	// ClassIncident: hostile or suspicious, usually from someone who is not
	// logged in. Incidents with a client address go to the Fail2ban file.
	ClassIncident = "incident"
	// ClassAudit: an action of an authenticated user.
	ClassAudit = "audit"
)

// Severity orders the events. It is stored as a number so that "at least
// medium" is a comparison.
type Severity int

const (
	SeverityInfo   Severity = 1
	SeverityLow    Severity = 2
	SeverityMedium Severity = 3
	SeverityHigh   Severity = 4
)

var severityNames = map[Severity]string{
	SeverityInfo:   "info",
	SeverityLow:    "low",
	SeverityMedium: "medium",
	SeverityHigh:   "high",
}

func (s Severity) String() string {
	if name, ok := severityNames[s]; ok {
		return name
	}
	return "info"
}

// ParseSeverity reads "info", "low", "medium" or "high".
func ParseSeverity(name string) (Severity, bool) {
	for severity, known := range severityNames {
		if known == name {
			return severity, true
		}
	}
	return 0, false
}

// The outcomes of an event.
const (
	OutcomeOK      = "ok"
	OutcomeFailed  = "failed"
	OutcomeDenied  = "denied"
	OutcomeBlocked = "blocked"
)

var outcomes = map[string]bool{OutcomeOK: true, OutcomeFailed: true, OutcomeDenied: true, OutcomeBlocked: true}

// Kind describes one kind of event.
type Kind struct {
	Kind     string `json:"kind"`
	Class    string `json:"class"`
	Severity string `json:"severity"`
	// Outcome is the outcome an event of this kind has when the caller gives
	// none.
	Outcome string `json:"outcome"`
	// Label is a short English name, Description one sentence for an
	// administrator.
	Label       string `json:"label"`
	Description string `json:"description"`

	severity Severity
}

func incident(kind string, severity Severity, outcome string, label string, description string) Kind {
	return Kind{Kind: kind, Class: ClassIncident, Severity: severity.String(), Outcome: outcome, Label: label, Description: description, severity: severity}
}

func audit(kind string, severity Severity, label string, description string) Kind {
	return Kind{Kind: kind, Class: ClassAudit, Severity: severity.String(), Outcome: OutcomeOK, Label: label, Description: description, severity: severity}
}

// catalogue is every kind there is. Severity "high" is only given to kinds
// that someone who is not logged in cannot produce at will: this is what
// keeps a flood from pushing the important rows out (see limiter.go and the
// per-kind caps in store.go).
var catalogue = func() map[string]Kind {
	kinds := []Kind{
		audit(utils.SecLoginSuccess, SeverityInfo, "Login", "A user logged in with the right password. Details: mfa says whether a two-factor code is still to come."),
		incident(utils.SecLoginFailed, SeverityMedium, OutcomeFailed, "Failed login", "A login was refused. Target: the name that was tried. Details: reason is unknown_user, wrong_password, not_registered or bad_request."),
		audit(utils.SecLogout, SeverityInfo, "Logout", "A user logged out."),
		audit(utils.SecMFASuccess, SeverityInfo, "Two-factor code accepted", "A user gave the right two-factor code."),
		incident(utils.SecMFAFailed, SeverityMedium, OutcomeFailed, "Wrong two-factor code", "A logged-in session gave a wrong two-factor code, or tried while the account's code check was closed (reason locked)."),
		incident(utils.SecMFALocked, SeverityHigh, OutcomeBlocked, "Two-factor check locked", "Too many wrong two-factor codes: the code check of the account is closed for a while. Whoever tried has the account's password."),
		audit(utils.SecMFASetup, SeverityLow, "Two-factor secret issued", "A new two-factor secret was issued to a user who had none in use."),
		audit(utils.SecSudoSuccess, SeverityLow, "Password confirmed", "A user confirmed the password to make changes (sudo)."),
		incident(utils.SecSudoFailed, SeverityMedium, OutcomeFailed, "Wrong password at the confirmation prompt", "A logged-in session gave a wrong password when asked to confirm it (sudo)."),
		incident(utils.SecSessionForged, SeverityMedium, OutcomeDenied, "Forged session", "A session cookie that this server did not sign, or that was altered, was presented."),
		incident(utils.SecSessionRejected, SeverityLow, OutcomeDenied, "Stale session", "A session cookie that is no longer valid was presented. Details: reason is expired, password_changed, unknown_user, wrong_domain, no_role or other. Usually harmless."),
		incident(utils.SecResetRequested, SeverityLow, OutcomeOK, "Password reset asked for", "The \"forgot password\" form was sent with the name and email of an account: a reset link is mailed (at most one a minute). Target: the account. The form is open to anyone, hence an incident: harmless when the user did it."),
		incident(utils.SecResetRejected, SeverityLow, OutcomeFailed, "Password reset for an unknown account", "The \"forgot password\" form was sent with a name and email that match no account. Target: the name that was tried."),
		audit(utils.SecPasswordChanged, SeverityMedium, "Password changed", "A user set a new password through a reset link."),
		incident(utils.SecRegisterFailed, SeverityMedium, OutcomeFailed, "Bad invitation or reset link", "The registration form was sent with a key that is wrong or expired. Target: the name that was tried."),

		audit(utils.SecUserCreated, SeverityMedium, "User created", "An administrator created an account. Target: the new account. Details: role."),
		audit(utils.SecUserDeleted, SeverityMedium, "User deleted", "An account was deleted. Target: the account. Details: self is yes when the user deleted the own account."),
		audit(utils.SecUserEdited, SeverityLow, "User edited", "The email of an account was changed. Target: the account."),
		audit(utils.SecUserRoleChanged, SeverityHigh, "Role changed", "The role of an account was changed. Target: the account. Details: role is the new one."),
		audit(utils.SecUserInviteCreated, SeverityLow, "Invitation link made", "An administrator made a new invitation link for an account. Target: the account."),
		audit(utils.SecUserInviteUsed, SeverityLow, "Invitation used", "An invited user set a password: the account is in use from now on."),
		audit(utils.SecUserResetCreated, SeverityMedium, "Password reset link made", "An administrator made a password reset link for an account. Target: the account."),
		audit(utils.SecUserMFAReset, SeverityMedium, "Two-factor reset", "An administrator removed the two-factor secret of an account. Target: the account."),

		audit(utils.SecTokenCreated, SeverityMedium, "API token created", "An API token was created. Target: its name."),
		audit(utils.SecTokenUpdated, SeverityMedium, "API token changed", "An API token was changed. Target: its name."),
		audit(utils.SecTokenDeleted, SeverityMedium, "API token deleted", "An API token was deleted. Target: its name."),
		incident(utils.SecTokenInvalid, SeverityMedium, OutcomeDenied, "Invalid API token", "A request carried an API token that does not exist."),
		incident(utils.SecTokenExpired, SeverityLow, OutcomeDenied, "Expired API token", "A request carried an API token that has expired. Target: its name."),
		incident(utils.SecTokenIPDenied, SeverityMedium, OutcomeDenied, "API token from a wrong address", "A valid API token was used from an address it is not allowed from. Target: its name."),

		incident(utils.SecAccessDenied, SeverityMedium, OutcomeDenied, "Permission refused", "A logged-in user or an API token asked for something it has no permission for. Details: permission."),
		incident(utils.SecAccessGrantDenied, SeverityHigh, OutcomeDenied, "Refused to grant a permission", "Someone who may manage users, roles or tokens tried to hand out a permission they do not hold themselves."),
		incident(utils.SecAccessIPDenied, SeverityMedium, OutcomeDenied, "Address not allowed", "A request was refused by an address restriction (the admin whitelist or a URL's own)."),
		incident(utils.SecAccessLocalDenied, SeverityLow, OutcomeDenied, "Local address asked from outside", "An address that is served on the local network only was requested from outside it."),

		incident(utils.SecShieldBlocked, SeverityMedium, OutcomeBlocked, "Blocked by SmartShield", "A request was refused because its sender is over a SmartShield budget or banned. Details: route."),
		incident(utils.SecShieldStrike, SeverityMedium, OutcomeBlocked, "SmartShield strike", "A client went over a SmartShield budget and is blocked for an hour. Details: limit, route."),
		incident(utils.SecShieldBanTemp, SeverityHigh, OutcomeBlocked, "Temporary ban", "SmartShield banned a client temporarily (three strikes). Details: route."),
		incident(utils.SecShieldBanPerm, SeverityHigh, OutcomeBlocked, "Permanent ban", "SmartShield banned a client until it is unbanned (three temporary bans in a week)."),
		audit(utils.SecShieldUnban, SeverityMedium, "Unbanned", "An administrator removed the strikes and bans of a client. Target: the client."),
		incident(utils.SecShieldGeo, SeverityLow, OutcomeBlocked, "Blocked by country", "A request was refused by the country filter. Details: country."),
		incident(utils.SecShieldBot, SeverityLow, OutcomeBlocked, "Bot blocked", "A request was refused by a URL's bot filter (no or a listed User-Agent)."),
		incident(utils.SecShieldHostname, SeverityLow, OutcomeBlocked, "Unknown hostname", "A request named a hostname this server does not serve: typically a scan of the IP address."),
		incident(utils.SecShieldRateLimited, SeverityLow, OutcomeBlocked, "Too many requests", "An address sent more than 500 requests a minute to the API and was slowed down."),
		incident(utils.SecScanProbe, SeverityMedium, OutcomeBlocked, "Probe for a vulnerable path", "A path that scanners ask for (/.env, /wp-login.php, /.git/config and the like) was requested and does not exist here. Details: probe is the family."),

		audit(utils.SecConfigSaved, SeverityMedium, "Settings saved", "The settings were saved. Details: sections lists the parts that changed, never their values."),
		audit(utils.SecRouteCreated, SeverityMedium, "URL created", "A URL (route) was created. Target: its name."),
		audit(utils.SecRouteUpdated, SeverityMedium, "URL changed", "A URL (route) was changed. Target: its name."),
		audit(utils.SecRouteDeleted, SeverityMedium, "URL deleted", "A URL (route) was deleted. Target: its name."),
		audit(utils.SecContainerAction, SeverityLow, "Container action", "A container was started, stopped, restarted, killed, paused, recreated or updated. Target: the container. Details: action."),
		audit(utils.SecContainerDeleted, SeverityMedium, "Container deleted", "A container was deleted. Target: the container."),
		audit(utils.SecContainerCreated, SeverityMedium, "Containers created", "Containers, networks or volumes were created from a compose description."),
		audit(utils.SecContainerEdited, SeverityLow, "Container edited", "The settings of a container were changed. Target: the container."),
		audit(utils.SecContainerTerminal, SeverityMedium, "Container terminal opened", "A terminal was opened in a container. Target: the container. Details: mode is new (a shell), attach (the container's own input and output) or attach_readonly."),
		audit(utils.SecHostTerminal, SeverityHigh, "Host terminal opened", "A terminal was opened on the server itself, as root."),
		audit(utils.SecServerUpdate, SeverityMedium, "Server update started", "An administrator asked for the server to be updated now."),
		incident(utils.SecServerUpdateRejected, SeverityHigh, OutcomeBlocked, "Server update refused", "A release was not installed because it could not be verified: no signature of the fork's release key, a file that does not match the signed checksum, a download from somewhere else than GitHub, or an older release. The server keeps running its version. Target: the release. Details: reason; by is server (before the download was placed) or launcher (before unpacking)."),
		audit(utils.SecServerRestart, SeverityMedium, "Restart asked for", "An administrator restarted Cosmos or the machine. Details: scope is cosmos or host."),
		audit(utils.SecSnapshotRestore, SeverityHigh, "Snapshot restore started", "A folder is being replaced by one of its snapshots. Details: snapshot."),
		audit(utils.SecAPIChange, SeverityLow, "Change through the API", "A logged-in user sent a request that changes something and that has no kind of its own. Path and method say which."),

		incident(utils.SecRecorderDropped, SeverityMedium, OutcomeFailed, "Events lost", "The record could not keep up (the database did not answer): Count events were not stored."),
	}
	byKind := make(map[string]Kind, len(kinds))
	for _, kind := range kinds {
		byKind[kind.Kind] = kind
	}
	return byKind
}()

// Kinds lists the catalogue, sorted by identifier.
func Kinds() []Kind {
	kinds := make([]Kind, 0, len(catalogue))
	for _, kind := range catalogue {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	return kinds
}

// KindOf looks a kind up.
func KindOf(kind string) (Kind, bool) {
	found, ok := catalogue[kind]
	return found, ok
}

// kindsMatching expands a filter entry: a kind, or a prefix ending in ".*"
// ("auth.*"). Unknown entries match nothing.
func kindsMatching(pattern string) []string {
	if prefix, isPrefix := strings.CutSuffix(pattern, "*"); isPrefix {
		if prefix == "" || !strings.HasSuffix(prefix, ".") {
			return nil
		}
		var matching []string
		for kind := range catalogue {
			if strings.HasPrefix(kind, prefix) {
				matching = append(matching, kind)
			}
		}
		sort.Strings(matching)
		return matching
	}
	if _, ok := catalogue[pattern]; ok {
		return []string{pattern}
	}
	return nil
}
