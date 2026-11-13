package maildetect

import (
	"context"
	"crypto/x509"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProviderTableIsWellFormed(t *testing.T) {
	if len(Providers()) < 20 {
		t.Fatalf("the table did not load: %d entries", len(Providers()))
	}
	notes := map[string]bool{"appPassword": true, "smtpAuthOff": true, "oauthOnly": true, "bridge": true,
		"enableAccess": true, "relayCredentials": true, "fixedUser": true, "regionInHost": true,
		"serverVaries": true, "verifiedSender": true}
	ids, domains := map[string]bool{}, map[string]string{}
	for _, p := range Providers() {
		if p.ID == "" || p.Name == "" || ids[p.ID] {
			t.Errorf("entry %q: missing or repeated id or name", p.ID)
		}
		ids[p.ID] = true
		if len(p.Servers) == 0 {
			t.Errorf("%s: no server", p.ID)
		}
		for _, s := range p.Servers {
			if !validHostName(s.Host) || !allowedPorts[s.Port] {
				t.Errorf("%s: server %s:%d is not a host name with a mail port", p.ID, s.Host, s.Port)
			}
			if (s.Port == 465) != (s.Security == "tls") || (s.Security != "tls" && s.Security != "starttls") {
				t.Errorf("%s: %s:%d with %q: 465 goes with tls, the others with starttls", p.ID, s.Host, s.Port, s.Security)
			}
		}
		for _, name := range append(append(append([]string{}, p.Domains...), p.MX...), p.Hosts...) {
			if !validHostName(name) {
				t.Errorf("%s: %q is not a lower-case domain name", p.ID, name)
			}
		}
		for _, d := range p.Domains {
			if other, taken := domains[d]; taken {
				t.Errorf("domain %s is listed for %s and %s", d, other, p.ID)
			}
			domains[d] = p.ID
		}
		switch p.User {
		case "address", "ask":
			if p.UserValue != "" {
				t.Errorf("%s: userValue without user=fixed", p.ID)
			}
		case "fixed":
			if p.UserValue == "" {
				t.Errorf("%s: user=fixed without userValue", p.ID)
			}
		default:
			t.Errorf("%s: user is %q", p.ID, p.User)
		}
		for _, n := range p.Notes {
			if !notes[n] {
				t.Errorf("%s: unknown note %q", p.ID, n)
			}
		}
		if p.URL != "" && !strings.HasPrefix(p.URL, "https://") {
			t.Errorf("%s: url %q", p.ID, p.URL)
		}
		if p.Relay && (len(p.Domains) > 0 || len(p.MX) > 0) {
			t.Errorf("%s: a relay is never found from an address", p.ID)
		}
	}
}

func TestProviderLookups(t *testing.T) {
	for domain, want := range map[string]string{
		"gmail.com": "gmail", "googlemail.com": "gmail", "hotmail.com": "outlook", "icloud.com": "icloud",
		"proton.me": "proton", "example.org": "", "notgmail.com": "", "mail.gmail.com": "",
	} {
		got := ""
		if p := providerByDomain(domain); p != nil {
			got = p.ID
		}
		if got != want {
			t.Errorf("providerByDomain(%s) = %q, want %q", domain, got, want)
		}
	}
	for host, want := range map[string]string{
		"aspmx.l.google.com": "google-workspace", "smtp.google.com": "google-workspace",
		"example-org.mail.protection.outlook.com": "microsoft365",
		"example-org.x-v1.mx.microsoft":           "microsoft365",
		"outlook-com.olc.protection.outlook.com":  "outlook",
		"in1-smtp.messagingengine.com":            "fastmail",
		"mx.zoho.eu":                              "zoho-org",
		"mail.protonmail.ch":                      "proton",
		// the ending must be whole labels
		"evilgoogle.com": "", "google.com.attacker.example": "", "mx.example.org": "", "": "",
	} {
		got := ""
		if p := providerByMX(host); p != nil {
			got = p.ID
		}
		if got != want {
			t.Errorf("providerByMX(%s) = %q, want %q", host, got, want)
		}
	}
}

func TestDomainOf(t *testing.T) {
	for input, want := range map[string]string{
		"me@example.org":        "example.org",
		"  Me@Example.ORG.  ":   "example.org",
		"example.org":           "example.org",
		"a@b@sub.example.co.uk": "sub.example.co.uk",
		"me@bücher.example":     "xn--bcher-kva.example",
		"me@home.lan":           "home.lan",
	} {
		got, err := DomainOf(input)
		if err != nil || got != want {
			t.Errorf("DomainOf(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"", "me", "me@", "@example.org", "me@localhost", "me@server", "me@app.localhost",
		"me@127.0.0.1", "me@[127.0.0.1]", "me@[::1]", "me@10.0.0.1", "me@2130706433", "me@0x7f.1",
		"me@example..org", "me@-example.org", "me@example-.org", "me@exa mple.org", "me@example.org/path",
		"me@example.org:25", "me@example.org\r\nRCPT", "me@exam_ple.org", "me@example.org?x=1", "me@*.example.org",
		"me@" + strings.Repeat("a", 64) + ".org", "me@" + strings.Repeat("a.", 130) + "org",
		strings.Repeat("a", 250) + "@example.org",
	} {
		if got, err := DomainOf(input); err == nil {
			t.Errorf("DomainOf(%q) = %q, want it refused", input, got)
		}
	}
}

func TestRefusedAddresses(t *testing.T) {
	own := []netip.Addr{netip.MustParseAddr("192.168.1.10"), netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("172.17.0.1")}
	refused := []string{
		"127.0.0.1", "127.8.9.1", "0.0.0.0", "0.1.2.3", "169.254.169.254", "169.254.1.1",
		"224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255", "192.0.0.8",
		"::", "::1", "fe80::1", "fe80::1%eth0", "ff02::1", "ff05::2",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe",
		"2002:7f00:1::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2", "64:ff9b:1::1",
		// the server's own addresses, also in their IPv6 dress
		"192.168.1.10", "::ffff:192.168.1.10", "2001:db8::10", "172.17.0.1", "64:ff9b::c0a8:10a",
	}
	for _, text := range refused {
		if !refusedAddr(netip.MustParseAddr(text), own) {
			t.Errorf("%s must be refused", text)
		}
	}
	// public addresses, and the private ranges: a mail server on the local
	// network is a legitimate target for an administrator
	allowed := []string{
		"8.8.8.8", "203.0.113.25", "2606:4700::1111", "::ffff:8.8.8.8", "64:ff9b::808:808",
		"10.0.0.25", "172.16.5.5", "192.168.1.11", "100.64.0.5", "fd00::25",
	}
	for _, text := range allowed {
		if refusedAddr(netip.MustParseAddr(text), own) {
			t.Errorf("%s must be allowed", text)
		}
	}
	if !refusedAddr(netip.Addr{}, own) {
		t.Error("an invalid address must be refused")
	}
}

func TestSocketGuard(t *testing.T) {
	guard := socketGuard(func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.10")} })
	for address, wantErr := range map[string]bool{
		"203.0.113.25:587": false, "203.0.113.25:465": false, "203.0.113.25:25": false, "203.0.113.25:2525": false,
		"[2606:4700::1111]:587": false, "10.0.0.25:25": false,
		"203.0.113.25:80": true, "203.0.113.25:443": true, "203.0.113.25:22": true, "203.0.113.25:6379": true,
		"127.0.0.1:25": true, "[::1]:587": true, "169.254.169.254:587": true, "192.168.1.10:587": true,
		"example.org:587": true, "": true,
	} {
		if err := guard("tcp", address, nil); (err != nil) != wantErr {
			t.Errorf("socketGuard(%q) = %v, want refused: %v", address, err, wantErr)
		}
	}
}

func TestKnownProviderNeedsNoDNSRecords(t *testing.T) {
	cert, roots := newCert(t, time.Now().Add(time.Hour), "smtp.gmail.com")
	resolver := &fakeResolver{ip: map[string][]string{"smtp.gmail.com": {"203.0.113.25"}}}
	implicit, upgraded := startFake(t, "tls", &cert), startFake(t, "starttls", &cert)
	d, _ := testDetector(resolver, map[string]*fakeServer{"465": implicit, "587": upgraded}, roots)

	result := d.Detect(context.Background(), "gmail.com")
	if result.Provider != "gmail" || result.ProviderVia != "domain" || !result.Complete {
		t.Fatalf("result: %+v", result)
	}
	if len(result.Candidates) != 2 || result.Candidates[0].Port != 465 || result.Candidates[1].Port != 587 {
		t.Fatalf("candidates: %s", list(result))
	}
	for _, c := range result.Candidates {
		if c.Host != "smtp.gmail.com" || c.Source != SourceProvider || c.Provider != "gmail" || c.Check != CheckOK || c.Cert != CertOK {
			t.Errorf("candidate: %+v", c)
		}
	}
	if got := strings.Join(result.Candidates[0].Auth, ","); got != "PLAIN,LOGIN,XOAUTH2" {
		t.Errorf("sign-in methods on 465: %q (unknown names must be dropped)", got)
	}
	if got := strings.Join(result.Candidates[1].Auth, ","); got != "PLAIN,LOGIN" {
		t.Errorf("sign-in methods on 587 are the ones offered after STARTTLS: %q", got)
	}
	if resolver.count("MX") != 0 || resolver.count("SRV") != 0 {
		t.Errorf("a known mailbox domain needs no MX or SRV question: %v", resolver.asked)
	}
}

func TestTableAnswersWithoutNetwork(t *testing.T) {
	// no DNS, no route: the table still proposes, and says it could not check
	d, _ := testDetector(&fakeResolver{}, nil, nil)
	result := d.Detect(context.Background(), "icloud.com")
	c := find(t, result, "smtp.mail.me.com", 587)
	if result.Provider != "icloud" || c.Source != SourceProvider || c.Check != CheckNoAnswer {
		t.Fatalf("result: %+v", result)
	}
}

func TestHostedDomainIsRecognisedByItsMX(t *testing.T) {
	resolver := &fakeResolver{
		mx: map[string][]*net.MX{"example.org": {
			{Host: "alt1.aspmx.l.google.com.", Pref: 5}, {Host: "aspmx.l.google.com.", Pref: 1},
		}},
		ip: map[string][]string{"smtp.gmail.com": {"203.0.113.25"}, "smtp.example.org": {"203.0.113.99"}},
	}
	cert, roots := newCert(t, time.Now().Add(time.Hour), "smtp.gmail.com")
	d, log := testDetector(resolver, map[string]*fakeServer{"465": startFake(t, "tls", &cert)}, roots)

	result := d.Detect(context.Background(), "example.org")
	if result.Provider != "google-workspace" || result.ProviderVia != "mx" || result.MX != "found" {
		t.Fatalf("result: %+v", result)
	}
	first := result.Candidates[0]
	if first.Host != "smtp.gmail.com" || first.Port != 465 || first.Source != SourceMXProvider || first.Check != CheckOK {
		t.Fatalf("candidates: %s", list(result))
	}
	// the provider is known: the usual names of the domain are not tried
	for _, address := range log.all() {
		if !strings.HasPrefix(address, "203.0.113.25:") {
			t.Errorf("connected to %s", address)
		}
	}
	if has(result, "smtp.example.org", 587) || has(result, "aspmx.l.google.com", 587) {
		t.Errorf("candidates: %s", list(result))
	}
}

func TestSRVRecords(t *testing.T) {
	resolver := &fakeResolver{
		srv: map[string][]*net.SRV{
			"_submissions._tcp.example.org": {
				{Target: "backup.example.net.", Port: 465, Priority: 20},
				{Target: "submit.example.net.", Port: 465, Priority: 10},
			},
			"_submission._tcp.example.org": {
				{Target: ".", Port: 0, Priority: 1},
				{Target: "bad_name.example.net.", Port: 587, Priority: 2},
				{Target: "odd.example.net.", Port: 8587, Priority: 3},
			},
		},
		ip: map[string][]string{"submit.example.net": {"203.0.113.30"}, "odd.example.net": {"203.0.113.31"}},
	}
	cert, roots := newCert(t, time.Now().Add(time.Hour), "submit.example.net")
	d, log := testDetector(resolver, map[string]*fakeServer{"465": startFake(t, "tls", &cert)}, roots)

	result := d.Detect(context.Background(), "example.org")
	best := result.Candidates[0]
	if best.Host != "submit.example.net" || best.Port != 465 || best.Security != "tls" || best.Source != SourceSRV || best.Check != CheckOK || best.Cert != CertOK {
		t.Fatalf("candidates: %s", list(result))
	}
	if has(result, "backup.example.net", 465) || has(result, "bad_name.example.net", 587) {
		t.Errorf("only the best valid record of each service is used: %s", list(result))
	}
	// a port that is not a mail port is listed, never connected to
	odd := find(t, result, "odd.example.net", 8587)
	if odd.Check != CheckNotChecked || odd.Security != "starttls" {
		t.Errorf("odd port: %+v", odd)
	}
	for _, address := range log.all() {
		if strings.HasSuffix(address, ":8587") {
			t.Errorf("connected to %s", address)
		}
	}
	if result.MX != "none" {
		t.Errorf("MX = %q", result.MX)
	}
}

func TestUsualNamesAreCheckedForReal(t *testing.T) {
	resolver := &fakeResolver{
		mx: map[string][]*net.MX{"example.org": {{Host: "mx1.example.org.", Pref: 10}}},
		ip: map[string][]string{
			"mail.example.org": {"203.0.113.40"},
			"mx1.example.org":  {"203.0.113.41"},
		},
	}
	cert, _ := newCert(t, time.Now().Add(time.Hour), "mail.example.org")
	upgraded := startFake(t, "starttls", &cert)
	// only mail.example.org answers, and only on 587
	d, log := testDetector(resolver, map[string]*fakeServer{"203.0.113.40:587": upgraded}, nil)

	result := d.Detect(context.Background(), "example.org")
	if len(result.Candidates) != 1 {
		t.Fatalf("guesses that did not answer must be dropped: %s", list(result))
	}
	c := result.Candidates[0]
	if c.Host != "mail.example.org" || c.Port != 587 || c.Security != "starttls" || c.Source != SourceGuess || c.Check != CheckOK || c.Cert != CertUntrusted {
		t.Fatalf("candidate: %+v", c)
	}
	// smtp.example.org does not exist: asked once, never connected to
	if resolver.count("IP smtp.example.org") != 1 {
		t.Errorf("questions: %v", resolver.asked)
	}
	want := map[string]bool{"203.0.113.40:465": true, "203.0.113.40:587": true, "203.0.113.41:465": true, "203.0.113.41:587": true}
	for _, address := range log.all() {
		if !want[address] {
			t.Errorf("unexpected connection to %s", address)
		}
		delete(want, address)
	}
	if len(want) != 0 {
		t.Errorf("not tried: %v", want)
	}
}

func TestPort25IsTheLastResort(t *testing.T) {
	resolver := &fakeResolver{ip: map[string][]string{"mail.example.org": {"10.0.0.25"}}}
	relay := startFake(t, "plain", nil)
	d, log := testDetector(resolver, map[string]*fakeServer{"25": relay}, nil)

	result := d.Detect(context.Background(), "example.org")
	c := find(t, result, "mail.example.org", 25)
	if c.Check != CheckNoStartTLS || len(result.Candidates) != 1 {
		t.Fatalf("candidates: %s", list(result))
	}
	if dialled := log.all(); len(dialled) != 3 || dialled[2] != "10.0.0.25:25" {
		t.Errorf("connections: %v", dialled)
	}

	// with an answer on 587, port 25 is not touched
	cert, _ := newCert(t, time.Now().Add(time.Hour), "mail.example.org")
	d, log = testDetector(&fakeResolver{ip: resolver.ip}, map[string]*fakeServer{"25": relay, "587": startFake(t, "starttls", &cert)}, nil)
	result = d.Detect(context.Background(), "example.org")
	for _, address := range log.all() {
		if strings.HasSuffix(address, ":25") {
			t.Errorf("port 25 tried although 587 answered: %v", log.all())
		}
	}
	if has(result, "mail.example.org", 25) {
		t.Errorf("candidates: %s", list(result))
	}
}

func TestNamesThatPointHomeAreNotContacted(t *testing.T) {
	cases := map[string][]string{
		"loopback":             {"127.0.0.1"},
		"loopback v6":          {"::1"},
		"metadata":             {"169.254.169.254"},
		"mapped loopback":      {"::ffff:127.0.0.1"},
		"own address":          {"192.168.1.10"},
		"one bad among good":   {"203.0.113.40", "127.0.0.1"},
		"good then own":        {"203.0.113.40", "2001:db8::1", "192.168.1.10"},
		"multicast":            {"224.0.0.251"},
		"unspecified":          {"0.0.0.0"},
		"nat64 of loopback":    {"64:ff9b::7f00:1"},
		"link-local with zone": {"fe80::1%eth0"},
	}
	for name, answers := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := &fakeResolver{ip: map[string][]string{"smtp.example.org": answers, "mail.example.org": answers}}
			everything := startFake(t, "plain", nil)
			d, log := testDetector(resolver, map[string]*fakeServer{"25": everything, "465": everything, "587": everything, "2525": everything}, nil)
			result := d.Detect(context.Background(), "example.org")
			if len(log.all()) != 0 || everything.connections() != 0 {
				t.Fatalf("connected to %v", log.all())
			}
			for _, c := range result.Candidates {
				if c.Check != CheckLocal {
					t.Errorf("candidate: %+v", c)
				}
			}
			if len(result.Candidates) == 0 {
				t.Error("the form is told that the name was found and left alone")
			}
		})
	}
}

func TestConnectsToTheAddressItValidated(t *testing.T) {
	// The first answer is a public address. A second question would get
	// loopback (DNS rebinding): there must be no second question, and both
	// connections go to the first answer.
	resolver := &fakeResolver{
		ip:    map[string][]string{"smtp.example.org": {"203.0.113.50"}},
		later: map[string][]string{"smtp.example.org": {"127.0.0.1"}},
	}
	cert, _ := newCert(t, time.Now().Add(time.Hour), "smtp.example.org")
	d, log := testDetector(resolver, map[string]*fakeServer{"465": startFake(t, "tls", &cert), "587": startFake(t, "starttls", &cert)}, nil)

	result := d.Detect(context.Background(), "example.org")
	if resolver.count("IP smtp.example.org") != 1 {
		t.Errorf("the name was resolved %d times: %v", resolver.count("IP smtp.example.org"), resolver.asked)
	}
	dialled := log.all()
	if len(dialled) != 2 {
		t.Fatalf("connections: %v", dialled)
	}
	for _, address := range dialled {
		if !strings.HasPrefix(address, "203.0.113.50:") {
			t.Errorf("connected to %s instead of the validated address", address)
		}
	}
	if find(t, result, "smtp.example.org", 465).Check != CheckOK || find(t, result, "smtp.example.org", 587).Check != CheckOK {
		t.Errorf("candidates: %s", list(result))
	}
}

func TestIPv4IsPreferred(t *testing.T) {
	resolver := &fakeResolver{ip: map[string][]string{"smtp.example.org": {"2606:4700::25", "::ffff:203.0.113.60"}}}
	d, log := testDetector(resolver, nil, nil)
	d.Detect(context.Background(), "example.org")
	for _, address := range log.all() {
		if !strings.HasPrefix(address, "203.0.113.60:") {
			t.Errorf("connected to %s", address)
		}
	}
	if len(log.all()) == 0 {
		t.Error("no connection")
	}
}

func TestWhatAServerCanBe(t *testing.T) {
	good, roots := newCert(t, time.Now().Add(time.Hour), "smtp.example.org")
	other, _ := newCert(t, time.Now().Add(time.Hour), "another.example.net")
	cases := []struct {
		name, mode string
		port       int
		security   string
		cert       *x509.CertPool
		wrongName  bool
		future     bool
		wantCheck  string
		wantCert   string
	}{
		{name: "TLS from the start", mode: "tls", port: 465, security: "tls", cert: roots, wantCheck: CheckOK, wantCert: CertOK},
		{name: "STARTTLS", mode: "starttls", port: 587, security: "starttls", cert: roots, wantCheck: CheckOK, wantCert: CertOK},
		{name: "self-signed", mode: "tls", port: 465, security: "tls", wantCheck: CheckOK, wantCert: CertUntrusted},
		{name: "certificate for another name", mode: "starttls", port: 587, security: "starttls", wrongName: true, wantCheck: CheckOK, wantCert: CertName},
		{name: "expired certificate", mode: "tls", port: 465, security: "tls", cert: roots, future: true, wantCheck: CheckOK, wantCert: CertExpired},
		{name: "no STARTTLS", mode: "plain", port: 587, security: "starttls", wantCheck: CheckNoStartTLS},
		{name: "plain server where TLS is expected", mode: "plain", port: 465, security: "tls", wantCheck: CheckTLSFailed},
		{name: "not a mail server", mode: "garbage", port: 587, security: "starttls", wantCheck: CheckNotSMTP},
		{name: "says nothing", mode: "silent", port: 587, security: "starttls", wantCheck: CheckNotSMTP},
		{name: "never stops talking", mode: "flood", port: 587, security: "starttls", wantCheck: CheckNotSMTP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert := &good
			if tc.wrongName {
				cert = &other
			}
			server := startFake(t, tc.mode, cert)
			d, _ := testDetector(&fakeResolver{}, map[string]*fakeServer{"465": server, "587": server}, tc.cert)
			d.ProbeTimeout = 700 * time.Millisecond
			if tc.future {
				d.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
			}
			started := time.Now()
			got := d.probe(context.Background(), "smtp.example.org", netip.MustParseAddr("203.0.113.70"), tc.port, tc.security)
			if got.check != tc.wantCheck || got.cert != tc.wantCert {
				t.Errorf("check %q cert %q, want %q %q", got.check, got.cert, tc.wantCheck, tc.wantCert)
			}
			if took := time.Since(started); took > 2*time.Second {
				t.Errorf("took %v", took)
			}
		})
	}
}

func TestDetectionSendsNothingButTheGreeting(t *testing.T) {
	cert, roots := newCert(t, time.Now().Add(time.Hour), "smtp.example.org", "mail.example.org")
	implicit, upgraded, relay := startFake(t, "tls", &cert), startFake(t, "starttls", &cert), startFake(t, "plain", nil)
	resolver := &fakeResolver{ip: map[string][]string{"smtp.example.org": {"203.0.113.80"}, "mail.example.org": {"203.0.113.81"}}}
	d, _ := testDetector(resolver, map[string]*fakeServer{"465": implicit, "587": upgraded, "203.0.113.81:587": relay}, roots)

	domain, err := DomainOf("very-private-mailbox-name@example.org")
	if err != nil {
		t.Fatal(err)
	}
	result := d.Detect(context.Background(), domain)
	if !anyAnswered(result.Candidates) {
		t.Fatalf("candidates: %s", list(result))
	}

	heard := append(append(implicit.heard(), upgraded.heard()...), relay.heard()...)
	if len(heard) < 6 {
		t.Fatalf("the servers heard too little for this test to mean anything: %v", heard)
	}
	for _, line := range heard {
		upper := strings.ToUpper(line)
		if upper != "EHLO LOCALHOST" && upper != "STARTTLS" && upper != "QUIT" {
			t.Errorf("the server was sent %q", line)
		}
		if strings.Contains(upper, "AUTH") || strings.Contains(upper, "MAIL") || strings.Contains(upper, "RCPT") ||
			strings.Contains(line, "very-private-mailbox-name") {
			t.Errorf("the server was sent %q", line)
		}
	}
}

func TestAnswerCarriesNothingAServerSaid(t *testing.T) {
	cert, _ := newCert(t, time.Now().Add(time.Hour), "smtp.example.org")
	resolver := &fakeResolver{ip: map[string][]string{"smtp.example.org": {"203.0.113.80"}}}
	d, _ := testDetector(resolver, map[string]*fakeServer{"465": startFake(t, "tls", &cert), "587": startFake(t, "starttls", &cert)}, nil)
	routeDetector, routeLimiter = d, &limiter{}
	t.Cleanup(func() { routeDetector, routeLimiter = NewDetector(), &limiter{} })

	rec := call("POST", "sudo", `{"address":"me@example.org"}`)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"smtp.example.org"`) {
		t.Fatalf("answer: %d %s", rec.Code, body)
	}
	for _, leak := range []string{secretBanner, "X-SECRET-METHOD", "203.0.113.80", "127.0.0.1", "fake.test", "me@"} {
		if strings.Contains(body, leak) {
			t.Errorf("the answer contains %q: %s", leak, body)
		}
	}
}

func TestTimeAndCountBounds(t *testing.T) {
	// every name exists and every port accepts the connection and then says
	// nothing: the slowest possible mail domain
	silent := startFake(t, "silent", nil)
	resolver := &fakeResolver{
		mx: map[string][]*net.MX{"example.org": {{Host: "mx.example.org.", Pref: 1}}},
		srv: map[string][]*net.SRV{
			"_submissions._tcp.example.org": {{Target: "a.example.org.", Port: 465}},
			"_submission._tcp.example.org":  {{Target: "b.example.org.", Port: 587}},
		},
		ip: map[string][]string{
			"smtp.example.org": {"203.0.113.1"}, "mail.example.org": {"203.0.113.2"}, "mx.example.org": {"203.0.113.3"},
			"a.example.org": {"203.0.113.4"}, "b.example.org": {"203.0.113.5"},
		},
	}
	d, log := testDetector(resolver, map[string]*fakeServer{"25": silent, "465": silent, "587": silent}, nil)
	d.Budget, d.ProbeTimeout, d.LookupTimeout = 900*time.Millisecond, 250*time.Millisecond, 100*time.Millisecond
	d.MaxProbes, d.Parallel = 5, 2

	started := time.Now()
	result := d.Detect(context.Background(), "example.org")
	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Errorf("detection took %v with a budget of %v", took, d.Budget)
	}
	if len(log.all()) > d.MaxProbes {
		t.Errorf("%d connections, at most %d allowed: %v", len(log.all()), d.MaxProbes, log.all())
	}
	if len(result.Candidates) > d.MaxCandidates {
		t.Errorf("%d candidates", len(result.Candidates))
	}

	// a resolver that never answers costs LookupTimeout per question, and the count is bounded
	slow := &fakeResolver{delay: time.Hour}
	d, log = testDetector(slow, nil, nil)
	d.Budget, d.LookupTimeout, d.MaxLookups = 600*time.Millisecond, 100*time.Millisecond, 4
	started = time.Now()
	result = d.Detect(context.Background(), "example.org")
	if took := time.Since(started); took > 1200*time.Millisecond {
		t.Errorf("detection took %v", took)
	}
	if len(slow.asked) > 4 || len(log.all()) != 0 {
		t.Errorf("questions: %v, connections: %v", slow.asked, log.all())
	}
	if result.MX != "failed" {
		t.Errorf("MX = %q", result.MX)
	}

	// the caller going away ends it
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, log = testDetector(resolver, map[string]*fakeServer{"465": silent, "587": silent}, nil)
	result = d.Detect(ctx, "example.org")
	if len(log.all()) != 0 || result.Complete {
		t.Errorf("connections after the request ended: %v, complete: %v", log.all(), result.Complete)
	}
}

func TestRanking(t *testing.T) {
	in := []Candidate{
		{Host: "mail.example.org", Port: 587, Source: SourceGuess, Check: CheckOK},
		{Host: "mail.example.org", Port: 465, Source: SourceGuess, Check: CheckOK},
		{Host: "smtp.example.org", Port: 465, Source: SourceGuess, Check: CheckNoAnswer},
		{Host: "here.example.org", Port: 587, Source: SourceGuess, Check: CheckLocal},
		{Host: "srv-down.example.net", Port: 465, Source: SourceSRV, Check: CheckNoAnswer},
		{Host: "srv-up.example.net", Port: 587, Source: SourceSRV, Check: CheckOK},
		{Host: "smtp.gmail.com", Port: 465, Source: SourceMXProvider, Provider: "google-workspace", Check: CheckNoAnswer},
		{Host: "smtp.gmail.com", Port: 587, Source: SourceMXProvider, Provider: "google-workspace", Check: CheckOK},
		{Host: "mail.gmx.net", Port: 587, Source: SourceProvider, Provider: "gmx", Check: CheckOK},
		{Host: "mail.gmx.net", Port: 465, Source: SourceProvider, Provider: "gmx", Check: CheckOK},
	}
	got := []string{}
	for _, c := range rank(in, 8) {
		got = append(got, c.Host+":"+strconv.Itoa(c.Port))
	}
	want := "mail.gmx.net:587 mail.gmx.net:465 smtp.gmail.com:587 smtp.gmail.com:465 srv-up.example.net:587 " +
		"mail.example.org:465 mail.example.org:587 srv-down.example.net:465"
	if strings.Join(got, " ") != want {
		t.Errorf("order:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if len(rank(in, 3)) != 3 {
		t.Error("the limit is not applied")
	}
}
