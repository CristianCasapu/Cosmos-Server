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
	errNoSuchEntry         = 714
	errConflictInMapping   = 718
	errOnlyPermanentLeases = 725
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

// mappingOwner returns the LAN address externalPort is forwarded to, or "" if
// the port is not forwarded.
func (g *gateway) mappingOwner(protocol string, externalPort int) (string, error) {
	values, err := g.soap("GetSpecificPortMappingEntry",
		soapArg{"NewRemoteHost", ""},
		soapArg{"NewExternalPort", strconv.Itoa(externalPort)},
		soapArg{"NewProtocol", protocol},
	)
	if isSOAPError(err, errNoSuchEntry) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return values["NewInternalClient"], nil
}

// addMapping forwards externalPort on the router to internalPort on this
// machine for lease seconds. It never replaces another device's mapping.
func (g *gateway) addMapping(protocol string, externalPort, internalPort int, description string, lease int) error {
	// Not every router implements the lookup; a failure there is not fatal,
	// the router still rejects a real conflict on its own.
	if owner, err := g.mappingOwner(protocol, externalPort); err == nil && owner != "" && owner != g.localIP.String() {
		return fmt.Errorf("external port %d is already forwarded to %s", externalPort, owner)
	}

	add := func(lease int) error {
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

	err := add(lease)
	if isSOAPError(err, errOnlyPermanentLeases) {
		err = add(0)
	}
	if isSOAPError(err, errConflictInMapping) {
		return fmt.Errorf("external port %d is already in use on the router", externalPort)
	}
	return err
}

// removeMapping deletes a forward of externalPort if it points to this machine.
func (g *gateway) removeMapping(protocol string, externalPort int) error {
	owner, err := g.mappingOwner(protocol, externalPort)
	if err == nil && owner != g.localIP.String() {
		return nil // not forwarded, or not ours to remove
	}
	_, err = g.soap("DeletePortMapping",
		soapArg{"NewRemoteHost", ""},
		soapArg{"NewExternalPort", strconv.Itoa(externalPort)},
		soapArg{"NewProtocol", protocol},
	)
	if isSOAPError(err, errNoSuchEntry) {
		return nil
	}
	return err
}
