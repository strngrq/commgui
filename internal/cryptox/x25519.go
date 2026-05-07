package cryptox

import (
	"crypto/ed25519"
	"crypto/sha512"

	"filippo.io/edwards25519"
)

// Ed25519PrivToX25519 конвертирует Ed25519 priv в X25519 priv по
// стандартной libsodium-схеме crypto_sign_ed25519_sk_to_curve25519.
//
// На вход принимается 64-байтный ed25519.PrivateKey (seed||pub); первые
// 32 байта — seed. Из seed вычисляется SHA-512, первые 32 байта
// "обрезаются" в формат scalar для X25519.
func Ed25519PrivToX25519(edPriv ed25519.PrivateKey) (*[32]byte, error) {
	if len(edPriv) != ed25519.PrivateKeySize {
		return nil, ErrInvalidKey
	}
	h := sha512.Sum512(edPriv.Seed())
	var x [32]byte
	copy(x[:], h[:32])
	x[0] &= 248
	x[31] &= 127
	x[31] |= 64
	return &x, nil
}

// Ed25519PubToX25519 конвертирует Ed25519 pub в X25519 pub через
// birational map Edwards25519 → Curve25519 (Montgomery).
//
// Возвращает ErrInvalidKey, если входные байты не образуют валидную точку
// на кривой Edwards25519.
func Ed25519PubToX25519(edPub ed25519.PublicKey) (*[32]byte, error) {
	if len(edPub) != ed25519.PublicKeySize {
		return nil, ErrInvalidKey
	}
	p, err := new(edwards25519.Point).SetBytes(edPub)
	if err != nil {
		return nil, ErrInvalidKey
	}
	var x [32]byte
	copy(x[:], p.BytesMontgomery())
	return &x, nil
}
