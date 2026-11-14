package certs

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// A domain whose DNS this server cannot write can still get certificates
// through the DNS challenge: its owner creates, once, at that domain's own
// provider, the record
//
//	_acme-challenge.example.net  CNAME  example-net._acme.example.org
//
// where example.org is a domain this server can write. Certificate
// authorities follow the CNAME when they look for the challenge, and so
// does the ACME library when it decides where to write it. Nothing else of
// example.net is touched or needed.

// Resolver is the DNS this package asks. The system's resolver on a
// server; a table in the tests.
type Resolver interface {
	// CNAME returns what a name is an alias of, "" when it is none.
	CNAME(ctx context.Context, name string) (string, error)
	// NS returns the name servers of a domain.
	NS(ctx context.Context, name string) ([]string, error)
}

var resolver Resolver = systemResolver{}

const (
	lookupTimeout = 5 * time.Second
	maxHops       = 8
)

// systemResolver asks the name servers of /etc/resolv.conf, and only them.
type systemResolver struct{}

func (systemResolver) servers() ([]string, error) {
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil || len(config.Servers) == 0 {
		return nil, errors.New("no name server is configured on this server")
	}
	servers := []string{}
	for _, server := range config.Servers {
		servers = append(servers, net.JoinHostPort(server, config.Port))
	}
	return servers, nil
}

func (r systemResolver) ask(ctx context.Context, name string, kind uint16) (*dns.Msg, error) {
	servers, err := r.servers()
	if err != nil {
		return nil, err
	}
	question := new(dns.Msg)
	question.SetQuestion(dns.Fqdn(name), kind)
	question.RecursionDesired = true
	var last error
	for _, server := range servers {
		for _, network := range []string{"udp", "tcp"} {
			client := &dns.Client{Net: network, Timeout: lookupTimeout}
			answer, _, err := client.ExchangeContext(ctx, question, server)
			if err != nil {
				last = err
				break
			}
			if answer.Truncated && network == "udp" {
				continue
			}
			if answer.Rcode == dns.RcodeSuccess || answer.Rcode == dns.RcodeNameError {
				return answer, nil
			}
			last = errors.New("the name server answered " + dns.RcodeToString[answer.Rcode])
			break
		}
	}
	if last == nil {
		last = errors.New("no answer")
	}
	return nil, last
}

func (r systemResolver) CNAME(ctx context.Context, name string) (string, error) {
	answer, err := r.ask(ctx, name, dns.TypeCNAME)
	if err != nil {
		return "", err
	}
	for _, record := range answer.Answer {
		if cname, ok := record.(*dns.CNAME); ok && strings.EqualFold(strings.TrimSuffix(cname.Hdr.Name, "."), normalName(name)) {
			return normalName(cname.Target), nil
		}
	}
	return "", nil
}

func (r systemResolver) NS(ctx context.Context, name string) ([]string, error) {
	answer, err := r.ask(ctx, name, dns.TypeNS)
	if err != nil {
		return nil, err
	}
	servers := []string{}
	for _, record := range answer.Answer {
		if ns, ok := record.(*dns.NS); ok {
			servers = append(servers, normalName(ns.Ns))
		}
	}
	sort.Strings(servers)
	return servers, nil
}

// challengeName is the record a certificate authority looks at for a name.
// A wildcard is proven at the name it is a wildcard of.
func challengeName(name string) string {
	return "_acme-challenge." + strings.TrimPrefix(normalName(name), "*.")
}

// DelegationTarget is the name proposed as the target of the CNAME for a
// name whose challenge is answered in the domain via. Any name under via
// works; this one says which name it is for and cannot be the address of
// an application ("_acme" is not a host name).
func DelegationTarget(name string, via string) string {
	label := strings.ReplaceAll(strings.TrimPrefix(normalName(name), "*."), ".", "-")
	if len(label) > 63 {
		label = strings.TrimRight(label[:63], "-")
	}
	return label + "._acme." + normalName(via)
}

// The states of the record of a delegated challenge.
const (
	DelegationOK        = "ok"        // a CNAME that ends in the other domain
	DelegationMissing   = "missing"   // no CNAME at the challenge name
	DelegationElsewhere = "elsewhere" // a CNAME, but to a name outside the other domain
	DelegationLoop      = "loop"      // aliases without an end
	DelegationFailed    = "failed"    // the lookup did not work
)

// Delegation is the state of the record for one name.
type Delegation struct {
	// Name is the name to certify, Record the record to create and Target
	// what it should point at.
	Name   string `json:"name"`
	Record string `json:"record"`
	Target string `json:"target"`
	State  string `json:"state"`
	// Found is where the record points now, when it exists.
	Found string `json:"found,omitempty"`
	// Detail is the resolver's own words when the lookup failed.
	Detail string `json:"detail,omitempty"`
}

func under(name string, domain string) bool {
	name, domain = normalName(name), normalName(domain)
	return name == domain || strings.HasSuffix(name, "."+domain)
}

// CheckDelegation looks the record of a name up: it must be a CNAME whose
// chain (eight aliases at most) ends at a name under via.
func CheckDelegation(ctx context.Context, r Resolver, name string, via string) Delegation {
	result := Delegation{Name: normalName(name), Record: challengeName(name), Target: DelegationTarget(name, via), State: DelegationMissing}
	current := result.Record
	seen := map[string]bool{current: true}
	for hop := 0; hop < maxHops; hop++ {
		lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
		target, err := r.CNAME(lookup, current)
		cancel()
		if err != nil {
			result.State = DelegationFailed
			result.Detail = clean(err.Error(), 200)
			return result
		}
		if target == "" {
			break
		}
		target = normalName(target)
		result.Found = target
		if seen[target] {
			result.State = DelegationLoop
			return result
		}
		seen[target] = true
		current = target
		if hop == maxHops-1 {
			result.State = DelegationLoop
			return result
		}
	}
	switch {
	case result.Found == "":
		result.State = DelegationMissing
	case under(result.Found, via):
		result.State = DelegationOK
	default:
		result.State = DelegationElsewhere
	}
	return result
}

// delegationReady checks, before the CA is asked anything, that the record
// of every name of an order hands its challenge to via. An order that
// cannot be validated would only use up the CA's patience.
func delegationReady(ctx context.Context, names []string, via string) *Failure {
	problems := []string{}
	checked := map[string]bool{}
	for _, name := range names {
		record := challengeName(name)
		if checked[record] {
			continue
		}
		checked[record] = true
		state := CheckDelegation(ctx, resolver, name, via)
		switch state.State {
		case DelegationOK:
		case DelegationFailed:
			problems = append(problems, state.Record+" could not be looked up ("+state.Detail+")")
		case DelegationElsewhere, DelegationLoop:
			problems = append(problems, state.Record+" points at "+state.Found+", not at a name under "+normalName(via))
		default:
			problems = append(problems, state.Record+" must be a CNAME to "+state.Target)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return &Failure{Code: FailDelegationMissing, Detail: clean(strings.Join(problems, "; "), maxDetail)}
}
