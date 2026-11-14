package configapi

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestShieldWhitelistChecked(t *testing.T) {
	stored := utils.Config{ShieldWhitelist: []utils.ShieldWhitelistEntry{{IP: "not-an-ip"}}}
	// an old odd entry that did not change does not block the save
	request := stored
	if p := checkSmallLists(&request, stored); p != nil {
		t.Fatal(p)
	}
	request = utils.Config{ShieldWhitelist: []utils.ShieldWhitelistEntry{{IP: " 192.168.1.0/24 ", Label: " home "}, {IP: "203.0.113.4"}}}
	if p := checkSmallLists(&request, stored); p != nil || request.ShieldWhitelist[0].IP != "192.168.1.0/24" || request.ShieldWhitelist[0].Label != "home" {
		t.Fatalf("%v %+v", p, request.ShieldWhitelist)
	}
	for _, bad := range [][]utils.ShieldWhitelistEntry{
		{{IP: "my laptop"}},
		{{IP: "10.0.0.1"}, {IP: "10.0.0.1"}},
		{{IP: "10.0.0.1", Label: "<script>"}},
	} {
		request := utils.Config{ShieldWhitelist: bad}
		if p := checkSmallLists(&request, stored); p == nil || p.Code != "UC040" {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestMarketSourcesChecked(t *testing.T) {
	stored := utils.Config{}
	request := utils.Config{}
	request.MarketConfig.Sources = []utils.MarketSource{{Name: "mine", Url: "https://example.com/market.json"}}
	if p := checkSmallLists(&request, stored); p != nil {
		t.Fatal(p)
	}
	for _, bad := range [][]utils.MarketSource{
		{{Name: "", Url: "https://example.com/m.json"}},
		{{Name: "cosmos-cloud", Url: "https://example.com/m.json"}},
		{{Name: "a", Url: "https://example.com/1.json"}, {Name: "A", Url: "https://example.com/2.json"}},
		{{Name: "a", Url: "file:///etc/passwd"}},
		{{Name: "a", Url: "example.com/m.json"}},
		{{Name: "a", Url: "https://user:pw@example.com/m.json"}},
	} {
		request := utils.Config{}
		request.MarketConfig.Sources = bad
		if p := checkSmallLists(&request, stored); p == nil || p.Code != "UC041" {
			t.Errorf("%+v accepted", bad)
		}
	}
}
