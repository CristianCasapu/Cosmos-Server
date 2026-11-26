package utils

// Fork addition (plan 47): what the connection test may and may not take
// from elsewhere, and what it repeats of the other end (review, finding 4).

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostgresTestTakesNothingFromTheEnvironment(t *testing.T) {
	pass := filepath.Join(t.TempDir(), "pgpass")
	os.WriteFile(pass, []byte("*:*:*:*:from-passfile\n"), 0o600)
	t.Setenv("PGPASSFILE", pass)
	t.Setenv("PGPASSWORD", "from-environment")
	t.Setenv("PGHOST", "192.0.2.99")
	t.Setenv("PGPORT", "6000")
	t.Setenv("PGUSER", "intruder")
	t.Setenv("PGDATABASE", "elsewhere")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGOPTIONS", "-c search_path=evil")
	db := DatabaseConfig{PostgresHost: "127.0.0.1:5433", PostgresUsername: "cosmos"}
	cfg, err := postgresCheckConfig(db)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "" || cfg.User != "cosmos" || cfg.Host != "127.0.0.1" || cfg.Port != 5433 || cfg.Database != "cosmos" {
		t.Errorf("taken from elsewhere: password %q user %q host %q port %d database %q", cfg.Password, cfg.User, cfg.Host, cfg.Port, cfg.Database)
	}
	// plan 51: only the store's own bounds, nothing from PGOPTIONS
	if len(cfg.RuntimeParams) != 2 || cfg.RuntimeParams["statement_timeout"] != "30000" || cfg.RuntimeParams["idle_in_transaction_session_timeout"] != "60000" {
		t.Errorf("runtime parameters: %v", cfg.RuntimeParams)
	}
	if cfg.ConnectTimeout != storeConnectTimeout || cfg.DialFunc == nil {
		t.Errorf("unbounded: %v", cfg.ConnectTimeout)
	}
	for _, fallback := range cfg.Fallbacks {
		if fallback.Host != "127.0.0.1" || fallback.Port != 5433 {
			t.Errorf("a fallback elsewhere: %+v", fallback)
		}
	}
	if cfg.TLSConfig == nil {
		t.Error("PGSSLMODE=disable was taken: the test asks for TLS first, as the store does")
	}
}

// fakeServer answers one connection with what speak writes.
func fakeServer(t *testing.T, speak func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				speak(conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func TestPostgresTestRepeatsNothingOfAnotherService(t *testing.T) {
	banner := "SSH-2.0-OpenSSH_9.6 secret-banner-of-the-service\r\n"
	addr := fakeServer(t, func(conn net.Conn) {
		io.WriteString(conn, banner)
		io.Copy(io.Discard, conn)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	problem := CheckPostgres(ctx, DatabaseConfig{PostgresHost: addr, PostgresUsername: "cosmos"})
	if problem == nil {
		t.Fatal("an SSH server passed as a database")
	}
	if strings.Contains(problem.Message, "SSH") || strings.Contains(problem.Message, "secret") {
		t.Errorf("the other service's bytes came back: %q", problem.Message)
	}

	// a server that answers like Postgres with an error code that is not one
	addr = fakeServer(t, func(conn net.Conn) {
		head := make([]byte, 8)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		if binary.BigEndian.Uint32(head[4:]) == 80877103 { // SSLRequest: no TLS here
			conn.Write([]byte{'N'})
			if _, err := io.ReadFull(conn, head[:4]); err != nil {
				return
			}
			rest := make([]byte, binary.BigEndian.Uint32(head[:4])-4)
			io.ReadFull(conn, rest)
		} else {
			rest := make([]byte, binary.BigEndian.Uint32(head[:4])-8)
			io.ReadFull(conn, rest)
		}
		fields := "SFATAL\x00VFATAL\x00C<b>99\x00Mread my <b>banner</b>\x00\x00"
		message := make([]byte, 5)
		message[0] = 'E'
		binary.BigEndian.PutUint32(message[1:], uint32(4+len(fields)))
		conn.Write(append(message, fields...))
	})
	problem = CheckPostgres(ctx, DatabaseConfig{PostgresHost: addr, PostgresUsername: "cosmos"})
	if problem == nil || strings.Contains(problem.Message, "<b>") || strings.Contains(problem.Message, "banner") {
		t.Errorf("the server's own text came back: %+v", problem)
	}
}

func TestAHostWithAnEmptyPortIsRefusedLikeThePanelDoes(t *testing.T) {
	for _, host := range []string{"db:", "[2001:db8::1]:"} {
		if PostgresFieldsProblem(DatabaseConfig{PostgresHost: host}) == "" {
			t.Errorf("%q accepted", host)
		}
	}
}
