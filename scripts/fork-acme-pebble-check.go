//go:build ignore

// Fork addition: the checks of scripts/fork-acme-pebble.sh, which starts
// what this program talks to (its header says how to run the whole thing).
// It drives a throwaway Cosmos through its API, the way the panel would,
// while a test certificate authority (Pebble) and a stand-in for Cloudflare
// answer on a Docker network without a way out, and looks at what the
// server then serves on its HTTPS port.
//
//	fork-acme-pebble-check roots   writes /shared/roots.pem: Pebble's issuing root and intermediate
//	                               plus the stand-in's authority, the "system" trust of the test server
//	fork-acme-pebble-check run     the checks; exits 1 when one fails
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

func env(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

var (
	server   = env("SERVER", "172.31.57.10")
	base     = "https://" + server + ":2053"
	plain    = "http://" + server + ":2052"
	fake     = env("FAKE", "http://172.31.57.2:8080")
	fakeDNS  = env("FAKE_DNS", "172.31.57.2:53")
	pebble   = env("PEBBLE", "https://pebble:15000")
	validity = 240 * time.Second
)

const (
	password  = "Test-Passw0rd!x"
	goodToken = "cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789"
)

var failures []string

func expect(ok bool, what string, detail ...interface{}) bool {
	mark := "  ok  "
	if !ok {
		mark = "  FAIL"
		failures = append(failures, what)
	}
	text := ""
	if len(detail) > 0 {
		text = " | " + strings.Join(strings.Fields(fmt.Sprint(detail...)), " ")
		if len(text) > 400 {
			text = text[:400] + "…"
		}
	}
	fmt.Println(mark, what+text)
	return ok
}

func note(text ...interface{}) { fmt.Println(append([]interface{}{"     "}, text...)...) }

// ---- HTTP helpers

var jar, _ = cookiejar.New(nil)
var panel = &http.Client{Jar: jar, Timeout: 20 * time.Second, Transport: &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // the panel is reached by address: its certificate is the self-signed one
}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type object = map[string]interface{}

func call(method string, address string, body interface{}) (int, object, string) {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, address, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := panel.Do(request)
	if err != nil {
		return 0, nil, err.Error()
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	parsed := object{}
	json.Unmarshal(raw, &parsed)
	return response.StatusCode, parsed, string(raw)
}

func api(method string, path string, body interface{}) (int, object, string) {
	return call(method, base+"/cosmos/api"+path, body)
}

func plainGet(address string) string {
	response, err := http.Get(address)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return string(raw)
}

func at(value interface{}, path ...string) interface{} {
	for _, key := range path {
		next, ok := value.(object)
		if !ok {
			return nil
		}
		value = next[key]
	}
	return value
}

func text(value interface{}, path ...string) string {
	found, _ := at(value, path...).(string)
	return found
}

func list(value interface{}, path ...string) []interface{} {
	found, _ := at(value, path...).([]interface{})
	return found
}

func until(what string, seconds int, check func() bool) bool {
	for i := 0; i < seconds; i++ {
		if check() {
			return true
		}
		time.Sleep(time.Second)
	}
	return expect(false, "timed out: "+what)
}

func serverUp() bool {
	return until("the server answers over HTTPS", 120, func() bool {
		status, _, _ := api("GET", "/status", nil)
		return status != 0 && status < 500
	})
}

func login() {
	a, _, _ := api("POST", "/login", object{"nickname": "admin", "password": password})
	b, _, _ := api("POST", "/sudo", object{"password": password})
	note("login", a, "password confirmation", b)
}

// ---- what the stand-in saw

func fakeCalls(clear bool) []object {
	address := fake + "/calls"
	if clear {
		address += "?clear=1"
	}
	var calls []object
	json.Unmarshal([]byte(plainGet(address)), &calls)
	return calls
}

// txtWrites lists the names of the TXT records created through the API.
func txtWrites(calls []object) []string {
	names := []string{}
	for _, call := range calls {
		if call["method"] != "POST" || !strings.HasSuffix(fmt.Sprint(call["path"]), "/dns_records") {
			continue
		}
		record := object{}
		json.Unmarshal([]byte(fmt.Sprint(call["body"])), &record)
		if record["type"] == "TXT" && strings.Contains(fmt.Sprint(record["name"]), "_acme") {
			names = append(names, fmt.Sprint(record["name"]))
		}
	}
	sort.Strings(names)
	return names
}

// ---- the certificate authority's roots and what the server serves

func pebbleRoots() (*x509.CertPool, []byte) {
	minica, err := os.ReadFile("/shared/pebble.minica.pem")
	if err != nil {
		fmt.Println("cannot read /shared/pebble.minica.pem:", err)
		os.Exit(2)
	}
	trusted := x509.NewCertPool()
	trusted.AppendCertsFromPEM(minica)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trusted}}}
	all := []byte{}
	for _, path := range []string{"/roots/0", "/intermediates/0"} {
		var body []byte
		for i := 0; i < 60; i++ {
			response, err := client.Get(pebble + path)
			if err == nil {
				body, _ = io.ReadAll(response.Body)
				response.Body.Close()
				break
			}
			time.Sleep(time.Second)
		}
		if len(body) == 0 {
			fmt.Println("Pebble does not answer on", pebble+path)
			os.Exit(2)
		}
		all = append(all, body...)
		all = append(all, '\n')
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(all)
	return pool, all
}

type served struct {
	serial  string
	names   []string
	issuer  string
	until   time.Time
	from    time.Time
	trusted bool
	key     string
	err     string
}

// handshake asks the server for the certificate of a name, the way a
// browser reaching it directly would, and verifies it against Pebble's root.
func handshake(name string, roots *x509.CertPool) served {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", server+":2053", &tls.Config{ServerName: name, InsecureSkipVerify: true})
	if err != nil {
		return served{err: err.Error()}
	}
	defer conn.Close()
	chain := conn.ConnectionState().PeerCertificates
	leaf := chain[0]
	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	_, verifyErr := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: intermediates})
	result := served{serial: leaf.SerialNumber.String(), names: leaf.DNSNames, issuer: leaf.Issuer.CommonName,
		until: leaf.NotAfter, from: leaf.NotBefore, trusted: verifyErr == nil, key: leaf.PublicKeyAlgorithm.String()}
	if verifyErr != nil {
		result.err = verifyErr.Error()
	}
	return result
}

// ---- the panel's view

func certStatus() object {
	_, answer, _ := api("GET", "/certificates", nil)
	data, _ := answer["data"].(object)
	return data
}

func certOf(status object, domain string) object {
	for _, entry := range list(status, "certificates") {
		if cert, ok := entry.(object); ok && cert["domain"] == domain {
			return cert
		}
	}
	return object{}
}

func hostOf(status object, host string) object {
	for _, entry := range list(status, "hosts") {
		if found, ok := entry.(object); ok && found["host"] == host {
			return found
		}
	}
	return object{}
}

func names(cert object) string {
	parts := []string{}
	for _, name := range list(cert, "names") {
		parts = append(parts, fmt.Sprint(name))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func setRoutes(change func(routes []interface{}) []interface{}) {
	_, answer, _ := api("GET", "/config", nil)
	config, _ := answer["data"].(object)
	httpConfig, _ := config["HTTPConfig"].(object)
	proxy, _ := httpConfig["ProxyConfig"].(object)
	if proxy == nil {
		proxy = object{}
		httpConfig["ProxyConfig"] = proxy
	}
	routes, _ := proxy["Routes"].([]interface{})
	proxy["Routes"] = change(routes)
	httpConfig["AdvertisedAddress"] = "203.0.113.10"
	status, _, raw := api("PUT", "/config", config)
	if status != 200 {
		expect(false, "the settings are saved", status, raw)
	}
	time.Sleep(3 * time.Second)
	serverUp()
	login()
}

func route(name string, host string) object {
	return object{"Name": name, "Mode": "PROXY", "Target": "http://" + strings.TrimPrefix(fake, "http://"), "UseHost": true, "Host": host}
}

func addRoute(name string, host string) {
	setRoutes(func(routes []interface{}) []interface{} { return append(routes, route(name, host)) })
}

func restartProcess() {
	api("GET", "/restart", nil)
	time.Sleep(4 * time.Second)
	serverUp()
	login()
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "roots" {
		_, pem := pebbleRoots()
		authority, err := os.ReadFile("/shared/ca.pem")
		if err != nil {
			fmt.Println("the stand-in has not written /shared/ca.pem yet")
			os.Exit(2)
		}
		if err := os.WriteFile("/shared/roots.pem", append(pem, authority...), 0644); err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
		fmt.Println("wrote /shared/roots.pem")
		return
	}

	roots, _ := pebbleRoots()

	// ---- the set-up wizard, as on the server this was written for:
	// HTTPS with the self-signed certificate, the address as the hostname
	fmt.Println("== set-up")
	fresh := false
	for i := 0; i < 90 && !fresh; i++ {
		// only a server that is not set up yet answers this on the plain port
		answer := strings.ReplaceAll(plainGet(plain+"/cosmos/api/status"), " ", "")
		fresh = strings.Contains(answer, `"status":"OK"`)
		if status, _, _ := api("GET", "/status", nil); status != 0 {
			break
		}
		if !fresh {
			time.Sleep(time.Second)
		}
	}
	if fresh {
		step := func(data object) int {
			encoded, _ := json.Marshal(data)
			response, err := http.Post(plain+"/cosmos/api/newInstall", "application/json", bytes.NewReader(encoded))
			if err != nil {
				return 0
			}
			response.Body.Close()
			return response.StatusCode
		}
		note("wizard:",
			step(object{"step": "2", "mongodbMode": "Create"}),
			step(object{"step": "3", "httpsCertificateMode": "SELFSIGNED", "hostname": server, "allowHTTPLocalIPAccess": true}),
			step(object{"step": "4", "nickname": "admin", "password": password, "email": "admin@example.org"}),
			step(object{"step": "5"}))
		time.Sleep(4 * time.Second)
	}
	if !serverUp() {
		os.Exit(1)
	}
	login()

	status := certStatus()
	expect(text(status, "ca", "kind") == "test", "the panel says orders go to a test authority", at(status, "ca"))
	expect(text(status, "challenges", "http") == "impossible" && text(status, "challenges", "reason") == "ports",
		"HTTP-01 and TLS-ALPN-01 are known to be impossible on ports 2052/2053", at(status, "challenges"))
	before := handshake("app1.example.org", roots)
	expect(!before.trusted, "before anything: a name of the domain gets a certificate no browser trusts", before.issuer)

	// ---- 1. the domain, as "Connect Cloudflare" stores it, and an application
	fmt.Println("== 1. first order: the wildcard pair")
	fakeCalls(true)
	code, _, raw := api("PUT", "/zones/example.org", object{
		"HTTPSCertificateMode": "LETSENCRYPT", "DNSChallengeProvider": "cloudflare",
		"DNSChallengeConfig": object{"CF_DNS_API_TOKEN": goodToken}, "UseWildcardCertificate": true,
		"ManageRecords": true, "WildcardRecord": false, "DNSChallengeResolvers": fakeDNS,
	})
	expect(code == 200, "the domain example.org is stored", code, raw)
	time.Sleep(3 * time.Second)
	saved := time.Now()
	serverUp()
	login()
	addRoute("app1", "app1.example.org")
	longestGap, lastAnswer := time.Duration(0), time.Now()
	issued := until("a certificate for example.org arrives", 120, func() bool {
		if status, _, _ := api("GET", "/status", nil); status != 0 {
			if gap := time.Since(lastAnswer); gap > longestGap {
				longestGap = gap
			}
			lastAnswer = time.Now()
		}
		return handshake("app1.example.org", roots).trusted
	})
	note("first certificate after", time.Since(saved).Round(time.Second), "- longest time without an answer from the panel meanwhile:", longestGap.Round(time.Millisecond))
	expect(longestGap < 15*time.Second, "the server kept answering while the order ran")
	first := handshake("app1.example.org", roots)
	expect(issued && first.trusted, "app1.example.org is served with a certificate that verifies against the authority's root", first.err)
	expect(strings.Join(first.names, " ") == "*.example.org example.org" || strings.Join(first.names, " ") == "example.org *.example.org",
		"it covers example.org and *.example.org", first.names)
	expect(first.key == "ECDSA", "its key is ECDSA", first.key)
	lifetime := first.until.Sub(first.from)
	expect(lifetime > validity-30*time.Second && lifetime < validity+90*time.Second, "its lifetime is the one the authority was told to give", lifetime)
	apex := handshake("example.org", roots)
	expect(apex.trusted && apex.serial == first.serial, "the domain's own name gets the same certificate")
	address := handshake(server, roots)
	expect(!address.trusted, "the server's address still gets the self-signed certificate", address.issuer)

	status = certStatus()
	cert := certOf(status, "example.org")
	expect(cert["present"] == true && cert["trust"] == "test" && cert["method"] == "dns" && cert["provider"] == "cloudflare",
		"the panel: a certificate through Cloudflare's DNS, from the test authority", cert["issuer"], cert["trust"])
	expect(names(cert) == "*.example.org example.org", "the panel: the names it covers", names(cert))
	expect(len(list(cert, "uncovered")) == 0, "the panel: no served name is left uncovered", cert["uncovered"])
	expect(at(cert, "last", "ok") == true, "the panel: the last order succeeded", cert["last"])
	renewAt, _ := time.Parse(time.RFC3339, text(cert, "renewAt"))
	expect(renewAt.After(first.from) && renewAt.Before(first.until.Add(-validity/5)),
		"the panel: the renewal is planned inside the lifetime, well before the end", text(cert, "renewAt"), "source", cert["renewSource"])
	host := hostOf(status, "app1.example.org")
	expect(host["trusted"] == true && host["shielded"] == false, "the panel: app1.example.org is served directly and browsers trust it", host)
	expect(len(txtWrites(fakeCalls(false))) == 2, "two TXT records were written through Cloudflare's API", txtWrites(fakeCalls(false)))
	left := 0
	var records []object
	json.Unmarshal([]byte(plainGet(fake+"/records")), &records)
	for _, record := range records {
		if record["type"] == "TXT" && strings.HasPrefix(fmt.Sprint(record["name"]), "_acme-challenge") {
			left++
		}
	}
	expect(left == 0, "and removed again after the order", left)

	// ---- 2. a name two levels down
	fmt.Println("== 2. a deeper name is added")
	fakeCalls(true)
	addRoute("deep", "a.b.example.org")
	until("a.b.example.org is covered", 120, func() bool { return handshake("a.b.example.org", roots).trusted })
	deep := handshake("a.b.example.org", roots)
	expect(deep.trusted && deep.serial != first.serial, "a new certificate covers a.b.example.org", deep.names, deep.err)
	expect(handshake("app1.example.org", roots).serial == deep.serial, "and app1.example.org is served with that one too")
	expect(len(txtWrites(fakeCalls(false))) == 3, "the order proved three names", txtWrites(fakeCalls(false)))

	// ---- 3. a restart with nothing due
	fmt.Println("== 3. restart: nothing is ordered")
	fakeCalls(true)
	restartProcess()
	time.Sleep(20 * time.Second)
	expect(len(txtWrites(fakeCalls(false))) == 0, "no order was started by the restart", txtWrites(fakeCalls(false)))
	expect(handshake("app1.example.org", roots).serial == deep.serial, "the same certificate is served after the restart")

	// ---- 4. through Cloudflare, then directly again
	fmt.Println("== 4. through Cloudflare, then directly")
	code, _, raw = api("POST", "/cloudflare/shield", object{"hostname": "app1.example.org", "shielded": true})
	if expect(code == 200, "app1.example.org is switched to \"through Cloudflare\"", code, raw) {
		time.Sleep(2 * time.Second)
		serverUp()
		login()
		shielded := handshake("app1.example.org", roots)
		expect(shielded.trusted && shielded.serial == deep.serial, "Cloudflare (Full strict) would get the trusted certificate", shielded.err)
		expect(hostOf(certStatus(), "app1.example.org")["shielded"] == true, "the panel: served through Cloudflare")
		fakeCalls(true)
		code, _, raw = api("POST", "/cloudflare/shield", object{"hostname": "app1.example.org", "shielded": false})
		expect(code == 200, "and back to \"directly\"", code, raw)
		time.Sleep(2 * time.Second)
		serverUp()
		login()
		direct := handshake("app1.example.org", roots)
		expect(direct.trusted && direct.serial == deep.serial, "served directly with the same trusted certificate, at once", direct.err)
		time.Sleep(8 * time.Second)
		expect(len(txtWrites(fakeCalls(false))) == 0, "without any order", txtWrites(fakeCalls(false)))
		host = hostOf(certStatus(), "app1.example.org")
		expect(host["trusted"] == true && host["shielded"] == false, "the panel: direct, and browsers trust it", host)
	}

	// ---- 5. the provider refuses
	fmt.Println("== 5. the DNS provider refuses the token")
	plainGet(fake + "/token?status=disabled")
	fakeCalls(true)
	addRoute("refused", "c.d.example.org")
	until("the failed order shows in the panel", 120, func() bool {
		return at(certOf(certStatus(), "example.org"), "last", "ok") == false
	})
	cert = certOf(certStatus(), "example.org")
	expect(text(cert, "last", "code") == "dnsCredentials", "the panel: the reason is the provider's refusal of the credentials", cert["last"])
	expect(!strings.Contains(fmt.Sprint(cert["last"]), goodToken), "the token is not in what the panel is told")
	expect(text(cert, "wait", "reason") == "backoff", "the panel: no automatic order for a while", cert["wait"])
	expect(fmt.Sprint(cert["uncovered"]) == "[c.d.example.org]", "the panel: c.d.example.org is not covered", cert["uncovered"])
	still := handshake("app1.example.org", roots)
	expect(still.trusted && still.serial == deep.serial, "HTTPS stays up: app1.example.org keeps the previous certificate", still.err)
	uncovered := handshake("c.d.example.org", roots)
	expect(uncovered.err != "" && !uncovered.trusted && uncovered.serial != "", "c.d.example.org is answered, with a certificate browsers do not trust", uncovered.issuer)
	expect(hostOf(certStatus(), "c.d.example.org")["trusted"] == false, "the panel says so for c.d.example.org", hostOf(certStatus(), "c.d.example.org"))
	_, notifications, _ := api("GET", "/notifications", nil)
	told := ""
	for _, entry := range list(notifications, "data") {
		if message := text(entry, "Message"); strings.Contains(message, "couldn't get the TLS certificate of example.org") {
			told = message
		}
	}
	expect(strings.Contains(told, "DNS provider refused the credentials"), "the administrators are told why", told)
	expect(!strings.Contains(told, goodToken), "without the token")

	failedCalls := len(fakeCalls(false))
	restartProcess()
	time.Sleep(15 * time.Second)
	cert = certOf(certStatus(), "example.org")
	expect(text(cert, "wait", "reason") == "backoff" && text(cert, "last", "code") == "dnsCredentials",
		"after a restart the failure and the wait are still known", cert["wait"])
	stand := 0
	for _, call := range fakeCalls(false)[failedCalls:] {
		if call["method"] == "POST" {
			stand++
		}
	}
	expect(stand == 0, "and no order was tried again", stand)

	fmt.Println("== 5b. the token works again: Renew now")
	plainGet(fake + "/token?status=active")
	code, _, raw = api("POST", "/certificates/renew", object{"domain": "example.org"})
	expect(code == 200, "\"Renew now\" is accepted although the wait is not over", code, raw)
	until("c.d.example.org is covered", 120, func() bool { return handshake("c.d.example.org", roots).trusted })
	renewed := handshake("c.d.example.org", roots)
	expect(renewed.trusted && renewed.serial != deep.serial, "a new certificate covers c.d.example.org", renewed.names)
	cert = certOf(certStatus(), "example.org")
	expect(at(cert, "last", "ok") == true && cert["wait"] == nil, "the panel: the last order succeeded, nothing to wait for", cert["last"], cert["wait"])

	// ---- 6. a domain whose DNS is somewhere else
	fmt.Println("== 6. a domain at another DNS provider: the challenge handed to example.org")
	plainGet(fake + "/dns?zone=example.com")
	code, info, raw := api("POST", "/certificates/domain", object{"domain": "example.com"})
	expect(code == 200 && fmt.Sprint(at(info, "data", "nameservers")) == "[ns1.example.com]" && text(info, "data", "provider") == "",
		"the name servers of example.com are found, and are none Cosmos knows", raw)
	offer := list(info, "data", "canAnswer")
	expect(len(offer) == 1 && text(offer[0], "record") == "_acme-challenge.example.com" && text(offer[0], "target") == "example-com._acme.example.org",
		"the record to create is proposed", offer)
	code, check, raw := api("POST", "/certificates/delegation", object{"domain": "example.com", "via": "example.org"})
	expect(code == 200 && text(check, "data", "state") == "missing", "the check says the record is not there yet", raw)

	fakeCalls(true)
	code, _, raw = api("PUT", "/zones/example.com", object{"HTTPSCertificateMode": "LETSENCRYPT", "ChallengeVia": "example.org", "UseWildcardCertificate": true})
	expect(code == 200, "the domain is stored with its challenge handed to example.org", code, raw)
	time.Sleep(3 * time.Second)
	serverUp()
	login()
	addRoute("other", "app.example.com")
	until("the order without the record ends", 60, func() bool {
		return text(certOf(certStatus(), "example.com"), "last", "code") != ""
	})
	cert = certOf(certStatus(), "example.com")
	expect(text(cert, "last", "code") == "delegationMissing" && strings.Contains(text(cert, "last", "detail"), "_acme-challenge.example.com must be a CNAME to example-com._acme.example.org"),
		"without the record: the order stops and names the record", cert["last"])
	expect(len(txtWrites(fakeCalls(false))) == 0, "nothing was written and the authority was not asked", txtWrites(fakeCalls(false)))

	plainGet(fake + "/dns?name=_acme-challenge.example.com&type=CNAME&value=example-com._acme.example.org")
	code, check, raw = api("POST", "/certificates/delegation", object{"domain": "example.com", "via": "example.org"})
	expect(code == 200 && text(check, "data", "state") == "ok", "the record is created at the other provider: the check finds it", raw)
	code, _, raw = api("POST", "/certificates/renew", object{"domain": "example.com"})
	expect(code == 200, "\"Renew now\" for example.com", code, raw)
	until("app.example.com is covered", 120, func() bool { return handshake("app.example.com", roots).trusted })
	other := handshake("app.example.com", roots)
	expect(other.trusted && len(other.names) == 2, "app.example.com is served with a trusted certificate for example.com and *.example.com", other.names, other.err)
	written := txtWrites(fakeCalls(false))
	expect(len(written) == 2 && written[0] == "example-com._acme.example.org", "the challenge was answered in example.org's DNS", written)
	cert = certOf(certStatus(), "example.com")
	expect(cert["method"] == "delegated" && cert["via"] == "example.org" && cert["present"] == true, "the panel: example.com, challenge answered in example.org", cert["method"], cert["via"])

	// ---- 7. a name under no domain with DNS access
	fmt.Println("== 7. a public name without DNS access: no order that cannot succeed")
	addRoute("nodns", "app.example.net")
	time.Sleep(10 * time.Second)
	cert = certOf(certStatus(), "app.example.net")
	expect(cert["method"] == "http" && cert["blocked"] == "noHTTPChallenge" && cert["last"] == nil,
		"the panel: needs DNS access, and no order was tried", cert["method"], cert["blocked"], cert["last"])
	expect(!handshake("app.example.net", roots).trusted, "it is answered with the self-signed certificate")

	// ---- 8. renewal when due
	fmt.Println("== 8. renewal by itself when due")
	cert = certOf(certStatus(), "example.org")
	note("example.org: valid until", text(cert, "notAfter"), "- renewal planned", text(cert, "renewAt"), "by", cert["renewSource"])
	current := handshake("app1.example.org", roots)
	fakeCalls(true)
	changed := until("the certificate of example.org is replaced before it ends", int(validity/time.Second), func() bool {
		now := handshake("app1.example.org", roots)
		return now.trusted && now.serial != current.serial
	})
	after := handshake("app1.example.org", roots)
	expect(changed && after.until.After(current.until), "a new certificate with a later end is served, without a restart", after.until.Sub(current.until).Round(time.Second))
	expect(time.Now().Before(current.until), "before the old one ended", time.Until(current.until).Round(time.Second), "left")
	expect(time.Since(current.from) > validity/3, "and not right after it was issued", time.Since(current.from).Round(time.Second), "old")
	cert = certOf(certStatus(), "example.org")
	expect(at(cert, "last", "ok") == true && names(cert) == "*.example.org a.b.example.org c.d.example.org example.org", "the panel: renewed, same names", names(cert))

	fmt.Println()
	if len(failures) > 0 {
		fmt.Println(len(failures), "FAILED:")
		for _, failure := range failures {
			fmt.Println("  -", failure)
		}
		os.Exit(1)
	}
	fmt.Println("all checks passed")
	_ = url.QueryEscape
}
