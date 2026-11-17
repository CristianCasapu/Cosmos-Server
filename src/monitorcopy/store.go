package monitorcopy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Store is one side of a copy: a handle and the dialect it speaks.
type Store struct {
	DB      *sql.DB
	Dialect string
}

func (s Store) postgres() bool { return s.Dialect == utils.DialectPostgres }

func (s Store) bind(query string) string { return utils.RebindFor(s.Dialect, query) }

// tables lists the base tables of the store (lower case), without SQLite's own.
func (s Store) tables(ctx context.Context) (map[string]bool, error) {
	query := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\'"
	if s.postgres() {
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'"
	}
	return s.names(ctx, query)
}

// columns lists the columns of a table (lower case).
func (s Store) columns(ctx context.Context, table string) (map[string]bool, error) {
	if s.postgres() {
		return s.names(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1", table)
	}
	return s.names(ctx, "SELECT name FROM pragma_table_info(?)", table)
}

func (s Store) names(ctx context.Context, query string, args ...interface{}) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = true
	}
	return out, rows.Err()
}

// layout is what the source has of a rule's table: whether it exists, and
// the SELECT list of the rule's columns (a missing column reads as its default).
type layout struct {
	exists bool
	list   string
}

func (s Store) layoutOf(ctx context.Context, r rule, tables map[string]bool) (layout, error) {
	if !tables[r.table] {
		return layout{}, nil
	}
	have, err := s.columns(ctx, r.table)
	if err != nil {
		return layout{}, err
	}
	parts := make([]string, len(r.columns))
	for i, c := range r.columns {
		if have[c.name] {
			parts[i] = c.name
		} else {
			parts[i] = c.def + " AS " + c.name
		}
	}
	for _, needed := range append(append([]string{}, r.key...), r.retentionCol, r.expireCol) {
		if needed != "" && !have[needed] {
			return layout{}, errOldLayout
		}
	}
	if r.surrogate() && !have["id"] {
		return layout{}, errOldLayout
	}
	if r.table == "notifications" && !have["fork_first"] {
		return layout{}, errOldLayout
	}
	return layout{exists: true, list: strings.Join(parts, ", ")}, nil
}

var errOldLayout = errors.New("monitorcopy: table of an older layout")

// scanRows reads rows of a rule: [id, w,] then the columns.
func scanRows(rows *sql.Rows, r rule, withID bool, withW bool) ([]row, error) {
	defer rows.Close()
	out := []row{}
	n := len(r.columns)
	extra := 0
	if withID {
		extra++
	}
	if withW {
		extra++
	}
	dest := make([]interface{}, n+extra)
	var id, w sql.NullInt64
	texts := make([]sql.NullString, n)
	ints := make([]sql.NullInt64, n)
	floats := make([]sql.NullFloat64, n)
	i := 0
	if withID {
		dest[i] = &id
		i++
	}
	if withW {
		dest[i] = &w
		i++
	}
	for c, column := range r.columns {
		switch column.kind {
		case text:
			dest[i+c] = &texts[c]
		case integer, integer32:
			dest[i+c] = &ints[c]
		case real:
			dest[i+c] = &floats[c]
		}
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		values := make([]interface{}, n)
		for c, column := range r.columns {
			// a NULL (none of the columns allows one) reads as the default
			switch column.kind {
			case text:
				values[c] = texts[c].String
			case integer, integer32:
				values[c] = ints[c].Int64
			case real:
				values[c] = floats[c].Float64
			}
		}
		out = append(out, row{id: id.Int64, w: w.Int64, values: values})
	}
	return out, rows.Err()
}

// errTooLarge: an INTEGER column holds a value PostgreSQL's 32 bits cannot.
type errTooLarge struct{ column string }

func (e errTooLarge) Error() string { return "monitorcopy: value too large for column " + e.column }

// fitFor makes a source row storable in the target: PostgreSQL's TEXT takes
// neither NUL nor invalid UTF-8 (SQLite's does), and its INTEGER is 32 bits.
func fitFor(target Store, r rule, values []interface{}) error {
	if !target.postgres() {
		return nil
	}
	for i, c := range r.columns {
		switch c.kind {
		case text:
			s := values[i].(string)
			if strings.IndexByte(s, 0) >= 0 {
				s = strings.ReplaceAll(s, "\x00", "")
			}
			if !utf8.ValidString(s) {
				s = strings.ToValidUTF8(s, "�")
			}
			values[i] = s
		case integer32:
			v := values[i].(int64)
			if v > math.MaxInt32 || v < math.MinInt32 {
				return errTooLarge{column: c.name}
			}
		}
	}
	return nil
}

// identityKey is a fixed-size digest of a row's identity values.
func identityKey(values []interface{}) [32]byte {
	h := sha256.New()
	var buf [8]byte
	for _, v := range values {
		switch x := v.(type) {
		case string:
			h.Write([]byte{'s'})
			binary.BigEndian.PutUint64(buf[:], uint64(len(x)))
			h.Write(buf[:])
			h.Write([]byte(x))
		case int64:
			h.Write([]byte{'i'})
			binary.BigEndian.PutUint64(buf[:], uint64(x))
			h.Write(buf[:])
		case float64:
			h.Write([]byte{'f'})
			binary.BigEndian.PutUint64(buf[:], math.Float64bits(x))
			h.Write(buf[:])
		default:
			h.Write([]byte(fmt.Sprintf("?%v", x)))
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// kv markers --------------------------------------------------------------

func (s Store) getKV(ctx context.Context, q queryer, key string) (string, bool, error) {
	var value string
	err := q.QueryRowContext(ctx, s.bind("SELECT value FROM monitoring_kv WHERE key = ?"), key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s Store) setKV(ctx context.Context, q execer, key, value string) error {
	_, err := q.ExecContext(ctx, s.bind("INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"), key, value)
	return err
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
