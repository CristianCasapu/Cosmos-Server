// Package fail2ban is the fork's link to a Fail2ban installed on the host.
//
// Fork addition: nothing here exists upstream. Fail2ban itself, the fork's
// filters and jails, and the service are put on the host once by
// fork/fail2ban/install.sh, which the owner runs; Cosmos never installs,
// enables, starts or stops anything. At run time this package
//
//   - tells whether Fail2ban is installed and running and which of the
//     fork's jails are loaded (status.go),
//   - shows their counts, thresholds and banned addresses,
//   - lifts the ban of one address,
//   - writes one generated file with the thresholds and the addresses that
//     are never banned, as set in the panel (override.go),
//   - records new bans in the security record (watch.go).
//
// Cosmos runs as root and so does Fail2ban. The rule that keeps this safe:
// nothing that comes from a request ever reaches a command line or a
// configuration file as text. client.go is the only file that builds an
// argument list, from constants plus a jail of the fixed list below and an
// address in the parser's own form; override.go is the only file that
// writes configuration, from bounded integers and parsed networks.
package fail2ban

import "github.com/azukaar/cosmos-server/src/utils"

// Thresholds of a jail: MaxRetry failures within FindTime seconds ban an
// address for BanTime seconds.
type Thresholds struct {
	MaxRetry int `json:"maxRetry"`
	FindTime int `json:"findTime"`
	BanTime  int `json:"banTime"`
}

// jail is one of the jails the fork installs. Values of this type only come
// from the list below: a jail name in an argument list is always one of
// these constants, never text from a request.
type jail struct {
	name     string
	defaults Thresholds
}

// The fork's jails, in the order they are shown. The names and the default
// thresholds are those of fork/fail2ban/jail.d/cosmos-10-jails.local; a test
// compares the two.
var jails = []jail{
	{"cosmos-auth", Thresholds{MaxRetry: 5, FindTime: 3600, BanTime: 14400}},
	{"cosmos-probe", Thresholds{MaxRetry: 3, FindTime: 3600, BanTime: 86400}},
	{"cosmos-scan", Thresholds{MaxRetry: 10, FindTime: 600, BanTime: 14400}},
	{"cosmos-abuse", Thresholds{MaxRetry: 3, FindTime: 86400, BanTime: 14400}},
	{"sshd", Thresholds{MaxRetry: 5, FindTime: 600, BanTime: 3600}},
	{"recidive", Thresholds{MaxRetry: 3, FindTime: 86400, BanTime: 604800}},
}

// jailByName returns the fork's jail of that exact name.
func jailByName(name string) (jail, bool) {
	for _, known := range jails {
		if known.name == name {
			return known, true
		}
	}
	return jail{}, false
}

// Bounds are what the panel accepts for a threshold.
type Bounds struct {
	MinMaxRetry int `json:"minMaxRetry"`
	MaxMaxRetry int `json:"maxMaxRetry"`
	MinFindTime int `json:"minFindTime"`
	MaxFindTime int `json:"maxFindTime"`
	MinBanTime  int `json:"minBanTime"`
	MaxBanTime  int `json:"maxBanTime"`
	MaxIgnoreIP int `json:"maxIgnoreIP"`
}

// The bounds. MaxRetry stops at 10 and FindTime starts at ten minutes for a
// reason: the security log holds at most ten lines of a kind from one
// address at once, then one a minute, and sums up the rest in one line.
// Within these bounds an address that was summed up has always crossed the
// threshold on its own lines already, so that Fail2ban counting lines
// instead of events loses nothing. BanTime ends at 31 days: Fail2ban is told
// to remember bans for 32 (fork/fail2ban/fail2ban.d/cosmos-fork.local), and
// there are no permanent bans.
var bounds = Bounds{
	MinMaxRetry: 1, MaxMaxRetry: 10,
	MinFindTime: 600, MaxFindTime: 7 * 86400,
	MinBanTime: 300, MaxBanTime: 31 * 86400,
	MaxIgnoreIP: 64,
}

func (t Thresholds) valid() bool {
	return t.MaxRetry >= bounds.MinMaxRetry && t.MaxRetry <= bounds.MaxMaxRetry &&
		t.FindTime >= bounds.MinFindTime && t.FindTime <= bounds.MaxFindTime &&
		t.BanTime >= bounds.MinBanTime && t.BanTime <= bounds.MaxBanTime
}

func thresholdsOf(config utils.Fail2banJailConfig) Thresholds {
	return Thresholds{MaxRetry: config.MaxRetry, FindTime: config.FindTime, BanTime: config.BanTime}
}
