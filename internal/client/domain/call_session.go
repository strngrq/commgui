package domain

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/strngrq/commgui/internal/client/domain/callfsm"
	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
)

type callDirection int

const (
	directionOutgoing callDirection = iota
	directionIncoming
)

// ForeignOffer — чужой call.offer, пришедший на sigConn активной сессии (§6a).
type ForeignOffer struct {
	Envelope   proto.Envelope
	From       Contact
	CallID     string
	OfferSDP   string
	DTLSFp     string
	ReceivedAt time.Time
}

// callTiming — метки времени для профилирования latency звонка.
type callTiming struct {
	BootstrapEnd time.Time // bootstrap завершён, actorLoop стартует

	FirstLocalCandidate  time.Time // первый EvLocalCandidate
	FirstRemoteCandidate time.Time // первый EvRemoteCandidate
	OfferSent            time.Time // call.offer отправлен (ActSendEnvelope offer)
	AnswerReceived       time.Time // call.answer получен
	AnswerSent           time.Time // call.answer отправлен
	ICEConnected         time.Time // ICE перешёл в connected
	FirstEventQRead      time.Time // первое событие прочитано из eventQ

	EventQLatencySum  time.Duration // сумма задержек eventQ
	EventQEvents      int           // число замеров eventQ latency
	BootstrapDuration time.Duration // длительность bootstrap'а
}

// Detail возвращает map с ключевыми длительностями для лога.
// Все смещения — относительно BootstrapEnd (момент старта actorLoop).
// Поля присутствуют, только если соответствующая метка была записана —
// это позволяет по выводу видеть, на каком этапе застрял звонок.
func (t callTiming) Detail() map[string]any {
	d := map[string]any{}
	if t.BootstrapEnd.IsZero() {
		return d
	}
	d["bootstrap_ms"] = t.BootstrapDuration.Milliseconds()
	if !t.FirstEventQRead.IsZero() {
		d["first_event_ms"] = t.FirstEventQRead.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.OfferSent.IsZero() {
		d["offer_sent_ms"] = t.OfferSent.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.AnswerReceived.IsZero() {
		d["answer_received_ms"] = t.AnswerReceived.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.AnswerSent.IsZero() {
		d["answer_sent_ms"] = t.AnswerSent.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.FirstLocalCandidate.IsZero() {
		d["first_local_cand_ms"] = t.FirstLocalCandidate.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.FirstRemoteCandidate.IsZero() {
		d["first_remote_cand_ms"] = t.FirstRemoteCandidate.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.ICEConnected.IsZero() {
		d["ice_connected_ms"] = t.ICEConnected.Sub(t.BootstrapEnd).Milliseconds()
		d["setup_ms"] = t.ICEConnected.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.AnswerReceived.IsZero() && !t.OfferSent.IsZero() {
		d["offer_to_answer_ms"] = t.AnswerReceived.Sub(t.OfferSent).Milliseconds()
	}
	if t.EventQEvents > 0 {
		d["eventq_avg_us"] = (t.EventQLatencySum / time.Duration(t.EventQEvents)).Microseconds()
		d["eventq_events"] = t.EventQEvents
	}
	return d
}

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

	// startState — начальное состояние сессии (wire-формат).
	startState CallState
	// initialFSMState — начальное состояние FSM. Если не задано,
	// вычисляется через wireToFSM(startState).
	initialFSMState callfsm.State

	// sentOfferEnvID — ID envelope, которым был отправлен call.offer.
	sentOfferEnvID string

	// onForeignOffer — callback для glare-детекции (§6a). Вызывается из
	// pumpSignaling при получении call.offer с другим callID.
	onForeignOffer func(ForeignOffer)

	// TURN credentials для ICE restart с обновлением.
	turnExpiresAt  int64
	turnUsername   string
	turnCredential string
	turnURIs       []string
}

// callSession — реализация CallSession.
// Шаг 5: состояние мутируется только из actor-loop через Transition.
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

	// peerPub — декодированный ed25519 публичный ключ пира.
	peerPub ed25519.PublicKey

	// TURN credentials для долгих звонков (>1ч).
	turnExpiresAt  int64
	turnUsername   string
	turnCredential string
	turnURIs       []string

	// --- FSM fields (Step 5: primary) ---
	eventQ   chan callfsm.Event
	fsmState callfsm.State

	// sentRestartOfferEnvID — ID envelope с restart-offer'ом для EvEnvelopeFailed.
	sentRestartOfferEnvID string

	// lastCreatedSDP/FP — результат последнего CreateOffer/CreateAnswer.
	// Инвариант: единовременно только один Create* в полёте (гарантируется FSM).
	lastCreatedSDP string
	lastCreatedFP  string

	// timing — метки времени для профилирования latency. Заполняются в actorLoop
	// при ключевых переходах, читаются в shutdownWith для лога call_closed.
	timing callTiming

	// keepSigConn — если true, shutdownWith не закроет sigConn (§6a glare yield).
	keepSigConn bool
	// failureReason — причина отказа для CallResult (§6c).
	failureReason string

	// rtcMu защищает s.cfg.rtc от гонки между recreatePC (write) и
	// асинхронными action-горутинами (read).
	rtcMu sync.RWMutex

	// timersMu защищает map FSM-таймеров.
	timersMu sync.Mutex
	timers   map[string]*time.Timer
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

	fsmSt := cfg.initialFSMState
	if fsmSt == callfsm.StateInvalid {
		fsmSt = wireToFSM(startState)
	}

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

		// FSM fields
		eventQ:   make(chan callfsm.Event, 64),
		fsmState: fsmSt,
		timers:   make(map[string]*time.Timer),
	}
}

// wireToFSM maps a wire-compatible port.CallState to the FSM State.
func wireToFSM(s port.CallState) callfsm.State {
	switch s {
	case port.CallStateIdle:
		return callfsm.StateIdle
	case port.CallStateCalling:
		return callfsm.StateCalling
	case port.CallStateIncoming:
		return callfsm.StateRinging
	case port.CallStateActive:
		return callfsm.StateActive
	case port.CallStateEnding:
		return callfsm.StateEnding
	case port.CallStateClosed:
		return callfsm.StateClosed
	default:
		return callfsm.StateIdle
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
	var stats port.SessionStats
	if s.cfg.rtc != nil {
		s.rtcMu.RLock()
		stats = s.cfg.rtc.Stats()
		s.rtcMu.RUnlock()
	}
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

// Hangup посылает call.hangup через FSM и ждёт завершения сессии.
func (s *callSession) Hangup(ctx context.Context) error {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}
	select {
	case s.eventQ <- callfsm.Event{Kind: callfsm.EvUserHangup}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-s.resultReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// YieldSigConn уступает звонок встречному glare-оферу (§6a). Отправляет
// EvYieldGlare в FSM, ждёт завершения shutdown и возвращает живой sigConn.
// Использовать только из onForeignOffer-обработчика на App-уровне.
func (s *callSession) YieldSigConn() (port.SignalConn, error) {
	select {
	case s.eventQ <- callfsm.Event{Kind: callfsm.EvYieldGlare}:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	select {
	case <-s.resultReady:
		if s.finalResult.Outcome != "yielded-glare" {
			return nil, fmt.Errorf("session closed before yield: %s", s.finalResult.Outcome)
		}
		return s.cfg.sigConn, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// run запускает жизненный цикл сессии. Шаг 5: actorLoop — главный цикл,
// все источники событий (pumpSignaling, pumpRTCEvents, pumpLocalCandidates)
// поститят в eventQ. Состояние мутируется только через Transition.
func (s *callSession) run() {
	s.timing.BootstrapEnd = time.Now()
	s.timing.BootstrapDuration = s.timing.BootstrapEnd.Sub(s.started)

	// Start initial timers.
	if s.cfg.direction == directionOutgoing && s.cfg.ringtime > 0 {
		s.startFSMTimer("ringTimeout", s.cfg.ringtime)
	}
	if s.cfg.maxDuration > 0 {
		s.startFSMTimer("maxDuration", s.cfg.maxDuration)
	}

	go s.pumpSignaling()
	if s.cfg.rtc != nil {
		go s.pumpRTCEvents()
		go s.pumpLocalCandidates()
	}

	s.actorLoop()
}

// pumpSignaling читает signaling-канал и постит события в eventQ.
// Валидация (подпись, callID, fp) — на стороне Transition (applyAction).
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
				select {
				case s.eventQ <- callfsm.Event{Kind: callfsm.EvSignalReadErr, Err: err}:
				case <-s.ctx.Done():
				}
			}
			return
		}
		if msg.Type == "envelope.failed" {
			if s.matchSentOffer(msg.ID) {
				select {
				case s.eventQ <- callfsm.Event{Kind: callfsm.EvEnvelopeFailed, EnvelopeID: msg.ID}:
				case <-s.ctx.Done():
				}
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
		callID := payloadString(payload, "call_id")
		if callID != s.cfg.callID {
			if payloadString(payload, "type") == "call.offer" && s.cfg.onForeignOffer != nil {
				from, found := contactByUserID(s.cfg.caller.state(), msg.Envelope.From)
				if !found {
					// Чужой offer от неизвестного отправителя — молча
					// отбрасываем, но логируем для видимости (§6a #4).
					continue
				}
				s.cfg.onForeignOffer(ForeignOffer{
					Envelope:   msg.Envelope,
					From:       from,
					CallID:     callID,
					OfferSDP:   payloadString(payload, "sdp"),
					DTLSFp:     payloadString(payload, "caller_dtls_fingerprint"),
					ReceivedAt: time.Now(),
				})
			}
			continue
		}

		ev := s.mapSignalingEvent(payload)
		if ev.Kind != callfsm.EvInvalid {
			select {
			case s.eventQ <- ev:
			case <-s.ctx.Done():
			}
		}
	}
}

// mapSignalingEvent converts a decrypted signaling payload to an FSM event.
func (s *callSession) mapSignalingEvent(payload map[string]any) callfsm.Event {
	switch payloadString(payload, "type") {
	case "call.offer":
		return callfsm.Event{
			Kind:   callfsm.EvOfferReceived,
			SDP:    payloadString(payload, "sdp"),
			DTLSFp: payloadString(payload, "caller_dtls_fingerprint"),
			CallID: payloadString(payload, "call_id"),
		}
	case "call.answer":
		return callfsm.Event{
			Kind:   callfsm.EvAnswerReceived,
			SDP:    payloadString(payload, "sdp"),
			DTLSFp: payloadString(payload, "callee_dtls_fingerprint"),
			CallID: payloadString(payload, "call_id"),
		}
	case "call.reject":
		return callfsm.Event{Kind: callfsm.EvPeerReject, Reason: payloadString(payload, "reason")}
	case "call.candidate":
		c, err := candidateFromPayload(payload)
		if err != nil {
			return callfsm.Event{Kind: callfsm.EvInvalid}
		}
		// Emit UI event directly (not state-transitioning).
		s.emit(CallEvent{Kind: CallEventRemoteCandidate, Detail: candidateDetail(c.Candidate, c.SDPMid, int(c.SDPMLineIndex))})
		return callfsm.Event{Kind: callfsm.EvRemoteCandidate, Candidate: c}
	case "call.hangup":
		s.emit(CallEvent{Kind: CallEventRemoteHangup})
		return callfsm.Event{Kind: callfsm.EvPeerHangup}
	case "call.cancel":
		s.emit(CallEvent{Kind: CallEventRemoteHangup})
		return callfsm.Event{Kind: callfsm.EvPeerCancel}
	default:
		return callfsm.Event{Kind: callfsm.EvInvalid}
	}
}

// matchSentOffer returns true if envID matches our sent offer or restart offer.
func (s *callSession) matchSentOffer(envID string) bool {
	if envID == "" {
		return false
	}
	if s.cfg.sentOfferEnvID != "" && envID == s.cfg.sentOfferEnvID {
		return true
	}
	if s.sentRestartOfferEnvID != "" && envID == s.sentRestartOfferEnvID {
		return true
	}
	return false
}

// pumpRTCEvents транслирует port.SessionEvent в FSM-события.
func (s *callSession) pumpRTCEvents() {
	s.rtcMu.RLock()
	rtcEvents := s.cfg.rtc.Events()
	s.rtcMu.RUnlock()
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-rtcEvents:
			if !ok {
				return
			}
			if fsmEv, ok := mapSessionEvent(ev); ok {
				select {
				case s.eventQ <- fsmEv:
				default:
				}
			}
		}
	}
}

// mapSessionEvent converts a port.SessionEvent to a callfsm.Event.
// Returns (Event, false) for events that shouldn't be posted to FSM shadow.
func mapSessionEvent(ev port.SessionEvent) (callfsm.Event, bool) {
	switch ev.Kind {
	case port.SessionEventConnected:
		return callfsm.Event{Kind: callfsm.EvICEStateChange, ICEState: "connected"}, true
	case port.SessionEventICEState:
		return callfsm.Event{Kind: callfsm.EvICEStateChange, ICEState: ev.ICEState}, true
	case port.SessionEventNeedsRestart:
		return callfsm.Event{Kind: callfsm.EvICENeedsRestart}, true
	case port.SessionEventClosed:
		return callfsm.Event{Kind: callfsm.EvICEStateChange, ICEState: "closed", Err: ev.Err}, true
	default:
		return callfsm.Event{}, false
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

	s.rtcMu.Lock()
	_ = s.cfg.rtc.Close()
	s.cfg.rtc = newRTC
	s.rtcMu.Unlock()

	// Перезапускаем сбор локальных кандидатов для нового PC.
	go s.pumpLocalCandidates()
	// Перезапускаем трансляцию RTC-событий.
	go s.pumpRTCEvents()
	return nil
}

// pumpLocalCandidates постит локальные ICE-кандидаты в eventQ.
func (s *callSession) pumpLocalCandidates() {
	s.rtcMu.RLock()
	localCands := s.cfg.rtc.LocalCandidates()
	s.rtcMu.RUnlock()
	for {
		select {
		case <-s.ctx.Done():
			return
		case c, ok := <-localCands:
			if !ok {
				return
			}
			s.emit(CallEvent{Kind: CallEventLocalCandidate, Detail: candidateDetail(c.Candidate, c.SDPMid, int(c.SDPMLineIndex))})
			select {
			case s.eventQ <- callfsm.Event{Kind: callfsm.EvLocalCandidate, Candidate: c}:
			default:
			}
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

		var stats port.SessionStats
		if s.cfg.rtc != nil {
			s.rtcMu.RLock()
			stats = s.cfg.rtc.Stats()
			s.rtcMu.RUnlock()
		}
		// Если ICE когда-либо подключался — звонок состоялся, поднимаем
		// любой не-success outcome до "answered". Покрывает: нормальный
		// hangup, peer hangup, ctx timeout (--max-duration), ICE restart
		// failure после connect.
		if stats.Connected && (outcome == "" || outcome == "completed" || outcome == "failed" || outcome == "timeout") {
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
			SelectedCandidate: stats.SelectedCandidate,
			TimingMs:          s.timingMs(),
			FailureReason:     s.failureReason,
		}

		s.mu.Lock()
		s.closed = true
		s.state = CallStateClosed
		s.finalResult = result
		ev := CallEvent{Kind: CallEventClosed, State: CallStateClosed, ICEState: stats.ICEState, Result: &result,
			Detail: s.timing.Detail(),
		}
		select {
		case s.events <- ev:
		default:
		}
		close(s.events)
		s.mu.Unlock()

		close(s.resultReady)

		s.rtcMu.RLock()
		if s.cfg.rtc != nil {
			_ = s.cfg.rtc.Close()
		}
		s.rtcMu.RUnlock()
		if !s.keepSigConn {
			_ = s.cfg.sigConn.Close()
		}
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

// ---- FSM shadow infrastructure (Step 4) ----

// actorLoop — главный цикл FSM. Читает события из eventQ, применяет
// Transition, выполняет действия. Блокирует до перехода в StateClosed.
func (s *callSession) actorLoop() {
	for {
		select {
		case <-s.ctx.Done():
			s.shutdownWith("failed")
			return
		case ev, ok := <-s.eventQ:
			if !ok {
				return
			}
			// Профилирование: замеряем latency eventQ + event-based метки.
			now := time.Now()
			if s.timing.FirstEventQRead.IsZero() {
				s.timing.FirstEventQRead = now
			}
			if !ev.Timestamp.IsZero() {
				s.timing.EventQLatencySum += now.Sub(ev.Timestamp)
				s.timing.EventQEvents++
			}
			s.recordEventTiming(ev, now)

			nextState, actions := callfsm.Transition(s.fsmState, ev)
			if nextState == s.fsmState && len(actions) == 0 {
				continue
			}
			if nextState == callfsm.StateClosed && s.failureReason == "" {
				switch ev.Kind {
				case callfsm.EvSignalReadErr, callfsm.EvSignalWriteErr:
					s.failureReason = "signal_lost"
					if ev.Err != nil {
						s.failureReason = ev.Err.Error()
					}
				}
			}
			s.recordStateTiming(nextState)
			s.fsmState = nextState
			s.mu.Lock()
			s.state = nextState.ToWire()
			s.mu.Unlock()
			for _, a := range actions {
				s.applyAction(a)
			}
			if nextState == callfsm.StateClosed {
				return
			}
		}
	}
}

// applyAction выполняет один Action, возвращённый Transition.
func (s *callSession) applyAction(a callfsm.Action) {
	switch a := a.(type) {
	case callfsm.ActSendEnvelope:
		payload := s.buildEnvelopePayload(a)
		env, err := makeEnvelopeWithOpts(s.cfg.caller.state(), s.cfg.caller.priv(),
			s.cfg.peer.UserID, s.peerPub, payload, 0, envelopeSignOpts{})
		if err != nil {
			s.eventQ <- callfsm.Event{Kind: callfsm.EvSignalWriteErr, Err: err}
			return
		}
		if a.TrackEnvID {
			s.sentRestartOfferEnvID = env.ID
		}
		s.writeMu.Lock()
		err = s.cfg.sigConn.Write(s.ctx, map[string]any{"type": "envelope.send", "envelope": env})
		s.writeMu.Unlock()
		if err != nil {
			select {
			case s.eventQ <- callfsm.Event{Kind: callfsm.EvSignalWriteErr, Err: err}:
			default:
			}
		}
		if a.PayloadType == "call.offer" && s.timing.OfferSent.IsZero() {
			s.timing.OfferSent = time.Now()
		}

	case callfsm.ActAddRemoteCandidate:
		cand := port.ICECandidate{
			Candidate:     a.Cand.Candidate,
			SDPMid:        a.Cand.SDPMid,
			SDPMLineIndex: a.Cand.SDPMLineIndex,
		}
		s.rtcMu.RLock()
		err := s.cfg.rtc.AddRemoteCandidate(cand)
		s.rtcMu.RUnlock()
		if err != nil {
			s.emit(CallEvent{Kind: CallEventError, Err: err,
				Detail: map[string]any{"phase": "add-remote-candidate", "candidate": cand.Candidate}})
		}

	case callfsm.ActStartTimer:
		s.startFSMTimer(a.Name, a.Duration)
	case callfsm.ActStopTimer:
		s.stopFSMTimer(a.Name)

	case callfsm.ActEmitCallEvent:
		ev := CallEvent{
			Kind:     CallEventKind(a.Kind),
			State:    a.State,
			ICEState: a.ICEState,
			Err:      a.Err,
			Detail:   a.Detail,
		}
		s.emit(ev)

	case callfsm.ActFinalizeWithOutcome:
		s.keepSigConn = a.KeepSigConn
		s.shutdownWith(a.Outcome)

	case callfsm.ActStartOutboundAudio:
		// Outbound audio pump is started by pionSession on ICE connect.

	// --- Async actions (plan §5.2): goroutine + return-event ---

	case callfsm.ActSetRemoteDescription:
		sdp, typ := a.SDP, a.Type
		go func() {
			s.rtcMu.RLock()
			err := s.cfg.rtc.SetRemoteDescription(port.SDP{SDP: sdp, Type: typ})
			s.rtcMu.RUnlock()
			s.postEvent(callfsm.Event{Kind: callfsm.EvSetRemoteDone, DoneType: typ, Err: err})
		}()

	case callfsm.ActCreateOffer:
		iceRestart := a.IceRestart
		go func() {
			s.rtcMu.RLock()
			offer, err := s.cfg.rtc.CreateOffer(port.CreateOfferOpts{ICERestart: iceRestart})
			if err != nil {
				s.rtcMu.RUnlock()
				s.postEvent(callfsm.Event{Kind: callfsm.EvCreateOfferDone, Err: err})
				return
			}
			err = s.cfg.rtc.SetLocalDescription(offer)
			s.rtcMu.RUnlock()
			if err != nil {
				s.postEvent(callfsm.Event{Kind: callfsm.EvCreateOfferDone, Err: err})
				return
			}
			s.lastCreatedSDP = offer.SDP
			s.lastCreatedFP = parseDTLSFingerprint(offer.SDP)
			s.postEvent(callfsm.Event{Kind: callfsm.EvCreateOfferDone, SDP: offer.SDP, DoneType: "offer"})
		}()

	case callfsm.ActCreateAnswer:
		go func() {
			s.rtcMu.RLock()
			answer, err := s.cfg.rtc.CreateAnswer()
			if err != nil {
				s.rtcMu.RUnlock()
				s.postEvent(callfsm.Event{Kind: callfsm.EvCreateAnswerDone, Err: err})
				return
			}
			err = s.cfg.rtc.SetLocalDescription(answer)
			s.rtcMu.RUnlock()
			if err != nil {
				s.postEvent(callfsm.Event{Kind: callfsm.EvCreateAnswerDone, Err: err})
				return
			}
			s.lastCreatedSDP = answer.SDP
			s.lastCreatedFP = parseDTLSFingerprint(answer.SDP)
			s.postEvent(callfsm.Event{Kind: callfsm.EvCreateAnswerDone, SDP: answer.SDP, DoneType: "answer"})
		}()

	case callfsm.ActFetchTurnCredentials:
		go func() {
			username, credential, ttlSec, uris, err := s.cfg.caller.fetchTurnCredentials(s.ctx)
			if err != nil {
				s.postEvent(callfsm.Event{Kind: callfsm.EvFetchTurnDone, Err: err})
				return
			}
			s.turnExpiresAt = time.Now().Unix() + ttlSec
			s.turnUsername = username
			s.turnCredential = credential
			s.turnURIs = uris
			s.postEvent(callfsm.Event{Kind: callfsm.EvFetchTurnDone})
		}()

	case callfsm.ActRefreshTurn:
		go func() {
			username, credential, ttlSec, uris, err := s.cfg.caller.fetchTurnCredentials(s.ctx)
			if err != nil {
				s.postEvent(callfsm.Event{Kind: callfsm.EvFetchTurnDone, Err: err})
				return
			}
			s.turnExpiresAt = time.Now().Unix() + ttlSec
			s.turnUsername = username
			s.turnCredential = credential
			s.turnURIs = uris
			s.postEvent(callfsm.Event{Kind: callfsm.EvFetchTurnDone})
		}()

	case callfsm.ActRecreatePC:
		go func() {
			err := s.recreatePC()
			s.postEvent(callfsm.Event{Kind: callfsm.EvRecreatePCDone, Err: err})
		}()

	case callfsm.ActClosePeerConnection:
		go func() {
			s.rtcMu.RLock()
			_ = s.cfg.rtc.Close()
			s.rtcMu.RUnlock()
			s.postEvent(callfsm.Event{Kind: callfsm.EvClosePCDone})
		}()

	case callfsm.ActCloseSignalConn:
		go func() {
			_ = s.cfg.sigConn.Close()
			s.postEvent(callfsm.Event{Kind: callfsm.EvCloseSignalDone})
		}()

	case callfsm.ActNewPeerConnection:
		// Handled during bootstrap — no-op at runtime.
	case callfsm.ActOpenAudioPipeline:
		// Handled during bootstrap.
	case callfsm.ActCloseAudioPipeline:
		// Handled during bootstrap.

	case callfsm.ActOpenSignalConn:
		// Handled during bootstrap.
	}
}

// newEvent создаёт Event с текущей временной меткой для профилирования.
func (s *callSession) newEvent(kind callfsm.EventKind) callfsm.Event {
	return callfsm.Event{Kind: kind, Timestamp: time.Now()}
}

// postEvent отправляет событие в eventQ. Блокирует, пока actor-loop не прочитает,
// но выходит при закрытии сессии (ctx.Done) — goroutine не утекает.
// Done-события критичны для цепочки асинхронных действий (restart, answer).
func (s *callSession) postEvent(ev callfsm.Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	select {
	case s.eventQ <- ev:
	case <-s.ctx.Done():
	}
}

// recordStateTiming фиксирует временные метки при ключевых FSM-переходах.
func (s *callSession) recordStateTiming(next callfsm.State) {
	now := time.Now()
	switch next {
	case callfsm.StateConnecting:
		if s.cfg.direction == directionIncoming && s.timing.AnswerSent.IsZero() {
			s.timing.AnswerSent = now
		}
	case callfsm.StateActive:
		if s.timing.ICEConnected.IsZero() {
			s.timing.ICEConnected = now
		}
	}
}

// recordEventTiming фиксирует метки при поступлении событий в actor-loop.
// Вызывается ДО Transition — single-writer invariant сохраняется.
func (s *callSession) recordEventTiming(ev callfsm.Event, now time.Time) {
	switch ev.Kind {
	case callfsm.EvAnswerReceived:
		if s.timing.AnswerReceived.IsZero() {
			s.timing.AnswerReceived = now
		}
	case callfsm.EvLocalCandidate:
		if s.timing.FirstLocalCandidate.IsZero() {
			s.timing.FirstLocalCandidate = now
		}
	case callfsm.EvRemoteCandidate:
		if s.timing.FirstRemoteCandidate.IsZero() {
			s.timing.FirstRemoteCandidate = now
		}
	}
}

// buildEnvelopePayload собирает полный payload для envelope из типа и полей сессии.
func (s *callSession) buildEnvelopePayload(a callfsm.ActSendEnvelope) map[string]any {
	base := map[string]any{
		"type":    a.PayloadType,
		"call_id": s.cfg.callID,
		"ts":      time.Now().UnixMilli(),
	}
	// Авто-подстановка SDP и fingerprint для offer/answer.
	switch a.PayloadType {
	case "call.offer":
		if s.lastCreatedSDP != "" {
			base["sdp"] = s.lastCreatedSDP
		}
		if s.lastCreatedFP != "" {
			base["caller_dtls_fingerprint"] = s.lastCreatedFP
		}
	case "call.answer":
		if s.lastCreatedSDP != "" {
			base["sdp"] = s.lastCreatedSDP
		}
		if s.lastCreatedFP != "" {
			base["callee_dtls_fingerprint"] = s.lastCreatedFP
		}
	}
	// Override/extend with explicit payload fields.
	for k, v := range a.Payload {
		base[k] = v
	}
	return base
}

// startFSMTimer запускает таймер, который по истечении шлёт событие в eventQ.
func (s *callSession) startFSMTimer(name string, d time.Duration) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		t.Stop()
	}
	s.timers[name] = time.AfterFunc(d, func() {
		ev := fsmTimerEvent(name)
		if ev.Kind != callfsm.EvInvalid {
			select {
			case s.eventQ <- ev:
			default:
			}
		}
	})
}

// stopFSMTimer останавливает FSM-таймер.
func (s *callSession) stopFSMTimer(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		t.Stop()
		delete(s.timers, name)
	}
}

// fsmTimerEvent возвращает FSM-событие для канонического имени таймера.
func fsmTimerEvent(name string) callfsm.Event {
	switch name {
	case "ringTimeout":
		return callfsm.Event{Kind: callfsm.EvRingTimeoutCaller}
	case "ringTimeoutCallee":
		return callfsm.Event{Kind: callfsm.EvRingTimeoutCallee}
	case "iceConnectTimeout":
		return callfsm.Event{Kind: callfsm.EvIceConnectTimeout}
	case "iceDisconnectGrace":
		return callfsm.Event{Kind: callfsm.EvIceDisconnectGrace}
	case "restartTimeout":
		return callfsm.Event{Kind: callfsm.EvRestartTimeout}
	case "endingTimeout":
		return callfsm.Event{Kind: callfsm.EvEndingTimeout}
	case "turnRefresh":
		return callfsm.Event{Kind: callfsm.EvTurnRefreshDue}
	case "maxDuration":
		return callfsm.Event{Kind: callfsm.EvMaxDurationReached}
	default:
		return callfsm.Event{Kind: callfsm.EvInvalid}
	}
}

// timingMs возвращает карту timing-значений для CallResult.TimingMs.
// Дублирует Detail() в формате int64-only для устойчивости JSON-парсинга в e2e.
func (s *callSession) timingMs() map[string]int64 {
	t := s.timing
	m := map[string]int64{}
	if t.BootstrapEnd.IsZero() {
		return m
	}
	m["bootstrap"] = t.BootstrapDuration.Milliseconds()
	if !t.FirstEventQRead.IsZero() {
		m["first_event"] = t.FirstEventQRead.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.OfferSent.IsZero() {
		m["offer_sent"] = t.OfferSent.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.AnswerReceived.IsZero() {
		m["answer_received"] = t.AnswerReceived.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.AnswerSent.IsZero() {
		m["answer_sent"] = t.AnswerSent.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.FirstLocalCandidate.IsZero() {
		m["first_local_cand"] = t.FirstLocalCandidate.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.FirstRemoteCandidate.IsZero() {
		m["first_remote_cand"] = t.FirstRemoteCandidate.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.ICEConnected.IsZero() {
		m["ice_connected"] = t.ICEConnected.Sub(t.BootstrapEnd).Milliseconds()
		m["setup"] = t.ICEConnected.Sub(t.BootstrapEnd).Milliseconds()
	}
	if !t.AnswerReceived.IsZero() && !t.OfferSent.IsZero() {
		m["offer_to_answer"] = t.AnswerReceived.Sub(t.OfferSent).Milliseconds()
	}
	if t.EventQEvents > 0 {
		m["eventq_avg_us"] = (t.EventQLatencySum / time.Duration(t.EventQEvents)).Microseconds()
	}
	return m
}
