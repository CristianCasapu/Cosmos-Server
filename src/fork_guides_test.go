package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition (plan 50): a name's certificate is "Cosmos' own" only when
// Cosmos' authority signed it: not a foreign one, not when there is none.
func TestForkHostOwnCA(t *testing.T) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Cosmos CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	leafTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "s3.local"}, DNSNames: []string{"s3.local"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, &leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	own := &tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
	foreignPub, foreignPriv := testCertPEM(t, "app.example.org")
	foreign, err := tls.X509KeyPair([]byte(foreignPub), []byte(foreignPriv))
	if err != nil {
		t.Fatal(err)
	}

	config := utils.Config{}
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["LETSENCRYPT"]
	config.HTTPConfig.CACert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	config.HTTPConfig.CAPrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}))
	utils.LoadBaseMainConfig(config)
	primaryCert, secondaryCert = &foreign, own
	t.Cleanup(func() {
		primaryCert, secondaryCert = nil, nil
		utils.LoadBaseMainConfig(utils.Config{})
	})

	if !forkHostOwnCA("s3.local") {
		t.Error("a .local name served with a certificate of Cosmos' authority is not seen as such")
	}
	if forkHostOwnCA("app.example.org") {
		t.Error("a name served with a foreign certificate is seen as Cosmos' own")
	}
	if forkHostOwnCA("") {
		t.Error("no name")
	}
	// a server whose certificates are all self-signed: every name is Cosmos' own
	selfSigned := config
	selfSigned.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["SELFSIGNED"]
	utils.LoadBaseMainConfig(selfSigned)
	if !forkHostOwnCA("app.example.org") {
		t.Error("a public name served self-signed is Cosmos' own")
	}
	utils.LoadBaseMainConfig(config)
	primaryCert, secondaryCert = nil, nil
	if forkHostOwnCA("app.example.org") || forkHostOwnCA("s3.local") {
		t.Error("no certificate at all is not Cosmos' own")
	}
	primaryCert, secondaryCert = &foreign, own
	utils.LoadBaseMainConfig(utils.Config{})
	if forkHostOwnCA("s3.local") {
		t.Error("without an authority nothing is Cosmos' own")
	}
}
