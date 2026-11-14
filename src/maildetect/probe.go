package maildetect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"net/smtp"
	"strings"
	"time"
)

// Outcomes of checking a candidate.
const (
	CheckOK         = "ok"          // SMTP answered, encrypted as proposed
	CheckNoAnswer   = "no-answer"   // name not found, or nothing accepted the connection in time
	CheckNotSMTP    = "not-smtp"    // something answered that is not a mail server
	CheckNoStartTLS = "no-starttls" // a mail server without STARTTLS on this port
	CheckTLSFailed  = "tls-failed"  // the TLS handshake failed
	CheckLocal      = "local"       // the name points to this server or to an address that is never contacted
	CheckNotChecked = "not-checked" // unusual port, or out of time
)

// What the certificate of a checked server is worth.
const (
	CertOK        = "ok"
	CertUntrusted = "untrusted" // self-signed or from an unknown authority
	CertName      = "name"      // issued for another name
	CertExpired   = "expired"
)

// the most a single check reads from a server
const maxProbeBytes = 16 << 10

// the sign-in methods reported; anything else a server names is dropped
var knownAuth = map[string]bool{
	"PLAIN": true, "LOGIN": true, "XOAUTH2": true, "OAUTHBEARER": true,
	"CRAM-MD5": true, "DIGEST-MD5": true, "NTLM": true, "GSSAPI": true,
	"SCRAM-SHA-1": true, "SCRAM-SHA-256": true,
}

type probeResult struct {
	check string
	cert  string
	auth  []string
}

// cappedConn fails once more than its allowance has been read, so that a
// server cannot make a check read without end.
type cappedConn struct {
	net.Conn
	left int
}

func (c *cappedConn) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("maildetect: the server sent too much")
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	n, err := c.Conn.Read(p)
	c.left -= n
	return n, err
}

// probe checks one candidate: connect to addr (already validated), TLS
// handshake where the candidate says so, greeting, EHLO, and QUIT. Nothing
// else is ever sent: there are no credentials here to send.
func (d *Detector) probe(ctx context.Context, host string, addr netip.Addr, port int, security string) probeResult {
	ctx, cancel := context.WithTimeout(ctx, d.ProbeTimeout)
	defer cancel()

	raw, err := d.Dial(ctx, "tcp", netip.AddrPortFrom(addr, uint16(port)).String())
	if err != nil {
		return probeResult{check: CheckNoAnswer}
	}
	defer raw.Close()
	if deadline, ok := ctx.Deadline(); ok {
		raw.SetDeadline(deadline)
	} else {
		raw.SetDeadline(time.Now().Add(d.ProbeTimeout))
	}
	conn := &cappedConn{Conn: raw, left: maxProbeBytes}

	// The handshake accepts any certificate: nothing secret travels on this
	// connection, and the form wants to know what is wrong with it. The
	// certificate is verified right after, by certState.
	tlsConfig := &tls.Config{ServerName: host, InsecureSkipVerify: true}
	result := probeResult{}

	var client *smtp.Client
	if security == "tls" {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return probeResult{check: CheckTLSFailed}
		}
		result.cert = d.certState(secure.ConnectionState(), host)
		if client, err = smtp.NewClient(secure, host); err != nil {
			return probeResult{check: CheckNotSMTP, cert: result.cert}
		}
	} else if client, err = smtp.NewClient(conn, host); err != nil {
		return probeResult{check: CheckNotSMTP}
	}
	defer client.Close()

	if err := client.Hello("localhost"); err != nil {
		result.check = CheckNotSMTP
		return result
	}
	if security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			result.check = CheckNoStartTLS
			return result
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			result.check = CheckTLSFailed
			return result
		}
		if state, ok := client.TLSConnectionState(); ok {
			result.cert = d.certState(state, host)
		}
	}
	if ok, methods := client.Extension("AUTH"); ok {
		for _, method := range strings.Fields(strings.ToUpper(methods)) {
			if knownAuth[method] && len(result.auth) < len(knownAuth) {
				result.auth = append(result.auth, method)
			}
		}
	}
	result.check = CheckOK
	client.Quit()
	return result
}

// certState verifies the certificate a server presented against its name.
func (d *Detector) certState(state tls.ConnectionState, host string) string {
	if len(state.PeerCertificates) == 0 {
		return CertUntrusted
	}
	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       host,
		Roots:         d.Roots,
		Intermediates: intermediates,
		CurrentTime:   d.now(),
	})
	if err == nil {
		return CertOK
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return CertName
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
		return CertExpired
	}
	return CertUntrusted
}
