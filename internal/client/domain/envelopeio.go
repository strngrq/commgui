package domain

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
)

// EnvelopeIO реализует низкоуровневые операции с envelope: send/wait.
// Используется в основном тестами и debug-командами; реальный обмен звонков
// идёт через Caller / CallSession.
type EnvelopeIO struct {
	client *Client
}

// Send собирает envelope и отправляет его через signaling, ждёт ack/error.
func (e *EnvelopeIO) Send(ctx context.Context, opts EnvelopeSendOpts) (*EnvelopeAck, error) {
	c := e.client
	if err := c.requireSession(); err != nil {
		return nil, err
	}

	recipient, ok := findContact(c.state, opts.To)
	if !ok {
		return nil, ErrContactNotFound(opts.To)
	}

	if opts.CallID == "" {
		opts.CallID = NewID()
	}

	payload := map[string]any{
		"type":    opts.PayloadType,
		"call_id": opts.CallID,
		"ts":      time.Now().Add(opts.TSOffset).UnixMilli(),
	}
	if opts.SDP != "" {
		payload["sdp"] = opts.SDP
	}
	if opts.Candidate != "" {
		payload["candidate"] = opts.Candidate
	}

	signOpts := envelopeSignOpts{
		FromOverride:    opts.SpoofFrom,
		BadSignature:    opts.BadSignature,
		WrongSigningKey: opts.WrongSigningKey,
	}

	recipientPub, _ := cryptox.DecodeBase64URL(recipient.Pubkey, ed25519.PublicKeySize)
	env, err := makeEnvelopeWithOpts(c.state, c.priv, recipient.UserID, ed25519.PublicKey(recipientPub), payload, opts.TSOffset, signOpts)
	if err != nil {
		return nil, err
	}

	conn, err := c.ports.Signaling.OpenSignal(ctx, chooseServer(c.state), c.state.Session.Token)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.Write(ctx, map[string]any{"type": "envelope.send", "envelope": env}); err != nil {
		return nil, err
	}

	readCtx, cancel := context.WithTimeout(ctx, opts.WaitAck)
	defer cancel()

	for {
		var msg map[string]any
		if err := conn.Read(readCtx, &msg); err != nil {
			return nil, fmt.Errorf("waiting for ack: %w", err)
		}
		switch msg["type"] {
		case "error":
			code, _ := msg["code"].(string)
			return nil, ErrEnvelopeError(code, fmt.Sprintf("%v", msg["message"]))
		case "envelope.failed":
			reason, _ := msg["reason"].(string)
			return nil, ErrEnvelopeDeliveryFailed(reason)
		case "envelope.ack":
			return &EnvelopeAck{EnvelopeID: env.ID, Response: msg}, nil
		}
	}
}

// Wait ждёт входящие envelope через signaling и вызывает handler для каждого.
func (e *EnvelopeIO) Wait(ctx context.Context, opts EnvelopeWaitOpts, handler EnvelopeHandler) error {
	c := e.client
	if err := c.requireState(); err != nil {
		return err
	}

	conn, err := c.ports.Signaling.OpenSignal(ctx, chooseServer(c.state), c.state.Session.Token)
	if err != nil {
		return err
	}
	defer conn.Close()

	var count int
	for count < opts.Count {
		var msg struct {
			Type     string         `json:"type"`
			Envelope proto.Envelope `json:"envelope"`
		}

		if err := conn.Read(ctx, &msg); err != nil {
			return err
		}
		if msg.Type != "envelope.deliver" {
			continue
		}

		from, found := contactByUserID(c.state, msg.Envelope.From)
		var senderPub ed25519.PublicKey
		if found {
			pub, err := cryptox.DecodeBase64URL(from.Pubkey, ed25519.PublicKeySize)
			if err == nil {
				senderPub = ed25519.PublicKey(pub)
			}
		}

		if opts.FilterType != "" {
			payload, err := DecodeEnvelopePayload(msg.Envelope, senderPub, e.client.priv)
			if errors.Is(err, ErrEnvelopeSignatureInvalid) {
				continue
			}
			if err != nil || payloadString(payload, "type") != opts.FilterType {
				if !opts.NoAck {
					_ = conn.Write(ctx, map[string]string{"type": "envelope.ack", "id": msg.Envelope.ID})
				}
				continue
			}
		}

		summary, err := envelopePayloadSummary(msg.Envelope, senderPub, e.client.priv)
		if errors.Is(err, ErrEnvelopeSignatureInvalid) {
			continue
		}
		if err != nil {
			if !opts.NoAck {
				_ = conn.Write(ctx, map[string]string{"type": "envelope.ack", "id": msg.Envelope.ID})
			}
			continue
		}
		if handler != nil {
			if err := handler(summary); err != nil {
				return err
			}
		}
		if !opts.NoAck {
			_ = conn.Write(ctx, map[string]string{"type": "envelope.ack", "id": msg.Envelope.ID})
		}
		count++
	}
	return nil
}
