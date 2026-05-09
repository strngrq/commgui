package callfsm

import (
	"crypto/subtle"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
)

// Transition computes the next state and actions for a given (state, event) pair.
// It is a pure function — no side effects, no blocking, no panics.
// For unhandled pairs, returns (state, nil) — the event is silently ignored.
//
// Source of truth: docs/002.1-API_implementation_guidelines.md §§8-9.
func Transition(state State, ev Event) (State, []Action) {
	switch state {
	case StateIdle:
		return transIdle(ev)
	case StateCalling:
		return transCalling(ev)
	case StateRinging:
		return transRinging(ev)
	case StateConnecting:
		return transConnecting(ev)
	case StateActive:
		return transActive(ev)
	case StateRestarting:
		return transRestarting(ev)
	case StateEnding:
		return transEnding(ev)
	case StateClosed:
		return StateClosed, nil // terminal
	default:
		return state, nil
	}
}

// ---- helpers ----

func same(s State, actions ...Action) (State, []Action) { return s, actions }

func closed(outcome string) (State, []Action) {
	return StateClosed, []Action{ActFinalizeWithOutcome{Outcome: outcome}}
}

func ending(actions ...Action) (State, []Action) {
	return StateEnding, append(actions,
		ActStartTimer{Name: timerEnding, Duration: endingTimeout},
		ActEmitCallEvent{Kind: CallEventStateChange, State: StateEnding.ToWire()},
	)
}

// closedWith is closed() + extra actions before ActFinalizeWithOutcome.
func closedWith(outcome string, actions ...Action) (State, []Action) {
	return StateClosed, append(actions, ActFinalizeWithOutcome{Outcome: outcome})
}

// ---- canonical timer names (002.1 §11) ----

const (
	timerRingCaller    = "ringTimeout"
	timerRingCallee    = "ringTimeoutCallee"
	timerIceConnect    = "iceConnectTimeout"
	timerIceDisconnect = "iceDisconnectGrace"
	timerRestart       = "restartTimeout"
	timerEnding        = "endingTimeout"
	timerTurnRefresh   = "turnRefresh"
	timerMaxDuration   = "maxDuration"
)

// ---- timer default durations ----

const (
	ringCallerDefault    = 30 * time.Second
	ringCalleeDefault    = 35 * time.Second
	iceConnectDefault    = 30 * time.Second
	iceDisconnectDefault = 5 * time.Second
	restartDefault       = 10 * time.Second
	endingTimeout        = 2 * time.Second
)

// ---- Idle (002.1 §8 caller, §9 callee) ----

func transIdle(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvUserStartCall:
		// Bootstrap: caller performs OpenSignal, FetchTurn, NewPC,
		// CreateOffer, SetLocal, SendEnvelope(call.offer) synchronously
		// before entering the actor-loop. FSM starts in Calling.
		return StateCalling, []Action{
			ActStartTimer{Name: timerRingCaller, Duration: ringCallerDefault},
		}
	case EvOfferReceived:
		// Bootstrap: callee receives offer via Listener. Application
		// creates CallSession in Ringing.
		// NOTE: ringTimeoutCallee timer is started at App level
		// (autoDeclinePending) or RingOnly(), not by FSM.
		return StateRinging, []Action{
			ActEmitCallEvent{Kind: CallEventStateChange, State: StateRinging.ToWire()},
		}
	default:
		return StateIdle, nil
	}
}

// ---- Calling — caller-only (002.1 §8) ----

func transCalling(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvAnswerReceived:
		if !fpMatch(ev.DTLSFp, ev.SDP) {
			return closed("failed")
		}
		return StateConnecting, []Action{
			ActStopTimer{Name: timerRingCaller},
			ActSetRemoteDescription{SDP: ev.SDP, Type: "answer"},
			ActStartTimer{Name: timerIceConnect, Duration: iceConnectDefault},
			ActEmitCallEvent{Kind: CallEventStateChange, State: StateConnecting.ToWire()},
		}

	case EvPeerReject:
		return closed("declined")
	case EvPeerCancel:
		return closed("cancelled-by-peer")
	case EvPeerHangup:
		return closed("completed")

	case EvEnvelopeFailed:
		// Caller must only post this event when envID matches sentOfferEnvID
		// (filtered at session level). If it reaches Transition, it's a match.
		return closed("failed")

	case EvUserHangup:
		return ending(
			ActStopTimer{Name: timerRingCaller},
			ActSendEnvelope{PayloadType: "call.cancel"},
		)
	case EvRingTimeoutCaller:
		return ending(
			ActSendEnvelope{PayloadType: "call.cancel"},
		)

	case EvSignalReadErr, EvSignalWriteErr:
		return closed("failed")

	case EvLocalCandidate:
		return same(StateCalling,
			ActSendEnvelope{PayloadType: "call.candidate", Payload: candidatePayload(ev.Candidate)},
		)
	case EvRemoteCandidate:
		return same(StateCalling,
			ActAddRemoteCandidate{Cand: ev.Candidate},
		)
	case EvICEStateChange:
		return same(StateCalling) // ICE gathering before answer — no-op
	case EvOfferReceived:
		// Glare (§12.1): peer is calling us while we're calling them.
		// Handled at App level via onForeignOffer — FSM ignores foreign
		// callID (pumpSignaling filters by callID, this event won't arrive).
		// If it does (misroute), treat as fatal.
		return closed("failed")
	case EvYieldGlare:
		// App-level glare resolution: yield this call to incoming offer.
		// KeepSigConn=true — sigConn передаётся новой сессии.
		return StateClosed, []Action{
			ActFinalizeWithOutcome{Outcome: "yielded-glare", KeepSigConn: true},
		}
	case EvICENeedsRestart:
		return same(StateCalling) // can't restart ICE before answer

	default:
		return same(StateCalling)
	}
}

// ---- Ringing — callee-only (002.1 §9) ----

func transRinging(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvUserAccept:
		// Bootstrap: callee does FetchTurn, NewPC, SetRemoteDescription(offer),
		// CreateAnswer, SetLocal, SendEnvelope(call.answer) synchronously
		// before entering actor-loop. FSM transitions to Connecting.
		return StateConnecting, []Action{
			ActStopTimer{Name: timerRingCallee},
			ActStartTimer{Name: timerIceConnect, Duration: iceConnectDefault},
			ActEmitCallEvent{Kind: CallEventStateChange, State: StateConnecting.ToWire()},
		}

	case EvUserDecline:
		return closedWith("declined-by-self",
			ActSendEnvelope{PayloadType: "call.reject"},
		)

	case EvPeerCancel:
		return closed("missed-by-self")
	case EvPeerHangup:
		return closed("missed-by-self")

	case EvRingTimeoutCallee:
		return closedWith("missed-by-self",
			ActSendEnvelope{PayloadType: "call.reject", Payload: map[string]any{"reason": "missed"}},
		)

	case EvOfferReceived:
		// Redundant offer from peer before accept — update SDP/fp, don't
		// disturb UI.
		return same(StateRinging)

	case EvEnvelopeFailed:
		return same(StateRinging) // someone else's failure

	case EvSignalReadErr, EvSignalWriteErr:
		return closed("failed")

	default:
		return same(StateRinging)
	}
}

// ---- Connecting — both caller and callee (002.1 §§8-9) ----

func transConnecting(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvICEStateChange:
		return connectingICEState(ev.ICEState)

	case EvIceDisconnectGrace:
		return enterRestarting()
	case EvICENeedsRestart:
		return enterRestarting()

	case EvIceConnectTimeout:
		return closed("failed")

	case EvUserHangup:
		return ending(
			ActSendEnvelope{PayloadType: "call.hangup"},
		)

	case EvPeerHangup:
		return closed("completed")
	case EvPeerCancel:
		return closed("cancelled-by-peer")
	case EvPeerReject:
		return closed("declined")

	case EvEnvelopeFailed:
		return same(StateConnecting) // candidates are best-effort

	case EvSignalReadErr, EvSignalWriteErr:
		return closed("failed")

	case EvLocalCandidate:
		return same(StateConnecting,
			ActSendEnvelope{PayloadType: "call.candidate", Payload: candidatePayload(ev.Candidate)},
		)
	case EvRemoteCandidate:
		return same(StateConnecting,
			ActAddRemoteCandidate{Cand: ev.Candidate},
		)

	case EvOfferReceived:
		// Peer initiated ICE restart.
		return peerRestart(ev)

	case EvSetRemoteDone:
		if ev.Err != nil {
			return closed("failed")
		}
		if ev.DoneType == "offer" {
			// Applied peer's offer — now create our answer.
			return same(StateConnecting, ActCreateAnswer{})
		}
		// Applied peer's answer — nothing more to do, wait for ICE.
		return same(StateConnecting)

	case EvCreateAnswerDone:
		if ev.Err != nil {
			return closed("failed")
		}
		// SetLocal already done in applyAction.ActCreateAnswer.
		return same(StateConnecting,
			ActSendEnvelope{PayloadType: "call.answer"},
		)

	case EvSetLocalDone:
		if ev.Err != nil {
			return closed("failed")
		}
		if ev.DoneType == "answer" {
			return same(StateConnecting,
				ActSendEnvelope{PayloadType: "call.answer"},
			)
		}
		return same(StateConnecting)

	default:
		return same(StateConnecting)
	}
}

func connectingICEState(ice string) (State, []Action) {
	switch ice {
	case "connected", "completed":
		return StateActive, []Action{
			ActStopTimer{Name: timerIceConnect},
			ActStartOutboundAudio{},
			ActEmitCallEvent{Kind: CallEventStateChange, State: StateActive.ToWire()},
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: ice},
		}
	case "disconnected":
		return same(StateConnecting,
			ActStartTimer{Name: timerIceDisconnect, Duration: iceDisconnectDefault},
		)
	case "failed":
		// B5 fix: stop iceConnectTimeout (not iceDisconnect).
		return StateRestarting, []Action{
			ActStopTimer{Name: timerIceConnect},
			ActStartTimer{Name: timerRestart, Duration: restartDefault},
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: "restarting"},
		}
	default:
		return same(StateConnecting,
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: ice},
		)
	}
}

// ---- Active — both caller and callee (002.1 §§8-9) ----

func transActive(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvICEStateChange:
		return activeICEState(ev.ICEState)

	case EvIceDisconnectGrace:
		return enterRestarting()
	case EvICENeedsRestart:
		return enterRestarting()

	case EvUserHangup:
		return ending(
			ActSendEnvelope{PayloadType: "call.hangup"},
		)

	case EvPeerHangup:
		return closed("completed")

	case EvSignalReadErr, EvSignalWriteErr:
		return closed("failed")

	case EvMaxDurationReached:
		return ending(
			ActSendEnvelope{PayloadType: "call.hangup"},
		)

	case EvTurnRefreshDue:
		return same(StateActive,
			ActRefreshTurn{},
		)

	case EvOfferReceived:
		// Peer initiated ICE restart.
		return peerRestart(ev)

	case EvRemoteCandidate:
		return same(StateActive,
			ActAddRemoteCandidate{Cand: ev.Candidate},
		)
	case EvLocalCandidate:
		return same(StateActive,
			ActSendEnvelope{PayloadType: "call.candidate", Payload: candidatePayload(ev.Candidate)},
		)
	case EvEnvelopeFailed:
		return same(StateActive) // candidates are best-effort

	// Async return-events for peer-initiated restart:
	case EvSetRemoteDone:
		if ev.Err != nil {
			return closed("failed")
		}
		if ev.DoneType == "offer" {
			return same(StateActive, ActCreateAnswer{})
		}
		return same(StateActive)
	case EvCreateAnswerDone:
		if ev.Err != nil {
			return closed("failed")
		}
		// SetLocal already done in applyAction.ActCreateAnswer.
		return same(StateActive,
			ActSendEnvelope{PayloadType: "call.answer"},
		)
	case EvSetLocalDone:
		if ev.Err != nil {
			return closed("failed")
		}
		if ev.DoneType == "answer" {
			return same(StateActive,
				ActSendEnvelope{PayloadType: "call.answer"},
			)
		}
		return same(StateActive)

	// Async return-events for turn refresh:
	case EvFetchTurnDone:
		if ev.Err != nil {
			// TURN refresh failed — keep going with old creds.
			return same(StateActive)
		}
		// After successful refresh, recreate PC.
		return same(StateActive,
			ActRecreatePC{},
		)
	case EvRecreatePCDone:
		if ev.Err != nil {
			return closed("failed")
		}
		return same(StateActive) // PC recreated, continue.

	default:
		return same(StateActive)
	}
}

func activeICEState(ice string) (State, []Action) {
	switch ice {
	case "disconnected":
		return same(StateActive,
			ActStartTimer{Name: timerIceDisconnect, Duration: iceDisconnectDefault},
		)
	case "connected": // briefly disconnected and came back
		return same(StateActive,
			ActStopTimer{Name: timerIceDisconnect},
		)
	case "failed":
		// Stop both timers that might be running.
		return StateRestarting, []Action{
			ActStopTimer{Name: timerIceDisconnect},
			ActStopTimer{Name: timerIceConnect},
			ActStartTimer{Name: timerRestart, Duration: restartDefault},
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: "restarting"},
		}
	default:
		return same(StateActive,
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: ice},
		)
	}
}

// ---- Restarting — both caller and callee (002.1 §§8-9) ----

func transRestarting(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvAnswerReceived:
		if !fpMatch(ev.DTLSFp, ev.SDP) {
			return closed("failed")
		}
		return StateConnecting, []Action{
			ActStopTimer{Name: timerRestart},
			ActSetRemoteDescription{SDP: ev.SDP, Type: "answer"},
			ActStartTimer{Name: timerIceConnect, Duration: iceConnectDefault},
		}

	case EvICEStateChange:
		return restartingICEState(ev.ICEState)

	case EvRestartTimeout:
		return closed("failed")
	case EvIceConnectTimeout:
		return closed("failed")

	case EvSignalReadErr:
		return closed("failed")
	case EvSignalWriteErr:
		return closed("failed")

	case EvUserHangup:
		return ending(
			ActSendEnvelope{PayloadType: "call.hangup"},
		)

	case EvPeerHangup:
		return closed("completed")
	case EvPeerReject:
		return closed("declined")
	case EvPeerCancel:
		return closed("cancelled-by-peer")

	case EvEnvelopeAck:
		// Restart offer acknowledged — wait for answer.
		return same(StateRestarting)

	case EvEnvelopeFailed:
		// Caller must only post when envID matches restart offer.
		return closed("failed")

	case EvOfferReceived:
		// Both peers restarted simultaneously — accept their offer
		// without re-entering Restarting (timers already running).
		return same(StateRestarting, restartProcessing(ev)...)

	case EvRemoteCandidate:
		return same(StateRestarting,
			ActAddRemoteCandidate{Cand: ev.Candidate},
		)
	case EvLocalCandidate:
		return same(StateRestarting,
			ActSendEnvelope{PayloadType: "call.candidate", Payload: candidatePayload(ev.Candidate)},
		)
	case EvICENeedsRestart:
		return same(StateRestarting) // one restart at a time (§10.4)

	// Async return-events:
	case EvSetRemoteDone:
		if ev.Err != nil {
			return closed("failed")
		}
		if ev.DoneType == "offer" {
			return same(StateRestarting, ActCreateAnswer{})
		}
		// Applied peer's answer to our restart offer — wait for ICE.
		return same(StateRestarting)
	case EvCreateAnswerDone:
		if ev.Err != nil {
			return closed("failed")
		}
		// SetLocal already done in applyAction.ActCreateAnswer.
		return same(StateRestarting,
			ActSendEnvelope{PayloadType: "call.answer"},
		)
	case EvSetLocalDone:
		if ev.Err != nil {
			return closed("failed")
		}
		if ev.DoneType == "answer" {
			return same(StateRestarting,
				ActSendEnvelope{PayloadType: "call.answer"},
			)
		}
		return same(StateRestarting)
	case EvCreateOfferDone:
		if ev.Err != nil {
			return closed("failed")
		}
		// SetLocal already done; now send the restart offer to peer.
		return same(StateRestarting,
			ActSendEnvelope{PayloadType: "call.offer", TrackEnvID: true},
		)
	case EvFetchTurnDone:
		if ev.Err != nil {
			return closed("failed")
		}
		return same(StateRestarting,
			ActRecreatePC{},
		)
	case EvRecreatePCDone:
		if ev.Err != nil {
			return closed("failed")
		}
		// PC recreated — now create restart offer.
		return same(StateRestarting,
			ActCreateOffer{IceRestart: true},
		)

	default:
		return same(StateRestarting)
	}
}

func restartingICEState(ice string) (State, []Action) {
	switch ice {
	case "connected", "completed":
		return StateActive, []Action{
			ActStopTimer{Name: timerRestart},
			ActStopTimer{Name: timerIceConnect},
			ActEmitCallEvent{Kind: CallEventStateChange, State: StateActive.ToWire()},
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: ice},
		}
	case "failed":
		return closed("failed")
	default:
		return same(StateRestarting,
			ActEmitCallEvent{Kind: CallEventICEState, ICEState: ice},
		)
	}
}

// ---- Ending — both caller and callee (002.1 §§8-9) ----

func transEnding(ev Event) (State, []Action) {
	switch ev.Kind {
	case EvICEStateChange:
		if ev.ICEState == "closed" {
			return closed("") // empty → shutdownWith derives outcome
		}
		return same(StateEnding)

	case EvEndingTimeout:
		return closed("") // force-close, outcome derived by shutdownWith

	case EvPeerHangup:
		return same(StateEnding) // we're already leaving

	case EvSignalReadErr:
		return closed("") // force-close PC
	case EvSignalWriteErr:
		return same(StateEnding) // §4.7: write in Ending is not fatal

	default:
		return same(StateEnding)
	}
}

// ---- shared helpers ----

// enterRestarting is the common transition for ICE restart triggers.
func enterRestarting() (State, []Action) {
	// ActCreateOffer — async (goroutine). EvCreateOfferDone в Restarting
	// триггерит ActSendEnvelope с TrackEnvID.
	return StateRestarting, []Action{
		ActStopTimer{Name: timerIceDisconnect},
		ActStopTimer{Name: timerIceConnect},
		ActStartTimer{Name: timerRestart, Duration: restartDefault},
		ActEmitCallEvent{Kind: CallEventICEState, ICEState: "restarting"},
		ActCreateOffer{IceRestart: true},
	}
}

// peerRestart handles an incoming ICE restart offer from the peer.
// Callers in Active/Connecting combine it with enterRestarting() for entry
// actions (stop timers, start restart timer, emit). Callers already in
// Restarting use restartProcessing() alone — entry actions must not repeat.
func peerRestart(ev Event) (State, []Action) {
	_, entry := enterRestarting()
	return StateRestarting, append(entry, restartProcessing(ev)...)
}

// restartProcessing returns only the SDP-processing action (no entry actions).
// Used when already in Restarting: both peers restarted simultaneously —
// we accept their offer without re-entering the restart state.
func restartProcessing(ev Event) []Action {
	return []Action{
		ActSetRemoteDescription{SDP: ev.SDP, Type: "offer"},
	}
}

// ---- cryptographic helpers ----

// fpMatch checks DTLS fingerprint from payload against SDP.
// Uses constant-time comparison as required by 002.1 §2.6.
func fpMatch(payloadFP, sdp string) bool {
	if payloadFP == "" || sdp == "" {
		return false
	}
	sdpFP := parseDTLSFingerprint(sdp)
	if sdpFP == "" {
		return false
	}
	// Normalise both sides: strip colons so "4A:AD:..." matches "4AAD..."
	a := stripColons(payloadFP)
	b := stripColons(sdpFP)
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// parseDTLSFingerprint extracts a=fingerprint:sha-256 from SDP.
// Returns the fingerprint as-is (with colons preserved); caller normalises.
// NOTE: differs from domain.parseDTLSFingerprint (caller.go) which strips colons.
func parseDTLSFingerprint(sdp string) string {
	const prefix = "a=fingerprint:sha-256 "
	i := 0
	for i <= len(sdp)-len(prefix) {
		// Ensure we're at line start.
		if i > 0 && sdp[i-1] != '\n' {
			i++
			continue
		}
		if sdp[i:i+len(prefix)] != prefix {
			i++
			continue
		}
		valStart := i + len(prefix)
		valEnd := valStart
		for valEnd < len(sdp) && sdp[valEnd] != '\n' && sdp[valEnd] != '\r' {
			valEnd++
		}
		return sdp[valStart:valEnd]
	}
	return ""
}

// stripColons removes ':' and ' ' characters from a hex fingerprint string.
func stripColons(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ':' && s[i] != ' ' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// candidatePayload builds the map for a call.candidate envelope.
func candidatePayload(cand port.ICECandidate) map[string]any {
	return map[string]any{
		"candidate": map[string]any{
			"candidate":     cand.Candidate,
			"sdpMid":        cand.SDPMid,
			"sdpMLineIndex": cand.SDPMLineIndex,
		},
	}
}
