package appsignin

// Fork addition (plan 51): the monitoring database's proposals are checked
// before they are shown, and a typed server that does not answer is given
// the likely right value.
//
// The check is a TCP connect from Cosmos to the address a proposal names
// (1.5 s, at most maxPostgresProposals at once, each address cached for
// reachCacheFor): only the addresses of this server's own PostgreSQL
// containers, never a typed one. The advice (utils.PostgresHostAdvice, used
// by the connection test and the save check of configapi) compares the typed
// server with the same facts.

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	reachTimeout  = 1500 * time.Millisecond
	reachCacheFor = 30 * time.Second
	adviceTimeout = 4 * time.Second
)

var (
	reachMu    sync.Mutex
	reachCache = map[string]reachEntry{}
	// reachDial connects; replaced by the tests.
	reachDial = func(ctx context.Context, address string) error {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err == nil {
			conn.Close()
		}
		return err
	}
	reachClock = time.Now
)

type reachEntry struct {
	answers bool
	at      time.Time
}

// reachOf says for each address whether it takes a TCP connection.
func reachOf(ctx context.Context, addresses []string) map[string]bool {
	out := map[string]bool{}
	todo := []string{}
	now := reachClock()
	reachMu.Lock()
	for _, a := range addresses {
		if e, ok := reachCache[a]; ok && now.Sub(e.at) < reachCacheFor {
			out[a] = e.answers
		} else if _, seen := out[a]; !seen {
			out[a] = false
			todo = append(todo, a)
		}
	}
	// the cache never grows past what was asked lately
	for a, e := range reachCache {
		if now.Sub(e.at) >= reachCacheFor {
			delete(reachCache, a)
		}
	}
	reachMu.Unlock()
	if len(todo) > maxPostgresProposals {
		todo = todo[:maxPostgresProposals]
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, a := range todo {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, reachTimeout)
			err := reachDial(c, address)
			cancel()
			mu.Lock()
			out[address] = err == nil
			mu.Unlock()
		}(a)
	}
	wg.Wait()
	reachMu.Lock()
	for _, a := range todo {
		reachCache[a] = reachEntry{answers: out[a], at: reachClock()}
	}
	reachMu.Unlock()
	return out
}

// checkedProposals are the proposals with their check.
func checkedProposals(ctx context.Context, candidates []pgCandidate) []PostgresProposal {
	inside := insideContainer()
	shared := map[string]bool{}
	if inside {
		shared = cosmosNetworks(ctx)
	}
	proposals := postgresProposals(candidates, inside, shared)
	hosts := []string{}
	for _, p := range proposals {
		if p.Host != "" {
			hosts = append(hosts, p.Host)
		}
	}
	reach := reachOf(ctx, hosts)
	for i := range proposals {
		if proposals[i].Host == "" {
			continue
		}
		if reach[proposals[i].Host] {
			proposals[i].Reach = "answers"
		} else {
			proposals[i].Reach = "silent"
		}
	}
	return proposals
}

func init() {
	utils.PostgresHostAdvice = postgresHostAdvice
}

// postgresHostAdvice is the sentence that names the likely right server
// after a typed one failed for reason (closed, dns, timeout, network); ""
// when nothing better is known.
func postgresHostAdvice(ctx context.Context, typed string, reason string) string {
	switch reason {
	case "closed", "dns", "timeout", "network":
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, adviceTimeout)
	defer cancel()
	candidates, err := postgresCandidates(ctx)
	if err != nil {
		return ""
	}
	return adviceFor(typed, reason, candidates, checkedProposals(ctx, candidates))
}

// adviceFor is the advice from the facts (pure, tested).
func adviceFor(typed string, reason string, candidates []pgCandidate, proposals []PostgresProposal) string {
	name, port := typed, "5432"
	if h, p, err := net.SplitHostPort(typed); err == nil {
		name, port = h, p
	}
	lower := strings.ToLower(strings.TrimSuffix(name, "."))

	// the best proposal: one that answers, a published port first
	var best *PostgresProposal
	for _, source := range []string{"published", "container-network", "address"} {
		for i := range proposals {
			p := &proposals[i]
			if best == nil && p.Host != "" && p.Reach == "answers" && p.HostSource == source && !strings.EqualFold(p.Host, typed) {
				best = p
			}
		}
	}
	use := func() string {
		if best == nil {
			return ""
		}
		return "use " + best.Host + " (the PostgreSQL container " + best.Container + ")."
	}

	loopback := lower == "localhost" || lower == "localhost.localdomain"
	if ip := net.ParseIP(name); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if loopback && (reason == "closed" || reason == "timeout") {
		// is the port published elsewhere only? (the typed port first)
		bindings := []pgBinding{}
		for _, c := range candidates {
			for _, b := range c.Published {
				if b.HostPort == port {
					bindings = append([]pgBinding{b}, bindings...)
				} else {
					bindings = append(bindings, b)
				}
			}
		}
		{
			for _, b := range bindings {
				ip := net.ParseIP(b.HostIP)
				if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
					continue
				}
				host, _ := bindingHost(b)
				if host == "" {
					continue
				}
				if insideContainer() {
					return "Cosmos runs in a container: " + name + " is Cosmos' own container, not the server. On this server PostgreSQL's port is published on " + ip.String() + ": use " + host + "."
				}
				return "On this server PostgreSQL's port is published on " + ip.String() + " only, not on " + name + ": use " + host + "."
			}
		}
		if insideContainer() {
			if best != nil {
				return "Cosmos runs in a container: " + name + " is Cosmos' own container, not the server: " + use()
			}
			return "Cosmos runs in a container: " + name + " is Cosmos' own container, not the server."
		}
		if best != nil {
			return "Nothing listens on port " + port + " of this server: " + use()
		}
		return ""
	}

	// a container's own address on Docker's network, typed or saved earlier:
	// it changed when the container was made again
	if ip := net.ParseIP(name); ip != nil {
		for _, c := range candidates {
			for _, a := range c.Addresses {
				if a == ip.String() {
					if best != nil && best.HostSource != "address" {
						return name + " is the address of the container " + strings.TrimPrefix(c.Info.Name, "/") + " on Docker's network: it changes when the container is made again; " + use()
					}
					return name + " is the address of the container " + strings.TrimPrefix(c.Info.Name, "/") + " on Docker's network: it changes when the container is made again; publish its port 5432 on 127.0.0.1 and use 127.0.0.1:5432."
				}
			}
		}
	}

	if reason == "dns" {
		container := ""
		for _, c := range candidates {
			if strings.EqualFold(strings.TrimPrefix(c.Info.Name, "/"), lower) {
				container = lower
			}
		}
		switch {
		case container != "" && !insideContainer():
			if best != nil {
				return "A container's name is not a host name for Cosmos itself (Cosmos runs on the server, not on a Docker network): " + use()
			}
			return "A container's name is not a host name for Cosmos itself (Cosmos runs on the server, not on a Docker network): publish the container's port 5432 on 127.0.0.1 and use 127.0.0.1:5432."
		case container != "":
			if best != nil {
				return "Cosmos shares no Docker network with the container " + container + ", so its name is not known to Cosmos: " + use()
			}
			return "Cosmos shares no Docker network with the container " + container + ", so its name is not known to Cosmos: use \"Let these apps reach it\" in the container's page."
		case strings.HasSuffix(lower, ".local"):
			if best != nil {
				return "Names ending in .local exist only for apps that have a URL in Cosmos, and a container's name is not a host name for Cosmos itself: " + use()
			}
			return "Names ending in .local exist only for apps that have a URL in Cosmos, and a container's name is not a host name for Cosmos itself."
		}
	}
	if best != nil {
		return "PostgreSQL runs on this server in the container " + best.Container + ": " + use()
	}
	return ""
}
