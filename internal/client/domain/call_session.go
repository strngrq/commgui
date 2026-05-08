package domain

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
)

type callDirection int

const (
	directionOutgoing callDirection = iota
	directionIncoming
)

type callSessionConfig struct {
	caller *Caller

	parentCtx context.Context

	callID      string
	peer        Contact
	audioMode   string
	recordPath  string
	ringtime    time.Duration
	maxDuration time.Duration

	direction callDirection

	sigConn  port.SignalConn
	rtc      port.WebRTCSession
	pipeline port.AudioPipeline

	// startState — начальное состояние сессии. Для outgoing — "calling" (по умолчанию),
	// для incoming-after-accept — "active".
	startState CallState

	// sentOfferEnvID — ID envelope, которым был отправлен call.offer.
	// Нужен для сопоставления envelope.failed от сервера.
	sentOfferEnvID string

	// TURN credentials для ICE restart с обновлением.
	turnExpiresAt  int64
	turnUsername   string
	turnCredential string
	turnURIs       []string
}

// callSession — реализация CallSession.
type callSession struct {
	cfg     callSessionConfig
	started time.Time

	mu     sync.Mutex
	state  CallState
	closed bool

	events chan CallEvent

	writeMu sync.Mutex // serialise writes to sigConn

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once

	finalResult CallResult
	resultReady chan struct{}

	// peerPub — декодированный ed25519 публичный ключ пира. Используется
	// для проверки подписи каждого входящего envelope.
	peerPub ed25519.PublicKey

	// restartMu защищает гонку между таймером restartTimeout и
	// pumpRTCEvents, чтобы не запустить два ICE restart одновременно.
	restartMu    sync.Mutex
	restarting   bool
	restartTimer *time.Timer

	// TURN credentials для долгих звонков (>1ч). Обновляются через
	// refreshTurnCredentials перед ICE restart, если близки к истечению.
	turnExpiresAt  int64
	turnUsername   string
	turnCredential string
	turnURIs       []string
}

func newCallSession(cfg callSessionConfig) *callSession {
	parent := cfg.parentCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if cfg.maxDuration > 0 {
		ctx, cancel = context.WithTimeout(parent, cfg.maxDuration)
	}

	startState := cfg.startState
	if startState == "" {
		if cfg.direction == directionOutgoing {
			startState = CallStateCalling
		} else {
			startState = CallStateActive
		}
	}

	peerPub, _ := cryptox.DecodeBase64URL(cfg.peer.Pubkey, ed25519.PublicKeySize)

	return &callSession{
		cfg:            cfg,
		started:        time.Now(),
		state:          startState,
		events:         make(chan CallEvent, 16),
		ctx:            ctx,
		cancel:         cancel,
		resultReady:    make(chan struct{}),
		peerPub:        ed25519.PublicKey(peerPub),
		turnExpiresAt:  cfg.turnExpiresAt,
		turnUsername:   cfg.turnUsername,
		turnCredential: cfg.turnCredential,
		turnURIs:       cfg.turnURIs,
	}
}

// ID возвращает идентификатор звонка.
func (s *callSession) ID() string { return s.cfg.callID }

// Peer возвращает контакт-собеседника.
func (s *callSession) Peer() Contact { return s.cfg.peer }

// State возвращает текущее состояние.
func (s *callSession) State() CallState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Events возвращает канал событий.
func (s *callSession) Events() <-chan CallEvent { return s.events }

// Stats возвращает текущий снимок CallResult (живой, не финальный).
func (s *callSession) Stats() CallResult {
	stats := s.cfg.rtc.Stats()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		<-s.resultReady
		return s.finalResult
	}
	return CallResult{
		CallID:      s.cfg.callID,
		Outcome:     "in-progress",
		DurationMs:  time.Since(s.started).Milliseconds(),
		ICEState:    stats.ICEState,
		AudioMode:   s.cfg.audioMode,
		RecordPath:  s.cfg.recordPath,
		RTPSent:     stats.RTPSent,
		RTPReceived: stats.RTPReceived,
	}
}

// Hangup посылает call.hangup и закрывает сессию.
func (s *callSession) Hangup(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.state = CallStateEnding
	s.mu.Unlock()
	s.emit(CallEvent{Kind: CallEventStateChange, State: CallStateEnding})

	hangupCtx := ctx
	if hangupCtx == nil {
		var cancel context.CancelFunc
		hangupCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
	}
	err := s.sendPayload(hangupCtx, map[string]any{
		"type":    "call.hangup",
		"call_id": s.cfg.callID,
		"ts":      time.Now().UnixMilli(),
	})
	s.shutdown("hangup")
	return err
}

// run управляет жизненным циклом сессии. Запускается из caller'а после Outgoing/Accept.
func (s *callSession) run() {
	defer s.shutdownWith("")

	go s.pumpRTCEvents()
	go s.pumpLocalCandidates()

	if s.cfg.direction == directionOutgoing {
		go s.ringTimeoutWatchdog()
	}

	s.pumpSignaling()
}

// pumpSignaling — основной читатель signaling-канала. Маршрутизирует
// call.offer (ICE restart) / call.answer / call.reject / call.candidate / call.hangup / envelope.failed.
func (s *callSession) pumpSignaling() {
	for {
		var msg struct {
			Type     string         `json:"type"`
			Envelope proto.Envelope `json:"envelope"`
			ID       string         `json:"id"`
			Reason   string         `json:"reason"`
		}
		if err := s.cfg.sigConn.Read(s.ctx, &msg); err != nil {
			if !errors.Is(err, context.Canceled) {
				s.emit(CallEvent{Kind: CallEventError, Err: err})
			}
			return
		}
		if msg.Type == "envelope.failed" {
			if s.cfg.sentOfferEnvID != "" && msg.ID == s.cfg.sentOfferEnvID {
				s.endWithOutcome("failed")
				return
			}
			continue
		}
		if msg.Type != "envelope.deliver" {
			continue
		}
		payload, err := DecodeEnvelopePayload(msg.Envelope, s.peerPub, s.cfg.caller.priv())
		if errors.Is(err, ErrEnvelopeSignatureInvalid) {
			continue
		}
		if err != nil {
			_ = s.ackEnvelope(msg.Envelope.ID)
			continue
		}
		_ = s.ackEnvelope(msg.Envelope.ID)
		if payloadString(payload, "call_id") != s.cfg.callID {
			continue
		}

		switch payloadString(payload, "type") {
		case "call.offer":
			// ICE restart: пир прислал новый offer с тем же call_id.
			// Принимаем офер, генерируем answer с новыми кандидатами.
			s.handleRestartOffer(payload)
		case "call.answer":
			s.handleAnswer(payload)
		case "call.reject":
			s.endWithOutcome("declined")
			return
		case "call.candidate":
			if c, err := candidateFromPayload(payload); err == nil {
				s.emit(CallEvent{Kind: CallEventRemoteCandidate, Detail: candidateDetail(c.Candidate, c.SDPMid, int(c.SDPMLineIndex))})
				if addErr := s.cfg.rtc.AddRemoteCandidate(c); addErr != nil {
					s.emit(CallEvent{Kind: CallEventError, Err: addErr, Detail: map[string]any{"phase": "add-remote-candidate", "candidate": c.Candidate}})
				}
			} else {
				s.emit(CallEvent{Kind: CallEventError, Err: err, Detail: map[string]any{"phase": "parse-remote-candidate"}})
			}
		case "call.hangup", "call.cancel":
			s.emit(CallEvent{Kind: CallEventRemoteHangup})
			s.endWithOutcome("completed")
			return
		}
	}
}

// pumpRTCEvents транслирует port.SessionEvent в CallEvent.
func (s *callSession) pumpRTCEvents() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-s.cfg.rtc.Events():
			if !ok {
				return
			}
			switch ev.Kind {
			case port.SessionEventConnected:
				s.mu.Lock()
				s.state = CallStateActive
				s.mu.Unlock()
				s.emit(CallEvent{Kind: CallEventStateChange, State: CallStateActive, ICEState: ev.ICEState})
			case port.SessionEventICEState:
				s.emit(CallEvent{Kind: CallEventICEState, ICEState: ev.ICEState})
			case port.SessionEventNeedsRestart:
				s.startICERestart()
			case port.SessionEventClosed:
				if ev.Err != nil {
					s.emit(CallEvent{Kind: CallEventError, Err: ev.Err})
				}
				s.endWithOutcome("completed")
				return
			}
		}
	}
}

// shouldRefreshTurn возвращает true, если TURN credentials истекают
// в ближайшие 5 минут и их пора обновить.
func (s *callSession) shouldRefreshTurn() bool {
	if s.turnExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() >= s.turnExpiresAt-300
}

// refreshTurnCredentials получает свежие TURN-credentials с сервера,
// регистрирует IP в allowlist и пересоздаёт PeerConnection с новыми creds.
func (s *callSession) refreshTurnCredentials() error {
	st := s.cfg.caller.state()
	serverURL := chooseServer(st)
	token := st.Session.Token

	// 1. Открываем push-WS чтобы зарегистрировать IP в peers-allowlist.
	if pushConn, err := s.cfg.caller.signaling.OpenPush(s.ctx, serverURL, token); err == nil {
		_ = pushConn.Close()
	}

	// 2. GET /turn/credentials
	resp, err := s.cfg.caller.client.Ports().HTTP.Do(port.HTTPRequest{
		Method: "GET",
		URL:    serverURL + "/v1/turn/credentials",
		Header: map[string]string{"Authorization": "Bearer " + token},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("turn credentials: server returned %d", resp.StatusCode)
	}
	var creds struct {
		Username   string   `json:"username"`
		Credential string   `json:"credential"`
		TTL        int64    `json:"ttl"`
		URIs       []string `json:"uris"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return err
	}

	// 3. Сохраняем новые credentials.
	s.turnExpiresAt = time.Now().Unix() + creds.TTL
	s.turnUsername = creds.Username
	s.turnCredential = creds.Credential
	s.turnURIs = creds.URIs

	// 4. Пересоздаём PeerConnection с новыми creds.
	return s.recreatePC()
}

// recreatePC закрывает старый PeerConnection и создаёт новый с текущими
// TURN-credentials. Вызывается после успешного refreshTurnCredentials.
func (s *callSession) recreatePC() error {
	newRTC, err := s.cfg.caller.webrtc.NewSession(s.ctx, port.WebRTCSessionOpts{
		ServerURL: chooseServer(s.cfg.caller.state()),
		Token:     s.cfg.caller.state().Session.Token,
		NoTurn:    len(s.turnURIs) == 0,
		Audio:     s.cfg.pipeline,
		ICEServers: []port.ICEServer{{
			URLs:       s.turnURIs,
			Username:   s.turnUsername,
			Credential: s.turnCredential,
		}},
	})
	if err != nil {
		return err
	}

	// Закрываем старый PC. Аудио-pipeline продолжает жить — новый PC
	// заново вызовет attachAudio и привяжет свежий трек.
	_ = s.cfg.rtc.Close()
	s.cfg.rtc = newRTC

	// Перезапускаем сбор локальных кандидатов для нового PC.
	go s.pumpLocalCandidates()
	// Перезапускаем трансляцию RTC-событий.
	go s.pumpRTCEvents()
	return nil
}

// startICERestart инициирует ICE restart: создаёт новый offer с ICE restart flag
// и отправляет его пиру. Защищён от повторного входа через restartMu.
func (s *callSession) startICERestart() {
	s.restartMu.Lock()
	if s.restarting {
		s.restartMu.Unlock()
		return
	}
	s.restarting = true
	s.restartMu.Unlock()

	defer func() {
		s.restartMu.Lock()
		s.restarting = false
		s.restartMu.Unlock()
	}()

	s.emit(CallEvent{Kind: CallEventICEState, ICEState: "restarting"})

	// Если TURN credentials близки к истечению — обновляем перед restart.
	// refreshTurnCredentials при успехе пересоздаст PC с новыми creds;
	// при ошибке продолжаем со старыми (allocation могла ещё не протухнуть).
	if s.shouldRefreshTurn() {
		_ = s.refreshTurnCredentials()
	}

	offer, err := s.cfg.rtc.CreateOffer()
	if err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}
	if err := s.cfg.rtc.SetLocalDescription(offer); err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}

	offerFingerprint := parseDTLSFingerprint(offer.SDP)
	payload := map[string]any{
		"type":                    "call.offer",
		"call_id":                 s.cfg.callID,
		"sdp":                     offer.SDP,
		"caller_dtls_fingerprint": offerFingerprint,
		"ts":                      time.Now().UnixMilli(),
	}
	if err := s.sendPayload(context.Background(), payload); err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}
}

// handleRestartOffer обрабатывает входящий call.offer в рамках активной сессии
// (ICE restart от пира). Принимает новый SDP, генерирует answer и отправляет пиру.
func (s *callSession) handleRestartOffer(payload map[string]any) {
	sdp := payloadString(payload, "sdp")
	if sdp == "" {
		s.emit(CallEvent{Kind: CallEventError, Err: ErrInvalidCallAnswer("missing SDP in restart offer")})
		s.endWithOutcome("failed")
		return
	}

	// Сверяем DTLS fingerprint.
	fpPayload, _ := payload["caller_dtls_fingerprint"].(string)
	fpSDP := parseDTLSFingerprint(sdp)
	if fpPayload == "" || fpSDP == "" || !cryptox.ConstantTimeEqual([]byte(fpPayload), []byte(fpSDP)) {
		s.emit(CallEvent{Kind: CallEventError, Err: ErrInvalidCallAnswer("DTLS fingerprint mismatch in restart offer")})
		s.endWithOutcome("failed")
		return
	}

	if err := s.cfg.rtc.SetRemoteDescription(port.SDP{Type: "offer", SDP: sdp}); err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}

	answer, err := s.cfg.rtc.CreateAnswer()
	if err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}
	if err := s.cfg.rtc.SetLocalDescription(answer); err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}

	answerFingerprint := parseDTLSFingerprint(answer.SDP)
	answerPayload := map[string]any{
		"type":                    "call.answer",
		"call_id":                 s.cfg.callID,
		"sdp":                     answer.SDP,
		"callee_dtls_fingerprint": answerFingerprint,
		"ts":                      time.Now().UnixMilli(),
	}
	if err := s.sendPayload(context.Background(), answerPayload); err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err})
		s.endWithOutcome("failed")
		return
	}
}

// pumpLocalCandidates пересылает кандидатов от webrtc в сторону пира.
func (s *callSession) pumpLocalCandidates() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case c, ok := <-s.cfg.rtc.LocalCandidates():
			if !ok {
				return
			}
			candPayload := map[string]any{
				"candidate":     c.Candidate,
				"sdpMid":        c.SDPMid,
				"sdpMLineIndex": c.SDPMLineIndex,
			}
			s.emit(CallEvent{Kind: CallEventLocalCandidate, Detail: candidateDetail(c.Candidate, c.SDPMid, int(c.SDPMLineIndex))})
			_ = s.sendPayload(s.ctx, map[string]any{
				"type":      "call.candidate",
				"call_id":   s.cfg.callID,
				"candidate": candPayload,
				"ts":        time.Now().UnixMilli(),
			})
		}
	}
}

// candidateDetail парсит SDP candidate-строку (RFC 5245) и возвращает
// поля для лога: тип (host/srflx/relay/prflx), транспорт, адрес, порт.
// Формат: "candidate:<foundation> <component> <transport> <priority> <addr> <port> typ <type> ..."
func candidateDetail(candidate, sdpMid string, sdpMLineIndex int) map[string]any {
	d := map[string]any{
		"candidate":     candidate,
		"sdpMid":        sdpMid,
		"sdpMLineIndex": sdpMLineIndex,
	}
	parts := splitFields(candidate)
	if len(parts) >= 8 {
		d["protocol"] = parts[2]
		d["addr"] = parts[4]
		d["port"] = parts[5]
		// "typ" в позиции 6, тип — 7.
		if parts[6] == "typ" {
			d["type"] = parts[7]
		}
	}
	return d
}

// splitFields — простой токенизатор по пробелам без аллокации regexp'а.
func splitFields(s string) []string {
	out := make([]string, 0, 12)
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// ringTimeoutWatchdog для исходящих звонков: если за ringtime не пришёл answer,
// шлём call.cancel и закрываемся с outcome=timeout.
func (s *callSession) ringTimeoutWatchdog() {
	if s.cfg.ringtime <= 0 {
		return
	}
	timer := time.NewTimer(s.cfg.ringtime)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-timer.C:
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()
		if state == CallStateCalling {
			_ = s.sendPayload(s.ctx, map[string]any{
				"type":    "call.cancel",
				"call_id": s.cfg.callID,
				"ts":      time.Now().UnixMilli(),
			})
			s.endWithOutcome("timeout")
		}
	}
}

func (s *callSession) handleAnswer(payload map[string]any) {
	sdp := payloadString(payload, "sdp")
	if sdp == "" {
		s.emit(CallEvent{Kind: CallEventError, Err: ErrInvalidCallAnswer("missing SDP")})
		s.endWithOutcome("failed")
		return
	}
	// Spec 001 §5.5: сверяем DTLS fingerprint из payload с SDP.
	// Если fingerprint отсутствует или не совпадает — сервер (или MITM)
	// подменил SDP, звонок отклоняется. Никакого fallback на «нет поля».
	fpPayload, _ := payload["callee_dtls_fingerprint"].(string)
	fpSDP := parseDTLSFingerprint(sdp)
	s.emit(CallEvent{Kind: CallEventAnswerReceived, Detail: map[string]any{
		"call_id":          s.cfg.callID,
		"dtls_fp_payload":  fpPayload,
		"dtls_fp_sdp":      fpSDP,
		"sdp_lines":        countSDPLines(sdp),
		"fp_match":         fpPayload != "" && fpSDP != "" && cryptox.ConstantTimeEqual([]byte(fpPayload), []byte(fpSDP)),
	}})
	if fpPayload == "" || fpSDP == "" || !cryptox.ConstantTimeEqual([]byte(fpPayload), []byte(fpSDP)) {
		s.emit(CallEvent{Kind: CallEventError, Err: ErrInvalidCallAnswer("DTLS fingerprint mismatch")})
		s.endWithOutcome("failed")
		return
	}
	if err := s.cfg.rtc.SetRemoteDescription(port.SDP{Type: "answer", SDP: sdp}); err != nil {
		s.emit(CallEvent{Kind: CallEventError, Err: err, Detail: map[string]any{"phase": "set-remote-answer"}})
		s.endWithOutcome("failed")
		return
	}
	// State transition к "active" произойдёт после ICE-connected,
	// который придёт через port.SessionEventConnected.
}

func (s *callSession) endWithOutcome(outcome string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.shutdownWith(outcome)
}

func (s *callSession) shutdown(outcome string) {
	s.shutdownWith(outcome)
}

// shutdownWith однажды закрывает сессию: формирует финальный CallResult,
// отправляет CallEventClosed, закрывает каналы и зависимости.
//
// Если outcome пустой, он выводится из текущего состояния и stats:
//   - peer ответил и ICE подключился → "answered"
//   - peer ответил, но ICE не подключился (rare) → "failed"
//   - сессия закрылась до active в outgoing → "failed"
//   - всё остальное (incoming after active, normal hangup) → "completed".
func (s *callSession) shutdownWith(outcome string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		prevState := s.state
		s.mu.Unlock()

		s.restartMu.Lock()
		if s.restartTimer != nil {
			s.restartTimer.Stop()
		}
		s.restartMu.Unlock()

		// Graceful-exit (outcome пустой) и сессия дошла до active — отправляем
		// call.hangup, чтобы пир смог корректно закрыть свою сторону. Делаем это
		// до cancel() и Close(), пока sigConn ещё жив.
		if outcome == "" && prevState == CallStateActive {
			sendCtx, sendCancel := context.WithTimeout(context.Background(), time.Second)
			_ = s.sendPayload(sendCtx, map[string]any{
				"type":    "call.hangup",
				"call_id": s.cfg.callID,
				"ts":      time.Now().UnixMilli(),
			})
			sendCancel()
		}

		s.cancel()

		stats := s.cfg.rtc.Stats()
		// Если ICE когда-либо подключался, financial outcome — "answered" вне зависимости
		// от того, как именно нас закрыли (peer hangup, ctx timeout, normal exit).
		if stats.Connected && (outcome == "" || outcome == "completed") {
			outcome = "answered"
		}
		if outcome == "" {
			if prevState == CallStateCalling {
				outcome = "failed"
			} else {
				outcome = "completed"
			}
		}
		result := CallResult{
			CallID:            s.cfg.callID,
			Outcome:           outcome,
			DurationMs:        time.Since(s.started).Milliseconds(),
			ICEState:          stats.ICEState,
			DTLSFingerprintOK: stats.Connected,
			AudioMode:         s.cfg.audioMode,
			RecordPath:        s.cfg.recordPath,
			RTPSent:           stats.RTPSent,
			RTPReceived:       stats.RTPReceived,
		}

		s.mu.Lock()
		s.closed = true
		s.state = CallStateClosed
		s.finalResult = result
		// Выталкиваем CallEventClosed под тем же mutex'ом, что и close(events),
		// чтобы emit() из конкурентных goroutine не отправил в закрытый канал.
		select {
		case s.events <- CallEvent{Kind: CallEventClosed, State: CallStateClosed, ICEState: stats.ICEState, Result: &result}:
		default:
		}
		close(s.events)
		s.mu.Unlock()

		close(s.resultReady)

		_ = s.cfg.rtc.Close()
		// Audio pipeline closes via rtc.Close(); sigConn — мы.
		_ = s.cfg.sigConn.Close()
	})
}

// emit безопасно для конкурентных горутин: проверяет s.closed под mu,
// и шлёт в s.events только если канал ещё не закрыт.
func (s *callSession) emit(ev CallEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.events <- ev:
	default:
	}
}

func (s *callSession) sendPayload(ctx context.Context, payload map[string]any) error {
	env, err := makeEnvelopeWithOpts(s.cfg.caller.state(), s.cfg.caller.priv(), s.cfg.peer.UserID, s.peerPub, payload, 0, envelopeSignOpts{})
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.cfg.sigConn.Write(ctx, map[string]any{"type": "envelope.send", "envelope": env})
}

func (s *callSession) ackEnvelope(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.cfg.sigConn.Write(s.ctx, map[string]string{"type": "envelope.ack", "id": id})
}
