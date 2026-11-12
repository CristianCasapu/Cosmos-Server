package utils

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// fakeSMTP accepts one plain SMTP session and rejects recipients at
// "rejected.test". It returns the address to dial and a channel that yields
// the message body once a message has been accepted.
func fakeSMTP(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	received := make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(line string) { conn.Write([]byte(line + "\r\n")) }

		say("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM"):
				say("250 ok")
			case strings.HasPrefix(cmd, "RCPT TO"):
				if strings.Contains(cmd, "REJECTED.TEST") {
					say("550 no such user")
				} else {
					say("250 ok")
				}
			case cmd == "DATA":
				say("354 go ahead")
				var body strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					body.WriteString(l)
				}
				received <- body.String()
				say("250 queued")
			case cmd == "QUIT":
				say("221 bye")
				return
			default:
				say("502 not implemented")
			}
		}
	}()

	return ln.Addr().String(), received
}

func configFor(t *testing.T, addr string) EmailConfig {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return EmailConfig{Host: host, Port: port, From: "cosmos@example.test"}
}

func stepNames(steps []EmailStep) string {
	names := []string{}
	for _, s := range steps {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

func TestDeliverEmailReportsEveryStep(t *testing.T) {
	addr, received := fakeSMTP(t)

	steps, err := DeliverEmail(configFor(t, addr), "cosmos@example.test",
		[]string{"admin@example.test"}, []byte("Subject: hi\r\n\r\nhello\r\n"))
	if err != nil {
		t.Fatalf("delivery failed: %v (steps: %+v)", err, steps)
	}
	if got := stepNames(steps); got != "connect,sender,recipient,send" {
		t.Errorf("steps = %s", got)
	}
	for _, s := range steps {
		if !s.OK {
			t.Errorf("step %s reported as failed: %s", s.Name, s.Detail)
		}
	}
	if body := <-received; !strings.Contains(body, "hello") {
		t.Errorf("server received %q", body)
	}
}

func TestDeliverEmailStopsAtRejectedRecipient(t *testing.T) {
	addr, _ := fakeSMTP(t)

	steps, err := DeliverEmail(configFor(t, addr), "cosmos@example.test",
		[]string{"nobody@rejected.test"}, []byte("Subject: hi\r\n\r\nhello\r\n"))
	if err == nil {
		t.Fatal("expected the rejected recipient to fail the delivery")
	}
	last := steps[len(steps)-1]
	if last.Name != "recipient" || last.OK {
		t.Errorf("last step = %+v, want a failed recipient step", last)
	}
	if !strings.Contains(last.Detail, "no such user") {
		t.Errorf("failure detail %q does not carry the server's reply", last.Detail)
	}
}

func TestDeliverEmailRequiresStartTLSWhenTLSIsOn(t *testing.T) {
	addr, _ := fakeSMTP(t) // offers no STARTTLS
	cfg := configFor(t, addr)
	cfg.UseTLS = true

	steps, err := DeliverEmail(cfg, cfg.From, []string{"admin@example.test"}, []byte("x"))
	if err == nil {
		t.Fatal("expected delivery to refuse a server without STARTTLS")
	}
	last := steps[len(steps)-1]
	if last.Name != "tls" || last.OK {
		t.Errorf("last step = %+v, want a failed tls step", last)
	}
}

func TestUsesImplicitTLS(t *testing.T) {
	cases := []struct {
		port   string
		useTLS bool
		want   bool
	}{
		{"465", true, true},
		{" 465 ", true, true},
		{"465", false, false},
		{"587", true, false},
		{"25", false, false},
	}
	for _, c := range cases {
		got := usesImplicitTLS(EmailConfig{Port: c.port, UseTLS: c.useTLS})
		if got != c.want {
			t.Errorf("port %q, UseTLS %v: got %v, want %v", c.port, c.useTLS, got, c.want)
		}
	}
}
