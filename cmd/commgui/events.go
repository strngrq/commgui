package main

import (
	"context"
	"time"

	"github.com/strngrq/commgui/internal/client/domain"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// eventBus — мост domain-событий → runtime.EventsEmit.
type eventBus struct {
	ctx          context.Context
	onCallEnded  func(CallLogEntryDTO)
	onCallClosed func()
	logFn        func(string, map[string]any)
}

func newEventBus(ctx context.Context) *eventBus {
	return &eventBus{ctx: ctx}
}

func (b *eventBus) emit(kind string, payload any) {
	runtime.EventsEmit(b.ctx, kind, payload)
}

func (b *eventBus) log(event string, data map[string]any) {
	if b.logFn != nil {
		b.logFn(event, data)
	}
}

// watchSession подписывается на Events() сессии и ретранслирует во фронтенд.
// Запускается в отдельной горутине, завершается при закрытии канала событий.
func (b *eventBus) watchSession(sess domain.CallSession, peerUserID, peerName, direction string) {
	go func() {
		startedAt := time.Now().UnixMilli()
		b.log("call_session_start", map[string]any{
			"call_id":   sess.ID(),
			"peer_id":   peerUserID,
			"peer_name": peerName,
			"direction": direction,
		})
		for ev := range sess.Events() {
			b.log("call_event", map[string]any{
				"call_id": sess.ID(),
				"kind":    string(ev.Kind),
				"state":   string(ev.State),
				"ice":     ev.ICEState,
			})
			switch ev.Kind {
			case domain.CallEventStateChange:
				b.emit("call-state", map[string]any{
					"callId": sess.ID(),
					"state":  string(ev.State),
				})
			case domain.CallEventICEState:
				b.emit("call-ice", map[string]any{
					"callId":   sess.ID(),
					"iceState": ev.ICEState,
				})
			case domain.CallEventRemoteHangup:
				b.emit("call-remote-hangup", map[string]any{
					"callId": sess.ID(),
				})
			case domain.CallEventError:
				errMsg := ""
				if ev.Err != nil {
					errMsg = ev.Err.Error()
				}
				b.emit("call-error", map[string]any{
					"callId": sess.ID(),
					"error":  errMsg,
				})
			case domain.CallEventClosed:
				if b.onCallClosed != nil {
					b.onCallClosed()
				}
				result := map[string]any{
					"callId":   sess.ID(),
					"reason":   "hangup",
					"outcome":  "",
					"duration": int64(0),
				}
				if ev.Result != nil {
					result["outcome"] = ev.Result.Outcome
					result["duration"] = ev.Result.DurationMs
				}
				b.emit("call-ended", result)
				if ev.Result != nil {
					b.log("call_closed", map[string]any{
						"call_id":              sess.ID(),
						"outcome":              ev.Result.Outcome,
						"duration_ms":          ev.Result.DurationMs,
						"ice_state":            ev.Result.ICEState,
						"selected_candidate":   ev.Result.SelectedCandidate,
						"dtls_fingerprint_ok":  ev.Result.DTLSFingerprintOK,
						"rtp_sent":             ev.Result.RTPSent,
						"rtp_received":         ev.Result.RTPReceived,
						"audio_mode":           ev.Result.AudioMode,
					})
				}
				if b.onCallEnded != nil {
					outcome := ""
					dur := int64(0)
					if ev.Result != nil {
						outcome = ev.Result.Outcome
						dur = ev.Result.DurationMs
					}
					st := time.Now().UnixMilli() - dur
					if startedAt != 0 {
						st = startedAt
					}
					b.onCallEnded(CallLogEntryDTO{
						CallID:     sess.ID(),
						PeerUserID: peerUserID,
						PeerName:   peerName,
						Direction:  direction,
						Outcome:    outcome,
						DurationMs: dur,
						StartedAt:  st,
					})
				}
			}
		}
	}()
}
