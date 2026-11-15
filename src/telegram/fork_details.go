package telegram

// What the notifications' details view (src/fork_notifications.go) reads
// from here: the state of the deliveries, and the cutting of the
// configuration's secrets out of a text shown in the panel.

import "github.com/azukaar/cosmos-server/src/utils"

// CurrentStatus is the state of the deliveries, as the status route says it.
func CurrentStatus() Status {
	if n := std.Load(); n != nil {
		return n.Status()
	}
	st := currentSettings()
	return Status{Enabled: st.enabled, Ready: st.usable}
}

// HideSecrets cuts the secrets of the configuration, and anything shaped
// like a bot token, out of a text.
func HideSecrets(text string) string {
	return hideSecrets(text, utils.GetMainConfig())
}
