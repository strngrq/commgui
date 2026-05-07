package crypto

import (
	"crypto/ed25519"
	"io"
	"os"
	"path/filepath"
)

type Ed25519Provider struct{}

func NewEd25519Provider() *Ed25519Provider {
	return &Ed25519Provider{}
}

func (e *Ed25519Provider) GenerateKey(rand io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand)
}

func (e *Ed25519Provider) Sign(priv ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(priv, message)
}

func (e *Ed25519Provider) Verify(pub ed25519.PublicKey, message, sig []byte) bool {
	return ed25519.Verify(pub, message, sig)
}

func (e *Ed25519Provider) LoadPrivateKey(profileDir string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(filepath.Join(profileDir, "identity.key"))
	if err != nil {
		return nil, err
	}
	return ed25519.PrivateKey(raw), nil
}

func (e *Ed25519Provider) SavePrivateKey(profileDir string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(profileDir, "identity.key"), priv, 0o400)
}
