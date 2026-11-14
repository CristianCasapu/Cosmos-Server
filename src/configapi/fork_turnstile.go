package configapi

// Fork addition: the settings of Cloudflare Turnstile (utils.ForkTurnstileConfig,
// package turnstile) in the configuration routes. The secret key never
// leaves the server through the configuration, whoever asks; the block is
// written only by the Turnstile routes, which test the keys first, so a full
// save keeps what is stored (keepCloudflareState).

import "github.com/azukaar/cosmos-server/src/utils"

// maskTurnstile hides the secret key in a configuration that is about to be
// sent to a client: "***" says that one is stored.
func maskTurnstile(config *utils.Config) {
	if config.Turnstile.SecretKey != "" {
		config.Turnstile.SecretKey = utils.MaskedSecret
	}
	config.Turnstile.Confirmed.Keys = ""
}

// keepTurnstileState puts the stored block back into a configuration a
// client sends.
func keepTurnstileState(request *utils.Config, stored utils.Config) {
	request.Turnstile = stored.Turnstile
}
