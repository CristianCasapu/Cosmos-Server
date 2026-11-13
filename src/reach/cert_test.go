package reach

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// issue makes a certificate for names, signed by parent (self-signed when nil).
func issue(t *testing.T, names []string, notAfter time.Time, ca bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "test"},
		DNSNames:              names,
		NotBefore:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              notAfter,
		IsCA:                  ca,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	signer, signerKey := tpl, key
	if parent != nil {
		signer, signerKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key, der
}

func TestCheckCertificate(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	later := now.AddDate(0, 3, 0)
	root, rootKey, _ := issue(t, nil, later.AddDate(1, 0, 0), true, nil, nil)
	roots := x509.NewCertPool()
	roots.AddCert(root)

	_, _, good := issue(t, []string{"app.example.org"}, later, false, root, rootKey)
	_, _, wildcard := issue(t, []string{"*.example.org"}, later, false, root, rootKey)
	_, _, expired := issue(t, []string{"app.example.org"}, now.AddDate(0, 0, -1), false, root, rootKey)
	_, _, self := issue(t, []string{"app.example.org"}, later, false, nil, nil)

	cases := []struct {
		name    string
		cert    *tls.Certificate
		host    string
		valid   bool
		problem string
	}{
		{"trusted", &tls.Certificate{Certificate: [][]byte{good}}, "app.example.org", true, ""},
		{"wildcard", &tls.Certificate{Certificate: [][]byte{wildcard}}, "app.example.org", true, ""},
		{"other name", &tls.Certificate{Certificate: [][]byte{good}}, "other.example.org", false, "wrongName"},
		{"expired", &tls.Certificate{Certificate: [][]byte{expired}}, "app.example.org", false, "expired"},
		{"self-signed", &tls.Certificate{Certificate: [][]byte{self}}, "app.example.org", false, "selfSigned"},
		{"none", nil, "app.example.org", false, "none"},
		{"garbage", &tls.Certificate{Certificate: [][]byte{{1, 2, 3}}}, "app.example.org", false, "none"},
	}
	for _, c := range cases {
		got := CheckCertificate(c.cert, c.host, now, roots)
		if got.Valid != c.valid || got.Problem != c.problem {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}
