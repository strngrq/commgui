package webrtc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/strngrq/commgui/internal/client/port"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// PionManager — реализация port.WebRTCManager поверх pion/webrtc.
type PionManager struct{}

func NewPionManager() *PionManager {
	return &PionManager{}
}

var _ port.WebRTCManager = (*PionManager)(nil)
var _ port.WebRTCSession = (*pionSession)(nil)

// NewSession создаёт новую WebRTC-сессию и запускает фоновую перекачку аудио,
// если pipeline его требует.
func (m *PionManager) NewSession(ctx context.Context, opts port.WebRTCSessionOpts) (port.WebRTCSession, error) {
	if opts.Audio == nil {
		return nil, fmt.Errorf("webrtc: opts.Audio is required (use audio.Engine.Build for null mode)")
	}

	cfg := webrtc.Configuration{}
	if !opts.NoTurn {
		if len(opts.ICEServers) > 0 {
			cfg.ICEServers = make([]webrtc.ICEServer, len(opts.ICEServers))
			for i, s := range opts.ICEServers {
				cfg.ICEServers[i] = webrtc.ICEServer{
					URLs:       s.URLs,
					Username:   s.Username,
					Credential: s.Credential,
				}
			}
		} else {
			servers, err := turnICEServers(opts.ServerURL, opts.Token)
			if err != nil {
				return nil, err
			}
			cfg.ICEServers = servers
		}
	}

	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, err
	}

	sessCtx, cancel := context.WithCancel(ctx)
	s := &pionSession{
		pc:         pc,
		audio:      opts.Audio,
		started:    time.Now(),
		ctx:        sessCtx,
		cancel:     cancel,
		events:     make(chan port.SessionEvent, 16),
		candidates: make(chan port.ICECandidate, 32),
		iceState:   "new",
	}

	pc.OnICEConnectionStateChange(s.onICEStateChange)
	pc.OnICECandidate(s.onLocalCandidate)
	pc.OnTrack(s.onRemoteTrack)

	if err := s.attachAudio(); err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}

	return s, nil
}

type pionSession struct {
	pc *webrtc.PeerConnection

	audio      port.AudioPipeline
	localTrack *webrtc.TrackLocalStaticSample
	dataChan   *webrtc.DataChannel

	started time.Time

	ctx    context.Context
	cancel context.CancelFunc

	events     chan port.SessionEvent
	candidates chan port.ICECandidate

	mu                sync.Mutex
	iceState          string
	connected         bool
	rtpSent           int
	rtpReceived       int
	closedAt          time.Time
	closeOnce         sync.Once
	selectedCandidate string
	// disconnectTimer срабатывает, если ICE задержался в Disconnected дольше
	// iceDisconnectGrace. По срабатыванию эмитится NeedsRestart, чтобы
	// перевыбрать пару (например, переключиться на relay при потере srflx-пути
	// после смены интерфейса/выключения VPN).
	disconnectTimer  *time.Timer
	restartScheduled bool

	outboundOnce sync.Once
}

// iceDisconnectGrace — сколько ждём в ICEConnectionStateDisconnected, прежде
// чем эмитить NeedsRestart. Pion сам может вернуться в Connected по
// consent-ping; даём ему пять секунд, потом форсируем ICE restart.
const iceDisconnectGrace = 5 * time.Second

// attachAudio привязывает AudioPipeline к pion-сессии.
// Для AudioKindData создаётся DataChannel; для AudioKindAudio — Opus-трек,
// и стартует goroutine, перекачивающая фреймы из Outbound в трек.
func (s *pionSession) attachAudio() error {
	switch s.audio.Kind() {
	case port.AudioKindData:
		dc, err := s.pc.CreateDataChannel("commclient-null", nil)
		if err != nil {
			return err
		}
		s.dataChan = dc
		return nil

	case port.AudioKindAudio:
		track, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
			"audio",
			"commclient",
		)
		if err != nil {
			return err
		}
		sender, err := s.pc.AddTrack(track)
		if err != nil {
			return err
		}
		s.localTrack = track

		// Drain RTCP feedback to keep the sender goroutine inside pion happy.
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, err := sender.Read(buf); err != nil {
					return
				}
			}
		}()

		// pumpOutbound will be started when ICE connects
		// (see onICEStateChange). Starting it before signaling
		// completes causes the remote peer to not see our track.
		return nil

	default:
		return fmt.Errorf("webrtc: unsupported audio kind %q", s.audio.Kind())
	}
}

// pumpOutbound читает фреймы из Audio.Outbound() и пишет их в локальный трек,
// пока не вернётся io.EOF/ctx.Err() либо пока сессия не закроется.
//
// Pacing — по абсолютной шкале времени: nextDeadline += dur на каждой итерации,
// поэтому расходы encode/WriteSample/scheduling не накапливаются и средний
// темп остаётся ровно 1/dur пакетов/сек. Самопейсящие источники (микрофон,
// который блокируется в Next до накопления фрейма) ловятся sleep<=0 и не
// получают двойной паузы. См. docs/007-outbound.md.
func (s *pionSession) pumpOutbound() {
	src := s.audio.Outbound()
	var nextDeadline time.Time
	for {
		payload, dur, err := src.Next(s.ctx)
		if err != nil {
			return
		}
		if err := s.localTrack.WriteSample(media.Sample{Data: payload, Duration: dur}); err != nil {
			return
		}
		s.mu.Lock()
		s.rtpSent++
		s.mu.Unlock()

		now := time.Now()
		if nextDeadline.IsZero() {
			nextDeadline = now.Add(dur)
		} else {
			nextDeadline = nextDeadline.Add(dur)
			// Защита от длительных stall'ов (GC, swap): если deadline уже
			// в прошлом, не навёрстываем burst'ом — ресетим на now.
			if nextDeadline.Before(now) {
				nextDeadline = now
			}
		}
		sleep := time.Until(nextDeadline)
		if sleep <= 0 {
			// Источник сам себя спейсит (real-mic блокируется в Next).
			// Просто продолжаем, иначе удвоим pacing.
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			continue
		}
		select {
		case <-time.After(sleep):
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *pionSession) onICEStateChange(state webrtc.ICEConnectionState) {
	s.mu.Lock()
	s.iceState = state.String()
	if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
		s.connected = true
		s.cancelDisconnectTimerLocked()
		s.restartScheduled = false
	}
	if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
		s.cancelDisconnectTimerLocked()
	}
	if state == webrtc.ICEConnectionStateDisconnected {
		s.armDisconnectTimerLocked(iceDisconnectGrace)
	}
	stateName := s.iceState
	connected := s.connected
	s.mu.Unlock()

	// При connected/completed обновляем selected pair (вне mu — Pion API
	// потенциально блокирующий).
	if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
		if pair := s.probeSelectedPair(); pair != "" {
			s.mu.Lock()
			s.selectedCandidate = pair
			s.mu.Unlock()
		}
	}

	s.emit(port.SessionEvent{Kind: port.SessionEventICEState, ICEState: stateName})
	if connected && (state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted) {
		s.emit(port.SessionEvent{Kind: port.SessionEventConnected, ICEState: stateName})
		// Start outbound audio pump once ICE is established.
		// Delaying until after signaling completes fixes a bug
		// where the remote peer's OnTrack wouldn't fire for
		// file-based audio (rtp_received=0).
		if s.audio.Kind() == port.AudioKindAudio {
			s.outboundOnce.Do(func() {
				go s.pumpOutbound()
			})
		}
	}
	switch state {
	case webrtc.ICEConnectionStateFailed:
		s.emit(port.SessionEvent{Kind: port.SessionEventNeedsRestart, ICEState: stateName})
	case webrtc.ICEConnectionStateClosed:
		s.signalClosed(nil)
	}
}

// armDisconnectTimerLocked запускает таймер на NeedsRestart, если ICE задержался
// в Disconnected. Идемпотентен: повторный disconnected не перезапускает таймер.
func (s *pionSession) armDisconnectTimerLocked(d time.Duration) {
	if s.disconnectTimer != nil || s.restartScheduled {
		return
	}
	s.disconnectTimer = time.AfterFunc(d, s.fireDisconnectRestart)
}

func (s *pionSession) cancelDisconnectTimerLocked() {
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
		s.disconnectTimer = nil
	}
}

func (s *pionSession) fireDisconnectRestart() {
	s.mu.Lock()
	s.disconnectTimer = nil
	stuck := s.iceState == webrtc.ICEConnectionStateDisconnected.String() && !s.restartScheduled
	if stuck {
		s.restartScheduled = true
	}
	stateName := s.iceState
	s.mu.Unlock()
	if stuck {
		s.emit(port.SessionEvent{Kind: port.SessionEventNeedsRestart, ICEState: stateName})
	}
}

// probeSelectedPair достаёт текущую выбранную ICE-пару через первый sender'а
// (transceiver) — для голосового вызова это media-DTLS-транспорт. Если pair
// пока не выбран или метод вернул ошибку — возвращает пустую строку.
func (s *pionSession) probeSelectedPair() string {
	for _, snd := range s.pc.GetSenders() {
		if pair := candidatePairFromTransport(snd.Transport()); pair != "" {
			return pair
		}
	}
	for _, rcv := range s.pc.GetReceivers() {
		if pair := candidatePairFromTransport(rcv.Transport()); pair != "" {
			return pair
		}
	}
	return ""
}

func candidatePairFromTransport(t *webrtc.DTLSTransport) string {
	if t == nil {
		return ""
	}
	ice := t.ICETransport()
	if ice == nil {
		return ""
	}
	pair, err := ice.GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return ""
	}
	return formatICECandidate(pair.Local) + " -> " + formatICECandidate(pair.Remote)
}

func formatICECandidate(c *webrtc.ICECandidate) string {
	if c == nil {
		return "?"
	}
	return c.Typ.String() + ":" + c.Address + ":" + strconv.Itoa(int(c.Port))
}

func (s *pionSession) onLocalCandidate(c *webrtc.ICECandidate) {
	if c == nil {
		return
	}
	init := c.ToJSON()
	cand := port.ICECandidate{
		Candidate:     init.Candidate,
		SDPMid:        derefStr(init.SDPMid),
		SDPMLineIndex: derefUint16(init.SDPMLineIndex),
	}
	select {
	case s.candidates <- cand:
	case <-s.ctx.Done():
	default:
		// канал переполнен — кандидат теряется; для diagnostics этого достаточно.
	}
}

func (s *pionSession) onRemoteTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	sink := s.audio.Inbound()
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.rtpReceived++
		s.mu.Unlock()
		sink.Push(pkt.Payload)
	}
}

func (s *pionSession) emit(ev port.SessionEvent) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	default:
	}
}

func (s *pionSession) signalClosed(err error) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closedAt = time.Now()
		s.cancelDisconnectTimerLocked()
		s.mu.Unlock()
		s.emit(port.SessionEvent{Kind: port.SessionEventClosed, Err: err})
		s.cancel()
		_ = s.audio.Close()
		_ = s.pc.Close()
	})
}

// CreateOffer / CreateAnswer / SetLocalDescription / SetRemoteDescription / AddRemoteCandidate.

func (s *pionSession) CreateOffer(opts port.CreateOfferOpts) (port.SDP, error) {
	var pionOpts *webrtc.OfferOptions
	if opts.ICERestart {
		pionOpts = &webrtc.OfferOptions{ICERestart: true}
	}
	o, err := s.pc.CreateOffer(pionOpts)
	if err != nil {
		return port.SDP{}, err
	}
	return port.SDP{Type: o.Type.String(), SDP: o.SDP}, nil
}

func (s *pionSession) CreateAnswer() (port.SDP, error) {
	a, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return port.SDP{}, err
	}
	return port.SDP{Type: a.Type.String(), SDP: a.SDP}, nil
}

func (s *pionSession) SetLocalDescription(sdp port.SDP) error {
	return s.pc.SetLocalDescription(webrtc.SessionDescription{
		Type: webrtc.NewSDPType(sdp.Type),
		SDP:  sdp.SDP,
	})
}

func (s *pionSession) SetRemoteDescription(sdp port.SDP) error {
	return s.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.NewSDPType(sdp.Type),
		SDP:  sdp.SDP,
	})
}

func (s *pionSession) AddRemoteCandidate(c port.ICECandidate) error {
	mid := c.SDPMid
	idx := c.SDPMLineIndex
	return s.pc.AddICECandidate(webrtc.ICECandidateInit{
		Candidate:     c.Candidate,
		SDPMid:        &mid,
		SDPMLineIndex: &idx,
	})
}

func (s *pionSession) LocalCandidates() <-chan port.ICECandidate { return s.candidates }
func (s *pionSession) Events() <-chan port.SessionEvent          { return s.events }

func (s *pionSession) Stats() port.SessionStats {
	s.mu.Lock()
	end := time.Now()
	if !s.closedAt.IsZero() {
		end = s.closedAt
	}
	stats := port.SessionStats{
		ICEState:          s.iceState,
		Connected:         s.connected,
		RTPSent:           s.rtpSent,
		RTPReceived:       s.rtpReceived,
		Duration:          end.Sub(s.started),
		SelectedCandidate: s.selectedCandidate,
	}
	s.mu.Unlock()
	// Если pair ещё не запомнили (например, ICE только что connected, а
	// onICEStateChange ещё не успел probe'нуть) — пробуем сейчас.
	if stats.Connected && stats.SelectedCandidate == "" {
		if pair := s.probeSelectedPair(); pair != "" {
			s.mu.Lock()
			s.selectedCandidate = pair
			s.mu.Unlock()
			stats.SelectedCandidate = pair
		}
	}
	return stats
}

func (s *pionSession) Close() error {
	s.signalClosed(nil)
	return nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefUint16(u *uint16) uint16 {
	if u == nil {
		return 0
	}
	return *u
}

// --- TURN credentials helper ---

type turnCredentialResponse struct {
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	URIs       []string `json:"uris"`
}

func turnICEServers(serverURL, token string) ([]webrtc.ICEServer, error) {
	var creds turnCredentialResponse
	if err := getJSON(serverURL+"/v1/turn/credentials", &creds, token); err != nil {
		return nil, err
	}
	if len(creds.URIs) == 0 {
		return nil, nil
	}
	return []webrtc.ICEServer{{
		URLs:       creds.URIs,
		Username:   creds.Username,
		Credential: creds.Credential,
	}}, nil
}

func getJSON(url string, out any, token string) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
