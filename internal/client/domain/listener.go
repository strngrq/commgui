package domain

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
)

// listener — реализация Listener.
//
// Жизненный цикл:
//   - run() читает signaling, при call.offer кладёт IncomingCall в incoming
//     и завершается (Listener одноразовый).
//   - Caller, получив IncomingCall, должен либо Accept (signaling переходит
//     в CallSession), либо Decline (signaling закрывается тут же).
//   - Close завершает run() извне и закрывает signaling, если он не передан.
type listener struct {
	caller  *Caller
	sigConn port.SignalConn
	parent  context.Context

	incoming chan IncomingCall
	errs     chan error
	done     chan struct{}

	mu        sync.Mutex
	delivered bool
	closed    bool
}

func (l *listener) Incoming() <-chan IncomingCall { return l.incoming }
func (l *listener) Errors() <-chan error          { return l.errs }

func (l *listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	delivered := l.delivered
	l.mu.Unlock()

	close(l.done)
	if !delivered {
		_ = l.sigConn.Close()
	}
	return nil
}

func (l *listener) run() {
	defer close(l.incoming)
	defer close(l.errs)

	for {
		select {
		case <-l.done:
			return
		case <-l.parent.Done():
			return
		default:
		}

		var msg struct {
			Type     string         `json:"type"`
			Envelope proto.Envelope `json:"envelope"`
		}
		if err := l.sigConn.Read(l.parent, &msg); err != nil {
			if !errors.Is(err, context.Canceled) {
				select {
				case l.errs <- err:
				default:
				}
			}
			return
		}
		if msg.Type != "envelope.deliver" {
			continue
		}

		from, found := contactByUserID(l.caller.state(), msg.Envelope.From)
		if !found {
			// Spec 002 §3.2.2: если from не в контактах — клиент
			// может отбросить envelope. Не шлём ack — пусть сервер
			// считает доставку неуспешной.
			continue
		}
		pub, err := cryptox.DecodeBase64URL(from.Pubkey, ed25519.PublicKeySize)
		if err != nil {
			continue
		}

		payload, err := DecodeEnvelopePayload(msg.Envelope, ed25519.PublicKey(pub), l.caller.priv())
		if errors.Is(err, ErrEnvelopeSignatureInvalid) {
			// Подпись не сошлась — молча отбрасываем, не шлём ack.
			continue
		}
		if err != nil {
			_ = l.ackEnvelope(msg.Envelope.ID)
			continue
		}
		if payloadString(payload, "type") != "call.offer" {
			_ = l.ackEnvelope(msg.Envelope.ID)
			continue
		}
		_ = l.ackEnvelope(msg.Envelope.ID)

		callID := payloadString(payload, "call_id")
		offerSDP := payloadString(payload, "sdp")

		// Spec 001 §5.5: сверяем DTLS fingerprint из payload с SDP.
		// Если fingerprint отсутствует или не совпадает — сервер (или MITM)
		// подменил SDP, звонок отклоняется. Никакого fallback на «нет поля».
		fpPayload, _ := payload["caller_dtls_fingerprint"].(string)
		fpSDP := parseDTLSFingerprint(offerSDP)
		if fpPayload == "" || fpSDP == "" || !cryptox.ConstantTimeEqual([]byte(fpPayload), []byte(fpSDP)) {
			continue
		}

		incoming := IncomingCall{
			From:      from,
			CallID:    callID,
			OfferSDP:  offerSDP,
			Envelope:  msg.Envelope,
			caller:    l.caller,
			signaling: l.sigConn,
			parentCtx: l.parent,
		}

		l.mu.Lock()
		l.delivered = true
		l.mu.Unlock()

		select {
		case l.incoming <- incoming:
		case <-l.done:
		}
		return // одноразовый
	}
}

func (l *listener) ackEnvelope(id string) error {
	return l.sigConn.Write(l.parent, map[string]string{"type": "envelope.ack", "id": id})
}
