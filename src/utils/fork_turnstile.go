package utils

// Fork addition: the settings of Cloudflare Turnstile on the panel's public
// forms (sign-in, forgot password, invitation and reset links). The logic is
// in src/turnstile; this file holds only the stored shape, so that Config
// can carry it.
//
// The block is written only by the routes of src/turnstile, which test the
// keys before they keep them. A full settings save puts the stored block back
// (configapi.keepCloudflareState) and every reader gets the secret key as
// MaskedSecret (configapi.maskTurnstile).

import "time"

// ForkTurnstileForms says which forms ask for the check.
type ForkTurnstileForms struct {
	Login    bool `json:"login"`
	Reset    bool `json:"reset"`
	Register bool `json:"register"`
}

// ForkTurnstileConfirmed is the last round trip that worked from an
// administrator's browser: a token made by the widget with the saved site key
// and verified with the saved secret key.
type ForkTurnstileConfirmed struct {
	// Keys is a hash of the two keys it was made with: new keys need a new round trip
	Keys     string    `json:"keys,omitempty"`
	At       time.Time `json:"at,omitzero"`
	Hostname string    `json:"hostname,omitempty"`
}

// ForkTurnstileConfig is Config.Turnstile.
type ForkTurnstileConfig struct {
	Enabled   bool   `json:"Enabled,omitempty"`
	SiteKey   string `json:"SiteKey,omitempty"`
	SecretKey string `json:"SecretKey,omitempty"`
	// FormsSet is true once the forms were chosen; before that all three ask
	FormsSet bool               `json:"FormsSet,omitempty"`
	Forms    ForkTurnstileForms `json:"Forms,omitzero"`
	// Appearance: "always" (default) or "interaction-only"
	Appearance string `json:"Appearance,omitempty"`
	// Mode: "managed" (default), "non-interactive" or "invisible"; the widget's
	// at Cloudflare, used when Cosmos makes the widget and to describe it
	Mode string `json:"Mode,omitempty"`
	// Source: "manual" (pasted) or "api" (made by Cosmos)
	Source     string                 `json:"Source,omitempty"`
	WidgetName string                 `json:"WidgetName,omitempty"`
	Hostnames  []string               `json:"Hostnames,omitempty"`
	KeysSaved  time.Time              `json:"KeysSaved,omitzero"`
	Confirmed  ForkTurnstileConfirmed `json:"Confirmed,omitzero"`
}

// FormsInForce returns the forms that ask: all three until they were chosen.
func (c ForkTurnstileConfig) FormsInForce() ForkTurnstileForms {
	if !c.FormsSet {
		return ForkTurnstileForms{Login: true, Reset: true, Register: true}
	}
	return c.Forms
}
