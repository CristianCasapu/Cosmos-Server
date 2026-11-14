package fail2ban

import (
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// JailStatus is one of the fork's jails as Fail2ban runs it.
type JailStatus struct {
	Name string `json:"name"`
	// Loaded says whether Fail2ban runs this jail. The counters and
	// thresholds below are only meaningful when it does.
	Loaded          bool `json:"loaded"`
	CurrentlyFailed int  `json:"currentlyFailed"`
	TotalFailed     int  `json:"totalFailed"`
	CurrentlyBanned int  `json:"currentlyBanned"`
	TotalBanned     int  `json:"totalBanned"`
	// Thresholds the jail runs with; the defaults of its file when the
	// jail is not loaded.
	Thresholds
}

// Ban is one banned address.
type Ban struct {
	Jail  string     `json:"jail"`
	IP    string     `json:"ip"`
	Since *time.Time `json:"since"`
	// Until is null for a ban that does not end by itself.
	Until *time.Time `json:"until"`
}

// Status is what the panel shows.
type Status struct {
	// Installed: fail2ban-client is on this server.
	Installed bool `json:"installed"`
	// Running: the Fail2ban server answers.
	Running bool `json:"running"`
	// ConfigInstalled: the fork's jail file is in Fail2ban's configuration.
	ConfigInstalled bool `json:"configInstalled"`
	// PanelFile: settings were written from the panel before.
	PanelFile bool `json:"panelFile"`
	// Incomplete: Fail2ban answered something that could not be read, or
	// not in time; what follows may lack jails or bans.
	Incomplete bool `json:"incomplete"`

	Jails []JailStatus `json:"jails"`
	// OtherJails counts the jails Fail2ban runs that are not the fork's.
	OtherJails int   `json:"otherJails"`
	Bans       []Ban `json:"bans"`

	// Settings is what the settings form starts from: what the jails run
	// with now.
	Settings Settings `json:"settings"`
	Bounds   Bounds   `json:"bounds"`
	// OtherIgnored counts entries of Fail2ban's never-ban list that are
	// not addresses (host names): they are not shown, and saving the
	// settings from the panel drops them.
	OtherIgnored int `json:"otherIgnored"`

	// ClientIP is the address this request came from, as Cosmos records
	// it; ClientIgnored says whether Fail2ban will never ban it.
	ClientIP      string `json:"clientIP"`
	ClientIgnored bool   `json:"clientIgnored"`
	// PrivateIgnored: the private networks (10/8, 172.16/12, 192.168/16)
	// are all on the never-ban list.
	PrivateIgnored bool `json:"privateIgnored"`
	// TrustedProxies is how many reverse proxies Cosmos trusts
	// (HTTPConfig.TrustedProxies).
	TrustedProxies int `json:"trustedProxies"`
	// ClientForwarded: ClientIP is what a proxy (Cloudflare, a trusted
	// proxy) reported, not the address this request's connection came from.
	// A ban of it would not cut this connection.
	ClientForwarded bool `json:"clientForwarded"`
	// CloudflareHostnames is how many hostnames are served through
	// Cloudflare. While there are any, Cloudflare's ranges
	// (CloudflareRanges of them) belong on the never-ban list;
	// CloudflareIgnored is how many of them the jails run with. They are
	// not part of Settings.IgnoreIP: Cosmos keeps them itself.
	CloudflareHostnames int `json:"cloudflareHostnames"`
	CloudflareRanges    int `json:"cloudflareRanges"`
	CloudflareIgnored   int `json:"cloudflareIgnored"`

	Time time.Time `json:"time"`
}

// shared is the part of the status that does not depend on who asks.
type shared struct {
	status  Status
	ignored []netip.Prefix
	read    time.Time
}

const (
	// a status is reused for this long: the page polls, and several
	// administrators may look at once
	statusTTL = 3 * time.Second
	// the thresholds of a jail change only with a reload
	thresholdsTTL = time.Minute
)

var (
	statusLock sync.Mutex
	lastStatus *shared

	thresholdsLock sync.Mutex
	thresholdsRead = map[string]thresholdsAt{}
)

type thresholdsAt struct {
	thresholds Thresholds
	read       time.Time
}

// forgetStatus drops what is cached: after an unban or a change of settings
// the next status is read anew.
func forgetStatus() {
	statusLock.Lock()
	lastStatus = nil
	statusLock.Unlock()
	thresholdsLock.Lock()
	thresholdsRead = map[string]thresholdsAt{}
	thresholdsLock.Unlock()
}

func cachedThresholds(j jail) (Thresholds, error) {
	thresholdsLock.Lock()
	cached, found := thresholdsRead[j.name]
	thresholdsLock.Unlock()
	if found && time.Since(cached.read) < thresholdsTTL {
		return cached.thresholds, nil
	}
	read, err := jailThresholds(j)
	if err != nil {
		return Thresholds{}, err
	}
	thresholdsLock.Lock()
	thresholdsRead[j.name] = thresholdsAt{thresholds: read, read: time.Now()}
	thresholdsLock.Unlock()
	return read, nil
}

var privateNetworks = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

// covers reports whether one of the networks holds all of inner.
func covers(networks []netip.Prefix, inner netip.Prefix) bool {
	for _, network := range networks {
		if network.Addr().BitLen() == inner.Addr().BitLen() && network.Bits() <= inner.Bits() && network.Contains(inner.Addr()) {
			return true
		}
	}
	return false
}

// ownAddresses lists the addresses of this server: Fail2ban never bans
// them (ignoreself). Tests replace it.
var ownAddresses = func() []netip.Addr {
	addresses := []netip.Addr{}
	found, err := net.InterfaceAddrs()
	if err != nil {
		return addresses
	}
	for _, entry := range found {
		if network, ok := entry.(*net.IPNet); ok {
			if address, ok := netip.AddrFromSlice(network.IP); ok {
				addresses = append(addresses, address.Unmap())
			}
		}
	}
	return addresses
}

// neverBanned reports whether Fail2ban will not ban the address: it is on
// the list, a loopback address or one of the server's own.
func neverBanned(address netip.Addr, ignored []netip.Prefix) bool {
	if address.IsLoopback() {
		return true
	}
	for _, network := range ignored {
		if network.Contains(address) {
			return true
		}
	}
	for _, own := range ownAddresses() {
		if own == address {
			return true
		}
	}
	return false
}

// savedSettings is what was last saved from the panel, or the defaults of
// the jail file where nothing was. Whatever the configuration file holds is
// checked like a request before it is used.
func savedSettings() Settings {
	config := utils.GetMainConfig().Fail2ban
	settings := Settings{Jails: map[string]Thresholds{}, IgnoreIP: []string{}}
	for _, known := range jails {
		settings.Jails[known.name] = known.defaults
		if saved, found := config.Jails[known.name]; found && thresholdsOf(saved).valid() {
			settings.Jails[known.name] = thresholdsOf(saved)
		}
	}
	for _, entry := range config.IgnoreIP {
		if prefix, ok := parsePrefix(entry); ok && len(settings.IgnoreIP) < bounds.MaxIgnoreIP {
			settings.IgnoreIP = append(settings.IgnoreIP, formatPrefix(prefix))
		}
	}
	return settings
}

// readShared asks Fail2ban for everything the status shows.
func readShared() *shared {
	result := &shared{read: time.Now()}
	status := &result.status
	status.Time = result.read
	status.Bounds = bounds
	status.Jails = []JailStatus{}
	status.Bans = []Ban{}
	status.Settings = savedSettings()
	for _, known := range jails {
		status.Jails = append(status.Jails, JailStatus{Name: known.name, Thresholds: status.Settings.Jails[known.name]})
	}

	cloudflare := map[netip.Prefix]bool{}
	for _, prefix := range cloudflareRanges() {
		cloudflare[prefix] = true
	}
	status.CloudflareHostnames = cloudflareHostnames()
	status.CloudflareRanges = len(cloudflare)

	status.Installed = clientInstalled()
	status.ConfigInstalled = configInstalled()
	status.PanelFile = panelFileExists()
	if !status.Installed {
		return result
	}
	status.Running = ping()
	if !status.Running {
		return result
	}

	loaded, err := loadedJails()
	if err != nil {
		status.Incomplete = true
		return result
	}
	isLoaded := map[string]bool{}
	for _, name := range loaded {
		if _, ours := jailByName(name); ours {
			isLoaded[name] = true
		} else {
			status.OtherJails++
		}
	}

	// one jail after the other would take a second or two; the client
	// itself bounds how many run at once
	var wait sync.WaitGroup
	var lock sync.Mutex
	for position, known := range jails {
		if !isLoaded[known.name] {
			continue
		}
		wait.Add(1)
		go func(position int, known jail) {
			defer wait.Done()
			entry := JailStatus{Name: known.name, Loaded: true, Thresholds: known.defaults}
			incomplete := false
			var bans []banTime

			if read, err := jailCounts(known); err == nil {
				entry.CurrentlyFailed, entry.TotalFailed = read.CurrentlyFailed, read.TotalFailed
				entry.CurrentlyBanned, entry.TotalBanned = read.CurrentlyBanned, read.TotalBanned
			} else {
				incomplete = true
			}
			if read, err := cachedThresholds(known); err == nil {
				entry.Thresholds = read
			} else {
				incomplete = true
			}
			if entry.CurrentlyBanned > 0 {
				if read, err := jailBans(known); err == nil {
					bans = read
				} else {
					incomplete = true
				}
			}

			lock.Lock()
			defer lock.Unlock()
			status.Jails[position] = entry
			status.Incomplete = status.Incomplete || incomplete
			for _, ban := range bans {
				since := ban.Since
				shown := Ban{Jail: known.name, IP: ban.Address.String(), Since: &since}
				if !ban.Until.IsZero() {
					until := ban.Until
					shown.Until = &until
				}
				status.Bans = append(status.Bans, shown)
			}
		}(position, known)
	}
	wait.Wait()

	sort.SliceStable(status.Bans, func(i, j int) bool {
		if !status.Bans[i].Since.Equal(*status.Bans[j].Since) {
			return status.Bans[i].Since.After(*status.Bans[j].Since)
		}
		if status.Bans[i].IP != status.Bans[j].IP {
			return status.Bans[i].IP < status.Bans[j].IP
		}
		return status.Bans[i].Jail < status.Bans[j].Jail
	})

	// the form starts from what runs: the thresholds of each loaded jail,
	// and the never-ban list of the first loaded one (the fork's files give
	// all of them the same list)
	for _, entry := range status.Jails {
		if entry.Loaded {
			status.Settings.Jails[entry.Name] = entry.Thresholds
		}
	}
	for _, known := range jails {
		if !isLoaded[known.name] {
			continue
		}
		networks, others, err := jailIgnored(known)
		if err != nil {
			status.Incomplete = true
			break
		}
		result.ignored = networks
		status.OtherIgnored = others
		status.Settings.IgnoreIP = []string{}
		for _, network := range networks {
			if cloudflare[network] {
				// Cosmos' own entries, not the administrator's
				status.CloudflareIgnored++
				continue
			}
			if len(status.Settings.IgnoreIP) < bounds.MaxIgnoreIP {
				status.Settings.IgnoreIP = append(status.Settings.IgnoreIP, formatPrefix(network))
			}
		}
		break
	}
	status.PrivateIgnored = true
	for _, private := range privateNetworks {
		if !covers(result.ignored, private) {
			status.PrivateIgnored = false
		}
	}
	return result
}

// GetStatus returns the state of Fail2ban for a request that comes from
// clientIP ("" when there is none).
func GetStatus(clientIP string) Status {
	statusLock.Lock()
	if lastStatus == nil || time.Since(lastStatus.read) > statusTTL {
		lastStatus = readShared()
	}
	current := lastStatus
	statusLock.Unlock()

	// a copy: the cached value is shared between requests
	status := current.status
	status.Jails = append([]JailStatus{}, current.status.Jails...)
	status.Bans = append([]Ban{}, current.status.Bans...)

	status.TrustedProxies = 0
	for _, proxy := range utils.GetMainConfig().HTTPConfig.TrustedProxies {
		if proxy != "" {
			status.TrustedProxies++
		}
	}
	if address, ok := parseAddress(clientIP); ok {
		status.ClientIP = address.String()
		status.ClientIgnored = neverBanned(address, current.ignored)
	}
	return status
}
