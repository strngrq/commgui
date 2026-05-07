package domain

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
	"github.com/strngrq/commgui/internal/wire"
)

// envelopeSignOpts — опции построения envelope, нужные тестам и внутренним вызовам.
type envelopeSignOpts struct {
	FromOverride    string
	AlternateSigner ed25519.PrivateKey
	BadSignature    bool
	WrongSigningKey bool
	FixedEnvelopeID string
}

// MakeEnvelope собирает, шифрует (crypto_box) и подписывает envelope для отправки.
//
// recipientPub — Ed25519 публичный ключ получателя; X25519 для DH выводится
// детерминированно через cryptox.Ed25519PubToX25519.
func MakeEnvelope(st *port.State, sessionPriv ed25519.PrivateKey, to string, recipientPub ed25519.PublicKey, payload map[string]any, tsOffset time.Duration) (proto.Envelope, error) {
	return makeEnvelopeWithOpts(st, sessionPriv, to, recipientPub, payload, tsOffset, envelopeSignOpts{})
}

func makeEnvelopeWithOpts(st *port.State, sessionPriv ed25519.PrivateKey, to string, recipientPub ed25519.PublicKey, payload map[string]any, tsOffset time.Duration, opt envelopeSignOpts) (proto.Envelope, error) {
	if len(recipientPub) != ed25519.PublicKeySize {
		return proto.Envelope{}, cryptox.ErrInvalidKey
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return proto.Envelope{}, err
	}

	// Один nonce — и в crypto_box, и в подпись envelope. Никаких вторых
	// генераций ниже по коду.
	nonce := make([]byte, cryptox.EnvelopeNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return proto.Envelope{}, err
	}

	signer := sessionPriv
	if opt.WrongSigningKey {
		_, alt, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return proto.Envelope{}, err
		}
		signer = alt
	}
	if len(opt.AlternateSigner) == ed25519.PrivateKeySize {
		signer = opt.AlternateSigner
	}

	ct, err := cryptox.SealEnvelopePayload(payloadBytes, nonce, signer, recipientPub)
	if err != nil {
		return proto.Envelope{}, err
	}

	ts := time.Now().Add(tsOffset).UnixMilli()

	id := NewID()
	if opt.FixedEnvelopeID != "" {
		id = opt.FixedEnvelopeID
	}

	from := st.User.UserID
	if opt.FromOverride != "" {
		from = opt.FromOverride
	}

	env := proto.Envelope{
		ID:         id,
		To:         to,
		From:       from,
		TS:         ts,
		Nonce:      cryptox.EncodeBase64URL(nonce),
		Ciphertext: cryptox.EncodeBase64URL(ct),
	}

	sig := ed25519.Sign(signer, wire.CanonicalEnvelope(env.ID, env.To, env.From, env.TS, nonce, ct))

	if opt.BadSignature && len(sig) > 0 {
		sig = append([]byte(nil), sig...)
		sig[0] ^= 0xff
	}

	env.Signature = cryptox.EncodeBase64URL(sig)
	return env, nil
}

// ErrEnvelopeSignatureInvalid — подпись envelope не прошла проверку.
var ErrEnvelopeSignatureInvalid = errors.New("envelope signature is invalid")

// DecodeEnvelopePayload проверяет Ed25519-подпись envelope и расшифровывает
// payload через crypto_box_open.
//
// senderPubkey — Ed25519 pub отправителя из локальной адресной книги; nil
// больше **не допускается** (без pubkey невозможен DH-расшифровка). Если
// контакт неизвестен — вызывающая сторона должна молча дропать envelope
// и не звать эту функцию.
//
// recipientPriv — наш Ed25519 priv для DH с отправителем.
//
// При неверной подписи возвращает ErrEnvelopeSignatureInvalid; при неудачной
// расшифровке — cryptox.ErrCiphertextInvalid (без подробностей).
func DecodeEnvelopePayload(env proto.Envelope, senderPubkey ed25519.PublicKey, recipientPriv ed25519.PrivateKey) (map[string]any, error) {
	if len(senderPubkey) != ed25519.PublicKeySize {
		return nil, ErrEnvelopeSignatureInvalid
	}
	nonce, err := cryptox.DecodeBase64URL(env.Nonce, cryptox.EnvelopeNonceSize)
	if err != nil {
		return nil, err
	}
	ct, err := cryptox.DecodeBase64URL(env.Ciphertext, -1)
	if err != nil {
		return nil, err
	}
	sig, err := cryptox.DecodeBase64URL(env.Signature, ed25519.SignatureSize)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(senderPubkey, wire.CanonicalEnvelope(env.ID, env.To, env.From, env.TS, nonce, ct), sig) {
		return nil, ErrEnvelopeSignatureInvalid
	}

	plaintext, err := cryptox.OpenEnvelopePayload(ct, nonce, recipientPriv, senderPubkey)
	if err != nil {
		return nil, err
	}

	var payload map[string]any
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// envelopePayloadSummary возвращает EnvelopeSummary с проверенным payload.
func envelopePayloadSummary(env proto.Envelope, senderPubkey ed25519.PublicKey, recipientPriv ed25519.PrivateKey) (EnvelopeSummary, error) {
	payload, err := DecodeEnvelopePayload(env, senderPubkey, recipientPriv)
	if err != nil {
		return EnvelopeSummary{}, err
	}
	return EnvelopeSummary{
		EnvelopeID: env.ID,
		From:       env.From,
		TS:         env.TS,
		Payload:    payload,
	}, nil
}
