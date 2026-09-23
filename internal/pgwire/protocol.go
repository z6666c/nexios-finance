// Package pgwire is a minimal, dependency-free PostgreSQL client implementing
// enough of the frontend/backend wire protocol (https://www.postgresql.org/docs/current/protocol.html)
// to serve as a database/sql driver: TCP connect, startup, cleartext/MD5
// authentication, the simple query protocol, and the extended query
// protocol (Parse/Bind/Describe/Execute/Sync) with text-format parameters
// and results, plus transactions.
//
// It exists because this project ships with zero third-party Go modules so
// that `go build ./...` succeeds on a freshly copied checkout with no
// access to a Go module proxy (common on an air-gapped or local/on-prem
// server) — the standard library alone cannot talk to Postgres, and
// pulling in github.com/jackc/pgx or lib/pq would reintroduce that
// dependency. This driver only needs to support the query patterns this
// codebase actually issues (parameterized INSERT/SELECT/UPDATE with
// RETURNING, and simple transactions), so it deliberately does not aim to
// be a general-purpose pgx/lib-pq replacement.
package pgwire

import (
	"bufio"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Config holds the connection parameters for a Postgres backend.
type Config struct {
	Host            string
	Port            string
	User            string
	Password        string
	Database        string
	ApplicationName string
	ConnectTimeout  time.Duration
}

// conn is a single, non-pooled connection to a Postgres backend speaking
// protocol version 3.
type conn struct {
	nc  net.Conn
	r   *bufio.Reader
	w   *bufio.Writer
	cfg Config
	// txStatus reflects the last ReadyForQuery transaction indicator:
	// 'I' = idle, 'T' = in transaction, 'E' = failed transaction.
	txStatus byte
}

// pgError represents an ErrorResponse from the backend.
type pgError struct {
	Severity string
	Code     string
	Message  string
	Detail   string
}

func (e *pgError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("pgwire: %s: %s (%s) - %s", e.Severity, e.Message, e.Code, e.Detail)
	}
	return fmt.Sprintf("pgwire: %s: %s (%s)", e.Severity, e.Message, e.Code)
}

// Connect opens a new connection and completes the startup/auth handshake.
func Connect(cfg Config) (*conn, error) {
	if cfg.Port == "" {
		cfg.Port = "5432"
	}
	if cfg.ApplicationName == "" {
		cfg.ApplicationName = "nexios-finance"
	}
	timeout := cfg.ConnectTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	nc, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, cfg.Port), timeout)
	if err != nil {
		return nil, fmt.Errorf("pgwire: dial: %w", err)
	}

	c := &conn{
		nc:  nc,
		r:   bufio.NewReader(nc),
		w:   bufio.NewWriter(nc),
		cfg: cfg,
	}

	if err := c.startup(); err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

func (c *conn) Close() error {
	// Best-effort Terminate message; ignore errors, we're closing anyway.
	_ = c.writeMessage('X', nil)
	_ = c.w.Flush()
	return c.nc.Close()
}

// --- message framing -------------------------------------------------

func (c *conn) writeMessage(msgType byte, body []byte) error {
	if msgType != 0 {
		if err := c.w.WriteByte(msgType); err != nil {
			return err
		}
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)+4))
	if _, err := c.w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := c.w.Write(body)
	return err
}

type backendMessage struct {
	typ  byte
	body []byte
}

func (c *conn) readMessage() (*backendMessage, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(c.r, header); err != nil {
		return nil, err
	}
	typ := header[0]
	length := binary.BigEndian.Uint32(header[1:5])
	if length < 4 {
		return nil, errors.New("pgwire: invalid message length")
	}
	body := make([]byte, length-4)
	if len(body) > 0 {
		if _, err := io.ReadFull(c.r, body); err != nil {
			return nil, err
		}
	}
	return &backendMessage{typ: typ, body: body}, nil
}

func parseErrorResponse(body []byte) *pgError {
	e := &pgError{}
	fields := strings.Split(string(body), "\x00")
	for _, f := range fields {
		if f == "" {
			continue
		}
		if len(f) < 1 {
			continue
		}
		code, val := f[0], f[1:]
		switch code {
		case 'S':
			e.Severity = val
		case 'C':
			e.Code = val
		case 'M':
			e.Message = val
		case 'D':
			e.Detail = val
		}
	}
	return e
}

// --- startup & auth ----------------------------------------------------

func cstr(s string) []byte {
	return append([]byte(s), 0)
}

func (c *conn) startup() error {
	var buf []byte
	buf = append(buf, 0, 3, 0, 0) // protocol version 3.0
	params := map[string]string{
		"user":             c.cfg.User,
		"database":         c.cfg.Database,
		"application_name": c.cfg.ApplicationName,
		"client_encoding":  "UTF8",
	}
	for k, v := range params {
		buf = append(buf, cstr(k)...)
		buf = append(buf, cstr(v)...)
	}
	buf = append(buf, 0)

	if err := c.writeMessage(0, buf); err != nil {
		return err
	}
	if err := c.w.Flush(); err != nil {
		return err
	}

	for {
		msg, err := c.readMessage()
		if err != nil {
			return fmt.Errorf("pgwire: startup: %w", err)
		}
		switch msg.typ {
		case 'E':
			return parseErrorResponse(msg.body)
		case 'R':
			if err := c.handleAuth(msg.body); err != nil {
				return err
			}
		case 'S': // ParameterStatus
			// ignored
		case 'K': // BackendKeyData
			// ignored; not using cancel requests
		case 'Z': // ReadyForQuery
			if len(msg.body) > 0 {
				c.txStatus = msg.body[0]
			}
			return nil
		default:
			// NoticeResponse and others: ignore
		}
	}
}

func (c *conn) handleAuth(body []byte) error {
	if len(body) < 4 {
		return errors.New("pgwire: malformed authentication message")
	}
	authType := binary.BigEndian.Uint32(body[0:4])
	switch authType {
	case 0: // AuthenticationOk
		return nil
	case 3: // cleartext password
		return c.sendPasswordMessage(c.cfg.Password)
	case 5: // MD5 password
		if len(body) < 8 {
			return errors.New("pgwire: malformed MD5 auth message")
		}
		salt := body[4:8]
		hashed := md5Hex(c.cfg.Password + c.cfg.User)
		hashed = "md5" + md5Hex(hashed+string(salt))
		return c.sendPasswordMessage(hashed)
	default:
		return fmt.Errorf("pgwire: unsupported authentication method %d (this driver supports trust, cleartext and md5 password auth — set POSTGRES_HOST_AUTH_METHOD=md5 on the Postgres container)", authType)
	}
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 32)
	for i, b := range sum {
		out[i*2] = hexDigits[b>>4]
		out[i*2+1] = hexDigits[b&0x0f]
	}
	return string(out)
}

func (c *conn) sendPasswordMessage(pw string) error {
	if err := c.writeMessage('p', cstr(pw)); err != nil {
		return err
	}
	return c.w.Flush()
}

// Ping verifies the connection is alive by round-tripping an empty query.
func (c *conn) Ping() error {
	_, err := c.simpleQuery("SELECT 1")
	return err
}
