// Package upnp is a fork addition: it asks the LAN's router, over UPnP IGD, to
// forward Cosmos' own HTTP(S) ports, so the server can be reached from the
// internet without configuring NAT by hand.
//
// It is deliberately small and uses only the standard library. Because any
// device on a LAN can answer UPnP discovery, the client talks to exactly one
// host: the machine's default gateway. Answers from, or URLs pointing to, any
// other address are ignored.
package upnp

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	discoveryTimeout   = 3 * time.Second
	httpTimeout        = 5 * time.Second
	maxDescriptionSize = 256 << 10
	maxSOAPSize        = 64 << 10
)

// WAN connection services that can create port mappings, most specific first.
var wanServices = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

var searchTargets = append([]string{
	"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
	"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
}, wanServices...)

// gateway is a router's port-mapping service.
type gateway struct {
	ip          net.IP // the router; every request goes to this address only
	localIP     net.IP // this machine's address on the router's network
	controlURL  string
	serviceType string
	name        string
}

// soapError is a UPnP error returned by the router.
type soapError struct {
	Code        int
	Description string
}

func (e *soapError) Error() string {
	return fmt.Sprintf("router refused the request: %s (UPnP error %d)", e.Description, e.Code)
}

func isSOAPError(err error, code int) bool {
	var se *soapError
	return errors.As(err, &se) && se.Code == code
}

const (
	errNotAuthorized       = 606
	errIndexInvalid        = 713
	errNoSuchEntry         = 714
	errConflictInMapping   = 718
	errOnlyPermanentLeases = 725
	errConflictWithOthers  = 729 // the port is used by something that is not a UPnP mapping
)

// defaultGateway returns the IPv4 default gateway from the kernel routing table.
func defaultGateway() (net.IP, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	return parseDefaultGateway(string(data))
}

func parseDefaultGateway(routes string) (net.IP, error) {
	for _, line := range strings.Split(routes, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x2 == 0 { // RTF_GATEWAY
			continue
		}
		raw, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil {
			continue
		}
		// The kernel prints the address in host byte order of a little-endian load.
		return net.IPv4(byte(raw), byte(raw>>8), byte(raw>>16), byte(raw>>24)), nil
	}
	return nil, errors.New("no default gateway found")
}

// localAddressFor returns the local address the kernel uses to reach ip.
func localAddressFor(ip net.IP) (net.IP, error) {
	conn, err := net.Dial("udp4", net.JoinHostPort(ip.String(), "1900"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP, nil
}

// discover finds the port-mapping service of the default gateway.
func discover() (*gateway, error) {
	gw, err := defaultGateway()
	if err != nil {
		return nil, err
	}
	local, err := localAddressFor(gw)
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Ask the gateway directly, and also the multicast group for routers that
	// only answer there. Either way only the gateway's answer is accepted.
	targets := []*net.UDPAddr{
		{IP: gw, Port: 1900},
		{IP: net.IPv4(239, 255, 255, 250), Port: 1900},
	}
	for _, st := range searchTargets {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
		for _, to := range targets {
			conn.WriteToUDP([]byte(msg), to)
		}
	}

	conn.SetReadDeadline(time.Now().Add(discoveryTimeout))
	buf := make([]byte, 4096)
	var lastErr error
	tried := map[string]bool{}
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline reached
		}
		if !from.IP.Equal(gw) {
			continue
		}
		location, err := parseSSDPLocation(buf[:n])
		if err != nil || tried[location] {
			continue
		}
		tried[location] = true

		g, err := loadGateway(location, gw)
		if err != nil {
			lastErr = err
			continue
		}
		g.localIP = local
		return g, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("the router (%s) did not answer UPnP discovery; UPnP may be turned off on it", gw)
}

// parseSSDPLocation extracts the description URL from an SSDP search response.
func parseSSDPLocation(packet []byte) (string, error) {
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(packet)), nil)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected SSDP status %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return "", errors.New("SSDP response without a location")
	}
	return location, nil
}

// onGateway checks that rawURL is a plain HTTP URL served by the gateway itself.
func onGateway(rawURL string, gw net.IP) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	host := net.ParseIP(u.Hostname())
	if host == nil || !host.Equal(gw) {
		return nil, fmt.Errorf("URL %q does not point to the router (%s)", rawURL, gw)
	}
	return u, nil
}

var httpClient = &http.Client{
	Timeout: httpTimeout,
	// A redirect could send the request, or the reply's trust, somewhere else.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type xmlDescription struct {
	URLBase string    `xml:"URLBase"`
	Device  xmlDevice `xml:"device"`
}

type xmlDevice struct {
	FriendlyName string       `xml:"friendlyName"`
	ModelName    string       `xml:"modelName"`
	Services     []xmlService `xml:"serviceList>service"`
	Devices      []xmlDevice  `xml:"deviceList>device"`
}

type xmlService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

func (d xmlDevice) findService(serviceType string) *xmlService {
	for i := range d.Services {
		if strings.TrimSpace(d.Services[i].ServiceType) == serviceType {
			return &d.Services[i]
		}
	}
	for _, child := range d.Devices {
		if s := child.findService(serviceType); s != nil {
			return s
		}
	}
	return nil
}

// loadGateway reads the device description at location and returns the
// port-mapping service it declares.
func loadGateway(location string, gw net.IP) (*gateway, error) {
	locURL, err := onGateway(location, gw)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Get(locURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("router description returned HTTP %d", resp.StatusCode)
	}

	var desc xmlDescription
	if err := xml.NewDecoder(io.LimitReader(resp.Body, maxDescriptionSize)).Decode(&desc); err != nil {
		return nil, fmt.Errorf("unreadable router description: %w", err)
	}

	for _, serviceType := range wanServices {
		service := desc.Device.findService(serviceType)
		if service == nil {
			continue
		}
		base := locURL
		if strings.TrimSpace(desc.URLBase) != "" {
			if b, err := onGateway(strings.TrimSpace(desc.URLBase), gw); err == nil {
				base = b
			}
		}
		ref, err := url.Parse(strings.TrimSpace(service.ControlURL))
		if err != nil {
			return nil, err
		}
		control, err := onGateway(base.ResolveReference(ref).String(), gw)
		if err != nil {
			return nil, err
		}

		name := strings.TrimSpace(desc.Device.FriendlyName)
		if name == "" {
			name = strings.TrimSpace(desc.Device.ModelName)
		}
		return &gateway{ip: gw, controlURL: control.String(), serviceType: serviceType, name: name}, nil
	}
	return nil, errors.New("the router does not offer a port-forwarding service over UPnP")
}

type soapArg struct{ name, value string }

// soap calls action on the gateway and returns the response's values by name.
func (g *gateway) soap(action string, args ...soapArg) (map[string]string, error) {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + ` xmlns:u="` + g.serviceType + `">`)
	for _, a := range args {
		body.WriteString("<" + a.name + ">")
		xml.EscapeText(&body, []byte(a.value))
		body.WriteString("</" + a.name + ">")
	}
	body.WriteString(`</u:` + action + `></s:Body></s:Envelope>`)

	req, err := http.NewRequest("POST", g.controlURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+g.serviceType+"#"+action+`"`)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSOAPSize))
	if err != nil {
		return nil, err
	}
	values := leafValues(data)

	if resp.StatusCode != http.StatusOK {
		if code, err := strconv.Atoi(values["errorCode"]); err == nil {
			return nil, &soapError{Code: code, Description: values["errorDescription"]}
		}
		return nil, fmt.Errorf("router returned HTTP %d for %s", resp.StatusCode, action)
	}
	return values, nil
}

// leafValues returns the text of every childless element in doc, by local
// name. SOAP replies and faults here are flat lists of such elements.
func leafValues(doc []byte) map[string]string {
	values := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var name string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return values
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name = t.Name.Local
			text.Reset()
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if name == t.Name.Local {
				values[name] = strings.TrimSpace(text.String())
			}
			name = ""
		}
	}
}

// externalIP returns the router's public address.
func (g *gateway) externalIP() (string, error) {
	values, err := g.soap("GetExternalIPAddress")
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(values["NewExternalIPAddress"])
	if ip == nil {
		return "", errors.New("the router did not report an external address")
	}
	return ip.String(), nil
}

// entry is one port forward as the router reports it. The texts come from
// the router, and the description from whichever device made the mapping:
// they are cleaned and never trusted.
type entry struct {
	Protocol       string
	ExternalPort   int
	InternalClient string
	InternalPort   int
	Description    string
	Lease          int // seconds left, 0 for a permanent one
}

const (
	maxListed     = 128 // entries read from the router's table
	maxRouterText = 64  // characters kept of a text the router sent
	listBudget    = 10 * time.Second
)

// cleanText makes a text from the router safe to keep and show: no control
// or formatting characters, bounded.
func cleanText(text string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(text) {
		if n >= max {
			break
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0xfeff {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func entryFrom(values map[string]string, protocol string, externalPort int) entry {
	e := entry{Protocol: protocol, ExternalPort: externalPort}
	if p := strings.ToUpper(strings.TrimSpace(values["NewProtocol"])); p == "TCP" || p == "UDP" {
		e.Protocol = p
	}
	if port, err := strconv.Atoi(strings.TrimSpace(values["NewExternalPort"])); err == nil && port > 0 && port < 65536 {
		e.ExternalPort = port
	}
	if port, err := strconv.Atoi(strings.TrimSpace(values["NewInternalPort"])); err == nil && port > 0 && port < 65536 {
		e.InternalPort = port
	}
	if ip := net.ParseIP(strings.TrimSpace(values["NewInternalClient"])); ip != nil {
		e.InternalClient = ip.String()
	} else {
		e.InternalClient = cleanText(values["NewInternalClient"], maxRouterText)
	}
	e.Description = cleanText(values["NewPortMappingDescription"], maxRouterText)
	if lease, err := strconv.Atoi(strings.TrimSpace(values["NewLeaseDuration"])); err == nil && lease > 0 {
		e.Lease = lease
	}
	return e
}

// lookup returns the forward of an external port, or nil when the router
// forwards nothing there. An error means the router could not be asked (not
// every router implements the lookup).
func (g *gateway) lookup(protocol string, externalPort int) (*entry, error) {
	values, err := g.soap("GetSpecificPortMappingEntry",
		soapArg{"NewRemoteHost", ""},
		soapArg{"NewExternalPort", strconv.Itoa(externalPort)},
		soapArg{"NewProtocol", protocol},
	)
	if isSOAPError(err, errNoSuchEntry) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e := entryFrom(values, protocol, externalPort)
	e.Protocol, e.ExternalPort = protocol, externalPort
	return &e, nil
}

// list reads the router's table of forwards, as far as it lets it be read:
// at most maxListed entries within listBudget. listed is false when the
// router does not answer the question at all; truncated when there may be
// more than was read.
func (g *gateway) list() (entries []entry, listed bool, truncated bool) {
	deadline := time.Now().Add(listBudget)
	seen := map[string]bool{}
	for i := 0; ; i++ {
		if i >= maxListed || time.Now().After(deadline) {
			return entries, true, true
		}
		values, err := g.soap("GetGenericPortMappingEntry", soapArg{"NewPortMappingIndex", strconv.Itoa(i)})
		if isSOAPError(err, errIndexInvalid) || isSOAPError(err, errNoSuchEntry) {
			return entries, true, false
		}
		if err != nil {
			// a router that does not implement the listing, or stopped answering
			return entries, i > 0, i > 0
		}
		e := entryFrom(values, "", 0)
		if e.Protocol == "" || e.ExternalPort == 0 {
			continue
		}
		key := e.Protocol + "/" + strconv.Itoa(e.ExternalPort)
		if seen[key] {
			// some routers answer every index with the same entry
			return entries, true, true
		}
		seen[key] = true
		entries = append(entries, e)
	}
}

// add asks for a forward, leased; a router that only knows permanent ones
// gets a permanent one (IGDv2 routers turn that into seven days).
func (g *gateway) add(protocol string, externalPort, internalPort int, description string, lease int) error {
	request := func(lease int) error {
		_, err := g.soap("AddPortMapping",
			soapArg{"NewRemoteHost", ""},
			soapArg{"NewExternalPort", strconv.Itoa(externalPort)},
			soapArg{"NewProtocol", protocol},
			soapArg{"NewInternalPort", strconv.Itoa(internalPort)},
			soapArg{"NewInternalClient", g.localIP.String()},
			soapArg{"NewEnabled", "1"},
			soapArg{"NewPortMappingDescription", description},
			soapArg{"NewLeaseDuration", strconv.Itoa(lease)},
		)
		return err
	}
	err := request(lease)
	if isSOAPError(err, errOnlyPermanentLeases) {
		err = request(0)
	}
	return err
}

// conflictError says an external port is held by something that is not
// Cosmos'. Holder is the mapping in the way when the router shows it; Code
// the router's refusal when it only refused.
type conflictError struct {
	Port   int
	Holder *entry
	Code   int
}

func (e *conflictError) Error() string {
	if e.Holder != nil {
		return fmt.Sprintf("external port %d is already forwarded to %s", e.Port, describeHolder(e.Holder))
	}
	return fmt.Sprintf("external port %d is already in use on the router", e.Port)
}

// describeHolder names who holds a port: the address, its port and what the
// mapping calls itself.
func describeHolder(e *entry) string {
	holder := e.InternalClient
	if holder == "" {
		holder = "another device"
	}
	if e.InternalPort != 0 {
		holder += ":" + strconv.Itoa(e.InternalPort)
	}
	if e.Description != "" {
		holder += " (" + e.Description + ")"
	}
	return holder
}

// The description of every mapping Cosmos creates starts with this. With the
// internal client being this machine it marks a mapping as Cosmos' own.
const descriptionPrefix = "Cosmos "

func isOurDescription(description string) bool {
	return strings.HasPrefix(description, descriptionPrefix) || description == strings.TrimSpace(descriptionPrefix)
}

// claim forwards externalPort to internalPort on this machine, or renews
// that forward. It never replaces a mapping that is not Cosmos': an entry on
// that external port is only written over when it already leads to the same
// port of this machine (it is what is wanted), or when it is Cosmos' own
// (known: created by this process; or by its description) pointing at an
// older internal port. Anything else is a *conflictError.
func (g *gateway) claim(protocol string, externalPort, internalPort int, description string, lease int, known bool) error {
	_, err := g.claimOrRenew(protocol, externalPort, internalPort, description, lease, known)
	return err
}

// claimOrRenew is claim, and says whether the forward was there already
// (then nothing new was opened: it was renewed).
func (g *gateway) claimOrRenew(protocol string, externalPort, internalPort int, description string, lease int, known bool) (renewed bool, err error) {
	local := g.localIP.String()
	// Not every router implements the lookup; a failure there is not fatal,
	// the router still rejects another device's port on its own.
	existing, lookupErr := g.lookup(protocol, externalPort)
	if lookupErr == nil && existing != nil {
		here := existing.InternalClient == local
		switch {
		case here && existing.InternalPort == internalPort:
			renewed = true
		case here && (known || isOurDescription(existing.Description)):
		default:
			return false, &conflictError{Port: externalPort, Holder: existing}
		}
	}

	err = g.add(protocol, externalPort, internalPort, description, lease)
	if isSOAPError(err, errConflictInMapping) {
		return false, &conflictError{Port: externalPort, Code: errConflictInMapping}
	}
	if isSOAPError(err, errConflictWithOthers) {
		return false, &conflictError{Port: externalPort, Code: errConflictWithOthers}
	}
	if err != nil {
		return false, err
	}

	// A router that says "done" and then shows the port going elsewhere did
	// not do it (a forward made by hand that wins over UPnP's).
	if lookupErr == nil && existing == nil {
		if now, err := g.lookup(protocol, externalPort); err == nil && now != nil && (now.InternalClient != local || now.InternalPort != internalPort) {
			return false, &conflictError{Port: externalPort, Holder: now}
		}
	}
	return renewed, nil
}

// release deletes the forward of externalPort when it is Cosmos' own: it
// leads to this machine and either carries Cosmos' description or is known
// (created by this process) and still leads to internalPort. It reports
// whether something was deleted. A mapping whose owner cannot be told is
// left alone: its lease ends it.
func (g *gateway) release(protocol string, externalPort, internalPort int, known bool) (bool, error) {
	existing, err := g.lookup(protocol, externalPort)
	if err == nil {
		if existing == nil || existing.InternalClient != g.localIP.String() {
			return false, nil // not forwarded, or not ours to remove
		}
		if !isOurDescription(existing.Description) && !(known && existing.InternalPort == internalPort) {
			return false, nil // another program on this machine made it
		}
	} else if !known {
		return false, nil
	}
	_, err = g.soap("DeletePortMapping",
		soapArg{"NewRemoteHost", ""},
		soapArg{"NewExternalPort", strconv.Itoa(externalPort)},
		soapArg{"NewProtocol", protocol},
	)
	if isSOAPError(err, errNoSuchEntry) {
		return false, nil
	}
	return err == nil, err
}
