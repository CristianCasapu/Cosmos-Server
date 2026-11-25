package watch

// A sign-in from a new country: for each successful sign-in from a public
// address, the country is looked up in the GeoLite file the server ships
// (utils.GetIPLocation: local only, the address never leaves the server)
// and compared with the countries this account signed in from before. A new
// one is a warning (login.newcountry). Home, VPN and other private
// addresses are never a country. Only country codes are remembered, a few
// per account, never the addresses; the first sign-in of an account that
// has none remembered (every account, once this ships) only records.

import (
	"net/netip"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxUsers          = 512
	maxCountries      = 16
	maxCountryUserLen = 64
)

var sharedCarrier = netip.MustParsePrefix("100.64.0.0/10")

// publicAddress reports whether an address can be in a country: not
// private, loopback, link-local, multicast, unspecified, nor a carrier's
// shared range.
func publicAddress(ip string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return addr, false
	}
	addr = addr.Unmap().WithZone("")
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || addr.IsInterfaceLocalMulticast() || sharedCarrier.Contains(addr) {
		return addr, false
	}
	return addr, true
}

func validCountry(code string) bool {
	if len(code) != 2 {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// checkSignIn looks at one successful sign-in.
func checkSignIn(st *store, user string, ip string, lookup func(string) (string, error)) {
	user = strings.TrimSpace(user)
	if user == "" || len(user) > maxCountryUserLen {
		return
	}
	addr, public := publicAddress(ip)
	if !public {
		return
	}
	country, err := lookup(addr.String())
	country = strings.ToUpper(strings.TrimSpace(country))
	if err != nil || !validCountry(country) {
		return
	}
	st.with(func(data *stateData) bool {
		known, has := data.Countries[user]
		for _, c := range known {
			if c == country {
				return false
			}
		}
		if !has {
			if len(data.Countries) >= maxUsers {
				// the account whose countries changed longest ago makes room
				oldest, at := "", time.Time{}
				for name := range data.Countries {
					if t := data.CountriesAt[name]; oldest == "" || t.Before(at) {
						oldest, at = name, t
					}
				}
				delete(data.Countries, oldest)
				delete(data.CountriesAt, oldest)
			}
			data.Countries[user] = []string{country}
			data.CountriesAt[user] = time.Now()
			return true // the first one only records
		}
		before := strings.Join(known, ", ")
		known = append(known, country)
		if len(known) > maxCountries {
			known = known[len(known)-maxCountries:]
		}
		data.Countries[user] = known
		data.CountriesAt[user] = time.Now()
		notify("warning", &utils.NotificationInfo{
			Kind: "login.newcountry", Source: "security",
			Title:   utils.NText("notif.login.newcountry.title", "user", user, "country", country),
			What:    utils.NText("notif.login.newcountry.what", "user", user, "country", country, "ip", addr.String(), "known", before),
			Means:   utils.NText("notif.login.newcountry.means"),
			Subject: &utils.NotificationSubject{Type: "setting", Name: user, Link: "/cosmos-ui/security-audit"},
		})
		return true
	})
}

// checkAccounts forgets the countries of accounts that no longer exist, so
// that a new account of the same name starts afresh.
func checkAccounts(st *store, users []string) {
	if users == nil {
		return // the list could not be read: nothing is forgotten
	}
	exists := map[string]bool{}
	for _, user := range users {
		exists[user] = true
	}
	st.with(func(data *stateData) bool {
		changed := false
		for user := range data.Countries {
			if !exists[user] {
				delete(data.Countries, user)
				delete(data.CountriesAt, user)
				changed = true
			}
		}
		return changed
	})
}
