package fail2ban

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"time"
)

// This file is the only place where fail2ban-client is run and the only
// place where an argument list is built. Every argument is a constant of
// this file, except:
//
//   - a jail name, which is jail.name of a value from the fixed list in
//     jails.go (a request can select one, it cannot supply one);
//   - the address of an unban, a netip.Addr printed by its own String
//     method: digits, a to f, dots and colons. It cannot be empty, cannot
//     begin with "-" and so cannot be taken for an option.
//
// There is no shell: exec runs the binary with the list as it is.

const (
	// clientPath is Debian's fail2ban-client. Absolute, so that PATH plays
	// no part in what is run.
	clientPath = "/usr/bin/fail2ban-client"
	// clientTimeout bounds one call. The client answers in a fraction of a
	// second; a server that hangs must not hold a request.
	clientTimeout = 5 * time.Second
	// maxOutput bounds what is read of one call's output. The longest
	// answer is a jail's list of banned addresses.
	maxOutput = 256 << 10
	// maxParallel bounds the clients running at once.
	maxParallel = 4
)

// what get reads of a jail
const (
	keyMaxRetry = "maxretry"
	keyFindTime = "findtime"
	keyBanTime  = "bantime"
)

var (
	errNotInstalled = errors.New("fail2ban is not installed")
	errClient       = errors.New("fail2ban-client failed")
	errTruncated    = errors.New("fail2ban-client wrote more than is read")
)

// capped keeps the first max bytes written to it and notes that there was
// more.
type capped struct {
	buffer    bytes.Buffer
	max       int
	truncated bool
}

func (c *capped) Write(data []byte) (int, error) {
	if room := c.max - c.buffer.Len(); room < len(data) {
		c.truncated = true
		if room > 0 {
			c.buffer.Write(data[:room])
		}
	} else {
		c.buffer.Write(data)
	}
	return len(data), nil // the process is never blocked or failed by the cap
}

var slots = make(chan struct{}, maxParallel)

// runClient runs fail2ban-client with the given arguments and returns what
// it wrote to its standard output. Tests replace it with a fake.
var runClient = func(args ...string) (string, error) {
	slots <- struct{}{}
	defer func() { <-slots }()

	ctx, cancel := context.WithTimeout(context.Background(), clientTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, clientPath, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LANG=C"}
	cmd.Dir = "/"
	out := &capped{max: maxOutput}
	cmd.Stdout = out
	cmd.Stderr = nil // discarded: the client's errors are not shown or logged
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", errClient
	}
	if out.truncated {
		return "", errTruncated
	}
	return out.buffer.String(), nil
}

// clientInstalled reports whether the client binary is there. Tests replace
// it.
var clientInstalled = func() bool {
	info, err := os.Stat(clientPath)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

// ping reports whether the Fail2ban server answers.
func ping() bool {
	out, err := runClient("ping")
	return err == nil && parsePong(out)
}

// loadedJails lists the names of the jails the server runs.
func loadedJails() ([]string, error) {
	out, err := runClient("status")
	if err != nil {
		return nil, err
	}
	return parseJailList(out), nil
}

// jailCounts reads the counters of a jail.
func jailCounts(j jail) (counts, error) {
	out, err := runClient("status", j.name)
	if err != nil {
		return counts{}, err
	}
	return parseJailStatus(out)
}

// jailBans lists the addresses a jail has banned, with when each ban began
// and when it ends.
func jailBans(j jail) ([]banTime, error) {
	out, err := runClient("get", j.name, "banip", "--with-time")
	if err != nil {
		return nil, err
	}
	return parseBanTimes(out, time.Local), nil
}

// jailThresholds reads the thresholds a jail runs with.
func jailThresholds(j jail) (Thresholds, error) {
	var read Thresholds
	for _, field := range []struct {
		key   string
		value *int
	}{{keyMaxRetry, &read.MaxRetry}, {keyFindTime, &read.FindTime}, {keyBanTime, &read.BanTime}} {
		out, err := runClient("get", j.name, field.key)
		if err != nil {
			return Thresholds{}, err
		}
		number, ok := parseNumber(out)
		if !ok {
			return Thresholds{}, errClient
		}
		*field.value = number
	}
	return read, nil
}

// jailIgnored lists the addresses and networks a jail never bans. Entries
// that are not addresses (Fail2ban also accepts host names) are left out
// and counted.
func jailIgnored(j jail) (networks []netip.Prefix, others int, err error) {
	out, err := runClient("get", j.name, "ignoreip")
	if err != nil {
		return nil, 0, err
	}
	networks, others = parseIgnoreList(out)
	return networks, others, nil
}

// bannedNow lists, for each of the fork's jails, the addresses banned at
// this moment: one call for all jails.
func bannedNow() (map[string][]netip.Addr, error) {
	out, err := runClient("banned")
	if err != nil {
		return nil, err
	}
	return parseBanned(out), nil
}

// unban lifts the ban of an address in a jail and reports whether there was
// one.
func unban(j jail, address netip.Addr) (bool, error) {
	if !address.IsValid() || address.Zone() != "" {
		return false, errClient
	}
	out, err := runClient("set", j.name, "unbanip", address.String())
	if err != nil {
		return false, err
	}
	number, ok := parseNumber(out)
	return ok && number > 0, nil
}

// reload makes the server read its configuration again. Bans are kept.
func reload() error {
	_, err := runClient("reload")
	return err
}
