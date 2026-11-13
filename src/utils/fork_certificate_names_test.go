package utils

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// Fork addition: tests of fork_certificate_names.go.

func selfSignedFor(t *testing.T, names []string) (*x509.Certificate, string) {
	t.Helper()
	prevFolder, prevConfig := CONFIGFOLDER, GetBaseMainConfig()
	CONFIGFOLDER = t.TempDir() + "/"
	LoadBaseMainConfig(Config{})
	t.Cleanup(func() {
		LoadBaseMainConfig(prevConfig)
		CONFIGFOLDER = prevFolder
	})

	certPEM, _, err := GenerateRSAWebCertificates(names)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("no certificate was made")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM
}

func TestSelfSignedCertificateIsValidForAnIPAddress(t *testing.T) {
	names := []string{"172.16.0.14", "jellyfin.local", "fd00::14", "cosmos.example.com"}
	cert, certPEM := selfSignedFor(t, names)

	// what a browser checks for https://172.16.0.14:2053
	for _, host := range []string{"172.16.0.14", "fd00::14", "jellyfin.local", "cosmos.example.com"} {
		if err := cert.VerifyHostname(host); err != nil {
			t.Errorf("the certificate is not valid for %s: %v", host, err)
		}
	}
	if err := cert.VerifyHostname("172.16.0.15"); err == nil {
		t.Error("the certificate is valid for an address it was not made for")
	}
	// and against Cosmos' own authority, as on a device that trusts it
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(GetBaseMainConfig().HTTPConfig.CACert)) {
		t.Fatal("no certificate authority was stored")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "172.16.0.14"}); err != nil {
		t.Errorf("a device that trusts Cosmos' authority does not accept the certificate for the address: %v", err)
	}

	if CertificateLacksIP(certPEM, names) {
		t.Error("a certificate made now is reported as lacking its addresses")
	}
	if !CertificateLacksIP(certPEM, append(names, "192.168.1.9")) {
		t.Error("a new address is not noticed")
	}
	if CertificateLacksIP(certPEM, []string{"only.names.example"}) || CertificateLacksIP("not a certificate", names) {
		t.Error("nothing to compare must not ask for a new certificate")
	}
}

func TestCertificateIPs(t *testing.T) {
	ips := CertificateIPs([]string{"172.16.0.14", "172.16.0.14:2053", "[fd00::14]:2053", "fd00::14", "app.local", "localhost", ""})
	if len(ips) != 4 {
		t.Fatalf("expected 4 addresses, got %v", ips)
	}
}
