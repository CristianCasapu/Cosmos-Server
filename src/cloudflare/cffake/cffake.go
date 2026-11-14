// Package cffake is a fork addition: a stand-in for the parts of Cloudflare's
// API the fork uses (token verification, zones, DNS records, the SSL setting,
// the address ranges, the client certificate of Authenticated Origin Pulls),
// for tests. Nothing in the server imports it; the tests
// of src/cloudflare do, and scripts/fork-fake-cloudflare.go serves it for the
// browser checks. It answers the way Cloudflare's documentation describes,
// and where that is silent the way the libdns client expects.
package cffake

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Token is what a credential may do.
type Token struct {
	Status string // active, expired, disabled
	// Account: owned by an account, so unknown to /user/tokens/verify
	Account bool
	// Zones the token sees (names); nil for all
	Zones []string
	// NoZoneRead: the token may not list zones at all
	NoZoneRead   bool
	ReadDNS      bool
	EditDNS      bool
	ReadSettings bool
	// EditSSL: "SSL and Certificates: Edit", which the "Edit zone DNS"
	// template does not include
	EditSSL bool
}

// OriginPullCert is a client certificate uploaded for a zone's Authenticated
// Origin Pulls: what Cloudflare's proxy would present to the origin.
type OriginPullCert struct {
	ID          string `json:"id"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

// Record is a DNS record.
type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

// Zone is a domain with its records.
type Zone struct {
	ID      string
	Name    string
	Plan    string
	SSLMode string
	Records []Record
	// zone-level Authenticated Origin Pulls
	OriginPullCerts   []OriginPullCert
	OriginPullEnabled bool
}

// Call is one request the server got, without its credential.
type Call struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body,omitempty"`
}

// Server is the stand-in. Its fields are guarded by the methods below.
type Server struct {
	mu     sync.Mutex
	tokens map[string]Token
	zones  []*Zone
	nextID int
	calls  []Call
	// Down makes every call answer 502 with a page, like a broken gateway
	down bool
	// RangesV4/V6 are what GET /ips returns
	rangesV4 []string
	rangesV6 []string
	// seenAs is the address /cdn-cgi/trace reports for the caller
	seenAs string
}

// New returns an empty stand-in with Cloudflare's real ranges.
func New() *Server {
	return &Server{
		tokens:   map[string]Token{},
		rangesV4: []string{"173.245.48.0/20", "103.21.244.0/22", "104.16.0.0/13", "172.64.0.0/13"},
		rangesV6: []string{"2400:cb00::/32", "2606:4700::/32", "2a06:98c0::/29"},
	}
}

// AddToken registers a credential.
func (s *Server) AddToken(token string, rights Token) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rights.Status == "" {
		rights.Status = "active"
	}
	s.tokens[token] = rights
}

// AddZone registers a domain and returns its id.
func (s *Server) AddZone(name string, records ...Record) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	zone := &Zone{ID: s.newID(), Name: name, Plan: "Free Website", SSLMode: "strict"}
	for _, record := range records {
		record.ID = s.newID()
		if record.TTL == 0 {
			record.TTL = 1
		}
		zone.Records = append(zone.Records, record)
	}
	s.zones = append(s.zones, zone)
	return zone.ID
}

// ZoneNames lists the domains the stand-in holds.
func (s *Server) ZoneNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := []string{}
	for _, zone := range s.zones {
		names = append(names, zone.Name)
	}
	return names
}

// AddRecord adds a record to a zone, the way an edit in Cloudflare's
// dashboard or another program would.
func (s *Server) AddRecord(zoneName string, record Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName {
			record.ID = s.newID()
			if record.TTL == 0 {
				record.TTL = 1
			}
			zone.Records = append(zone.Records, record)
		}
	}
}

// SetContent changes what a record points at, the way another program
// keeping it current would.
func (s *Server) SetContent(zoneName string, recordID string, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name != zoneName {
			continue
		}
		for i := range zone.Records {
			if zone.Records[i].ID == recordID {
				zone.Records[i].Content = content
			}
		}
	}
}

// SetSSLMode sets a zone's SSL setting.
func (s *Server) SetSSLMode(zoneName string, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName {
			zone.SSLMode = mode
		}
	}
}

// SetRanges sets what GET /ips returns.
func (s *Server) SetRanges(v4 []string, v6 []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rangesV4, s.rangesV6 = v4, v6
}

// SetSeenAs sets the address the caller is told it comes from.
func (s *Server) SetSeenAs(address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenAs = address
}

// SetDown switches the broken-gateway mode.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Records returns a copy of a zone's records, sorted by name and type.
func (s *Server) Records(zoneName string) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName {
			out := append([]Record{}, zone.Records...)
			sort.SliceStable(out, func(i, j int) bool {
				if out[i].Name != out[j].Name {
					return out[i].Name < out[j].Name
				}
				return out[i].Type < out[j].Type
			})
			return out
		}
	}
	return nil
}

// SetProxied changes a record's flag the way an edit in Cloudflare's
// dashboard would.
func (s *Server) SetProxied(zoneName string, name string, recordType string, proxied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name != zoneName {
			continue
		}
		for i := range zone.Records {
			if zone.Records[i].Name == name && zone.Records[i].Type == recordType {
				zone.Records[i].Proxied = proxied
			}
		}
	}
}

// OriginPull returns the client certificates uploaded for a zone, the most
// recent last, and whether its zone-level Authenticated Origin Pulls are on.
func (s *Server) OriginPull(zoneName string) ([]OriginPullCert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName {
			return append([]OriginPullCert{}, zone.OriginPullCerts...), zone.OriginPullEnabled
		}
	}
	return nil, false
}

// Calls returns the requests received so far; clear empties the list.
func (s *Server) Calls(clear bool) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Call{}, s.calls...)
	if clear {
		s.calls = nil
	}
	return out
}

// Writes counts the calls that change something (POST, PATCH, PUT), and the
// DELETEs of a record that exists.
func Writes(calls []Call) int {
	count := 0
	for _, call := range calls {
		if call.Method == "POST" || call.Method == "PATCH" || call.Method == "PUT" ||
			(call.Method == "DELETE" && !strings.HasSuffix(call.Path, strings.Repeat("0", 32))) {
			count++
		}
	}
	return count
}

func (s *Server) newID() string {
	s.nextID++
	return fmt.Sprintf("%032x", s.nextID)
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func fail(w http.ResponseWriter, status int, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false, "errors": []apiError{{code, message}}, "messages": []string{}, "result": nil,
	})
}

func ok(w http.ResponseWriter, result interface{}, info map[string]int) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]interface{}{"success": true, "errors": []apiError{}, "messages": []string{}, "result": result}
	if info != nil {
		body["result_info"] = info
	}
	json.NewEncoder(w).Encode(body)
}

var (
	bearerForm  = regexp.MustCompile(`^Bearer [A-Za-z0-9_-]{40,}$`)
	recordsPath = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/dns_records(?:/([0-9a-f]{32}))?$`)
	sslPath     = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/settings/ssl$`)
	pullsPath   = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/origin_tls_client_auth(/settings)?$`)
)

func page(req *http.Request, total int, fallback int) (from int, to int, info map[string]int) {
	perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
	if perPage < 1 {
		perPage = fallback
	}
	number, _ := strconv.Atoi(req.URL.Query().Get("page"))
	if number < 1 {
		number = 1
	}
	from = (number - 1) * perPage
	if from > total {
		from = total
	}
	to = from + perPage
	if to > total {
		to = total
	}
	pages := (total + perPage - 1) / perPage
	return from, to, map[string]int{"page": number, "per_page": perPage, "count": to - from, "total_count": total, "total_pages": pages}
}

func (s *Server) sees(token Token, zone *Zone) bool {
	if token.Zones == nil {
		return true
	}
	for _, name := range token.Zones {
		if name == zone.Name {
			return true
		}
	}
	return false
}

// ServeHTTP answers as api.cloudflare.com.
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(req.Body, 1<<20))

	s.mu.Lock()
	defer s.mu.Unlock()
	logged := string(raw)
	if strings.Contains(logged, "private_key") {
		// a private key is not kept in the list of calls
		logged = "(a certificate and its private key)"
	}
	s.calls = append(s.calls, Call{Method: req.Method, Path: req.URL.Path, Body: logged})

	if s.down {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>Bad Gateway</html>")
		return
	}

	path := req.URL.Path
	if path == "/cdn-cgi/trace" && req.Method == "GET" {
		// what every name behind Cloudflare answers: the address the caller is seen with
		io.WriteString(w, "fl=000f000\nh=api.cloudflare.com\nip="+s.seenAs+"\nts=1700000000.000\nvisit_scheme=https\n")
		return
	}
	if path == "/client/v4/ips" && req.Method == "GET" {
		ok(w, map[string]interface{}{"ipv4_cidrs": s.rangesV4, "ipv6_cidrs": s.rangesV6, "etag": "fake"}, nil)
		return
	}

	header := req.Header.Get("Authorization")
	if header == "" {
		fail(w, http.StatusBadRequest, 1001, `Missing "Authorization" header`)
		return
	}
	if !bearerForm.MatchString(header) {
		fail(w, http.StatusBadRequest, 6003, "Invalid request headers")
		return
	}
	token, known := s.tokens[strings.TrimPrefix(header, "Bearer ")]

	if path == "/client/v4/user/tokens/verify" && req.Method == "GET" {
		if !known || token.Account {
			fail(w, http.StatusUnauthorized, 1000, "Invalid API Token")
			return
		}
		ok(w, map[string]string{"id": strings.Repeat("a", 32), "status": token.Status}, nil)
		return
	}
	if !known || token.Status != "active" {
		fail(w, http.StatusForbidden, 9109, "Invalid access token")
		return
	}

	if path == "/client/v4/zones" && req.Method == "GET" {
		if token.NoZoneRead {
			ok(w, []interface{}{}, map[string]int{"page": 1, "per_page": 20, "count": 0, "total_count": 0, "total_pages": 0})
			return
		}
		visible := []map[string]interface{}{}
		for _, zone := range s.zones {
			if !s.sees(token, zone) {
				continue
			}
			// the libdns client asks with the trailing dot of a zone name
			if name := strings.TrimSuffix(req.URL.Query().Get("name"), "."); name != "" && name != zone.Name {
				continue
			}
			visible = append(visible, map[string]interface{}{
				"id": zone.ID, "name": zone.Name, "status": "active", "paused": false, "type": "full",
				"plan": map[string]string{"name": zone.Plan, "legacy_id": "free"},
			})
		}
		from, to, info := page(req, len(visible), 20)
		ok(w, visible[from:to], info)
		return
	}

	if match := sslPath.FindStringSubmatch(path); match != nil && req.Method == "GET" {
		zone := s.zone(match[1], token)
		if zone == nil || !token.ReadSettings {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		ok(w, map[string]interface{}{"id": "ssl", "value": zone.SSLMode, "editable": true}, nil)
		return
	}

	if match := pullsPath.FindStringSubmatch(path); match != nil {
		zone := s.zone(match[1], token)
		if zone == nil || !token.EditSSL {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		switch {
		case match[2] == "" && req.Method == "POST":
			var upload struct {
				Certificate string `json:"certificate"`
				PrivateKey  string `json:"private_key"`
			}
			if json.Unmarshal(raw, &upload) != nil || upload.PrivateKey == "" {
				fail(w, http.StatusBadRequest, 1400, "Request body is invalid.")
				return
			}
			block, _ := pem.Decode([]byte(upload.Certificate))
			if block == nil {
				fail(w, http.StatusBadRequest, 1402, "certificate could not be parsed")
				return
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || cert.IsCA {
				// what Cloudflare answers when a root is uploaded
				fail(w, http.StatusBadRequest, 1401, "missing leaf certificate")
				return
			}
			stored := OriginPullCert{ID: s.newID(), Certificate: upload.Certificate, PrivateKey: upload.PrivateKey}
			zone.OriginPullCerts = append(zone.OriginPullCerts, stored)
			ok(w, map[string]interface{}{"id": stored.ID, "status": "active", "expires_on": cert.NotAfter, "certificate": stored.Certificate}, nil)
		case match[2] != "" && req.Method == "PUT":
			var setting struct {
				Enabled *bool `json:"enabled"`
			}
			if json.Unmarshal(raw, &setting) != nil || setting.Enabled == nil {
				fail(w, http.StatusBadRequest, 1400, "Request body is invalid.")
				return
			}
			zone.OriginPullEnabled = *setting.Enabled
			ok(w, map[string]bool{"enabled": zone.OriginPullEnabled}, nil)
		default:
			fail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed for this authentication scheme")
		}
		return
	}

	match := recordsPath.FindStringSubmatch(path)
	if match == nil {
		fail(w, http.StatusNotFound, 7003, "Could not route to "+path)
		return
	}
	zone := s.zone(match[1], token)
	if zone == nil {
		fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	recordID := match[2]

	switch {
	case req.Method == "GET" && recordID == "":
		if !token.ReadDNS && !token.EditDNS {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		query := req.URL.Query()
		matching := []Record{}
		for _, record := range zone.Records {
			if t := query.Get("type"); t != "" && t != record.Type {
				continue
			}
			if n := strings.TrimSuffix(query.Get("name"), "."); n != "" && n != record.Name {
				continue
			}
			if c := query.Get("content.exact"); c != "" && c != record.Content {
				continue
			}
			if c := query.Get("content.contains"); c != "" && !strings.Contains(record.Content, c) {
				continue
			}
			matching = append(matching, record)
		}
		from, to, info := page(req, len(matching), 100)
		ok(w, matching[from:to], info)

	case req.Method == "POST" && recordID == "":
		if !token.EditDNS {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		var record Record
		if json.Unmarshal(raw, &record) != nil || record.Type == "" || record.Content == "" {
			fail(w, http.StatusBadRequest, 9005, "Content for A record must be a valid IPv4 address.")
			return
		}
		record.Name = absolute(record.Name, zone.Name)
		for _, existing := range zone.Records {
			if existing.Type == record.Type && existing.Name == record.Name && existing.Content == record.Content {
				fail(w, http.StatusBadRequest, 81058, "An identical record already exists.")
				return
			}
		}
		if problem := proxyProblem(record); problem != "" {
			fail(w, http.StatusBadRequest, 9003, problem)
			return
		}
		record.ID = s.newID()
		if record.Proxied || record.TTL == 0 {
			record.TTL = 1
		}
		zone.Records = append(zone.Records, record)
		ok(w, record, nil)

	case req.Method == "PATCH" && recordID != "":
		if !token.EditDNS {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		for i := range zone.Records {
			if zone.Records[i].ID != recordID {
				continue
			}
			var change struct {
				Proxied *bool   `json:"proxied"`
				TTL     *int    `json:"ttl"`
				Content *string `json:"content"`
			}
			if json.Unmarshal(raw, &change) != nil {
				fail(w, http.StatusBadRequest, 9207, "Request body is invalid.")
				return
			}
			updated := zone.Records[i]
			if change.Proxied != nil {
				updated.Proxied = *change.Proxied
			}
			if change.TTL != nil {
				updated.TTL = *change.TTL
			}
			if change.Content != nil {
				updated.Content = *change.Content
			}
			if problem := proxyProblem(updated); problem != "" {
				fail(w, http.StatusBadRequest, 9003, problem)
				return
			}
			if updated.Proxied {
				updated.TTL = 1
			}
			zone.Records[i] = updated
			ok(w, updated, nil)
			return
		}
		fail(w, http.StatusNotFound, 81044, "Record does not exist.")

	case req.Method == "DELETE" && recordID != "":
		if !token.EditDNS {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		for i := range zone.Records {
			if zone.Records[i].ID == recordID {
				zone.Records = append(zone.Records[:i], zone.Records[i+1:]...)
				ok(w, map[string]string{"id": recordID}, nil)
				return
			}
		}
		fail(w, http.StatusNotFound, 81044, "Record does not exist.")

	default:
		fail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed for this authentication scheme")
	}
}

func (s *Server) zone(id string, token Token) *Zone {
	for _, zone := range s.zones {
		if zone.ID == id && s.sees(token, zone) {
			return zone
		}
	}
	return nil
}

// absolute turns the name of a new record into a full one: Cloudflare takes
// "app", "@" and "app.example.org" alike.
func absolute(name string, zone string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	switch {
	case name == "" || name == "@":
		return zone
	case name == zone || strings.HasSuffix(name, "."+zone):
		return name
	}
	return name + "." + zone
}

func proxyProblem(record Record) string {
	if !record.Proxied {
		return ""
	}
	if record.Type != "A" && record.Type != "AAAA" && record.Type != "CNAME" {
		return "This record type cannot be proxied."
	}
	if ip := net.ParseIP(record.Content); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return "Target " + record.Content + " is not allowed for a proxied record."
	}
	return ""
}
