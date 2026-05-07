package port

import (
	"crypto/ed25519"
	"io"
)

// CryptoProvider — абстракция криптографических операций.
type CryptoProvider interface {
	GenerateKey(rand io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error)
	Sign(priv ed25519.PrivateKey, message []byte) []byte
	Verify(pub ed25519.PublicKey, message, sig []byte) bool
	LoadPrivateKey(profileDir string) (ed25519.PrivateKey, error)
	SavePrivateKey(profileDir string, priv ed25519.PrivateKey) error
}
