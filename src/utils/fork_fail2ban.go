package utils

// Fork addition: the settings of the Fail2ban integration (src/fail2ban).

// Fail2banConfig is what an administrator set in the panel for the Fail2ban
// jails the fork installs. It is empty until the settings are saved there
// for the first time; until then the jails run with what their files say.
// Cosmos writes these values into one generated file of Fail2ban's
// configuration; every value is a bounded number or an address.
type Fail2banConfig struct {
	// Jails holds the thresholds by jail name. Only the fork's jails are
	// accepted.
	Jails map[string]Fail2banJailConfig `json:"Jails,omitempty"`
	// IgnoreIP lists the addresses and networks (CIDR) that are never
	// banned, in canonical form.
	IgnoreIP []string `json:"IgnoreIP,omitempty"`
}

// Fail2banJailConfig are the thresholds of one jail: MaxRetry failures
// within FindTime seconds ban an address for BanTime seconds.
type Fail2banJailConfig struct {
	MaxRetry int `json:"MaxRetry"`
	FindTime int `json:"FindTime"`
	BanTime  int `json:"BanTime"`
}
