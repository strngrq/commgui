package callfsm

import (
	"github.com/strngrq/commgui/internal/client/port"
	"time"
)

// Action is a side-effect intention returned by Transition.
// Every action implements the marker method isAction().
// Actions are applied sequentially in slice order — ordering is semantically
// significant. The rule:
//
//	(1) stop irrelevant timers
//	(2) state-mutating actions (SetRemote, AddCandidate, CreateOffer)
//	(3) network actions (SendEnvelope)
//	(4) start new timers
//	(5) emit UI event
//	(6) finalize (if Closed)
//
// When a Transition returns []Action, the caller MUST apply them in order.
type Action interface {
	isAction()
}

// ---- Signaling / network ----

// ActOpenSignalConn opens a new /ws/signal connection.
// Async: post-back via EvOpenSignalDone.
type ActOpenSignalConn struct{}

func (ActOpenSignalConn) isAction() {}

// ActCloseSignalConn closes the signaling connection.
// Async: post-back via EvCloseSignalDone (best-effort).
type ActCloseSignalConn struct{}

func (ActCloseSignalConn) isAction() {}

// ActSendEnvelope sends an encrypted envelope through the signaling channel.
// On write error the action runner posts EvSignalWriteErr back to eventQ.
// If TrackEnvID is true, the runner remembers the envelope ID so subsequent
// EvEnvelopeFailed can match it.
//
// Async: no success return-event (write is quick); error → EvSignalWriteErr.
type ActSendEnvelope struct {
	PayloadType string         // "call.offer" | "call.answer" | "call.candidate" | "call.cancel" | "call.hangup" | "call.reject"
	Payload     map[string]any // the encrypted inner payload
	TrackEnvID  bool           // if true, register envID for EvEnvelopeFailed matching
}

func (ActSendEnvelope) isAction() {}

// ---- TURN ----

// ActFetchTurnCredentials requests fresh TURN credentials from the server.
// Used at call start. Async: post-back via EvFetchTurnDone.
type ActFetchTurnCredentials struct{}

func (ActFetchTurnCredentials) isAction() {}

// ActRefreshTurn is like ActFetchTurnCredentials but used mid-call (triggers
// recreatePC after completion). Async: post-back via EvFetchTurnDone.
type ActRefreshTurn struct{}

func (ActRefreshTurn) isAction() {}

// ---- WebRTC ----

// ActNewPeerConnection creates a new PeerConnection. Sync (fast).
type ActNewPeerConnection struct {
	ICEServers []port.ICEServer
}

func (ActNewPeerConnection) isAction() {}

// ActCreateOffer generates an SDP offer. Async: post-back via EvCreateOfferDone.
type ActCreateOffer struct {
	IceRestart bool
}

func (ActCreateOffer) isAction() {}

// ActCreateAnswer generates an SDP answer. Async: post-back via EvCreateAnswerDone.
type ActCreateAnswer struct{}

func (ActCreateAnswer) isAction() {}

// ActSetLocalDescription sets the local SDP. Async: post-back via EvSetLocalDone.
type ActSetLocalDescription struct {
	SDP  string
	Type string // "offer" | "answer"
}

func (ActSetLocalDescription) isAction() {}

// ActSetRemoteDescription sets the remote SDP. Async: post-back via EvSetRemoteDone.
type ActSetRemoteDescription struct {
	SDP  string
	Type string // "offer" | "answer"
}

func (ActSetRemoteDescription) isAction() {}

// ActAddRemoteCandidate feeds a remote ICE candidate to the PeerConnection. Sync.
type ActAddRemoteCandidate struct {
	Cand port.ICECandidate
}

func (ActAddRemoteCandidate) isAction() {}

// ActRecreatePC tears down the old PeerConnection and creates a new one with
// fresh TURN credentials. Audio pipeline survives. Async: post-back via EvRecreatePCDone.
type ActRecreatePC struct {
	ICEServers []port.ICEServer
}

func (ActRecreatePC) isAction() {}

// ActClosePeerConnection closes the PeerConnection (DTLS teardown).
// Async: post-back via EvClosePCDone.
type ActClosePeerConnection struct{}

func (ActClosePeerConnection) isAction() {}

// ActStartOutboundAudio begins pumping outbound audio to the PeerConnection.
// Sync: just spawns the pump goroutine via sync.Once.
type ActStartOutboundAudio struct{}

func (ActStartOutboundAudio) isAction() {}

// ---- Audio ----

// ActOpenAudioPipeline initialises the OS audio device. Sync — may take
// up to 1s on macOS, but this happens at call start when UI shows "connecting…".
type ActOpenAudioPipeline struct {
	Opts port.AudioOpts
}

func (ActOpenAudioPipeline) isAction() {}

// ActCloseAudioPipeline releases the audio device. Sync.
// MUST be called before ActNewPeerConnection for the next call (§2.5).
type ActCloseAudioPipeline struct{}

func (ActCloseAudioPipeline) isAction() {}

// ---- Timers ----

// ActStartTimer starts a named timer. When it fires, an event is posted to eventQ.
type ActStartTimer struct {
	Name     string // see 002.1 §11 for canonical names
	Duration time.Duration
}

func (ActStartTimer) isAction() {}

// ActStopTimer cancels a named timer.
type ActStopTimer struct {
	Name string
}

func (ActStopTimer) isAction() {}

// ---- UI / finalisation ----

// CallEventKind mirrors domain.CallEventKind.
type CallEventKind string

const (
	CallEventStateChange    CallEventKind = "state-change"
	CallEventICEState       CallEventKind = "ice-state"
	CallEventRemoteHangup   CallEventKind = "remote-hangup"
	CallEventClosed         CallEventKind = "closed"
	CallEventError          CallEventKind = "error"
	CallEventLocalCandidate CallEventKind = "local-candidate"
	// … additional kinds emitted by Transition as needed.
)

// ActEmitCallEvent sends a UI event to the frontend. Sync, non-blocking send
// into a buffered channel. The action runner converts this to domain.CallEvent
// at the boundary.
type ActEmitCallEvent struct {
	Kind     CallEventKind
	State    port.CallState
	ICEState string
	Err      error
	Detail   map[string]any
}

func (ActEmitCallEvent) isAction() {}

// ActFinalizeWithOutcome assembles the final CallResult, emits CallEventClosed,
// and closes all resources. Always the last action when transitioning to Closed.
type ActFinalizeWithOutcome struct {
	Outcome     string
	KeepSigConn bool // если true — shutdownWith не закрывает s.cfg.sigConn (§6a)
}

func (ActFinalizeWithOutcome) isAction() {}
