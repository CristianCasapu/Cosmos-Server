// Package cloudflare is a fork addition: the guided Cloudflare integration.
// It checks an API token, shows what connecting a domain will write, sets the
// "proxied" flag of the records Cosmos manages (the choice between serving an
// application through Cloudflare's proxy or directly, which the libdns
// provider used by src/dnsrecords cannot make), and keeps Cloudflare's
// address ranges current.
//
// This file is the only place that talks to Cloudflare: one fixed host, a
// timeout on every call, answers read through a size limit, redirects
// refused. The token goes into the Authorization header and nowhere else: it
// is never logged and never part of an error.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// the one address this package sends requests to
const apiBase = "https://api.cloudflare.com/client/v4"

const (
	callTimeout  = 15 * time.Second
	maxAnswer    = 2 << 20 // bytes read of one answer
	recordsPage  = 500
	maxRecordsPg = 10 // 5000 records
	zonesPage    = 50
	maxZonesPg   = 4 // 200 zones
)

// apiTransport carries the requests; the tests put a stand-in here.
var apiTransport http.RoundTripper = &http.Transport{
	Proxy:                 nil, // straight to Cloudflare, whatever the environment says
	DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
	TLSHandshakeTimeout:   8 * time.Second,
	ResponseHeaderTimeout: callTimeout,
	MaxIdleConns:          4,
	IdleConnTimeout:       60 * time.Second,
	ForceAttemptHTTP2:     true,
}

// transportFunc lets the transport be swapped while clients exist.
type transportFunc struct{}

func (transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return apiTransport.RoundTrip(req)
}

// httpClient is used for every call, the libdns provider's included (which
// would otherwise use Go's default client, without a timeout).
var httpClient = &http.Client{
	Timeout:   callTimeout,
	Transport: transportFunc{},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// APIError is a refusal by Cloudflare, or a failure to reach it. Kind is what
// the UI turns into a sentence; Message is Cloudflare's own short text,
// cleaned, for the admin to read next to it.
type APIError struct {
	Kind    string // invalid_token, malformed_token, forbidden, not_found, rate_limited, unreachable, server, other
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	text := "Cloudflare: " + e.Kind
	if e.Status != 0 {
		text += " (HTTP " + strconv.Itoa(e.Status)
		if e.Code != 0 {
			text += ", code " + strconv.Itoa(e.Code)
		}
		text += ")"
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// kindOf says what went wrong, "other" for an error that is not Cloudflare's.
func kindOf(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Kind
	}
	return "other"
}

// cleanText keeps what can be shown or logged of a text that came from
// outside: one line, printable, bounded.
func cleanText(text string, max int) string {
	var b strings.Builder
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		if b.Len() >= max {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// validID reports whether a text can be a zone or record id: only such a
// text is ever put into the path of a request.
func validID(id string) bool { return idPattern.MatchString(id) }

type envelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	ResultInfo *struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

// Client makes calls with one token ("" for the calls that need none).
type Client struct {
	token string
}

// NewClient returns a client for a token.
func NewClient(token string) *Client { return &Client{token: strings.TrimSpace(token)} }

func (c *Client) call(ctx context.Context, method string, path string, query url.Values, body interface{}, result interface{}) (*envelope, error) {
	target := apiBase + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, &APIError{Kind: "other", Message: "request could not be built"}
		}
		reader = bytes.NewReader(raw)
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, &APIError{Kind: "other", Message: "request could not be built"}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// the error of the HTTP client names the address, never a header
		return nil, &APIError{Kind: "unreachable", Message: unreachableReason(err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, &APIError{Kind: "unreachable", Status: resp.StatusCode, Message: "the answer could not be read"}
	}
	if len(raw) > maxAnswer {
		return nil, &APIError{Kind: "other", Status: resp.StatusCode, Message: "the answer is larger than expected"}
	}

	var env envelope
	parsed := json.Unmarshal(raw, &env) == nil

	if resp.StatusCode >= 300 || !parsed || !env.Success {
		apiErr := &APIError{Status: resp.StatusCode, Kind: "other"}
		if parsed && len(env.Errors) > 0 {
			apiErr.Code = env.Errors[0].Code
			apiErr.Message = cleanText(env.Errors[0].Message, 200)
		}
		switch {
		case apiErr.Code == 6003 || apiErr.Code == 6111 || apiErr.Code == 1001:
			apiErr.Kind = "malformed_token"
		case resp.StatusCode == http.StatusUnauthorized || apiErr.Code == 1000 || apiErr.Code == 9109:
			apiErr.Kind = "invalid_token"
		case resp.StatusCode == http.StatusForbidden:
			apiErr.Kind = "forbidden"
		case resp.StatusCode == http.StatusNotFound:
			apiErr.Kind = "not_found"
		case resp.StatusCode == http.StatusTooManyRequests:
			apiErr.Kind = "rate_limited"
		case resp.StatusCode >= 500:
			apiErr.Kind = "server"
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			apiErr.Message = "unexpected redirect"
		}
		return nil, apiErr
	}

	if result != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, result); err != nil {
			return nil, &APIError{Kind: "other", Status: resp.StatusCode, Message: "the answer is not in the expected form"}
		}
	}
	return &env, nil
}

func unreachableReason(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "no answer in time"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "api.cloudflare.com could not be looked up"
	}
	return "the connection failed"
}

// VerifyToken asks Cloudflare whether the token is valid: "active",
// "disabled" or "expired".
func (c *Client) VerifyToken(ctx context.Context) (string, error) {
	var result struct {
		Status string `json:"status"`
	}
	if _, err := c.call(ctx, "GET", "/user/tokens/verify", nil, nil, &result); err != nil {
		return "", err
	}
	switch result.Status {
	case "active", "disabled", "expired":
		return result.Status, nil
	}
	return "", &APIError{Kind: "other", Message: "unknown token status"}
}

// Zone is a domain as Cloudflare lists it.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Paused bool   `json:"paused"`
	Type   string `json:"type"`
	Plan   struct {
		Name     string `json:"name"`
		LegacyID string `json:"legacy_id"`
	} `json:"plan"`
}

// ListZones lists the zones the token sees, at most 200; name narrows it to
// one. more is true when there are others.
func (c *Client) ListZones(ctx context.Context, name string) (zones []Zone, more bool, err error) {
	for page := 1; page <= maxZonesPg; page++ {
		query := url.Values{"per_page": {strconv.Itoa(zonesPage)}, "page": {strconv.Itoa(page)}}
		if name != "" {
			query.Set("name", name)
		}
		var batch []Zone
		env, err := c.call(ctx, "GET", "/zones", query, nil, &batch)
		if err != nil {
			return nil, false, err
		}
		for _, zone := range batch {
			zone.Name = strings.ToLower(strings.TrimSuffix(cleanText(zone.Name, 253), "."))
			if validID(zone.ID) && zone.Name != "" {
				zones = append(zones, zone)
			}
		}
		if len(batch) < zonesPage || env.ResultInfo == nil || page >= env.ResultInfo.TotalPages {
			return zones, false, nil
		}
	}
	return zones, true, nil
}

// Record is a DNS record as Cloudflare holds it. Name is the full name.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

// ListRecords lists the records of a zone, of one type or ("") all, at most
// 5000. more is true when the zone has others.
func (c *Client) ListRecords(ctx context.Context, zoneID string, recordType string) (records []Record, more bool, err error) {
	if !validID(zoneID) {
		return nil, false, &APIError{Kind: "other", Message: "not a zone id"}
	}
	for page := 1; page <= maxRecordsPg; page++ {
		query := url.Values{"per_page": {strconv.Itoa(recordsPage)}, "page": {strconv.Itoa(page)}}
		if recordType != "" {
			query.Set("type", recordType)
		}
		var batch []Record
		env, err := c.call(ctx, "GET", "/zones/"+zoneID+"/dns_records", query, nil, &batch)
		if err != nil {
			return nil, false, err
		}
		for _, record := range batch {
			record.Name = strings.ToLower(strings.TrimSuffix(record.Name, "."))
			records = append(records, record)
		}
		if len(batch) < recordsPage || env.ResultInfo == nil || page >= env.ResultInfo.TotalPages {
			return records, false, nil
		}
	}
	return records, true, nil
}

// CreateRecord adds a record.
func (c *Client) CreateRecord(ctx context.Context, zoneID string, record Record) error {
	if !validID(zoneID) {
		return &APIError{Kind: "other", Message: "not a zone id"}
	}
	record.ID = ""
	_, err := c.call(ctx, "POST", "/zones/"+zoneID+"/dns_records", nil, record, nil)
	return err
}

// SetProxied switches Cloudflare's proxy on or off for one record. A proxied
// record has no TTL of its own (1 means automatic); a direct one gets ttl.
func (c *Client) SetProxied(ctx context.Context, zoneID string, recordID string, proxied bool, ttl int) error {
	if !validID(zoneID) || !validID(recordID) {
		return &APIError{Kind: "other", Message: "not a record id"}
	}
	if proxied {
		ttl = 1
	}
	body := map[string]interface{}{"proxied": proxied, "ttl": ttl}
	_, err := c.call(ctx, "PATCH", "/zones/"+zoneID+"/dns_records/"+recordID, nil, body, nil)
	return err
}

// the id no record has
const noRecordID = "00000000000000000000000000000000"

// CanEdit finds out whether the token may change the records of a zone
// without changing anything: it asks for the deletion of a record that does
// not exist. Cloudflare answers "not found" to a token that may edit and
// refuses one that may not. "yes", "no", or "unknown" for any other answer.
func (c *Client) CanEdit(ctx context.Context, zoneID string) string {
	if !validID(zoneID) {
		return "unknown"
	}
	_, err := c.call(ctx, "DELETE", "/zones/"+zoneID+"/dns_records/"+noRecordID, nil, nil, nil)
	var apiErr *APIError
	if err == nil || !errors.As(err, &apiErr) {
		return "unknown"
	}
	switch {
	case apiErr.Status == http.StatusNotFound || (apiErr.Status == http.StatusBadRequest && apiErr.Code >= 81000 && apiErr.Code < 82000):
		return "yes"
	case apiErr.Status == http.StatusForbidden || apiErr.Status == http.StatusUnauthorized:
		return "no"
	}
	return "unknown"
}

// SSLMode reads how Cloudflare talks to the origin for a zone: off, flexible,
// full, strict or origin_pull. Most DNS tokens may not read it.
func (c *Client) SSLMode(ctx context.Context, zoneID string) (string, error) {
	if !validID(zoneID) {
		return "", &APIError{Kind: "other", Message: "not a zone id"}
	}
	var result struct {
		Value string `json:"value"`
	}
	if _, err := c.call(ctx, "GET", "/zones/"+zoneID+"/settings/ssl", nil, nil, &result); err != nil {
		return "", err
	}
	switch result.Value {
	case "off", "flexible", "full", "strict", "origin_pull":
		return result.Value, nil
	}
	return "", &APIError{Kind: "other", Message: "unknown SSL mode"}
}

// PublishedRanges asks Cloudflare for its address ranges. Needs no token.
func PublishedRanges(ctx context.Context) (v4 []string, v6 []string, err error) {
	var result struct {
		V4 []string `json:"ipv4_cidrs"`
		V6 []string `json:"ipv6_cidrs"`
	}
	if _, err := NewClient("").call(ctx, "GET", "/ips", nil, nil, &result); err != nil {
		return nil, nil, err
	}
	return result.V4, result.V6, nil
}
