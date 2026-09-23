// Package uuid is a tiny, dependency-free replacement for github.com/google/uuid.
//
// The original codebase depended on github.com/google/uuid, but a fresh
// `git clone` of this repository has no go.sum entry for it and this
// project intentionally ships with zero third-party dependencies so that
// `go build ./...` works out of the box on an air-gapped / local server
// with no access to proxy.golang.org. This package implements the small
// subset of the google/uuid API this codebase actually uses (New, Parse,
// MustParse, UUID.String, and the UUID type itself as a [16]byte), using
// only the standard library (crypto/rand).
package uuid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// UUID is a 128-bit universally unique identifier, laid out exactly as
// RFC 4122 / google/uuid represents it: 16 raw bytes.
type UUID [16]byte

// Nil is the zero-value UUID (00000000-0000-0000-0000-000000000000).
var Nil UUID

// New returns a new random (version 4, variant 10) UUID.
//
// google/uuid's New() panics on a read failure from the OS RNG; we match
// that behavior since callers in this codebase never check an error here.
func New() UUID {
	u, err := NewRandom()
	if err != nil {
		panic(err)
	}
	return u
}

// NewString is a convenience wrapper equivalent to New().String().
func NewString() string {
	return New().String()
}

// NewRandom returns a new random (version 4, variant 10) UUID, or an
// error if the system's secure RNG could not be read.
func NewRandom() (UUID, error) {
	var u UUID
	if _, err := rand.Read(u[:]); err != nil {
		return Nil, err
	}
	// Version 4: the 4 most significant bits of byte 6 are 0100.
	u[6] = (u[6] & 0x0f) | 0x40
	// Variant 10 (RFC 4122): the 2 most significant bits of byte 8 are 10.
	u[8] = (u[8] & 0x3f) | 0x80
	return u, nil
}

// Parse decodes a UUID in the standard
// "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" (8-4-4-4-12 hex) form, or the
// same bytes without dashes.
func Parse(s string) (UUID, error) {
	var u UUID

	switch len(s) {
	case 36: // with dashes
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return Nil, errors.New("uuid: invalid format, expected dashes at positions 8, 13, 18, 23")
		}
		hexStr := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
		if err := decodeHex(u[:], hexStr); err != nil {
			return Nil, err
		}
	case 32: // no dashes
		if err := decodeHex(u[:], s); err != nil {
			return Nil, err
		}
	default:
		return Nil, errors.New("uuid: invalid length, expected 36 (with dashes) or 32 (without)")
	}

	return u, nil
}

// MustParse is like Parse but panics if s cannot be parsed. It is
// intended for package-level fixed/well-known UUID constants.
func MustParse(s string) UUID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func decodeHex(dst []byte, s string) error {
	if len(s) != 32 {
		return errors.New("uuid: invalid hex length")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	copy(dst, b)
	return nil
}

// String returns the standard 8-4-4-4-12 hyphenated hex representation.
func (u UUID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], u[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], u[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], u[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], u[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], u[10:16])
	return string(buf[:])
}

// IsNil reports whether u is the zero-value UUID.
func (u UUID) IsNil() bool {
	return u == Nil
}

// MarshalText implements encoding.TextMarshaler so UUID fields serialize
// as their standard string form in encoding/json.
func (u UUID) MarshalText() ([]byte, error) {
	return []byte(u.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler so UUID fields parse
// from their standard string form in encoding/json.
func (u *UUID) UnmarshalText(data []byte) error {
	parsed, err := Parse(string(data))
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}
