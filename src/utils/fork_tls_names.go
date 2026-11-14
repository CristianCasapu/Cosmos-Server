package utils

// Fork addition: what the TLS listener needs to decide, per handshake and
// before any cryptography, whether a name is one this server answers for
// (src/fork_tls_names.go): the class of a name, from an index rebuilt only
// when the configuration is replaced, and Cosmos' own certificate authority,
// with which a certificate for exactly one name can be made.

import (
	"crypto"
	"crypto/x509"
	"net"
	"strings"
	"sync"
)

// TLSNameClass is what a name sent in a handshake (or as a request's Host)
// is to this server.
type TLSNameClass int

const (
	// TLSNameNone: no name, or an address
	TLSNameNone TLSNameClass = iota
	// TLSNameUnknown: a name this server does not serve
	TLSNameUnknown
	// TLSNameLocalOnly: localhost, a .local name, a name under an active
	// home suffix: answered on the local network only
	TLSNameLocalOnly
	// TLSNameShielded: served through Cloudflare's proxy only
	TLSNameShielded
	// TLSNameDirect: served, to anyone
	TLSNameDirect
)

// TLSNameIndex is the names of one configuration, sorted by class.
type TLSNameIndex struct {
	// AnyName: the server answers every name (a new install, or the panel's
	// hostname 0.0.0.0)
	AnyName bool
	// AcceptAll: the admin chose to accept every hostname
	AcceptAll bool
	// AnyHostPorts are the ports of routes that answer any host (":8443")
	AnyHostPorts map[string]bool
	// PublicIPs are the public addresses among the served names (a server
	// reached by its address, such as a panel at https://203.0.113.5)
	PublicIPs []net.IP

	direct       map[string]bool
	shielded     map[string]bool
	homeSuffixes []string
}

// BuildTLSNameIndex sorts the hostnames a configuration serves (as
// GetAllHostnames(false) lists them) by class.
func BuildTLSNameIndex(config Config, hostnames []string) *TLSNameIndex {
	index := &TLSNameIndex{
		AnyName:      config.NewInstall || zoneHost(config.HTTPConfig.Hostname) == "0.0.0.0",
		AcceptAll:    config.HTTPConfig.AcceptAllInsecureHostname,
		AnyHostPorts: map[string]bool{},
		direct:       map[string]bool{},
		shielded:     map[string]bool{},
	}
	for _, name := range config.HTTPConfig.Cloudflare.Shielded {
		if name = CloudflareHostname(name); name != "" {
			index.shielded[name] = true
		}
	}
	for _, zone := range ActiveHomeZones(config) {
		index.homeSuffixes = append(index.homeSuffixes, "."+zone.Suffix)
	}
	for _, hostname := range hostnames {
		hostname = strings.ToLower(strings.TrimSpace(hostname))
		host, port := hostname, ""
		if h, p, err := net.SplitHostPort(hostname); err == nil {
			host, port = h, p
		}
		host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
		if host == "" || host == "0.0.0.0" || host == "::" {
			if port != "" {
				index.AnyHostPorts[port] = true
			}
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			if !IsLocalIP(ip.String()) && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
				index.PublicIPs = append(index.PublicIPs, ip)
			}
			continue
		}
		index.direct[host] = true
	}
	return index
}

// tlsName brings a name to the form the index holds: lower case, no port,
// no brackets, no trailing dot.
func tlsName(name string) string {
	name = CloudflareHostname(name)
	return strings.TrimSuffix(strings.Trim(name, "[]"), ".")
}

// IsHome reports whether a name is under an active home suffix.
func (index *TLSNameIndex) IsHome(name string) bool {
	name = tlsName(name)
	for _, suffix := range index.homeSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// Class says what a name is to this server.
func (index *TLSNameIndex) Class(name string) TLSNameClass {
	name = tlsName(name)
	if name == "" || net.ParseIP(name) != nil {
		return TLSNameNone
	}
	if name == "localhost" || strings.HasSuffix(name, ".localhost") || strings.HasSuffix(name, ".local") || index.IsHome(name) {
		return TLSNameLocalOnly
	}
	if index.shielded[name] {
		return TLSNameShielded
	}
	if index.direct[name] || index.AcceptAll {
		return TLSNameDirect
	}
	return TLSNameUnknown
}

var tlsNameCache struct {
	sync.Mutex
	config *Config
	index  *TLSNameIndex
}

// CurrentTLSNames is the index of the configuration in force, built again
// only when the configuration was replaced.
func CurrentTLSNames() *TLSNameIndex {
	current := mainConfig.Load()
	if current == nil {
		return BuildTLSNameIndex(Config{NewInstall: true}, nil)
	}
	tlsNameCache.Lock()
	cached, index := tlsNameCache.config, tlsNameCache.index
	tlsNameCache.Unlock()
	if cached == current && index != nil {
		return index
	}
	index = BuildTLSNameIndex(*current, GetAllHostnames(false))
	tlsNameCache.Lock()
	tlsNameCache.config, tlsNameCache.index = current, index
	tlsNameCache.Unlock()
	return index
}

var cosmosCACache struct {
	sync.Mutex
	pem  string
	cert *x509.Certificate
	key  crypto.Signer
}

// ForkCosmosCA returns Cosmos' own certificate authority (the one that signs
// the self-signed certificate), nil when there is none that can be used.
func ForkCosmosCA() (*x509.Certificate, crypto.Signer) {
	http := GetMainConfig().HTTPConfig
	key := http.CACert + "\x00" + http.CAPrivateKey
	cosmosCACache.Lock()
	defer cosmosCACache.Unlock()
	if cosmosCACache.pem == key {
		return cosmosCACache.cert, cosmosCACache.key
	}
	cosmosCACache.pem, cosmosCACache.cert, cosmosCACache.key = key, nil, nil
	ca, err := loadCA()
	if err == nil && ca != nil && ca.Certificate != nil && ca.PrivateKey != nil && ca.Certificate.IsCA {
		cosmosCACache.cert, cosmosCACache.key = ca.Certificate, ca.PrivateKey
	}
	return cosmosCACache.cert, cosmosCACache.key
}
