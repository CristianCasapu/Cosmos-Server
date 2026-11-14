package proxy

// Fork addition: what the panel is told about the .local names, see
// fork_mdns.go: whether Avahi runs, on which interfaces and with which
// address the names are published, what Avahi made of each name and whether
// the name is really answered on the local network.
//
// The last part asks the network: one mDNS question per name ("legacy
// unicast", from a port of its own, so responders answer that socket
// directly) on each interface the names are published on. Only the names
// Cosmos publishes are asked for, at most mdnsMaxChecked of them, with a
// deadline of 1.5 s, one check at a time, kept for 30 s. The socket exists
// for the length of the question, like a DNS lookup: nothing listens.

import (
	"bufio"
	"encoding/json"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	mdnsMaxChecked   = 64
	mdnsCheckTimeout = 1500 * time.Millisecond
	mdnsCheckKeep    = 30 * time.Second
	mdnsCheckAgain   = 5 * time.Second // a forced check reuses one younger than this
	mdnsMaxBody      = 1024
)

type mdnsAnswer struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	From      string `json:"from"` // the machine that answered
	Own       bool   `json:"own"`  // this server, with the address it publishes
}

type localNameStatus struct {
	Name     string       `json:"name"`
	State    string       `json:"state"`
	Detail   string       `json:"detail,omitempty"`
	Checked  bool         `json:"checked"`  // asked on the network
	Answered bool         `json:"answered"` // this server answered with its address
	Answers  []mdnsAnswer `json:"answers"`
}

type avahiConfInfo struct {
	Read            bool     `json:"read"`
	AllowInterfaces []string `json:"allowInterfaces"`
	DenyInterfaces  []string `json:"denyInterfaces"`
}

type localNamesResponse struct {
	Enabled    bool              `json:"enabled"`
	Avahi      string            `json:"avahi"` // running, absent, off
	HostName   string            `json:"hostName"`
	Interfaces []lanInterface    `json:"interfaces"`
	Problems   []mdnsProblem     `json:"problems"`
	Names      []localNameStatus `json:"names"`
	CheckedAt  time.Time         `json:"checkedAt"`
	AvahiConf  avahiConfInfo     `json:"avahiConf"`
	HTTPSPort  string            `json:"httpsPort"`
}

// mdnsAsk asks the network on one interface for the names; the tests replace it.
var mdnsAsk = askMDNS

// mdnsAvahiConfPath is read (never written) to say whether Avahi is limited
// to some interfaces.
var mdnsAvahiConfPath = "/etc/avahi/avahi-daemon.conf"

var (
	checkMu    sync.Mutex // one check at a time
	checkCache struct {
		sync.Mutex
		at      time.Time
		answers map[string][]mdnsAnswer // name -> answers
		checked map[string]bool
		spec    string
	}
)

func forgetLocalNamesCheck() {
	checkCache.Lock()
	checkCache.at = time.Time{}
	checkCache.Unlock()
}

// snapshot is the publisher's view, refreshed against Avahi.
func (p *localNamesPublisher) snapshot() localNamesResponse {
	p.mu.Lock()
	defer p.mu.Unlock()
	// the states change while Avahi probes: ask again
	p.reconcileLocked(false)

	config := utils.GetMainConfig()
	resp := localNamesResponse{
		Enabled:    config.HTTPConfig.PublishMDNS,
		Avahi:      "running",
		HostName:   p.hostFqdn,
		Interfaces: append([]lanInterface{}, p.ifaces...),
		Problems:   append([]mdnsProblem{}, p.problems...),
		Names:      []localNameStatus{},
		HTTPSPort:  config.HTTPConfig.HTTPSPort,
	}
	if p.avahiErr != "" {
		resp.Avahi = p.avahiErr
	}
	for _, name := range p.order {
		status := localNameStatus{Name: name, Answers: []mdnsAnswer{}}
		if entry := p.entries[name]; entry != nil {
			status.State, status.Detail = entry.state, entry.detail
		}
		if p.avahiErr == "absent" {
			status.State = nameFailed
		}
		resp.Names = append(resp.Names, status)
	}
	return resp
}

// localNamesStatus is the answer of the route. `force` asks the network again.
func localNamesStatus(force bool) localNamesResponse {
	resp := localNames.snapshot()
	resp.AvahiConf = readAvahiConf()
	if !resp.Enabled || resp.Avahi != "running" || len(resp.Interfaces) == 0 {
		return resp
	}

	spec := specOf(resp.Interfaces)
	checkCache.Lock()
	age := time.Since(checkCache.at)
	fresh := checkCache.spec == spec && (age < mdnsCheckKeep && !force || age < mdnsCheckAgain)
	checkCache.Unlock()

	if !fresh {
		checkMu.Lock()
		checkCache.Lock()
		age = time.Since(checkCache.at)
		again := checkCache.spec != spec || age >= mdnsCheckAgain && (force || age >= mdnsCheckKeep)
		checkCache.Unlock()
		if again {
			names := []string{}
			for _, status := range resp.Names {
				if (status.State == namePublished || status.State == nameRegistering || status.State == nameConflict || status.State == nameHostName) && len(names) < mdnsMaxChecked {
					names = append(names, status.Name)
				}
			}
			answers := map[string][]mdnsAnswer{}
			checked := map[string]bool{}
			if len(names) > 0 {
				var wg sync.WaitGroup
				var mu sync.Mutex
				for _, iface := range resp.Interfaces {
					wg.Add(1)
					go func(iface lanInterface) {
						defer wg.Done()
						got := mdnsAsk(iface, names, mdnsCheckTimeout)
						mu.Lock()
						defer mu.Unlock()
						for name, list := range got {
							answers[name] = append(answers[name], list...)
						}
					}(iface)
				}
				wg.Wait()
				for _, name := range names {
					checked[name] = true
				}
			}
			checkCache.Lock()
			checkCache.at, checkCache.answers, checkCache.checked, checkCache.spec = time.Now(), answers, checked, spec
			checkCache.Unlock()
		}
		checkMu.Unlock()
	}

	checkCache.Lock()
	defer checkCache.Unlock()
	resp.CheckedAt = checkCache.at
	for i := range resp.Names {
		name := &resp.Names[i]
		name.Checked = checkCache.checked[name.Name]
		for _, answer := range checkCache.answers[name.Name] {
			name.Answers = append(name.Answers, answer)
			if answer.Own {
				name.Answered = true
			}
		}
	}
	return resp
}

// askMDNS sends one question per name on the interface and collects the
// address answers until the deadline.
func askMDNS(iface lanInterface, names []string, timeout time.Duration) map[string][]mdnsAnswer {
	out := map[string][]mdnsAnswer{}
	local := net.ParseIP(iface.Address)
	if local == nil {
		return out
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local})
	if err != nil {
		return out
	}
	defer conn.Close()
	packet := ipv4.NewPacketConn(conn)
	if sys, err := net.InterfaceByIndex(iface.Index); err == nil {
		packet.SetMulticastInterface(sys)
	}
	packet.SetMulticastTTL(255)
	packet.SetMulticastLoopback(true) // this server's own Avahi answers too

	wanted := map[string]bool{}
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	for _, name := range names {
		query := new(dns.Msg)
		query.Id = uint16(rand.Intn(1 << 16))
		query.RecursionDesired = false
		query.SetQuestion(dns.Fqdn(name), dns.TypeA)
		packed, err := query.Pack()
		if err != nil {
			continue
		}
		wanted[strings.ToLower(dns.Fqdn(name))] = true
		conn.WriteToUDP(packed, group)
	}

	conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 9000)
	seen := map[string]bool{}
	for reads := 0; reads < 1024; reads++ {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		for _, answer := range parseMDNSAnswers(buf[:n], wanted) {
			key := answer.name + "|" + answer.address + "|" + from.IP.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			name := strings.TrimSuffix(answer.name, ".")
			out[name] = append(out[name], mdnsAnswer{
				Interface: iface.Name,
				Address:   answer.address,
				From:      from.IP.String(),
				Own:       answer.address == iface.Address,
			})
		}
	}
	return out
}

type parsedAnswer struct{ name, address string }

// parseMDNSAnswers returns the A records of a reply for the wanted names
// (lower case, with the final dot).
func parseMDNSAnswers(raw []byte, wanted map[string]bool) []parsedAnswer {
	reply := new(dns.Msg)
	if reply.Unpack(raw) != nil || !reply.Response {
		return nil
	}
	out := []parsedAnswer{}
	for _, rr := range append(append([]dns.RR{}, reply.Answer...), reply.Extra...) {
		a, ok := rr.(*dns.A)
		if !ok {
			continue
		}
		name := strings.ToLower(a.Hdr.Name)
		if !wanted[name] || a.A == nil {
			continue
		}
		out = append(out, parsedAnswer{name: name, address: a.A.String()})
	}
	return out
}

// readAvahiConf reads the interface limits of Avahi's configuration, if it
// can be read.
func readAvahiConf() avahiConfInfo {
	info := avahiConfInfo{AllowInterfaces: []string{}, DenyInterfaces: []string{}}
	file, err := os.Open(mdnsAvahiConfPath)
	if err != nil {
		return info
	}
	defer file.Close()
	info.Read = true
	section := ""
	scanner := bufio.NewScanner(io.LimitReader(file, 64*1024))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section != "server" {
			continue
		}
		list := []string{}
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if validMDNSInterfaceName.MatchString(item) && len(list) < 16 {
				list = append(list, item)
			}
		}
		switch strings.TrimSpace(key) {
		case "allow-interfaces":
			info.AllowInterfaces = list
		case "deny-interfaces":
			info.DenyInterfaces = list
		}
	}
	return info
}

// LocalNamesRoute: GET the state of the .local names; POST {"check": true}
// asks the network again. Admins only: it names interfaces and addresses.
func LocalNamesRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
		return
	}
	force := false
	switch req.Method {
	case "GET":
	case "POST":
		raw, err := io.ReadAll(io.LimitReader(req.Body, mdnsMaxBody+1))
		if err != nil || len(raw) > mdnsMaxBody {
			utils.HTTPError(w, "Request too large", http.StatusBadRequest, "MDN001")
			return
		}
		var body struct {
			Check bool `json:"check"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &body) != nil {
			utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "MDN002")
			return
		}
		force = body.Check
	default:
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   localNamesStatus(force),
	})
}
