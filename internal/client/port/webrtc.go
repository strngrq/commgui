package port

import (
	"context"
	"time"
)

// WebRTCManager — фабрика WebRTC-сессий.
type WebRTCManager interface {
	NewSession(ctx context.Context, opts WebRTCSessionOpts) (WebRTCSession, error)
}

// WebRTCSessionOpts — параметры сессии.
type WebRTCSessionOpts struct {
	ServerURL string
	Token     string
	NoTurn    bool
	// Audio задаёт исходящий и входящий аудио-pipeline. Обязателен.
	// Для "только DataChannel"-режима используйте pipeline с Kind() == AudioKindData
	// (например, audio.Engine.Build с mode="null").
	Audio AudioPipeline
	// ICEServers, если заданы, используются вместо автоматического запроса
	// /turn/credentials. Нужно для обновления TURN-credentials без разрыва сессии
	// (ICE restart с refresh).
	ICEServers []ICEServer
}

// ICEServer — pre-fetched TURN server config для WebRTC.
type ICEServer struct {
	URLs       []string
	Username   string
	Credential string
}

// WebRTCSession — активная WebRTC-сессия.
//
// Жизненный цикл (caller):
//  1. CreateOffer / SetLocalDescription, послать SDP пиру через signaling.
//  2. SetRemoteDescription c answer от пира.
//  3. Кандидаты, сгенерированные локально, читать из LocalCandidates() и слать пиру.
//  4. Кандидаты от пира класть в AddRemoteCandidate.
//  5. Состояние ICE и завершение — через Events().
//  6. По окончанию — Close (audio pipeline закрывается автоматически).
type WebRTCSession interface {
	CreateOffer() (SDP, error)
	CreateAnswer() (SDP, error)
	SetLocalDescription(sdp SDP) error
	SetRemoteDescription(sdp SDP) error
	AddRemoteCandidate(c ICECandidate) error

	LocalCandidates() <-chan ICECandidate
	Events() <-chan SessionEvent

	Stats() SessionStats
	Close() error
}

// SessionEventKind — тип события WebRTC-сессии.
type SessionEventKind string

const (
	// SessionEventICEState — изменение ICE connection state (любое).
	SessionEventICEState SessionEventKind = "ice-state"
	// SessionEventConnected — ICE достиг "connected" или "completed".
	SessionEventConnected SessionEventKind = "connected"
	// SessionEventNeedsRestart — ICE перешёл в "failed" и не восстановился;
	// владелец сессии должен инициировать ICE restart (новый offer с тем же call_id).
	SessionEventNeedsRestart SessionEventKind = "needs-restart"
	// SessionEventClosed — сессия завершена (failed/closed/disconnected или Close()).
	SessionEventClosed SessionEventKind = "closed"
)

// SessionEvent — событие сессии.
type SessionEvent struct {
	Kind     SessionEventKind
	ICEState string // "new", "checking", "connected", "completed", "failed", "closed", "disconnected"
	Err      error
}

// SessionStats — счётчики сессии.
type SessionStats struct {
	ICEState    string
	Connected   bool
	RTPSent     int
	RTPReceived int
	Duration    time.Duration
}

// SDP — Session Description Protocol.
type SDP struct {
	Type string
	SDP  string
}

// ICECandidate — ICE candidate.
type ICECandidate struct {
	Candidate     string
	SDPMid        string
	SDPMLineIndex uint16
}
