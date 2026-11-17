package configapi

// Fork addition (plan 45): what the settings code does for the sign-in details
// proposed to new apps (utils.ForkAppSignInConfig). They are not secrets but
// personal data: a reader who may not read credentials gets them masked, like
// the SMTP account, and a save that sends the mask back keeps the stored value.

import "github.com/azukaar/cosmos-server/src/utils"

func maskAppSignIn(config *utils.Config, canReadCredentials bool) {
	if canReadCredentials {
		return
	}
	if config.AppSignIn.User != "" {
		config.AppSignIn.User = utils.AppSignInMasked
	}
	if config.AppSignIn.Email != "" {
		config.AppSignIn.Email = utils.AppSignInMasked
	}
}

// keepAppSignIn puts back what came masked and checks what changed; the error
// is a sentence for the administrator. Unchanged settings are left alone.
func keepAppSignIn(request *utils.Config, stored utils.Config) error {
	if request.AppSignIn.User == utils.AppSignInMasked {
		request.AppSignIn.User = stored.AppSignIn.User
	}
	if request.AppSignIn.Email == utils.AppSignInMasked {
		request.AppSignIn.Email = stored.AppSignIn.Email
	}
	if request.AppSignIn == stored.AppSignIn {
		return nil
	}
	return utils.ValidateAppSignIn(request.AppSignIn)
}
