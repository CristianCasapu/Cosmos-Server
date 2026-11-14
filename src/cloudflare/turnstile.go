package cloudflare

// Fork addition: making a Turnstile widget for the panel's sign-in through
// Cloudflare's API (src/turnstile asks for it). One call:
//
//	POST /accounts/{account_id}/challenges/widgets {name, domains, mode}
//
// which needs the permission "Turnstile Sites Write" (dashboard: Account >
// Turnstile > Edit) or "Account Settings Write". The "Edit zone DNS" token
// Cosmos keeps has neither: Cloudflare then answers 403, and the caller says
// which permission to add. The account is the one that holds the panel's
// zone (the zone's account.id). The secret key of the new widget is returned
// to the caller only; it is never logged and never part of an error.

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// TurnstileWidget is what Cloudflare answers for a new widget.
type TurnstileWidget struct {
	SiteKey string
	Secret  string
	Mode    string
	Domains []string
}

var turnstileKeyPattern = regexp.MustCompile(`^[0-9A-Za-z_-]{10,100}$`)

// TurnstileZone finds the configured Cloudflare domain that holds name (the
// panel's public hostname) and returns its stored token and its name.
func TurnstileZone(name string) (token string, zoneName string, ok bool) {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	best := ""
	for _, zone := range utils.GetMainConfig().HTTPConfig.DNSZones {
		zoneLower := strings.ToLower(zone.Zone)
		if (name == zoneLower || strings.HasSuffix(name, "."+zoneLower)) && len(zoneLower) > len(best) {
			if stored := storedToken(zone); stored != "" {
				best, token = zoneLower, stored
			}
		}
	}
	return token, best, best != ""
}

// CreateTurnstileWidget makes a widget in the account that holds zoneName,
// for exactly the hostnames given.
func CreateTurnstileWidget(ctx context.Context, token string, zoneName string, widgetName string, hostnames []string, mode string) (TurnstileWidget, error) {
	client := NewClient(token)
	zones, _, err := client.ListZones(ctx, zoneName)
	if err != nil {
		return TurnstileWidget{}, err
	}
	account := ""
	for _, zone := range zones {
		if zone.Name == zoneName {
			account = strings.TrimSpace(zone.Account.ID)
		}
	}
	if !validID(account) {
		// the zone is not visible to the token, or Cloudflare named no account
		return TurnstileWidget{}, &APIError{Kind: "not_found", Message: "the account of the domain is not known"}
	}

	var result struct {
		SiteKey string   `json:"sitekey"`
		Secret  string   `json:"secret"`
		Mode    string   `json:"mode"`
		Domains []string `json:"domains"`
	}
	body := map[string]interface{}{"name": widgetName, "domains": hostnames, "mode": mode}
	if _, err := client.call(ctx, "POST", "/accounts/"+account+"/challenges/widgets", nil, body, &result); err != nil {
		return TurnstileWidget{}, err
	}
	if !turnstileKeyPattern.MatchString(result.SiteKey) || !turnstileKeyPattern.MatchString(result.Secret) {
		return TurnstileWidget{}, &APIError{Kind: "other", Message: "the answer is not in the expected form"}
	}
	return TurnstileWidget{SiteKey: result.SiteKey, Secret: result.Secret, Mode: result.Mode, Domains: result.Domains}, nil
}

// TurnstileErrorKind is the kind of a failed call ("forbidden" when the
// token lacks the permission).
func TurnstileErrorKind(err error) string { return kindOf(err) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// ForkTestTransport replaces what carries the calls to Cloudflare's API, for
// the tests of other packages (src/turnstile), and returns what puts it back.
func ForkTestTransport(roundTrip func(*http.Request) (*http.Response, error)) func() {
	previous := apiTransport
	apiTransport = roundTripFunc(roundTrip)
	return func() { apiTransport = previous }
}
