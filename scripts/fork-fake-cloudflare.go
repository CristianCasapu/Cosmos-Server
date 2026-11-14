//go:build ignore

// Fork addition: a stand-in for Cloudflare's API, for trying the Cloudflare
// integration (src/cloudflare) in a browser without sending anything to
// Cloudflare. Used with scripts/fork-ui-check-cloudflare.mjs. The stand-in
// itself is the package the Go tests use (src/cloudflare/cffake).
//
// It answers HTTPS on :443 as api.cloudflare.com, with a certificate from an
// authority it makes at start and writes to /shared/ca.pem, and has a plain
// control port (:8080): GET /records lists the records of example.org, GET
// /calls the requests received (?clear=1 empties the list), GET
// /proxied?name=<record>&on=<0|1> flips a record's flag the way an edit in
// Cloudflare's dashboard would.
//
// What it holds at start is the layout this was written for: the domain
// example.org whose own name is proxied and points at the address the test
// server advertises (203.0.113.10), a website alias, a mail record; one
// token limited to that domain, one that may only read, one that sees a
// second domain too.
//
// It is also the name server of what it holds (:53, UDP and TCP), so that
// the record a certificate order writes through the API can be seen by a
// test certificate authority (scripts/fork-acme-pebble.sh): SOA and NS for
// each domain, A, TXT and CNAME from the records, an alias followed like a
// resolver would. Records "at another DNS provider" (a domain the API does
// not have) are added on the control port: GET /dns?zone=<domain> makes the
// domain, GET /dns?name=<name>&type=<A|TXT|CNAME>&value=<value> adds a record,
// GET /dns?clear=<name> removes what was added at a name. GET
// /token?status=<active|disabled> switches the first token, and GET
// /queries lists the DNS questions received (?clear=1 empties the list).
//
// How it is used: on a Docker network made with --internal (no way out), run
// it in a container with the network alias api.cloudflare.com and a folder
// mounted at /shared; start the throwaway Cosmos on the same network with
// that folder mounted and SSL_CERT_FILE=/shared/ca.pem. The server's own code
// then talks to this program, unchanged: fixed host, certificate verified.
// It is not part of any build (go run scripts/fork-fake-cloudflare.go).
//
// Authenticated Origin Pulls. The stand-in also takes the client certificate
// Cosmos uploads for a domain (with the token SSLToken, which may do that
// and nothing else; the "Edit zone DNS" tokens above may not) and lists what
// it holds at GET /aop on the control port. A second role stands in for
// Cloudflare's proxy:
//
//	fork-fake-cloudflare edge <cosmos address:port> <address to come from> <control URL of the API stand-in>
//
// It has to run inside the test Cosmos' own network namespace, where
// <address to come from> (one of Cloudflare's published addresses, such as
// 173.245.48.5) was added to the loopback interface: the requests then reach
// Cosmos from that address without any route on the host or the test network
// being touched. Its control port (:8081) makes it visit:
//
//	GET /visit?host=<name>&cert=uploaded|wrong|none&n=<count>&visitor=<address>&path=<path>
//
// "uploaded" presents the certificate Cosmos gave to the API stand-in, the
// way Cloudflare presents what was uploaded for a zone; "wrong" one from an
// authority of its own; "none" nothing. The answer lists the status of each
// request and whether Cosmos asked for a certificate.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"strconv"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
)

const (
	GoodToken  = "cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789"
	ReadToken  = "readonly0123456789abcdefghijklmnopqrstuvwxYZ"
	BroadToken = "allzones0123456789abcdefghijklmnopqrstuvwxYZ"
	// SSLToken may upload a client certificate for example.org ("SSL and
	// Certificates: Edit") and nothing else: it does not even list the domain
	SSLToken = "sslcerts0123456789abcdefghijklmnopqrstuvwxYZ"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "edge" {
		if len(os.Args) != 5 {
			log.Fatal("usage: edge <cosmos address:port> <address to come from> <control URL of the API stand-in>")
		}
		edge(os.Args[2], os.Args[3], os.Args[4])
		return
	}

	fake := cffake.New()
	fake.AddZone("example.org",
		cffake.Record{Type: "A", Name: "example.org", Content: "203.0.113.10", Proxied: true},
		cffake.Record{Type: "CNAME", Name: "www.example.org", Content: "example.org", Proxied: true},
		cffake.Record{Type: "MX", Name: "example.org", Content: "mail.example.org"},
		cffake.Record{Type: "A", Name: "mail.example.org", Content: "203.0.113.10"},
		cffake.Record{Type: "TXT", Name: "example.org", Content: `"v=spf1 mx -all"`},
	)
	fake.AddZone("example.net")
	fake.AddToken(GoodToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true})
	fake.AddToken(ReadToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true})
	fake.AddToken(BroadToken, cffake.Token{ReadDNS: true, EditDNS: true, ReadSettings: true})
	fake.AddToken(SSLToken, cffake.Token{Zones: []string{"example.org"}, NoZoneRead: true, EditSSL: true})

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cosmos-cloudflare test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.cloudflare.com"},
		DNSNames:  []string{"api.cloudflare.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)

	if err := os.WriteFile("/shared/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644); err != nil {
		log.Fatal(err)
	}

	go func() {
		server := &http.Server{Addr: ":443", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("%s %s", r.Method, r.URL.Path)
			fake.ServeHTTP(w, r)
		}), TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		}}
		log.Fatal(server.ListenAndServeTLS("", ""))
	}()

	control := http.NewServeMux()
	control.HandleFunc("/records", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fake.Records("example.org"))
	})
	control.HandleFunc("/calls", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fake.Calls(r.URL.Query().Get("clear") != ""))
	})
	control.HandleFunc("/proxied", func(w http.ResponseWriter, r *http.Request) {
		fake.SetProxied("example.org", r.URL.Query().Get("name"), "A", r.URL.Query().Get("on") == "1")
		io.WriteString(w, "ok\n")
	})
	control.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fake.AddToken(GoodToken, cffake.Token{Status: r.URL.Query().Get("status"), Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true})
		io.WriteString(w, "ok\n")
	})
	names := &nameServer{fake: fake, extra: map[string][]cffake.Record{}}
	control.HandleFunc("/dns", names.control)
	control.HandleFunc("/queries", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(names.asked(r.URL.Query().Get("clear") != ""))
	})
	names.start()
	control.HandleFunc("/aop", func(w http.ResponseWriter, r *http.Request) {
		certs, enabled := fake.OriginPull("example.org")
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": enabled, "certificates": certs})
	})
	log.Println("fake Cloudflare ready")
	log.Fatal(http.ListenAndServe(":8080", control))
}

// nameServer answers DNS for the domains of the stand-in and for the ones
// added on the control port.
type nameServer struct {
	fake *cffake.Server
	mu   sync.Mutex
	// extra: domain -> records kept somewhere else than the API
	extra   map[string][]cffake.Record
	queries []string
}

func (n *nameServer) control(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	query := r.URL.Query()
	lower := func(key string) string { return strings.TrimSuffix(strings.ToLower(query.Get(key)), ".") }
	switch {
	case query.Get("zone") != "":
		if _, known := n.extra[lower("zone")]; !known {
			n.extra[lower("zone")] = []cffake.Record{}
		}
	case query.Get("clear") != "":
		for zone, records := range n.extra {
			kept := []cffake.Record{}
			for _, record := range records {
				if record.Name != lower("clear") {
					kept = append(kept, record)
				}
			}
			n.extra[zone] = kept
		}
	case query.Get("name") != "":
		zone := ""
		for known := range n.extra {
			if (lower("name") == known || strings.HasSuffix(lower("name"), "."+known)) && len(known) > len(zone) {
				zone = known
			}
		}
		if zone == "" {
			http.Error(w, "no such domain: make it with ?zone= first", http.StatusBadRequest)
			return
		}
		n.extra[zone] = append(n.extra[zone], cffake.Record{Type: strings.ToUpper(query.Get("type")), Name: lower("name"), Content: query.Get("value")})
	}
	io.WriteString(w, "ok\n")
}

func (n *nameServer) asked(clear bool) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	list := append([]string{}, n.queries...)
	if clear {
		n.queries = nil
	}
	return list
}

// zoneOf returns the domain a name belongs to and its records.
func (n *nameServer) zoneOf(name string) (string, []cffake.Record) {
	zone := ""
	held := map[string]bool{}
	for _, known := range n.fake.ZoneNames() {
		held[known] = true
	}
	n.mu.Lock()
	for known := range n.extra {
		held[known] = false
	}
	n.mu.Unlock()
	for known := range held {
		if (name == known || strings.HasSuffix(name, "."+known)) && len(known) > len(zone) {
			zone = known
		}
	}
	if zone == "" {
		return "", nil
	}
	if held[zone] {
		return zone, n.fake.Records(zone)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return zone, append([]cffake.Record{}, n.extra[zone]...)
}

func (n *nameServer) soa(zone string) dns.RR {
	return &dns.SOA{Hdr: dns.RR_Header{Name: dns.Fqdn(zone), Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 60},
		Ns: "ns1." + dns.Fqdn(zone), Mbox: "hostmaster." + dns.Fqdn(zone), Serial: uint32(time.Now().Unix()), Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 5}
}

// answer fills in the records of a name; an alias is followed, eight at most.
func (n *nameServer) answer(message *dns.Msg, name string, kind uint16, own net.IP, hops int) (exists bool) {
	zone, records := n.zoneOf(name)
	if zone == "" {
		return false
	}
	header := func(kind uint16) dns.RR_Header {
		return dns.RR_Header{Name: dns.Fqdn(name), Rrtype: kind, Class: dns.ClassINET, Ttl: 5}
	}
	if name == zone {
		exists = true
		if kind == dns.TypeSOA {
			message.Answer = append(message.Answer, n.soa(zone))
		}
		if kind == dns.TypeNS {
			message.Answer = append(message.Answer, &dns.NS{Hdr: header(dns.TypeNS), Ns: "ns1." + dns.Fqdn(zone)})
		}
	}
	if name == "ns1."+zone {
		exists = true
		if kind == dns.TypeA {
			message.Answer = append(message.Answer, &dns.A{Hdr: header(dns.TypeA), A: own})
		}
	}
	for _, record := range records {
		if strings.ToLower(record.Name) != name {
			if strings.HasSuffix(strings.ToLower(record.Name), "."+name) {
				exists = true // an empty name above a record
			}
			continue
		}
		exists = true
		switch {
		case record.Type == "CNAME":
			target := strings.TrimSuffix(strings.ToLower(record.Content), ".")
			message.Answer = append(message.Answer, &dns.CNAME{Hdr: header(dns.TypeCNAME), Target: dns.Fqdn(target)})
			if kind != dns.TypeCNAME && hops < 8 {
				n.answer(message, target, kind, own, hops+1)
			}
		case record.Type == "TXT" && kind == dns.TypeTXT:
			message.Answer = append(message.Answer, &dns.TXT{Hdr: header(dns.TypeTXT), Txt: []string{strings.Trim(record.Content, `"`)}})
		case record.Type == "A" && kind == dns.TypeA:
			if ip := net.ParseIP(record.Content).To4(); ip != nil {
				message.Answer = append(message.Answer, &dns.A{Hdr: header(dns.TypeA), A: ip})
			}
		}
	}
	return exists
}

func (n *nameServer) serve(w dns.ResponseWriter, request *dns.Msg) {
	message := new(dns.Msg)
	message.SetReply(request)
	message.Authoritative = true
	message.RecursionAvailable = true
	if len(request.Question) != 1 {
		message.Rcode = dns.RcodeFormatError
		w.WriteMsg(message)
		return
	}
	question := request.Question[0]
	name := strings.TrimSuffix(strings.ToLower(question.Name), ".")
	n.mu.Lock()
	n.queries = append(n.queries, dns.TypeToString[question.Qtype]+" "+name)
	if len(n.queries) > 5000 {
		n.queries = n.queries[len(n.queries)-5000:]
	}
	n.mu.Unlock()

	own := net.IPv4(127, 0, 0, 1)
	if local, ok := w.LocalAddr().(*net.UDPAddr); ok && local.IP.To4() != nil && !local.IP.IsUnspecified() {
		own = local.IP
	} else if local, ok := w.LocalAddr().(*net.TCPAddr); ok && local.IP.To4() != nil && !local.IP.IsUnspecified() {
		own = local.IP
	} else if ownAddress != nil {
		own = ownAddress
	}

	zone, _ := n.zoneOf(name)
	if zone == "" {
		message.Rcode = dns.RcodeRefused
		w.WriteMsg(message)
		return
	}
	exists := n.answer(message, name, question.Qtype, own, 0)
	if len(message.Answer) == 0 {
		message.Ns = append(message.Ns, n.soa(zone))
		if !exists {
			message.Rcode = dns.RcodeNameError
		}
	}
	w.WriteMsg(message)
}

// ownAddress is this container's address, for the A record of the name
// server's own name when a question arrives on the wildcard socket.
var ownAddress net.IP

func (n *nameServer) start() {
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() {
				ownAddress = network.IP.To4()
				break
			}
		}
	}
	handler := dns.HandlerFunc(n.serve)
	for _, network := range []string{"udp", "tcp"} {
		server := &dns.Server{Addr: ":53", Net: network, Handler: handler}
		go func() { log.Fatal(server.ListenAndServe()) }()
	}
}

// edge stands in for Cloudflare's proxy: it makes requests to Cosmos from an
// address inside Cloudflare's ranges, presenting what it is told to.
func edge(target string, from string, api string) {
	source := net.ParseIP(from)
	if source == nil {
		log.Fatal("not an address: " + from)
	}
	wrongAuthority := cffake.NewAuthority("an authority Cosmos does not know")
	wrong := wrongAuthority.Issue(cffake.Leaf{})

	uploaded := func() (*tls.Certificate, error) {
		resp, err := http.Get(api + "/aop")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var held struct {
			Enabled      bool                    `json:"enabled"`
			Certificates []cffake.OriginPullCert `json:"certificates"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&held); err != nil {
			return nil, err
		}
		if !held.Enabled || len(held.Certificates) == 0 {
			// nothing uploaded, or not switched on: Cloudflare presents nothing
			return nil, nil
		}
		// the most recently uploaded one is the one in use
		last := held.Certificates[len(held.Certificates)-1]
		pair, err := tls.X509KeyPair([]byte(last.Certificate), []byte(last.PrivateKey))
		if err != nil {
			return nil, err
		}
		return &pair, nil
	}

	control := http.NewServeMux()
	control.HandleFunc("/visit", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		host, visitor, path := query.Get("host"), query.Get("visitor"), query.Get("path")
		if path == "" {
			path = "/"
		}
		if visitor == "" {
			visitor = "198.51.100.77"
		}
		count, _ := strconv.Atoi(query.Get("n"))
		if count < 1 || count > 50 {
			count = 1
		}
		var present *tls.Certificate
		switch query.Get("cert") {
		case "uploaded":
			var err error
			if present, err = uploaded(); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		case "wrong":
			present = &wrong
		}

		var mu sync.Mutex
		asked := false
		statuses := []int{}
		problem := ""
		for i := 0; i < count; i++ {
			transport := &http.Transport{
				DisableKeepAlives: true,
				DialContext:       (&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: source}}).DialContext,
				TLSClientConfig: &tls.Config{
					ServerName: host,
					// the test Cosmos serves a certificate of its own making
					InsecureSkipVerify: true,
					GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
						mu.Lock()
						asked = true
						mu.Unlock()
						if present == nil {
							return &tls.Certificate{}, nil
						}
						return present, nil
					},
				},
			}
			req, _ := http.NewRequest("GET", "https://"+target+path, nil)
			req.Host = host
			req.Header.Set("CF-Connecting-IP", visitor)
			req.Header.Set("X-Forwarded-For", visitor)
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			if err != nil {
				statuses = append(statuses, 0)
				problem = err.Error()
				continue
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			statuses = append(statuses, resp.StatusCode)
		}
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]interface{}{"statuses": statuses, "asked": asked, "presented": present != nil, "problem": problem})
	})
	log.Println("fake Cloudflare edge ready, coming from " + from)
	log.Fatal(http.ListenAndServe(":8081", control))
}
