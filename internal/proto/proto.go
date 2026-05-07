package proto

import (
	"errors"
	"regexp"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	Version             = "0.1.0"
	APIVersion         = "v1"
	MaxRESTBodyBytes   = 8 * 1024
	MaxEnvelopeBytes   = 64 * 1024
	DefaultSessionDays = 30
)

var (
	ErrInvalidRequest = errors.New("invalid request")
	ulidRe            = regexp.MustCompile(`^[0123456789ABCDEFGHJKMNPQRSTVWXYZ]{26}$`)
	inviteRe          = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type ServerInfo struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	APIVersions      []string `json:"api_versions"`
	ServerPubkey     string   `json:"server_pubkey"`
	OpenRegistration bool     `json:"open_registration"`
	PushTransports   []string `json:"push_transports"`
	TURNURIs         []string `json:"turn_uris"`
	MinClientVersion string   `json:"min_client_version"`
}

type RegisterRequest struct {
	InviteToken string `json:"invite_token"`
	ClientPub   string `json:"client_pub"`
	Name        string `json:"name"`
	TS          int64  `json:"ts"`
	Signature   string `json:"signature"`
}

type RegisterResponse struct {
	UserID       string       `json:"user_id"`
	SessionToken string       `json:"session_token"`
	ExpiresAt    int64        `json:"expires_at"`
	Inviter      *PublicUser  `json:"inviter"`
}

type PublicUser struct {
	UserID string `json:"user_id,omitempty"`
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Pubkey string `json:"pubkey"`
}

type ChallengeRequest struct {
	UserID string `json:"user_id"`
}

type ChallengeResponse struct {
	Nonce      string `json:"nonce"`
	ValidUntil int64  `json:"valid_until"`
}

type SessionRequest struct {
	UserID    string `json:"user_id"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type SessionResponse struct {
	SessionToken string     `json:"session_token"`
	ExpiresAt    int64      `json:"expires_at"`
	User         PublicUser `json:"user"`
}

type InviteCreateRequest struct {
	TTLSeconds int64  `json:"ttl_seconds"`
	MaxUses    int    `json:"max_uses"`
	Label      string `json:"label,omitempty"`
}

type InviteResponse struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	QRPayload string `json:"qr_payload"`
	ExpiresAt int64  `json:"expires_at"`
	MaxUses   int    `json:"max_uses"`
}

type InvitePreviewResponse struct {
	Valid          bool    `json:"valid"`
	Reason         string  `json:"reason,omitempty"`
	ServerName     string  `json:"server_name,omitempty"`
	IssuedByName   *string `json:"issued_by_name,omitempty"`
	IssuedByPubkey *string `json:"issued_by_pubkey,omitempty"`
	Label          *string `json:"label,omitempty"`
	ExpiresAt      int64   `json:"expires_at,omitempty"`
	RemainingUses  int     `json:"remaining_uses,omitempty"`
}

type Envelope struct {
	ID         string `json:"id"`
	To         string `json:"to"`
	From       string `json:"from"`
	TS         int64  `json:"ts"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Signature  string `json:"signature"`
}

func ValidULID(value string) bool {
	return ulidRe.MatchString(value)
}

func ValidInviteToken(value string) bool {
	return inviteRe.MatchString(value)
}

func ValidateTimestamp(now time.Time, ts int64, skew time.Duration) bool {
	t := time.UnixMilli(ts)
	if t.After(now.Add(skew)) || t.Before(now.Add(-skew)) {
		return false
	}
	return true
}

func NormalizeName(name string) (string, error) {
	name = norm.NFC.String(name)
	if !utf8.ValidString(name) {
		return "", ErrInvalidRequest
	}
	count := 0
	for _, r := range name {
		count++
		if r <= 0x1f || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return "", ErrInvalidRequest
		}
		switch r {
		case 0x200b, 0x200c, 0x200d, 0xfeff:
			return "", ErrInvalidRequest
		}
		if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return "", ErrInvalidRequest
		}
	}
	if count < 1 || count > 64 {
		return "", ErrInvalidRequest
	}
	return name, nil
}
