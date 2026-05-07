package port

// CallState — состояние звонка.
type CallState string

const (
	CallStateIdle     CallState = "idle"
	CallStateIncoming CallState = "incoming"
	CallStateCalling  CallState = "calling"
	CallStateActive   CallState = "active"
	CallStateEnding   CallState = "ending"
	CallStateClosed   CallState = "closed"
)

// CallResult — результат звонка.
type CallResult struct {
	CallID            string `json:"call_id"`
	Outcome           string `json:"outcome"`
	DurationMs        int64  `json:"duration_ms"`
	ICEState          string `json:"ice_state"`
	SelectedCandidate string `json:"selected_candidate"`
	DTLSFingerprintOK bool   `json:"dtls_fingerprint_ok"`
	AudioMode         string `json:"audio"`
	RecordPath        string `json:"record"`
	RTPSent           int    `json:"rtp_sent"`
	RTPReceived       int    `json:"rtp_received"`
}

// CLIPresenter — форматированный вывод для одноразовых CLI-команд.
// Реализации: text (pretty-printed), json.
type CLIPresenter interface {
	Success(data any) error
	Failure(err error) error
}

// Event — событие для долгоживущего UI (TUI, Wails, JSON-stream).
type Event struct {
	Kind    string
	Payload any
}

// EventSink — приёмник событий для долгоживущих UI.
// Send не блокируется надолго; адаптер сам решает, что делать при переполнении.
// Реализации: TUISink (bubbletea), JSONSink (stdout-стрим), Wails (будущее).
type EventSink interface {
	Send(event Event)
}
