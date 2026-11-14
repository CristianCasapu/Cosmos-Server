package proxy

// Fork addition: the bot filter of a URL ("Block Common Bots") matched the
// whole User-Agent for equality with short names, so only a request without a
// User-Agent was ever refused. Here a name is matched against the words of
// the User-Agent (its product names and the words of its comments), ignoring
// case: a listed name matches a word it starts ("ahrefs" matches
// "AhrefsBot/7.0", "sqlmap" matches "sqlmap/1.7"), never the middle of a
// word.
//
// The list is upstream's (botblock.go) without the names of clients people
// use: this server's own name, Windows' HTTP stack (winhttp), Windows Phone's
// media apps (zune), download managers, and dictionary words or brands that
// now start the names of ordinary apps and devices. "python" is narrowed to
// the default agents of Python's HTTP libraries: Home Assistant and other
// aiohttp clients send "Python/3.x" after their own name.

import "strings"

var commonBotNames = []string{
	"360spider", "acapbot", "acoonbot", "ahrefs", "alexibot", "asterias", "attackbot", "backdorbot", "becomebot", "binlar",
	"blackwidow", "blekkobot", "blexbot", "bunnys", "careerbot", "checkpriv",
	"cheesebot", "cherrypick", "chinaclaw", "clshttp", "cmsworld", "copernic", "copyrightcheck",
	"cy_cho", "datacha", "diavol", "discobot", "dittospyder", "dotbot", "dotnetdotcom", "dumbot", "emailcollector",
	"emailsiphon", "emailwolf", "exabot", "eyenetie", "feedfinder", "flicky", "foobot",
	"g00g1e", "gigabot", "go-ahead-got", "grabnet", "grafula", "heritrix", "httrack",
	"icarus6j", "jetbot", "jikespider", "kmccrew", "libweb", "linkextractor", "linkscan", "linkwalker",
	"majestic", "masscan", "mechanize", "mj12bot", "morfeus", "moveoverbot", "netmechanic", "netspider",
	"nicerspro", "nikto", "nutch", "pagegrabber", "planetwork", "postrank", "proximic", "purebot",
	"pycurl", "python-requests", "python-urllib", "queryn", "queryseeker", "radian6", "rogerbot", "seekerspider",
	"semalt", "siclab", "sindice", "sistrix", "sitebot", "siteexplorer", "sitesnagger", "skygrid", "snoopy",
	"sosospider", "spankbot", "spbot", "sqlmap", "stackrambler", "surftbot", "sux0r", "suzukacz",
	"suzuran", "teleport", "telesoft", "true_robots", "turingos", "turnit", "vikspider", "voideye",
	"webleacher", "webreaper", "webstripper", "webvac", "webwhacker", "wwwoffle", "woxbot",
	"xaldon", "xxxyy", "yamanalab", "yioopbot", "youda", "zmeu", "zyborg",
}

// a User-Agent is read this far; real ones are a few hundred bytes
const botUserAgentScan = 2048

// isCommonBot reports whether a User-Agent names a listed bot.
func isCommonBot(userAgent string) bool {
	if len(userAgent) > botUserAgentScan {
		userAgent = userAgent[:botUserAgentScan]
	}
	userAgent = strings.ToLower(userAgent)

	start := -1
	for i := 0; i <= len(userAgent); i++ {
		if i < len(userAgent) && isBotWordByte(userAgent[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if startsWithBotName(userAgent[start:i]) {
				return true
			}
			start = -1
		}
	}
	return false
}

// a word is a run of letters, digits, "-" and "_": the characters of the
// listed names. Everything else ("/", ";", "(", ".", spaces...) separates.
func isBotWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

func startsWithBotName(word string) bool {
	for _, name := range commonBotNames {
		if strings.HasPrefix(word, name) {
			return true
		}
	}
	return false
}
