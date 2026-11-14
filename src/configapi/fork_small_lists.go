package configapi

// Fork addition: two lists saved with the whole settings that nothing
// checked: the SmartShield whitelist and the app market's sources. The forms
// check the same (pages/config/users/shieldWhitelist.jsx, pages/market/
// sources.jsx); this is the guard. Only a list that changed is checked, so an
// old odd entry never blocks saving something else.
//
//   - a whitelist entry is an address or a range (anything else exempted
//     nobody, silently), with a short label of plain text; at most 256;
//   - a market source has a unique name of plain text and an http(s) address
//     without user name or password (Cosmos fetches it on every market
//     refresh); at most 32.

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxShieldWhitelist = 256
	maxMarketSources   = 32
	maxLabelLen        = 64
)

func plainLabel(value string) bool {
	if len(value) > maxLabelLen {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f || c == '<' || c == '>' {
			return false
		}
	}
	return true
}

// checkSmallLists is the hook of PUT /api/config.
func checkSmallLists(request *utils.Config, stored utils.Config) *httpSettingsProblem {
	if !reflect.DeepEqual(request.ShieldWhitelist, stored.ShieldWhitelist) && len(request.ShieldWhitelist)+len(stored.ShieldWhitelist) > 0 {
		if len(request.ShieldWhitelist) > maxShieldWhitelist {
			return &httpSettingsProblem{"UC040", fmt.Sprintf("The whitelist holds at most %d entries", maxShieldWhitelist)}
		}
		seen := map[string]bool{}
		for i := range request.ShieldWhitelist {
			entry := &request.ShieldWhitelist[i]
			entry.IP = strings.TrimSpace(entry.IP)
			entry.Label = strings.TrimSpace(entry.Label)
			if !validAddressEntry(entry.IP) {
				return &httpSettingsProblem{"UC040", "Not an address or a range: " + plainText(entry.IP) + ". Enter for example 192.168.1.20 or 192.168.1.0/24"}
			}
			if seen[entry.IP] {
				return &httpSettingsProblem{"UC040", entry.IP + " is on the whitelist twice"}
			}
			seen[entry.IP] = true
			if !plainLabel(entry.Label) {
				return &httpSettingsProblem{"UC040", fmt.Sprintf("A label is plain text of at most %d characters", maxLabelLen)}
			}
		}
	}

	if !reflect.DeepEqual(request.MarketConfig.Sources, stored.MarketConfig.Sources) && len(request.MarketConfig.Sources)+len(stored.MarketConfig.Sources) > 0 {
		if len(request.MarketConfig.Sources) > maxMarketSources {
			return &httpSettingsProblem{"UC041", fmt.Sprintf("At most %d market sources", maxMarketSources)}
		}
		names := map[string]bool{"cosmos-cloud": true}
		for i := range request.MarketConfig.Sources {
			source := &request.MarketConfig.Sources[i]
			source.Name, source.Url = strings.TrimSpace(source.Name), strings.TrimSpace(source.Url)
			if source.Name == "" || !plainLabel(source.Name) {
				return &httpSettingsProblem{"UC041", fmt.Sprintf("A market source needs a name of plain text, at most %d characters", maxLabelLen)}
			}
			if names[strings.ToLower(source.Name)] {
				return &httpSettingsProblem{"UC041", "Two market sources are named " + plainText(source.Name) + " (cosmos-cloud is the built-in one): give each its own name"}
			}
			names[strings.ToLower(source.Name)] = true
			u, err := url.Parse(source.Url)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(source.Url) > 512 || strings.ContainsAny(source.Url, " \t\r\n") {
				return &httpSettingsProblem{"UC041", "The address of the market " + plainText(source.Name) + " must be a web address such as https://example.com/cosmos-market.json"}
			}
		}
	}
	return nil
}
