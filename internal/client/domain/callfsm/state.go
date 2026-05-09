// Package callfsm provides the typed finite state machine for call sessions.
// Source of truth: docs/002.1-API_implementation_guidelines.md §§6-9.
package callfsm

import "github.com/strngrq/commgui/internal/client/port"

// State describes the current phase of a call session.
type State int

//go:generate stringer -type=State -trimprefix=State

const (
	StateInvalid State = iota // sentinel for exhaustive linter checks

	StateIdle       // session just created, nothing sent yet
	StateCalling    // caller: offer sent, waiting for answer
	StateRinging    // callee: offer received, waiting for user decision
	StateConnecting // ICE checking, not connected yet
	StateActive     // ICE connected, RTP flowing
	StateRestarting // ICE restart in progress
	StateEnding     // sent call.hangup, waiting for ICE.closed
	StateClosed     // final; CallResult ready
)

// ToWire converts the internal FSM state to the wire-compatible port.CallState.
// Until the frontend learns Connecting/Restarting, those map to existing states.
func (s State) ToWire() port.CallState {
	switch s {
	case StateIdle:
		return port.CallStateIdle
	case StateCalling:
		return port.CallStateCalling
	case StateRinging:
		return port.CallStateIncoming
	case StateConnecting:
		return port.CallStateCalling // UI compat: frontend doesn't know Connecting yet
	case StateActive:
		return port.CallStateActive
	case StateRestarting:
		return port.CallStateActive // UI compat: frontend doesn't know Restarting yet
	case StateEnding:
		return port.CallStateEnding
	case StateClosed:
		return port.CallStateClosed
	default:
		return port.CallStateIdle
	}
}

// IsLive returns true if the state is not Closed and not Invalid.
func (s State) IsLive() bool {
	return s > StateInvalid && s < StateClosed
}
