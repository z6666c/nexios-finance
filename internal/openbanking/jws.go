package openbanking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// PayloadSigner signs the request payload before sending it to the bank,
// in line with FAPI 1.0 Advanced's requirement to sign payloads via JWS.
type PayloadSigner interface {
	Sign(payload []byte) (string, error)
}

// HMACSigner is a simplified demonstration implementation using HMAC-SHA256.
// Warning: this is NOT a real FAPI-compliant signature. FAPI 1.0 Advanced
// requires PS256 or ES256 with a private key stored in an HSM. Replace this
// with a real JWS signer (e.g. go-jose) backed by an HSM before production use.
type HMACSigner struct {
	secret []byte
}

func NewHMACSigner(secret []byte) *HMACSigner {
	return &HMACSigner{secret: secret}
}

func (s *HMACSigner) Sign(payload []byte) (string, error) {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

type NoopSigner struct{}

func (NoopSigner) Sign(payload []byte) (string, error) { return "", nil }
