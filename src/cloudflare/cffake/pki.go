package cffake

// Certificates for tests: authorities made on the spot and client
// certificates under them, the way Cloudflare's proxy would hold one for
// Authenticated Origin Pulls. Nothing here is used by the server.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
)

// Authority is a certificate authority with its key.
type Authority struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	// PEM is the authority's certificate
	PEM string
	// chain holds the certificates between this authority and its root,
	// nearest first (empty for a root)
	chain [][]byte
}

func serial() *big.Int {
	number, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		panic(err)
	}
	return number
}

func newAuthority(name string, parent *Authority) *Authority {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	signer, signerKey := template, key
	if parent != nil {
		signer, signerKey = parent.Cert, parent.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	authority := &Authority{Cert: cert, Key: key, PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
	if parent != nil {
		authority.chain = append([][]byte{der}, parent.chain...)
	}
	return authority
}

// NewAuthority makes a root authority.
func NewAuthority(name string) *Authority { return newAuthority(name, nil) }

// Intermediate makes an authority under this one. Certificates issued by it
// carry it along, as a client sends its chain.
func (a *Authority) Intermediate(name string) *Authority { return newAuthority(name, a) }

// Leaf says what kind of certificate to issue.
type Leaf struct {
	// Expired: valid until an hour ago
	Expired bool
	// ServerOnly: usable for a server, not for a client
	ServerOnly bool
	// Names for a server certificate
	Names []string
}

// Issue makes a certificate with its key under the authority: a client
// certificate unless leaf says otherwise.
func (a *Authority) Issue(leaf Leaf) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "a certificate for a test"},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:    leaf.Names,
	}
	if leaf.Expired {
		template.NotAfter = time.Now().Add(-time.Hour)
	}
	if leaf.ServerOnly || len(leaf.Names) > 0 {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.Cert, &key.PublicKey, a.Key)
	if err != nil {
		panic(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return tls.Certificate{Certificate: append([][]byte{der}, a.chain...), PrivateKey: key, Leaf: parsed}
}

// Presented is what a server sees of a certificate after the handshake: the
// certificates the client sent, parsed.
func Presented(cert tls.Certificate) []*x509.Certificate {
	out := []*x509.Certificate{}
	for _, der := range cert.Certificate {
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			panic(err)
		}
		out = append(out, parsed)
	}
	return out
}
