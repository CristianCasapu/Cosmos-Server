package proxy

// Fork addition: tests of fork_botblock.go, with User-Agents as clients send them.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var passingUserAgents = map[string]string{
	"Chrome on Windows":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Chrome on Mac":         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Chrome on Android":     "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
	"Firefox on Linux":      "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Firefox on Windows":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Firefox on Android":    "Mozilla/5.0 (Android 14; Mobile; rv:133.0) Gecko/133.0 Firefox/133.0",
	"Safari on iPhone":      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
	"Safari on iPad":        "Mozilla/5.0 (iPad; CPU OS 17_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.7 Mobile/15E148 Safari/604.1",
	"Safari on Mac":         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
	"Edge":                  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
	"Samsung Internet":      "Mozilla/5.0 (Linux; Android 14; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36",
	"Android WebView":       "Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/AP2A.240805.005; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/131.0.6778.39 Mobile Safari/537.36",
	"iOS app WebView":       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148",
	"Opera":                 "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0",
	"Windows Phone":         "Mozilla/5.0 (compatible; MSIE 9.0; Windows Phone OS 7.5; Trident/5.0; IEMobile/9.0; NOKIA; Lumia 800) ZuneWP7",
	"Android app":           "Dalvik/2.1.0 (Linux; U; Android 14; Pixel 8 Build/AP2A.240805.005)",
	"Casper phone":          "Mozilla/5.0 (Linux; Android 11; Casper_VIA_A4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36",
	"curl":                  "curl/8.11.0",
	"wget":                  "Wget/1.24.5",
	"Windows WinHTTP":       "Microsoft-WinHTTP/5.1",
	"Home Assistant":        "HomeAssistant/2024.11.1 aiohttp/3.10.10 Python/3.12",
	"Home Assistant app":    "Home Assistant/2024.10.0 (io.robbie.HomeAssistant; build:2024.1050; iOS 18.1.0)",
	"Jellyfin web":          "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0 Jellyfin",
	"Jellyfin Android":      "Jellyfin-Android/2.6.2 (Linux; Android 14)",
	"Jellyfin Media Player": "JellyfinMediaPlayer 1.11.1 (Windows-10.0.22631 10.0.22631)",
	"Swiftfin":              "Swiftfin/1.0.0 (iPhone; iOS 18.1; Scale/3.00)",
	"Plex Media Server":     "PlexMediaServer/1.41.2.9200-c6bbc1b53",
	"Plex for Android":      "Plex/10.24.0.1127 (Android 14)",
	"Infuse":                "Infuse/7.8.1 CFNetwork/1568.200.51 Darwin/24.1.0",
	"Kodi":                  "Kodi/21.1 (X11; Linux x86_64) Debian/13 App_Bitness/64 Version/21.1-(21.1.0)-Git:20240824",
	"Nextcloud desktop":     "Mozilla/5.0 (Windows) mirall/3.14.3stable-Win64 (build 20241107) (Nextcloud, windows-10.0.22631 ClientArchitecture: x86_64 OsArchitecture: x86_64)",
	"Immich app":            "Immich_Android_1.120.2",
	"Uptime Kuma":           "Uptime-Kuma/1.23.15",
	"Go client":             "Go-http-client/2.0",
	"Cosmos itself":         "cosmos-server",
	"Googlebot":             "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	"Bingbot":               "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm) Chrome/116.0.1938.76 Safari/537.36",
	"DuckDuckBot":           "DuckDuckBot/1.1; (+http://duckduckgo.com/duckduckbot.html)",
}

var refusedUserAgents = map[string]string{
	"sqlmap":          "sqlmap/1.8.11#stable (https://sqlmap.org)",
	"sqlmap bare":     "sqlmap",
	"Nikto":           "Mozilla/5.00 (Nikto/2.5.0) (Evasions:None) (Test:000562)",
	"masscan":         "masscan/1.3 (https://github.com/robertdavidgraham/masscan)",
	"ZmEu":            "ZmEu",
	"python-requests": "python-requests/2.32.3",
	"Python urllib":   "Python-urllib/3.12",
	"PycURL":          "PycURL/7.45.3 libcurl/8.5.0 OpenSSL/3.0.13",
	"Mechanize":       "Mechanize/2.10 Ruby/3.3.0 (http://github.com/sparklemotion/mechanize/)",
	"AhrefsBot":       "Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)",
	"AhrefsSiteAudit": "Mozilla/5.0 (compatible; AhrefsSiteAudit/6.1; +http://ahrefs.com/robot/site-audit)",
	"MJ12bot":         "Mozilla/5.0 (compatible; MJ12bot/v1.4.8; http://mj12bot.com/)",
	"MJ12bot bare":    "MJ12bot",
	"DotBot":          "Mozilla/5.0 (compatible; DotBot/1.2; +https://opensiteexplorer.org/dotbot; help@moz.com)",
	"BLEXBot":         "Mozilla/5.0 (compatible; BLEXBot/1.0; +https://help.seranking.com/submit-ticket)",
	"360Spider":       "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/50.0.2661.102 Safari/537.36; 360Spider",
	"Exabot":          "Mozilla/5.0 (compatible; Exabot/3.0; +http://www.exabot.com/go/robot)",
	"SISTRIX":         "Mozilla/5.0 (compatible; SISTRIX Crawler; http://crawler.sistrix.net/)",
	"Nutch":           "Apache Nutch/1.20 (+https://nutch.apache.org/bot.html)",
	"Heritrix":        "Mozilla/5.0 (compatible; heritrix/3.4.0 +http://archive.org/details/archive.org_bot)",
	"HTTrack":         "Mozilla/4.5 (compatible; HTTrack 3.0x; Windows 98)",
	"Majestic":        "Mozilla/5.0 (compatible; MJ12bot/v2.0.4; http://www.majestic12.co.uk/bot.php?+)",
}

func TestBotFilterRefusesListedAgentsAndPassesRealClients(t *testing.T) {
	for name, agent := range passingUserAgents {
		if isCommonBot(agent) {
			t.Errorf("%s (%q) is taken for a bot", name, agent)
		}
	}
	for name, agent := range refusedUserAgents {
		if !isCommonBot(agent) {
			t.Errorf("%s (%q) is not taken for a bot", name, agent)
		}
	}
}

func TestBotFilterMatchesTheStartOfAWordOnly(t *testing.T) {
	cases := map[string]bool{
		"SQLMAP/1.7":                    true,  // case is ignored
		"Mozilla/5.0 (X; mj12bot)":      true,  // a word of a comment
		"Mozilla/5.0 notsqlmap/1.0":     false, // the middle of a word
		"Mozilla/5.0 (X; mysqlmapper)":  false,
		"Mozilla/5.0 Gecko Python/3.12": false, // Python itself is no bot, its requests library is
		"":                              false,
		strings.Repeat("a", 4096) + " sqlmap/1.7": false, // read as far as a real User-Agent goes
	}
	for agent, want := range cases {
		if got := isCommonBot(agent); got != want {
			t.Errorf("isCommonBot(%.60q) = %v, want %v", agent, got, want)
		}
	}
}

func TestBotFilterRefusesAListedAgentAndRecordsIt(t *testing.T) {
	resetShieldState(t)
	recorded := captureSecurity(t)
	reached := 0
	bots := BotDetectionMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached++ }))

	answer := func(agent string) int {
		req := httptest.NewRequest("GET", "https://media.example/", nil)
		req.RemoteAddr = "86.120.1.1:40000"
		req.Header.Set("User-Agent", agent)
		w := httptest.NewRecorder()
		bots.ServeHTTP(w, req)
		return w.Code
	}
	if code := answer(refusedUserAgents["AhrefsBot"]); code != http.StatusForbidden {
		t.Errorf("AhrefsBot answered %d, want 403", code)
	}
	if code := answer(refusedUserAgents["sqlmap"]); code != http.StatusForbidden {
		t.Errorf("sqlmap answered %d, want 403", code)
	}
	if code := answer(passingUserAgents["Safari on iPhone"]); code != http.StatusOK {
		t.Errorf("Safari answered %d, want 200", code)
	}
	if reached != 1 {
		t.Errorf("%d requests reached the application, want the browser's only", reached)
	}
	time.Sleep(50 * time.Millisecond) // metrics are pushed from goroutines
	if got := kindsRecorded(recorded()); got != utils.SecShieldBot+" "+utils.SecShieldBot {
		t.Errorf("recorded %s, want the two refusals", got)
	}
}
