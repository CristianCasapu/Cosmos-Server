package telegram

// Fork addition: the sentence of the block at Cloudflare's edge
// (src/edgeblock), the one kind of it with high severity.

import "github.com/azukaar/cosmos-server/src/utils"

func init() {
	securitySentences[utils.SecEdgeBlocked] = "Blocked at Cloudflare: an address that came through Cloudflare kept offending, and Cloudflare now blocks or challenges it before it reaches the server, for a limited time. Lift it in the panel (Configuration > Domains) if it was a mistake."
}
