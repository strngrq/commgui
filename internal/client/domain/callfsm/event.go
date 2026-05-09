package callfsm

import (
	"time"

	"github.com/strngrq/commgui/internal/client/port"
)

// EventKind enumerates every event that can drive a call-session state transition.
// Source of truth: docs/002.1-API_implementation_guidelines.md §4 and §8-9.
type EventKind int

//go:generate stringer -type=EventKind -trimprefix=Ev

const (
	EvInvalid EventKind = iota

	// --- UI events (§4.1) ---
	EvUserStartCall     // user tapped "call"
	EvUserAccept        // user tapped "accept" on incoming call
	EvUserDecline       // user tapped "decline" on incoming call
	EvUserHangup        // user tapped "hangup"
	EvYieldGlare        // App: yield this call to incoming glare offer (§6a)

	// --- Signaling events (§4.2) — decrypted envelope payloads ---
	EvOfferReceived     // call.offer from peer (new offer or ICE-restart, distinguished by callID)
	EvAnswerReceived    // call.answer from peer
	EvRemoteCandidate   // call.candidate from peer
	EvPeerReject        // call.reject from peer
	EvPeerHangup        // call.hangup from peer
	EvPeerCancel        // call.cancel from peer

	// --- Server control events (§4.3) ---
	EvEnvelopeFailed    // server rejected our envelope (payload: envID)
	EvEnvelopeAck       // server acknowledged our envelope (payload: envID)
	EvSignalReadErr     // sigConn.Read() returned error — fatal in any live state except Ending (§4.7)
	EvSignalWriteErr    // sigConn.Write() returned error — fatal except in Ending (§4.7)

	// --- WebRTC events (§4.5) ---
	EvLocalCandidate    // pion gathered a local ICE candidate
	EvICEStateChange    // ICE connection state changed (payload: ICEState)
	EvICENeedsRestart   // derived: from ICE failed or disconnect-grace timer expiry

	// --- Timer events (§11) ---
	EvRingTimeoutCaller  // caller: 30s from Calling start
	EvRingTimeoutCallee  // callee: 35s from Ringing start (§12.2)
	EvIceConnectTimeout  // 30s from Connecting/Restarting entry
	EvIceDisconnectGrace // 5s in Disconnected → needs restart
	EvRestartTimeout     // 10s from Restarting entry
	EvEndingTimeout      // 2s from Ending entry
	EvTurnRefreshDue     // TTL−300s: time to refresh TURN credentials
	EvMaxDurationReached // optional: custom max call duration exceeded

	// --- Async return-events (§5.2) ---
	// Long-running actions post these back to the FSM so the actor-loop never blocks.
	EvCreateOfferDone  // payload: SDP, fp; or Err
	EvCreateAnswerDone // payload: SDP, fp; or Err
	EvSetRemoteDone    // ok / Err
	EvSetLocalDone     // ok / Err
	EvFetchTurnDone    // payload: creds; or Err
	EvRecreatePCDone   // ok / Err
	EvOpenSignalDone   // ok / Err
	EvCloseSignalDone  // ok (always; used in Ending)
	EvClosePCDone      // ok (always)
)

// Event is a typed input to the FSM. Every event carries optional payload fields;
// which fields are populated depends on EventKind.
type Event struct {
	Kind EventKind

	// Payload fields — populated per Kind.
	CallID     string
	SDP        string
	DTLSFp     string
	ICEState   string
	Candidate  port.ICECandidate
	EnvelopeID string
	Reason     string
	Err        error
	// Timestamp — время создания события (для профилирования eventQ latency).
	Timestamp time.Time
	// IsRestart is true for OfferReceived when callID matches the active session's callID.
	IsRestart bool
	// DoneType carries the SDP type ("offer" or "answer") for async return-events
	// (EvSetRemoteDone, EvSetLocalDone, EvCreateOfferDone, EvCreateAnswerDone).
	// Empty for other event kinds.
	DoneType string
}
