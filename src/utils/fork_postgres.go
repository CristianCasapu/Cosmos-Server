package utils

// Fork addition (plan 47, I2e): the check behind "Test the connection" of the
// monitoring database form, and the shapes its fields must have. The DSN is
// the one InitMetricsDatabase uses (postgresDSN), with a bound on the dial.
// No error returned here holds the password.

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresProblem is why a connection failed: an identifier and a sentence.
type PostgresProblem struct {
	Reason  string
	Message string
}

func (p *PostgresProblem) Error() string { return p.Message }

var (
	postgresHostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,252})$`)
	postgresIdent    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,62}$`)
)

// PostgresFieldsProblem says in words what is wrong with the shapes of the
// monitoring database's settings, "" when nothing. An empty host means the
// built-in database (SQLite): the other fields are then not looked at.
func PostgresFieldsProblem(db DatabaseConfig) string {
	host := db.PostgresHost
	if host == "" {
		return ""
	}
	name, port := host, ""
	if h, p, err := net.SplitHostPort(host); err == nil {
		name, port = h, p
		if port == "" { // "db:" (the panel refuses it too)
			return "The port of the database server is a number from 1 to 65535"
		}
	} else if strings.Count(host, ":") == 1 {
		return "The database server is a name or an address, optionally followed by :port, for example 127.0.0.1:5432"
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "The port of the database server is a number from 1 to 65535"
		}
	}
	if net.ParseIP(name) == nil && !postgresHostName.MatchString(name) {
		return "The database server is a name or an address, optionally followed by :port, for example 127.0.0.1:5432 (no http://, no path)"
	}
	if db.PostgresDatabase != "" && !postgresIdent.MatchString(db.PostgresDatabase) {
		return "The database name has at most 63 letters, digits and _ . $ -"
	}
	if db.PostgresUsername != "" && !postgresIdent.MatchString(db.PostgresUsername) {
		return "The user name has at most 63 letters, digits and _ . $ -"
	}
	if len(db.PostgresPassword) > 1024 || strings.ContainsAny(db.PostgresPassword, "\x00") {
		return "The password is at most 1024 bytes (fewer characters when some are not plain letters)"
	}
	return ""
}

// postgresCheckDSN is the DSN of the monitoring store with a bound on the dial.
func postgresCheckDSN(db DatabaseConfig, timeoutSeconds int) string {
	u, err := url.Parse(postgresDSN(db))
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("connect_timeout", strconv.Itoa(timeoutSeconds))
	q.Set("sslmode", "prefer") // what the monitoring store gets by default; never PGSSLMODE
	q.Set("passfile", "")      // never ~/.pgpass
	u.RawQuery = q.Encode()
	return u.String()
}

// CheckPostgres connects to the database the settings name and asks it one
// question. nil: it answers.
func CheckPostgres(ctx context.Context, db DatabaseConfig) *PostgresProblem {
	if problem := PostgresFieldsProblem(db); problem != "" {
		return &PostgresProblem{"settings", problem}
	}
	if db.PostgresHost == "" {
		return &PostgresProblem{"settings", "Enter the database server first"}
	}
	if db.PostgresUsername == "" {
		return &PostgresProblem{"settings", "Enter the user name to test the connection"}
	}
	cfg, err := postgresCheckConfig(db)
	if err != nil {
		return &PostgresProblem{"settings", "These settings do not make a database address"}
	}
	conn, err := pgxConnect(ctx, cfg)
	if err == nil {
		defer conn.Close(context.Background())
		err = conn.Ping(ctx)
	}
	if err == nil {
		return nil
	}
	return postgresProblemOf(err, db)
}

// postgresCheckConfig is the configuration a test connects with: the form's
// values and nothing else.
func postgresCheckConfig(db DatabaseConfig) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(postgresCheckDSN(db, 8))
	if err != nil {
		return nil, err
	}
	explicitPostgresConfig(cfg, db)
	return cfg, nil
}

// explicitPostgresConfig makes the parsed configuration hold the form's
// values only. pgx fills what a connection string leaves out from the PG*
// variables of the environment, a service file and ~/.pgpass (an empty
// password above all): none of that may decide where a test connects, as whom
// or with which password.
func explicitPostgresConfig(cfg *pgx.ConnConfig, db DatabaseConfig) {
	host, port := db.PostgresHost, uint16(5432)
	if h, p, err := net.SplitHostPort(db.PostgresHost); err == nil {
		host = h
		if n, err := strconv.Atoi(p); err == nil {
			port = uint16(n)
		}
	}
	cfg.Host, cfg.Port = host, port
	cfg.Database = postgresDatabaseName(db)
	cfg.User = db.PostgresUsername
	cfg.Password = db.PostgresPassword
	cfg.RuntimeParams = map[string]string{}
	cfg.KerberosSrvName, cfg.KerberosSpn = "", ""
	kept := cfg.Fallbacks[:0]
	for _, fallback := range cfg.Fallbacks {
		if fallback.Host == host && fallback.Port == port {
			kept = append(kept, fallback)
		}
	}
	cfg.Fallbacks = kept
}

// PostgresHostAdvice names the likely right server after a typed one failed
// for a reason (closed, dns, timeout, network), from the PostgreSQL
// containers of this server; "" when nothing better is known. Set by
// src/appsignin (plan 51), which knows the containers.
var PostgresHostAdvice = func(ctx context.Context, typed string, reason string) string { return "" }

// pgxConnect is pgx.ConnectConfig; replaced by the tests.
var pgxConnect = pgx.ConnectConfig

// postgresErrorCode is the SQLSTATE of a server's error, "" for anything else.
func postgresErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// postgresCode is what a server's error code looks like (SQLSTATE); anything
// else from the other end is not repeated.
var postgresCode = regexp.MustCompile(`^[0-9A-Z]{5}$`)

func postgresProblemOf(err error, db DatabaseConfig) *PostgresProblem {
	var pgErr *pgconn.PgError
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &pgErr):
		switch pgErr.Code {
		case "28P01":
			return &PostgresProblem{"password", "The database refused the password of user " + db.PostgresUsername + "."}
		case "28000":
			return &PostgresProblem{"user", "The database does not let user " + db.PostgresUsername + " in from this server (no such user, or not allowed by its pg_hba.conf)."}
		case "3D000":
			return &PostgresProblem{"database", "The database server has no database called " + postgresDatabaseName(db) + "."}
		}
		if !postgresCode.MatchString(pgErr.Code) {
			return &PostgresProblem{"refused", "The database server refused the connection."}
		}
		return &PostgresProblem{"refused", "The database server refused the connection (code " + pgErr.Code + ")."}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &PostgresProblem{"closed", "Nothing answers at " + db.PostgresHost + ": check the address and the port, and that the database runs."}
	case errors.As(err, &dnsErr):
		return &PostgresProblem{"dns", "The name of the database server could not be looked up."}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return &PostgresProblem{"timeout", "The database server did not answer in time."}
	}
	text := err.Error()
	if db.PostgresPassword != "" {
		text = strings.ReplaceAll(text, db.PostgresPassword, "[password]")
	}
	if strings.Contains(strings.ToLower(text), "timeout") {
		return &PostgresProblem{"timeout", "The database server did not answer in time."}
	}
	return &PostgresProblem{"network", "The database server could not be reached."}
}

func postgresDatabaseName(db DatabaseConfig) string {
	if db.PostgresDatabase == "" {
		return "cosmos"
	}
	return db.PostgresDatabase
}
