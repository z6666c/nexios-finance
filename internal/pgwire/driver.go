package pgwire

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func init() {
	sql.Register("pgwire", &sqlDriver{})
}

// sqlDriver implements database/sql/driver.Driver and DriverContext so
// `sql.Open("pgwire", dsn)` works with the standard library's connection
// pool. DSNs are plain Postgres URLs:
// postgres://user:password@host:port/dbname?sslmode=disable (sslmode is
// accepted for compatibility but this driver only ever speaks plaintext —
// see the package doc for why TLS wasn't in scope).
type sqlDriver struct{}

func (d *sqlDriver) Open(dsn string) (driver.Conn, error) {
	cfg, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	c, err := Connect(cfg)
	if err != nil {
		return nil, err
	}
	return &driverConn{c: c}, nil
}

func parseDSN(dsn string) (Config, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return Config{}, fmt.Errorf("pgwire: invalid DSN: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return Config{}, fmt.Errorf("pgwire: DSN must use postgres:// or postgresql://")
	}
	cfg := Config{
		Host:     u.Hostname(),
		Port:     u.Port(),
		Database: strings.TrimPrefix(u.Path, "/"),
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	return cfg, nil
}

// driverConn adapts *conn to driver.Conn plus the optional interfaces
// database/sql uses for prepared statements, transactions, pinging and
// context cancellation.
type driverConn struct {
	mu sync.Mutex
	c  *conn
}

var (
	_ driver.Conn              = (*driverConn)(nil)
	_ driver.ConnBeginTx       = (*driverConn)(nil)
	_ driver.ExecerContext     = (*driverConn)(nil)
	_ driver.QueryerContext    = (*driverConn)(nil)
	_ driver.Pinger            = (*driverConn)(nil)
	_ driver.NamedValueChecker = (*driverConn)(nil)
)

func (dc *driverConn) Prepare(query string) (driver.Stmt, error) {
	return &stmt{dc: dc, query: query}, nil
}

func (dc *driverConn) Close() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.c.Close()
}

func (dc *driverConn) Begin() (driver.Tx, error) {
	return dc.BeginTx(context.Background(), driver.TxOptions{})
}

func (dc *driverConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if _, err := dc.c.simpleQuery("BEGIN"); err != nil {
		return nil, err
	}
	return &tx{dc: dc}, nil
}

func (dc *driverConn) Ping(ctx context.Context) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.c.Ping()
}

// NamedValueChecker: accept the value types this codebase actually binds
// (string, int64, float64, bool, time.Time, []byte, nil) as-is; let
// database/sql's default converter handle everything else (e.g. our
// uuid.UUID via its driver.Valuer / fmt.Stringer, once wrapped by callers
// as a string).
func (dc *driverConn) CheckNamedValue(nv *driver.NamedValue) error {
	switch nv.Value.(type) {
	case nil, string, int64, float64, bool, time.Time, []byte:
		return nil
	}
	if v, ok := nv.Value.(driver.Valuer); ok {
		val, err := v.Value()
		if err != nil {
			return err
		}
		nv.Value = val
		return nil
	}
	if s, ok := nv.Value.(fmt.Stringer); ok {
		nv.Value = s.String()
		return nil
	}
	return driver.ErrSkip
}

func (dc *driverConn) execContext(ctx context.Context, query string, args []*string) (*resultSet, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.c.execExtended(query, args)
}

func (dc *driverConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	strArgs, err := toTextArgs(args)
	if err != nil {
		return nil, err
	}
	rs, err := dc.execContext(ctx, query, strArgs)
	if err != nil {
		return nil, err
	}
	return execResult{rowsAffected: rs.rowsAffected}, nil
}

func (dc *driverConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	strArgs, err := toTextArgs(args)
	if err != nil {
		return nil, err
	}
	rs, err := dc.execContext(ctx, query, strArgs)
	if err != nil {
		return nil, err
	}
	return &rows{rs: rs}, nil
}

func toTextArgs(args []driver.NamedValue) ([]*string, error) {
	out := make([]*string, len(args))
	for i, a := range args {
		if a.Value == nil {
			out[i] = nil
			continue
		}
		s := valueToText(a.Value)
		out[i] = &s
	}
	return out, nil
}

func valueToText(v driver.Value) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case time.Time:
		return t.UTC().Format("2006-01-02 15:04:05.999999-07")
	default:
		return fmt.Sprintf("%v", t)
	}
}

// --- driver.Stmt ---------------------------------------------------------

type stmt struct {
	dc    *driverConn
	query string
}

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return -1 } // let database/sql skip validation; Postgres will error if wrong

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	named := toNamedValues(args)
	return s.dc.ExecContext(context.Background(), s.query, named)
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	named := toNamedValues(args)
	return s.dc.QueryContext(context.Background(), s.query, named)
}

func toNamedValues(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, a := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: a}
	}
	return out
}

// --- driver.Tx -------------------------------------------------------

type tx struct {
	dc *driverConn
}

func (t *tx) Commit() error {
	t.dc.mu.Lock()
	defer t.dc.mu.Unlock()
	_, err := t.dc.c.simpleQuery("COMMIT")
	return err
}

func (t *tx) Rollback() error {
	t.dc.mu.Lock()
	defer t.dc.mu.Unlock()
	_, err := t.dc.c.simpleQuery("ROLLBACK")
	return err
}

// --- driver.Result -----------------------------------------------------

type execResult struct {
	rowsAffected int64
}

func (r execResult) LastInsertId() (int64, error) {
	return 0, errors.New("pgwire: LastInsertId not supported; use RETURNING with QueryRow instead")
}

func (r execResult) RowsAffected() (int64, error) {
	return r.rowsAffected, nil
}

// --- driver.Rows -------------------------------------------------------

type rows struct {
	rs  *resultSet
	pos int
}

func (r *rows) Columns() []string {
	return r.rs.columns
}

func (r *rows) Close() error {
	r.pos = len(r.rs.rows)
	return nil
}

// Postgres type OIDs this driver converts out of text format into native
// Go types recognized by database/sql; everything else (text, varchar,
// uuid, numeric, ...) is left as a string, which this codebase's repository
// layer parses itself (e.g. uuid.Parse for uuid columns, since numeric
// ledger amounts are always stored as bigint minor units, never numeric).
const (
	oidBool        = 16
	oidInt8        = 20
	oidInt4        = 23
	oidTimestamp   = 1114
	oidTimestampTZ = 1184
)

func (r *rows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rs.rows) {
		return io.EOF
	}
	row := r.rs.rows[r.pos]
	r.pos++
	for i, cell := range row {
		if i >= len(dest) {
			break
		}
		if cell == nil {
			dest[i] = nil
			continue
		}
		var oid uint32
		if i < len(r.rs.columnOIDs) {
			oid = r.rs.columnOIDs[i]
		}
		dest[i] = convertCell(*cell, oid)
	}
	return nil
}

func convertCell(s string, oid uint32) driver.Value {
	switch oid {
	case oidBool:
		return s == "t" || s == "true"
	case oidInt8, oidInt4:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return s
	case oidTimestamp, oidTimestampTZ:
		for _, layout := range []string{
			"2006-01-02 15:04:05.999999-07",
			"2006-01-02 15:04:05.999999Z07:00",
			"2006-01-02 15:04:05-07",
			"2006-01-02 15:04:05",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
		return s
	default:
		return s
	}
}
