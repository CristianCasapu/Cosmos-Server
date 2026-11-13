package main

// Fork addition: the icon of an application is fetched and passed on with care.
//
// /cosmos/api/favicon asks the application behind a route for its icon and
// hands the answer to the browser from the panel's own address. What is
// fetched, kept and sent used to be decided by the application: the media
// type it claimed was repeated to the browser, redirects and icon links were
// followed wherever they pointed, and the size was not limited. Now:
//
//   - the type is read from the bytes themselves. PNG, JPEG, GIF, WebP, BMP
//     and ICO pictures are passed on under that type; an SVG drawing is passed
//     on as SVG; anything else is not an icon and the default one is shown;
//   - every icon answer tells the browser to treat it as a picture and
//     nothing more (a Content-Security-Policy that forbids scripts, frames and
//     requests, and "sandbox"), so opening it in a tab of its own shows a
//     picture at most;
//   - an icon is at most one megabyte, and it is read within the time limit
//     of the request (upstream ended that limit before reading, so an icon
//     arrived only when it was already complete);
//   - a redirect or an icon link that leaves the application's own host is
//     followed only to a public address: an application cannot make the server
//     ask the local network, the machine itself or another container.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.deanishe.net/favicon"
)

const maxIconSize = 1 << 20

// what an icon may be, by what its first bytes say
var iconPictureTypes = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/gif":                true,
	"image/webp":               true,
	"image/bmp":                true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

// iconContentType returns the media type an icon is passed on with, and false
// when the bytes are not a picture.
func iconContentType(claimed string, body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	sniffed := http.DetectContentType(body)
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = sniffed[:i]
	}
	if iconPictureTypes[sniffed] {
		return sniffed, true
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(claimed)), "image/svg+xml") && looksLikeSVG(body) {
		return "image/svg+xml", true
	}
	return "", false
}

func looksLikeSVG(body []byte) bool {
	head := body
	if len(head) > 1024 {
		head = head[:1024]
	}
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	head = bytes.ToLower(bytes.TrimSpace(head))
	return (bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<svg")) || bytes.HasPrefix(head, []byte("<!--")) || bytes.HasPrefix(head, []byte("<!doctype svg"))) && bytes.Contains(head, []byte("<svg"))
}

// readIcon reads an icon and refuses one that is larger than an icon gets.
func readIcon(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxIconSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxIconSize {
		return nil, errors.New("larger than an icon may be")
	}
	return data, nil
}

// iconHeaders is the hook of sendImage: whatever is sent is a picture and
// nothing more to the browser.
func iconHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox; frame-ancestors 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// publicAddress reports whether an address is one of the internet: not this
// machine, not a private or link-local network, not a carrier or special range.
func publicAddress(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// carrier NAT 100.64.0.0/10, 0.0.0.0/8, 192.0.0.0/24, benchmark 198.18.0.0/15, reserved 240.0.0.0/4
		if (v4[0] == 100 && v4[1]&0xc0 == 64) || v4[0] == 0 || (v4[0] == 192 && v4[1] == 0 && v4[2] == 0) ||
			(v4[0] == 198 && v4[1]&0xfe == 18) || v4[0] >= 240 {
			return false
		}
	}
	return true
}

// lookupIconHost resolves a host name (a variable so that tests need no DNS)
var lookupIconHost = net.LookupIP

// iconURLAllowed reports whether an address may be fetched while looking for
// the icon of the application at base: the application's own host always,
// another host only when it is on the internet.
func iconURLAllowed(target *url.URL, base *url.URL) bool {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return false
	}
	if base != nil && strings.EqualFold(target.Hostname(), base.Hostname()) {
		return true
	}
	if ip := net.ParseIP(target.Hostname()); ip != nil {
		return publicAddress(ip)
	}
	ips, err := lookupIconHost(target.Hostname())
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !publicAddress(ip) {
			return false
		}
	}
	return true
}

// iconClient is the HTTP client of the icon search for the application at
// base: redirects are followed under the rule of iconURLAllowed, five at most.
func iconClient(base *url.URL) *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects for an icon")
			}
			if !iconURLAllowed(req.URL, base) {
				return errors.New("an icon is not followed to " + printable(req.URL.Host, 100))
			}
			return nil
		},
	}
}

// fetchForIcon is the hook of httpGetWithTimeout. The time limit is the
// client's, which also covers reading the answer: the caller's own limit ends
// when it returns, before the answer has been read, so an icon only arrived
// when it happened to be complete by then.
func fetchForIcon(req *http.Request, timeout time.Duration) (*http.Response, error) {
	client := iconClient(req.URL)
	client.Timeout = timeout
	return client.Do(req.WithContext(context.Background()))
}

// findIcons is the hook that replaces favicon.Find: the same search, with
// the client above.
func findIcons(siteurl string) ([]*favicon.Icon, error) {
	base, err := url.Parse(siteurl)
	if err != nil {
		return nil, err
	}
	return favicon.New(favicon.WithClient(iconClient(base))).Find(siteurl)
}
