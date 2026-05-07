package cryptox

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrInvalidBase64 = errors.New("invalid base64url value")
	ErrInvalidKey    = errors.New("invalid key length")
)

func NewEd25519Keypair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

func EncodeBase64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func DecodeBase64URL(value string, expectedLen int) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalidBase64
	}
	if expectedLen >= 0 && len(raw) != expectedLen {
		return nil, ErrInvalidKey
	}
	return raw, nil
}

func SHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func Fingerprint(data []byte, bytes int) string {
	sum := sha256.Sum256(data)
	if bytes > len(sum) {
		bytes = len(sum)
	}
	return hex.EncodeToString(sum[:bytes])
}

func ConstantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		subtle.ConstantTimeCompare(a, a)
		subtle.ConstantTimeCompare(b, b)
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

func VerifyPublicKey(pub []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrInvalidKey
	}
	return nil
}

func SafetyNumber(pubA, pubB []byte) string {
	a := append([]byte(nil), pubA...)
	b := append([]byte(nil), pubB...)
	if strings.Compare(string(a), string(b)) > 0 {
		a, b = b, a
	}
	h := sha256.New()
	h.Write([]byte("commsrv-safety-v1\x00"))
	h.Write(a)
	h.Write(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h.Sum(nil)))
}

func ContactFingerprint(pub []byte) string {
	h := sha256.New()
	h.Write([]byte("commsrv-contact-fp-v1\x00"))
	h.Write(pub)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h.Sum(nil)))[:8]
}
