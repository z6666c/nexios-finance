package pgwire

import (
	"encoding/binary"
	"fmt"
)

// resultSet is a decoded query result: column names/type OIDs plus
// text-format rows (nil cell = SQL NULL).
type resultSet struct {
	columns      []string
	columnOIDs   []uint32
	rows         [][]*string
	rowsAffected int64
	err          error
}

// simpleQuery executes one or more semicolon-separated statements using the
// simple query protocol. It is used for schema migrations (DDL) and for
// transaction control (BEGIN/COMMIT/ROLLBACK), never for statements that
// take caller-supplied parameters (those must go through the extended
// query protocol in execExtended to avoid any risk of SQL injection).
func (c *conn) simpleQuery(sql string) (*resultSet, error) {
	if err := c.writeMessage('Q', cstr(sql)); err != nil {
		return nil, err
	}
	if err := c.w.Flush(); err != nil {
		return nil, err
	}
	return c.collectResults()
}

// execExtended runs a single parameterized statement via
// Parse/Bind/Describe/Execute/Sync, with all parameters and all results in
// text format. This is the path used for every application query. A nil
// entry in args is bound as SQL NULL (encoded on the wire as a -1 length
// prefix, per the protocol); every other entry is sent as its text form.
func (c *conn) execExtended(sql string, args []*string) (*resultSet, error) {
	var buf []byte

	// Parse: unnamed statement, no explicit parameter types (let the
	// backend infer them from context; we always send params as text).
	buf = append(buf, cstr("")...)  // statement name
	buf = append(buf, cstr(sql)...) // query
	buf = append(buf, 0, 0)         // 0 parameter type OIDs
	if err := c.writeMessage('P', buf); err != nil {
		return nil, err
	}

	// Bind: unnamed portal, unnamed statement.
	buf = buf[:0]
	buf = append(buf, cstr("")...) // portal name
	buf = append(buf, cstr("")...) // statement name
	buf = append(buf, 0, 1, 0, 0)  // 1 parameter format code = text (0)
	var paramCount [2]byte
	binary.BigEndian.PutUint16(paramCount[:], uint16(len(args)))
	buf = append(buf, paramCount[:]...)
	for _, a := range args {
		lenBuf := make([]byte, 4)
		if a == nil {
			binary.BigEndian.PutUint32(lenBuf, 0xFFFFFFFF) // -1 as int32: SQL NULL
			buf = append(buf, lenBuf...)
			continue
		}
		binary.BigEndian.PutUint32(lenBuf, uint32(len(*a)))
		buf = append(buf, lenBuf...)
		buf = append(buf, []byte(*a)...)
	}
	buf = append(buf, 0, 1, 0, 0) // 1 result format code = text (0)
	if err := c.writeMessage('B', buf); err != nil {
		return nil, err
	}

	// Describe the portal so we get RowDescription even for statements the
	// backend might otherwise treat as producing no described columns.
	buf = buf[:0]
	buf = append(buf, 'P')
	buf = append(buf, cstr("")...)
	if err := c.writeMessage('D', buf); err != nil {
		return nil, err
	}

	// Execute: unnamed portal, no row limit.
	buf = buf[:0]
	buf = append(buf, cstr("")...)
	buf = append(buf, 0, 0, 0, 0)
	if err := c.writeMessage('E', buf); err != nil {
		return nil, err
	}

	if err := c.writeMessage('S', nil); err != nil {
		return nil, err
	}
	if err := c.w.Flush(); err != nil {
		return nil, err
	}

	return c.collectResults()
}

// collectResults reads backend messages until ReadyForQuery, accumulating
// any RowDescription/DataRow/CommandComplete into a resultSet. It surfaces
// the first ErrorResponse encountered (continuing to drain messages so the
// connection is left in a consistent state for the next ReadyForQuery).
func (c *conn) collectResults() (*resultSet, error) {
	rs := &resultSet{}
	var firstErr error

	for {
		msg, err := c.readMessage()
		if err != nil {
			return nil, err
		}
		switch msg.typ {
		case 'T': // RowDescription
			rs.columns, rs.columnOIDs = parseRowDescription(msg.body)
		case 'D': // DataRow
			row, err := parseDataRow(msg.body)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			rs.rows = append(rs.rows, row)
		case 'C': // CommandComplete
			rs.rowsAffected += parseCommandTag(msg.body)
		case 'E': // ErrorResponse
			if firstErr == nil {
				firstErr = parseErrorResponse(msg.body)
			}
		case 'N': // NoticeResponse - ignore
		case 'Z': // ReadyForQuery
			if len(msg.body) > 0 {
				c.txStatus = msg.body[0]
			}
			if firstErr != nil {
				return nil, firstErr
			}
			return rs, nil
		case '1', '2', '3': // ParseComplete, BindComplete, CloseComplete
		case 'I': // EmptyQueryResponse
		default:
			// ignore anything else (e.g. ParameterStatus mid-stream)
		}
	}
}

func parseRowDescription(body []byte) ([]string, []uint32) {
	if len(body) < 2 {
		return nil, nil
	}
	n := int(binary.BigEndian.Uint16(body[0:2]))
	cols := make([]string, 0, n)
	oids := make([]uint32, 0, n)
	off := 2
	for i := 0; i < n; i++ {
		start := off
		for off < len(body) && body[off] != 0 {
			off++
		}
		cols = append(cols, string(body[start:off]))
		off++    // null terminator
		off += 6 // table OID(4) + attnum(2)
		if off+4 <= len(body) {
			oids = append(oids, binary.BigEndian.Uint32(body[off:off+4]))
		} else {
			oids = append(oids, 0)
		}
		off += 4 // type OID
		off += 8 // typlen(2) + typmod(4) + format code(2)
	}
	return cols, oids
}

func parseDataRow(body []byte) ([]*string, error) {
	if len(body) < 2 {
		return nil, nil
	}
	n := int(binary.BigEndian.Uint16(body[0:2]))
	row := make([]*string, n)
	off := 2
	for i := 0; i < n; i++ {
		if off+4 > len(body) {
			return nil, fmt.Errorf("pgwire: truncated data row")
		}
		l := int32(binary.BigEndian.Uint32(body[off : off+4]))
		off += 4
		if l < 0 {
			row[i] = nil
			continue
		}
		if off+int(l) > len(body) {
			return nil, fmt.Errorf("pgwire: truncated data row value")
		}
		s := string(body[off : off+int(l)])
		row[i] = &s
		off += int(l)
	}
	return row, nil
}

// parseCommandTag extracts the affected-row count from a CommandComplete
// tag such as "INSERT 0 1", "UPDATE 3", "DELETE 1", "SELECT 5".
func parseCommandTag(body []byte) int64 {
	tag := string(body)
	if len(tag) > 0 && tag[len(tag)-1] == 0 {
		tag = tag[:len(tag)-1]
	}
	var n int64
	var lastNum int64 = -1
	var cur int64
	haveDigits := false
	for _, ch := range tag {
		if ch >= '0' && ch <= '9' {
			cur = cur*10 + int64(ch-'0')
			haveDigits = true
		} else {
			if haveDigits {
				lastNum = cur
			}
			cur = 0
			haveDigits = false
		}
	}
	if haveDigits {
		lastNum = cur
	}
	if lastNum >= 0 {
		n = lastNum
	}
	return n
}
