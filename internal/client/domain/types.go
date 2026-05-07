package domain

import (
	"github.com/strngrq/commgui/internal/client/port"
	"time"
)

// Aliases to port types — domain uses port.State, port.Contact, etc.
type (
	State       = port.State
	ServerInfo  = port.ServerInfo
	UserInfo    = port.UserInfo
	SessionInfo = port.SessionInfo
	Contact     = port.Contact
	CallState   = port.CallState
	CallResult  = port.CallResult
)

const (
	CallStateIdle     = port.CallStateIdle
	CallStateIncoming = port.CallStateIncoming
	CallStateCalling  = port.CallStateCalling
	CallStateActive   = port.CallStateActive
	CallStateEnding   = port.CallStateEnding
	CallStateClosed   = port.CallStateClosed
)

// InitOpts — параметры инициализации профиля.
type InitOpts struct {
	ServerURL string
	Name      string
}

// InitResult — результат инициализации.
type InitResult struct {
	Profile   string `json:"profile"`
	Pubkey    string `json:"pubkey"`
	Fingerprint string `json:"fingerprint"`
}

// RegisterOpts — параметры регистрации.
type RegisterOpts struct {
	InviteURL                string
	Name                     string
	PreviewOnly              bool
	ServerOverride           string
	AcceptServerFingerprint  string
}

// RegisterResult — результат регистрации.
type RegisterResult struct {
	UserID              string `json:"user_id"`
	SessionTokenPresent bool   `json:"session_token_present"`
	Inviter             *InviterInfo `json:"inviter,omitempty"`
	Valid               bool   `json:"valid,omitempty"`
	ServerURL           string `json:"server_url,omitempty"`
	ServerPubkey        string `json:"server_pubkey,omitempty"`
	ServerFP            string `json:"server_fp,omitempty"`
	IssuedByName        string `json:"issued_by_name,omitempty"`
	IssuedByPubkey      string `json:"issued_by_pubkey,omitempty"`
	ExpiresAt           int64  `json:"expires_at,omitempty"`
	RemainingUses       int    `json:"remaining_uses,omitempty"`
}

// InviterInfo — информация о пригласившем.
type InviterInfo struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Pubkey string `json:"pubkey"`
}

// LoginResult — результат логина.
type LoginResult struct {
	SessionTokenPresent bool `json:"session_token_present"`
}

// ProfileInfo — информация о профиле.
type ProfileInfo struct {
	Profile    string `json:"profile"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Pubkey     string `json:"pubkey"`
	ContactURL string `json:"contact_url"`
}

// AddContactOpts — параметры добавления контакта.
type AddContactOpts struct {
	ContactURL string
	Alias      string
	AutoVerify bool
}

// CreateInviteOpts — параметры создания инвайта.
type CreateInviteOpts struct {
	Label     string
	TTL       time.Duration
	MaxUses   int
}

// ListInvitesOpts — параметры списка инвайтов.
type ListInvitesOpts struct {
	Status string // "active", "expired", "all"; пусто = "active"
}

// InviteResult — результат создания инвайта.
type InviteResult struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	QRPayload string `json:"qr_payload,omitempty"`
}

// Invite — запись инвайта.
type Invite struct {
	Token     string `json:"token"`
	Label     string `json:"label"`
	MaxUses   int    `json:"max_uses"`
	UsedCount int    `json:"used_count"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	RevokedAt *int64 `json:"revoked_at,omitempty"`
}

// EnvelopeSendOpts — параметры отправки envelope.
type EnvelopeSendOpts struct {
	To              string
	PayloadType     string
	CallID          string
	TSOffset        time.Duration
	SDP             string
	Candidate       string
	BadSignature    bool
	WrongSigningKey bool
	SpoofFrom       string
	WaitAck         time.Duration
}

// EnvelopeAck — подтверждение доставки envelope.
type EnvelopeAck struct {
	EnvelopeID string                 `json:"envelope_id"`
	Response   map[string]interface{} `json:"response"`
}

// EnvelopeWaitOpts — параметры ожидания envelope.
type EnvelopeWaitOpts struct {
	Count     int
	Timeout   time.Duration
	FilterType string
	NoAck     bool
	NDJSON    bool
}

// EnvelopeSummary — краткая информация о полученном envelope.
type EnvelopeSummary struct {
	EnvelopeID string                 `json:"envelope_id"`
	From       string                 `json:"from"`
	TS         int64                  `json:"ts"`
	Payload    map[string]interface{} `json:"payload"`
}

// EnvelopeHandler — callback для полученного envelope.
type EnvelopeHandler func(summary EnvelopeSummary) error

// HealthResult — результат health check.
type HealthResult struct {
	ServerInfo  map[string]interface{} `json:"server_info"`
	PushWSOK    bool                   `json:"push_ws_ok"`
	PushWSError string                 `json:"push_ws_error,omitempty"`
}

// FetchTurnCredentialsOpts — опции получения TURN-credentials.
type FetchTurnCredentialsOpts struct {
	NoWSUpgrade bool
}

// TurnCredentials — TURN-credentials.
type TurnCredentials struct {
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	URIs       []string `json:"uris"`
}

// PushWakeup — push wakeup сообщение.
type PushWakeup struct {
	Type       string         `json:"type"`
	Kind       string         `json:"kind"`
	EnvelopeID string         `json:"envelope_id"`
	WakeupID   string         `json:"wakeup_id"`
	Data       map[string]any `json:"data,omitempty"`
}

type PushListenOpts struct {
	MaxEvents int
}

type PushWakeupHandler func(wakeup PushWakeup)

// PendingEnvelope — pending envelope для обработки.
type PendingEnvelope struct {
	EnvelopeID string
	From       string
	Payload    map[string]interface{}
}

// EnvelopePayload — расшифрованный payload.
type EnvelopePayload struct {
	Type   string `json:"type"`
	CallID string `json:"call_id,omitempty"`
}

// InvitePreview — preview инвайта.
type InvitePreview struct {
	Valid          bool   `json:"valid"`
	ServerURL      string `json:"server_url"`
	ServerPubkey   string `json:"server_pubkey"`
	ServerFP       string `json:"server_fp"`
	IssuedByName   string `json:"issued_by_name"`
	IssuedByPubkey string `json:"issued_by_pubkey"`
	ExpiresAt      int64  `json:"expires_at"`
	RemainingUses  int    `json:"remaining_uses"`
}

// ContactURL — parsed contact URL.
type ContactURL struct {
	Server string
	UserID string
	Pubkey string
	Name   string
	Fingerprint string
}

// InviteURL — parsed invite URL.
type InviteURL struct {
	Server string
	Token  string
	Srv    string
}

// TurnRelayOpts — параметры проверки TURN relay.
type TurnRelayOpts struct {
	ServerAddr string
	Username   string
	Credential string
	PeerAddr   string
}

// TurnRelayResult — результат проверки TURN relay.
type TurnRelayResult struct {
	OK          bool   `json:"ok"`
	RelayedAddr string `json:"relayed_addr,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
}
