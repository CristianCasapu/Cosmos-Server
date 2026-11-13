// Fork addition: a mail provider for the browser check of the email settings
// (scripts/fork-ui-check-email.mjs). Nothing of it is part of Cosmos.
//
// One process, to run in a container on the test network, that is:
//   - the only name server the test instance has (start the instance with
//     --dns <address of this container>): it knows three made-up mail
//     domains and answers "no such name" for everything else, so no question
//     of a test ever reaches a real name server;
//   - a mail server with TLS from the start on 465 and STARTTLS on 587, a
//     self-signed certificate and one account. It accepts mail for anyone
//     and delivers it nowhere;
//   - a small HTTP page on 8025 (/state) that tells the test what it was
//     sent: the commands, the sign-ins and the messages.
//
// The domains (FAKE_DOMAIN, default fakemail.test, and two fixed ones):
//
//	fakemail.test   MX mx.fakemail.test, SRV records for smtp.fakemail.test
//	                on 465 and 587; smtp. and mx. point to this container
//	plainmail.test  no MX, no SRV; smtp.plainmail.test points here
//	hosted.test     MX aspmx.l.google.com (a domain "hosted at Google");
//	                smtp.gmail.com does not resolve here, so nothing answers
//
// Build: CGO_ENABLED=0 go build -o fake-mail ./scripts/fork-fake-mail
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

var (
	domain   = env("FAKE_DOMAIN", "fakemail.test")
	account  = env("FAKE_USER", "cosmos@"+env("FAKE_DOMAIN", "fakemail.test"))
	password = env("FAKE_PASSWORD", "right-password")
)

type signIn struct {
	User string `json:"user"`
	OK   bool   `json:"ok"`
	TLS  bool   `json:"tls"`
}

type message struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Header  string   `json:"header"`
	Subject string   `json:"subject"`
}

var state struct {
	sync.Mutex
	Questions []string  `json:"questions"`
	Commands  []string  `json:"commands"`
	SignIns   []signIn  `json:"signIns"`
	Messages  []message `json:"messages"`
}

func ownAddress() net.IP {
	if text := os.Getenv("FAKE_IP"); text != "" {
		return net.ParseIP(text)
	}
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP
		}
	}
	log.Fatal("no address to answer with")
	return nil
}

// ---- name server --------------------------------------------------------------

func serveDNS(here net.IP) {
	hosts := map[string]bool{
		"smtp." + domain + ".": true, "mx." + domain + ".": true, "smtp.plainmail.test.": true,
	}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(r)
		reply.Authoritative = true
		if len(r.Question) != 1 {
			reply.Rcode = dns.RcodeFormatError
			w.WriteMsg(reply)
			return
		}
		q := r.Question[0]
		name := strings.ToLower(q.Name)
		state.Lock()
		state.Questions = append(state.Questions, dns.TypeToString[q.Qtype]+" "+name)
		state.Unlock()
		header := dns.RR_Header{Name: q.Name, Rrtype: q.Qtype, Class: dns.ClassINET, Ttl: 30}
		known := hosts[name] || name == domain+"." || name == "hosted.test." || name == "plainmail.test." ||
			strings.HasSuffix(name, "._tcp."+domain+".")
		switch {
		case q.Qtype == dns.TypeA && hosts[name]:
			reply.Answer = append(reply.Answer, &dns.A{Hdr: header, A: here})
		case q.Qtype == dns.TypeMX && name == domain+".":
			reply.Answer = append(reply.Answer, &dns.MX{Hdr: header, Preference: 10, Mx: "mx." + domain + "."})
		case q.Qtype == dns.TypeMX && name == "hosted.test.":
			reply.Answer = append(reply.Answer, &dns.MX{Hdr: header, Preference: 1, Mx: "aspmx.l.google.com."})
		case q.Qtype == dns.TypeSRV && name == "_submissions._tcp."+domain+".":
			reply.Answer = append(reply.Answer, &dns.SRV{Hdr: header, Priority: 0, Weight: 1, Port: 465, Target: "smtp." + domain + "."})
		case q.Qtype == dns.TypeSRV && name == "_submission._tcp."+domain+".":
			reply.Answer = append(reply.Answer, &dns.SRV{Hdr: header, Priority: 0, Weight: 1, Port: 587, Target: "smtp." + domain + "."})
		case known:
			// the name exists, without a record of this kind
		default:
			reply.Rcode = dns.RcodeNameError
		}
		w.WriteMsg(reply)
	})
	for _, network := range []string{"udp", "tcp"} {
		server := &dns.Server{Addr: ":53", Net: network, Handler: handler}
		go func() { log.Fatal(server.ListenAndServe()) }()
	}
}

// ---- mail server --------------------------------------------------------------

func certificate() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "smtp." + domain},
		DNSNames:     []string{"smtp." + domain, "mx." + domain, "smtp.plainmail.test"},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(72 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func serveSMTP(addr string, config *tls.Config, fromStart bool) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(60 * time.Second))
			if fromStart {
				secure := tls.Server(conn, config)
				if secure.Handshake() != nil {
					return
				}
				session(secure, nil, true, true)
				return
			}
			session(conn, config, false, true)
		}()
	}
}

func note(command string) {
	state.Lock()
	state.Commands = append(state.Commands, command)
	state.Unlock()
}

func decode(text string) string {
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	return string(raw)
}

func session(conn net.Conn, upgrade *tls.Config, encrypted, greet bool) {
	reader := bufio.NewReader(conn)
	say := func(line string) { conn.Write([]byte(line + "\r\n")) }
	read := func() (string, bool) {
		line, err := reader.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err == nil
	}
	check := func(user, pass string) {
		ok := user == account && pass == password
		state.Lock()
		state.SignIns = append(state.SignIns, signIn{User: user, OK: ok, TLS: encrypted})
		state.Unlock()
		if ok {
			say("235 2.7.0 Authentication successful")
		} else {
			say("535 5.7.8 Username and Password not accepted")
		}
	}
	if greet {
		say("220 smtp." + domain + " ESMTP fake mail for tests")
	}
	signedIn := false
	var from string
	var to []string
	for {
		line, ok := read()
		if !ok {
			return
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			note(line)
			say("250-smtp." + domain)
			if upgrade != nil {
				say("250-STARTTLS")
			}
			if encrypted {
				say("250-AUTH PLAIN LOGIN")
			}
			say("250 SIZE 10485760")
		case upper == "STARTTLS" && upgrade != nil:
			note(line)
			say("220 2.0.0 Ready to start TLS")
			secure := tls.Server(conn, upgrade)
			if secure.Handshake() != nil {
				return
			}
			session(secure, nil, true, false)
			return
		case strings.HasPrefix(upper, "AUTH") && !encrypted:
			note("AUTH (refused: not encrypted)")
			say("530 5.7.0 Must issue a STARTTLS command first")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			note("AUTH PLAIN")
			blob := strings.TrimSpace(line[len("AUTH PLAIN"):])
			if blob == "" {
				say("334 ")
				blob, _ = read()
			}
			parts := strings.Split(decode(blob), "\x00")
			if len(parts) != 3 {
				say("501 5.5.2 malformed")
				continue
			}
			check(parts[1], parts[2])
			signedIn = parts[1] == account && parts[2] == password
		case upper == "AUTH LOGIN":
			note("AUTH LOGIN")
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			user, _ := read()
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			pass, _ := read()
			check(decode(user), decode(pass))
			signedIn = decode(user) == account && decode(pass) == password
		case strings.HasPrefix(upper, "MAIL FROM:"):
			note(line)
			if !signedIn {
				say("530 5.7.0 Authentication required")
				continue
			}
			from = strings.Trim(strings.TrimSpace(line[len("MAIL FROM:"):]), "<>")
			to = nil
			say("250 2.1.0 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			note(line)
			if !signedIn {
				say("530 5.7.0 Authentication required")
				continue
			}
			to = append(to, strings.Trim(strings.TrimSpace(line[len("RCPT TO:"):]), "<>"))
			say("250 2.1.5 ok")
		case upper == "DATA":
			note(line)
			if !signedIn || from == "" || len(to) == 0 {
				say("503 5.5.1 bad sequence")
				continue
			}
			say("354 end with <CRLF>.<CRLF>")
			m := message{From: from, To: to}
			inHeaders := true
			for {
				l, ok := read()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				if l == "" {
					inHeaders = false
				}
				if inHeaders && strings.HasPrefix(l, "From:") {
					m.Header = strings.TrimSpace(l[len("From:"):])
				}
				if inHeaders && strings.HasPrefix(l, "Subject:") {
					m.Subject = strings.TrimSpace(l[len("Subject:"):])
				}
			}
			state.Lock()
			state.Messages = append(state.Messages, m)
			state.Unlock()
			say("250 2.0.0 queued (and delivered nowhere)")
		case upper == "RSET", upper == "NOOP":
			note(line)
			say("250 ok")
		case upper == "QUIT":
			note(line)
			say("221 2.0.0 bye")
			return
		default:
			note(line)
			say("502 5.5.1 not implemented")
		}
	}
}

func main() {
	here := ownAddress()
	cert := certificate()
	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	serveDNS(here)
	go serveSMTP(":465", config, true)
	go serveSMTP(":587", config, false)

	http.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()
		if r.Method == "DELETE" {
			state.Questions, state.Commands, state.SignIns, state.Messages = nil, nil, nil, nil
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&state)
	})
	log.Printf("fake mail for %s at %s: DNS 53, SMTP 465 and 587, state on 8025; account %s", domain, here, account)
	log.Fatal(http.ListenAndServe(":8025", nil))
}
