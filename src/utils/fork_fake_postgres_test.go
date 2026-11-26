package utils

// Fork addition (plan 51, review round 2): a PostgreSQL stand-in that speaks
// enough of the protocol for CheckPostgresStore, so that its rules are tested
// without a server: the probe table, the ownership of the store's tables,
// whether their owner is a superuser.

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

type fakePG struct {
	owner     string // owner of "notifications"; "" when the account owns everything
	super     bool
	createErr string // SQLSTATE of the probe table, "" when it may create
}

func (f fakePG) answer(b *pgproto3.Backend, sql string, formats []int16) {
	format := func(i int) int16 {
		switch {
		case len(formats) == 1:
			return formats[0]
		case i < len(formats):
			return formats[i]
		}
		return 0
	}
	trimmed := strings.TrimSpace(strings.ToLower(sql))
	switch {
	case trimmed == "" || trimmed == ";":
		b.Send(&pgproto3.EmptyQueryResponse{})
	case strings.HasPrefix(trimmed, "create table fork_cosmos_probe"):
		if f.createErr != "" {
			b.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: f.createErr, Message: "permission denied for schema public"})
			return
		}
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("CREATE TABLE")})
	case strings.Contains(trimmed, "from pg_tables"):
		if f.owner != "" {
			b.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("notifications"), []byte(f.owner)}})
			b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		} else {
			b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 0")})
		}
	case strings.Contains(trimmed, "rolsuper"):
		v := []byte("f")
		if f.super {
			v = []byte("t")
		}
		if format(0) == 1 {
			v = []byte{0}
			if f.super {
				v = []byte{1}
			}
		}
		b.Send(&pgproto3.DataRow{Values: [][]byte{v}})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
	default: // begin, rollback, set
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte(strings.ToUpper(strings.Fields(trimmed + " x")[0]))})
	}
}

// describe: the parameters and the columns of a statement.
func (f fakePG) describe(b *pgproto3.Backend, sql string) {
	lower := strings.ToLower(sql)
	switch {
	case strings.Contains(lower, "from pg_tables"):
		b.Send(&pgproto3.ParameterDescription{ParameterOIDs: []uint32{1009}})
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{
			{Name: []byte("tablename"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1},
			{Name: []byte("tableowner"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1}}})
	case strings.Contains(lower, "rolsuper"):
		b.Send(&pgproto3.ParameterDescription{ParameterOIDs: []uint32{25}})
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("rolsuper"), DataTypeOID: 16, DataTypeSize: 1, TypeModifier: -1}}})
	default:
		b.Send(&pgproto3.ParameterDescription{})
		b.Send(&pgproto3.NoData{})
	}
}

func (f fakePG) serve(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.session(conn)
		}
	}()
	return ln.Addr().String()
}

func (f fakePG) session(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	b := pgproto3.NewBackend(conn, conn)
	for {
		msg, err := b.ReceiveStartupMessage()
		if err != nil {
			return
		}
		if _, ok := msg.(*pgproto3.SSLRequest); ok {
			conn.Write([]byte{'N'})
			continue
		}
		break
	}
	b.Send(&pgproto3.AuthenticationOk{})
	b.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "18.0"})
	b.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
	b.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
	b.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
	b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	b.Flush()
	statements := map[string]string{}
	portal := ""
	var formats []int16
	failed := false
	for {
		msg, err := b.Receive()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *pgproto3.Query:
			f.answer(b, m.String, nil)
			b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			b.Flush()
		case *pgproto3.Parse:
			statements[m.Name] = m.Query
			b.Send(&pgproto3.ParseComplete{})
		case *pgproto3.Describe:
			if m.ObjectType == 'S' {
				f.describe(b, statements[m.Name])
			} else {
				b.Send(&pgproto3.NoData{})
			}
		case *pgproto3.Bind:
			portal = statements[m.PreparedStatement]
			formats = append([]int16(nil), m.ResultFormatCodes...)
			b.Send(&pgproto3.BindComplete{})
		case *pgproto3.Execute:
			if !failed {
				f.answer(b, portal, formats)
			}
		case *pgproto3.Sync:
			failed = false
			b.Send(&pgproto3.ReadyForQuery{TxStatus: 'T'})
			b.Flush()
		case *pgproto3.Terminate:
			return
		}
	}
}

// Mutation (d) of the second review: the ownership check is tested without
// a server; and the advice for a superuser's tables (M3).
func TestCheckPostgresStoreOwnershipWithoutAServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	check := func(f fakePG) *PostgresProblem {
		return CheckPostgresStore(ctx, DatabaseConfig{PostgresHost: f.serve(t), PostgresDatabase: "cosmos", PostgresUsername: "my-user", PostgresPassword: "x"})
	}
	if p := check(fakePG{}); p != nil {
		t.Fatalf("the account's own tables: %+v", p)
	}
	p := check(fakePG{owner: "keeper"})
	if p == nil || p.Reason != "owner" || !strings.Contains(p.Message, `REASSIGN OWNED BY "keeper" TO "my-user"`) {
		t.Errorf("another role's tables: %+v", p)
	}
	p = check(fakePG{owner: "postgres", super: true})
	if p == nil || p.Reason != "owner" || strings.Contains(p.Message, "REASSIGN") || !strings.Contains(p.Message, `ALTER TABLE "notifications" OWNER TO "my-user";`) ||
		strings.Contains(p.Message, "account") {
		t.Errorf("a superuser's tables: %+v", p)
	}
	p = check(fakePG{createErr: "42501"})
	if p == nil || p.Reason != "create" || !strings.Contains(p.Message, `GRANT CREATE ON SCHEMA public TO "my-user"`) {
		t.Errorf("may not create: %+v", p)
	}
}
