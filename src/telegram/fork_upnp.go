package telegram

// Fork addition: the sentence of the router port forwarding's one event of
// high severity (src/upnp). It lives here, not in the table of sources.go,
// so that the table has one place less where two branches meet.

import "github.com/azukaar/cosmos-server/src/utils"

func init() {
	securitySentences[utils.SecUPnPPortOpened] = "Port opened on the router: an administrator asked the router to forward a port of an app. It is reachable from the internet directly, without Cosmos' login."
	securitySentences[utils.SecExposeOpened] = "App exposed to the internet: an administrator made an app reachable from outside, through a domain and/or a port forwarded by the router. A port opened this way reaches the app directly, without Cosmos' login."
}
