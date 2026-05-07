package cryptox

import (
	"crypto/ed25519"
	"errors"

	"golang.org/x/crypto/nacl/box"
)

// EnvelopeNonceSize — длина nonce для envelope (NaCl crypto_box).
const EnvelopeNonceSize = 24

// ErrCiphertextInvalid — расшифровка не удалась: ciphertext, nonce или
// ключ не совпали. Возвращается единым кодом без подробностей, чтобы не
// давать оракулов.
var ErrCiphertextInvalid = errors.New("envelope ciphertext failed authenticated decryption")

// SealEnvelopePayload шифрует plaintext по схеме NaCl crypto_box
// (X25519 + XSalsa20-Poly1305).
//
// nonce — 24 байта, должен быть **тем же**, что попадёт в подпись envelope
// (см. wire.CanonicalEnvelope). Генерируется один раз вызывающей стороной
// и передаётся сюда.
//
// senderEdPriv / recipientEdPub — Ed25519 ключи; X25519 выводятся
// детерминированно через birational map (см. x25519.go).
//
// Возвращает ciphertext длиной len(plaintext)+box.Overhead.
func SealEnvelopePayload(plaintext, nonce []byte, senderEdPriv ed25519.PrivateKey, recipientEdPub ed25519.PublicKey) ([]byte, error) {
	if len(nonce) != EnvelopeNonceSize {
		return nil, ErrInvalidKey
	}
	xPriv, err := Ed25519PrivToX25519(senderEdPriv)
	if err != nil {
		return nil, err
	}
	xPub, err := Ed25519PubToX25519(recipientEdPub)
	if err != nil {
		return nil, err
	}
	var n [EnvelopeNonceSize]byte
	copy(n[:], nonce)
	return box.Seal(nil, plaintext, &n, xPub, xPriv), nil
}

// OpenEnvelopePayload расшифровывает ciphertext, выработанный
// SealEnvelopePayload.
//
// При любой ошибке (битый ct, неверный nonce, не тот ключ) возвращает
// ErrCiphertextInvalid без подробностей.
func OpenEnvelopePayload(ciphertext, nonce []byte, recipientEdPriv ed25519.PrivateKey, senderEdPub ed25519.PublicKey) ([]byte, error) {
	if len(nonce) != EnvelopeNonceSize {
		return nil, ErrCiphertextInvalid
	}
	xPriv, err := Ed25519PrivToX25519(recipientEdPriv)
	if err != nil {
		return nil, ErrCiphertextInvalid
	}
	xPub, err := Ed25519PubToX25519(senderEdPub)
	if err != nil {
		return nil, ErrCiphertextInvalid
	}
	var n [EnvelopeNonceSize]byte
	copy(n[:], nonce)
	plaintext, ok := box.Open(nil, ciphertext, &n, xPub, xPriv)
	if !ok {
		return nil, ErrCiphertextInvalid
	}
	return plaintext, nil
}
