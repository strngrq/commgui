package domain

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
)

// CallEventKind — тип события звонка для UI.
type CallEventKind string

const (
	// CallEventStateChange — изменение CallSession.State.
	CallEventStateChange CallEventKind = "state-change"
	// CallEventICEState — изменение ICE connection state.
	CallEventICEState CallEventKind = "ice-state"
	// CallEventRemoteHangup — пир положил трубку.
	CallEventRemoteHangup CallEventKind = "remote-hangup"
	// CallEventClosed — сессия завершена; Result содержит финальный CallResult.
	CallEventClosed CallEventKind = "closed"
	// CallEventError — асинхронная ошибка; Err заполнен.
	CallEventError CallEventKind = "error"
)

// CallEvent — событие активной сессии.
type CallEvent struct {
	Kind     CallEventKind
	State    CallState
	ICEState string
	Result   *CallResult
	Err      error
}

// CallSession — активная сессия звонка. UI владеет ею до закрытия.
type CallSession interface {
	ID() string
	Peer() Contact
	State() CallState
	Events() <-chan CallEvent
	Hangup(ctx context.Context) error
	Stats() CallResult
}

// OutgoingOpts — параметры исходящего звонка.
type OutgoingOpts struct {
	Contact          Contact
	AudioMode        string
	InputDeviceID    string
	OutputDeviceID   string
	RecordPath       string
	NoTurn           bool
	MaxDuration      time.Duration
	Ringtime         time.Duration
	PlaybackBufferMs  int
	PlaybackPrebufMs  int
	EchoCancellation  bool
	AECTailMs         int
	OnUnderrun        func(count int)
}

// AcceptOpts — параметры принятия входящего звонка.
type AcceptOpts struct {
	AudioMode        string
	InputDeviceID    string
	OutputDeviceID   string
	RecordPath       string
	NoTurn           bool
	MaxDuration      time.Duration
	PlaybackBufferMs int
	PlaybackPrebufMs int
	EchoCancellation bool
	AECTailMs        int
	OnUnderrun       func(count int)
}

// IncomingCall — входящий звонок, переданный Listener'ом.
type IncomingCall struct {
	From     Contact
	CallID   string
	OfferSDP string
	Envelope proto.Envelope

	caller    *Caller
	signaling port.SignalConn
	parentCtx context.Context
}

// Accept принимает звонок и открывает сессию. Listener при этом теряет signaling-
// соединение (не следует продолжать читать Incoming() того же Listener'а).
func (i IncomingCall) Accept(ctx context.Context, opts AcceptOpts) (CallSession, error) {
	if i.caller == nil {
		return nil, errors.New("incoming call: zero value")
	}
	return i.caller.acceptIncoming(ctx, i, opts)
}

// Decline отклоняет звонок и закрывает signaling-соединение Listener'а.
func (i IncomingCall) Decline(ctx context.Context) error {
	return i.DeclineWithReason(ctx, "")
}

// DeclineWithReason отклоняет звонок с указанной причиной (например, "busy").
func (i IncomingCall) DeclineWithReason(ctx context.Context, reason string) error {
	if i.caller == nil {
		return errors.New("incoming call: zero value")
	}
	return i.caller.declineIncoming(ctx, i, reason)
}

// WatchCancel запускает мониторинг signaling-соединения на предмет
// call.cancel / call.hangup от звонящего. При получении такого сообщения
// вызывает onCancel. Блокирует горутину до отмены или закрытия соединения.
// Используется GUI для показа/скрытия экрана входящего звонка.
func (i IncomingCall) WatchCancel(ctx context.Context, onCancel func()) {
	go func() {
		for {
			var msg struct {
				Type     string         `json:"type"`
				Envelope proto.Envelope `json:"envelope"`
			}
			if err := i.signaling.Read(ctx, &msg); err != nil {
				return
			}
			if msg.Type != "envelope.deliver" {
				continue
			}
			pub, err := cryptox.DecodeBase64URL(i.From.Pubkey, ed25519.PublicKeySize)
			if err != nil {
				continue
			}
			payload, err := DecodeEnvelopePayload(msg.Envelope, ed25519.PublicKey(pub), i.caller.priv())
			if errors.Is(err, ErrEnvelopeSignatureInvalid) {
				continue
			}
			if err != nil {
				continue
			}
			if payloadString(payload, "call_id") != i.CallID{
				continue
			}
			switch payloadString(payload, "type") {
			case "call.cancel", "call.hangup":
				onCancel()
				return
			}
		}
	}()
}

// Listener вычитывает входящие call.offer пакеты с signaling-соединения
// и отдаёт их через Incoming(). Listener одноразовый: после Accept или Decline
// его signaling-соединение передаётся в CallSession (или закрывается),
// и новых Incoming() из него не будет.
type Listener interface {
	Incoming() <-chan IncomingCall
	Errors() <-chan error
	Close() error
}

// Caller отвечает за исходящие и входящие звонки. Зависит от *Client,
// чтобы видеть актуальный state/priv после Login (см. registrar.go).
type Caller struct {
	client    *Client
	signaling port.SignalingClient
	webrtc    port.WebRTCManager
	audio     port.AudioEngine
}

// state возвращает текущий state клиента.
func (c *Caller) state() *port.State { return c.client.state }

// priv возвращает текущий приватный ключ клиента.
func (c *Caller) priv() ed25519.PrivateKey { return c.client.priv }

// ResolveContact ищет контакт по UserID или alias в state'е активного профиля.
func (c *Caller) ResolveContact(key string) (Contact, error) {
	st := c.state()
	if st == nil {
		return Contact{}, ErrNoState()
	}
	contact, ok := findContact(st, key)
	if !ok {
		return Contact{}, ErrContactNotFound(key)
	}
	return contact, nil
}

// fetchTurnCredentials получает TURN-credentials с сервера и регистрирует
// IP в allowlist через краткосрочный push-WS. Используется при старте сессии
// и при обновлении перед ICE restart.
func (c *Caller) fetchTurnCredentials(ctx context.Context) (username, credential string, ttlSec int64, uris []string, err error) {
	st := c.state()
	serverURL := chooseServer(st)

	if pushConn, wsErr := c.signaling.OpenPush(ctx, serverURL, st.Session.Token); wsErr == nil {
		_ = pushConn.Close()
	}

	resp, err := c.client.Ports().HTTP.Do(port.HTTPRequest{
		Method: "GET",
		URL:    serverURL + "/v1/turn/credentials",
		Header: map[string]string{"Authorization": "Bearer " + st.Session.Token},
	})
	if err != nil {
		return "", "", 0, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", 0, nil, fmt.Errorf("turn credentials: server returned %d", resp.StatusCode)
	}
	var creds struct {
		Username   string   `json:"username"`
		Credential string   `json:"credential"`
		TTL        int64    `json:"ttl"`
		URIs       []string `json:"uris"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return "", "", 0, nil, err
	}
	return creds.Username, creds.Credential, creds.TTL, creds.URIs, nil
}

// Outgoing открывает signaling, отправляет call.offer и возвращает CallSession.
// Сессия живёт пока её не закроют через Hangup или пока не придёт remote-hangup.
//
// Outgoing возвращает быстро: всё, что блокирует, выполняется в фоновых
// goroutines, состояние и ошибки приходят через Events().
func (c *Caller) Outgoing(ctx context.Context, opts OutgoingOpts) (CallSession, error) {
	st := c.state()
	if st == nil {
		return nil, ErrNoState()
	}
	serverURL := chooseServer(st)
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}
	if opts.Contact.UserID == "" {
		return nil, ErrContactNotFound("")
	}
	if opts.Ringtime <= 0 {
		opts.Ringtime = 30 * time.Second
	}

	pipeline, err := c.audio.Build(ctx, port.AudioOpts{
		Mode:             opts.AudioMode,
		RecordPath:       opts.RecordPath,
		MaxDuration:      opts.MaxDuration,
		InputDeviceID:    opts.InputDeviceID,
		OutputDeviceID:   opts.OutputDeviceID,
		PlaybackBufferMs: opts.PlaybackBufferMs,
		PlaybackPrebufMs: opts.PlaybackPrebufMs,
		EchoCancellation: opts.EchoCancellation,
		AECTailMs:        opts.AECTailMs,
		OnUnderrun:       opts.OnUnderrun,
	})
	if err != nil {
		return nil, err
	}

	sigConn, err := c.signaling.OpenSignal(ctx, serverURL, st.Session.Token)
	if err != nil {
		_ = pipeline.Close()
		return nil, err
	}

	rtc, err := c.webrtc.NewSession(ctx, port.WebRTCSessionOpts{
		ServerURL: serverURL,
		Token:     st.Session.Token,
		NoTurn:    opts.NoTurn,
		Audio:     pipeline,
	})
	if err != nil {
		_ = sigConn.Close()
		return nil, err
	}

	offer, err := rtc.CreateOffer()
	if err != nil {
		_ = rtc.Close()
		_ = sigConn.Close()
		return nil, err
	}
	if err := rtc.SetLocalDescription(offer); err != nil {
		_ = rtc.Close()
		_ = sigConn.Close()
		return nil, err
	}

	callID := NewID()
	offerPub, _ := cryptox.DecodeBase64URL(opts.Contact.Pubkey, ed25519.PublicKeySize)
	offerEnv, err := makeEnvelopeWithOpts(c.state(), c.priv(), opts.Contact.UserID, ed25519.PublicKey(offerPub), map[string]any{
		"type":                    "call.offer",
		"call_id":                 callID,
		"sdp":                     offer.SDP,
		"caller_dtls_fingerprint": parseDTLSFingerprint(offer.SDP),
		"ts":                      time.Now().UnixMilli(),
	}, 0, envelopeSignOpts{})
	if err != nil {
		_ = rtc.Close()
		_ = sigConn.Close()
		return nil, err
	}
	if err := sigConn.Write(ctx, map[string]any{"type": "envelope.send", "envelope": offerEnv}); err != nil {
		_ = rtc.Close()
		_ = sigConn.Close()
		return nil, err
	}

	cs := newCallSession(callSessionConfig{
		caller:      c,
		parentCtx:   ctx,
		callID:      callID,
		peer:        opts.Contact,
		audioMode:   opts.AudioMode,
		recordPath:  opts.RecordPath,
		ringtime:    opts.Ringtime,
		maxDuration: opts.MaxDuration,
		direction:   directionOutgoing,
		sigConn:     sigConn,
		rtc:         rtc,
		pipeline:       pipeline,
		sentOfferEnvID: offerEnv.ID,
	})
	go cs.run()
	return cs, nil
}

// Listen открывает signaling-соединение и вычитывает входящие call.offer.
// Каждый IncomingCall из канала может быть либо Accept'нут (open CallSession),
// либо Decline'н. После любого решения Listener закрывается.
func (c *Caller) Listen(ctx context.Context) (Listener, error) {
	st := c.state()
	if st == nil {
		return nil, ErrNoState()
	}
	serverURL := chooseServer(st)
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}
	sigConn, err := c.signaling.OpenSignal(ctx, serverURL, st.Session.Token)
	if err != nil {
		return nil, err
	}
	l := &listener{
		caller:   c,
		sigConn:  sigConn,
		incoming: make(chan IncomingCall, 1),
		errs:     make(chan error, 1),
		done:     make(chan struct{}),
		parent:   ctx,
	}
	go l.run()
	return l, nil
}

// acceptIncoming собирает CallSession для уже принятого call.offer.
// Listener'овский signaling-conn передаётся сессии и продолжает использоваться
// для ICE-trickle и hangup-обмена.
func (c *Caller) acceptIncoming(ctx context.Context, in IncomingCall, opts AcceptOpts) (CallSession, error) {
	pipeline, err := c.audio.Build(ctx, port.AudioOpts{
		Mode:             opts.AudioMode,
		RecordPath:       opts.RecordPath,
		MaxDuration:      opts.MaxDuration,
		InputDeviceID:    opts.InputDeviceID,
		OutputDeviceID:   opts.OutputDeviceID,
		PlaybackBufferMs: opts.PlaybackBufferMs,
		PlaybackPrebufMs: opts.PlaybackPrebufMs,
		EchoCancellation: opts.EchoCancellation,
		AECTailMs:        opts.AECTailMs,
		OnUnderrun:       opts.OnUnderrun,
	})
	if err != nil {
		_ = in.signaling.Close()
		return nil, err
	}

	st := c.state()
	serverURL := chooseServer(st)
	turnUser, turnCred, turnTTL, turnURIs, _ := c.fetchTurnCredentials(ctx)
	rtcOpts := port.WebRTCSessionOpts{
		ServerURL: serverURL,
		Token:     st.Session.Token,
		NoTurn:    opts.NoTurn || len(turnURIs) == 0,
		Audio:     pipeline,
	}
	if len(turnURIs) > 0 {
		rtcOpts.ICEServers = []port.ICEServer{{URLs: turnURIs, Username: turnUser, Credential: turnCred}}
	}
	rtc, err := c.webrtc.NewSession(ctx, rtcOpts)
	if err != nil {
		_ = pipeline.Close()
		_ = in.signaling.Close()
		return nil, err
	}

	if err := rtc.SetRemoteDescription(port.SDP{Type: "offer", SDP: in.OfferSDP}); err != nil {
		_ = rtc.Close()
		_ = in.signaling.Close()
		return nil, err
	}
	answer, err := rtc.CreateAnswer()
	if err != nil {
		_ = rtc.Close()
		_ = in.signaling.Close()
		return nil, err
	}
	if err := rtc.SetLocalDescription(answer); err != nil {
		_ = rtc.Close()
		_ = in.signaling.Close()
		return nil, err
	}

	acceptPub, _ := cryptox.DecodeBase64URL(in.From.Pubkey, ed25519.PublicKeySize)
	if err := c.sendEnvelope(ctx, in.signaling, in.Envelope.From, ed25519.PublicKey(acceptPub), map[string]any{
		"type":                    "call.answer",
		"call_id":                 in.CallID,
		"sdp":                     answer.SDP,
		"callee_dtls_fingerprint": parseDTLSFingerprint(answer.SDP),
		"ts":                      time.Now().UnixMilli(),
	}); err != nil {
		_ = rtc.Close()
		_ = in.signaling.Close()
		return nil, err
	}

	cs := newCallSession(callSessionConfig{
		caller:      c,
		parentCtx:   ctx,
		callID:      in.CallID,
		peer:        in.From,
		audioMode:   opts.AudioMode,
		recordPath:  opts.RecordPath,
		maxDuration: opts.MaxDuration,
		direction:   directionIncoming,
		sigConn:     in.signaling,
		rtc:         rtc,
		pipeline:    pipeline,
		startState:  CallStateActive,
		turnExpiresAt:  time.Now().Unix() + turnTTL,
		turnUsername:   turnUser,
		turnCredential: turnCred,
		turnURIs:       turnURIs,
	})
	go cs.run()
	return cs, nil
}

// declineIncoming шлёт call.reject и закрывает signaling.
func (c *Caller) declineIncoming(ctx context.Context, in IncomingCall, reason string) error {
	defer in.signaling.Close()
	payload := map[string]any{
		"type":    "call.reject",
		"call_id": in.CallID,
		"ts":      time.Now().UnixMilli(),
	}
	if reason != "" {
		payload["reason"] = reason
	}
	declinePub, _ := cryptox.DecodeBase64URL(in.From.Pubkey, ed25519.PublicKeySize)
	return c.sendEnvelope(ctx, in.signaling, in.Envelope.From, ed25519.PublicKey(declinePub), payload)
}

// sendEnvelope собирает envelope и отправляет его как envelope.send.
// thread-safety обеспечивает сам caller (для callSession — через session.writeMu).
func (c *Caller) sendEnvelope(ctx context.Context, conn port.SignalConn, to string, recipientPub ed25519.PublicKey, payload map[string]any) error {
	env, err := makeEnvelopeWithOpts(c.state(), c.priv(), to, recipientPub, payload, 0, envelopeSignOpts{})
	if err != nil {
		return err
	}
	return conn.Write(ctx, map[string]any{"type": "envelope.send", "envelope": env})
}

// payloadString безопасно достаёт строковое поле из payload.
func payloadString(payload map[string]any, key string) string {
	v, _ := payload[key].(string)
	return v
}

// candidateFromPayload парсит call.candidate payload в port.ICECandidate.
func candidateFromPayload(payload map[string]any) (port.ICECandidate, error) {
	raw, err := json.Marshal(payload["candidate"])
	if err != nil {
		return port.ICECandidate{}, err
	}
	var init struct {
		Candidate     string  `json:"candidate"`
		SDPMid        *string `json:"sdpMid"`
		SDPMLineIndex *uint16 `json:"sdpMLineIndex"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		return port.ICECandidate{}, err
	}
	if init.Candidate == "" {
		return port.ICECandidate{}, fmt.Errorf("empty ice candidate")
	}
	cand := port.ICECandidate{Candidate: init.Candidate}
	if init.SDPMid != nil {
		cand.SDPMid = *init.SDPMid
	}
	if init.SDPMLineIndex != nil {
		cand.SDPMLineIndex = *init.SDPMLineIndex
	}
	return cand, nil
}

// parseDTLSFingerprint извлекает значение a=fingerprint:sha-256 из SDP.
// Возвращает hex-строку (без двоеточий) или пустую строку, если fingerprint
// не найден.
func parseDTLSFingerprint(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=fingerprint:sha-256 ") {
			continue
		}
		hex := strings.TrimSpace(strings.TrimPrefix(line, "a=fingerprint:sha-256 "))
		// Убираем двоеточия: "4A:AD:2B:..." → "4AAD2B..."
		return strings.ReplaceAll(hex, ":", "")
	}
	return ""
}
