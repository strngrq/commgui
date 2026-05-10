package domain

import (
	"errors"
	"fmt"
)

// Exit codes per 004-test_client.md §5.2.
const (
	ExitOK           = 0
	ExitGeneralError = 1
	ExitValidation   = 2
	ExitNetwork      = 10
	ExitAuthExpired  = 11
	ExitForbidden    = 12
	ExitServerError  = 20
	ExitNotFound     = 21
	ExitConflict     = 22
	ExitCrypto       = 30
	ExitAudio        = 40
	ExitWebRTC       = 50
)

// DomainError — ошибка бизнес-логики с кодом выхода и машиночитаемым кодом.
type DomainError struct {
	Code    string
	Message string
	Details map[string]interface{}
	Exit    int
	Err     error
}

func (e *DomainError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *DomainError) Unwrap() error {
	return e.Err
}

// NewDomainError создаёт DomainError с заданным exit code.
func NewDomainError(code, message string, exit int) *DomainError {
	return &DomainError{Code: code, Message: message, Exit: exit}
}

// NewDomainErrorf — форматированный вариант.
func NewDomainErrorf(code, message string, exit int, args ...interface{}) *DomainError {
	return &DomainError{Code: code, Message: fmt.Sprintf(message, args...), Exit: exit}
}

// WithDetails добавляет детали к ошибке.
func (e *DomainError) WithDetails(key string, value interface{}) *DomainError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// WithErr оборачивает underlying error.
func (e *DomainError) WithErr(err error) *DomainError {
	e.Err = err
	return e
}

// Конструкторы для стандартных ошибок.

func ErrServerUnreachable(msg string, err error) *DomainError {
	return NewDomainError("server_unreachable", msg, ExitNetwork).WithErr(err)
}

func ErrAuthExpired(msg string) *DomainError {
	return NewDomainError("auth_expired", msg, ExitAuthExpired)
}

func ErrForbidden(msg string) *DomainError {
	return NewDomainError("forbidden", msg, ExitForbidden)
}

func ErrValidation(code, msg string) *DomainError {
	return NewDomainError(code, msg, ExitValidation)
}

func ErrServer(code, msg string) *DomainError {
	return NewDomainError(code, msg, ExitServerError)
}

func ErrNotFound(msg string) *DomainError {
	return NewDomainError("not_found", msg, ExitNotFound)
}

func ErrConflict(code, msg string) *DomainError {
	return NewDomainError(code, msg, ExitConflict)
}

func ErrCrypto(msg string, err error) *DomainError {
	return NewDomainError("crypto_error", msg, ExitCrypto).WithErr(err)
}

func ErrAudio(msg string, err error) *DomainError {
	return NewDomainError("audio_error", msg, ExitAudio).WithErr(err)
}

func ErrWebRTC(msg string, err error) *DomainError {
	return NewDomainError("webrtc_error", msg, ExitWebRTC).WithErr(err)
}

func ErrInvalidInvite(msg string) *DomainError {
	return NewDomainError("invalid_invite", msg, ExitValidation)
}

func ErrServerPinMismatch(inviteFP, serverFP string) *DomainError {
	return NewDomainError(
		"server_pin_mismatch",
		fmt.Sprintf("invite srv=%s, server fingerprint=%s", inviteFP, serverFP),
		ExitCrypto,
	)
}

func ErrMissingServerPin() *DomainError {
	return NewDomainError(
		"missing_srv",
		"invite URL is missing srv=<fingerprint>; refusing to register without server pinning",
		ExitValidation,
	)
}

func ErrInviteNotUsable(reason string) *DomainError {
	return NewDomainError("invite_expired", "invite is not usable: "+reason, ExitConflict)
}

func ErrMissingNameForRegistration() *DomainError {
	return NewDomainError("missing_name", "--name is required for registration", ExitValidation)
}

func ErrNoStateForLogin() *DomainError {
	return NewDomainError("no_state", "no state found; run init or register first", ExitValidation)
}

func ErrMissingServerURL() *DomainError {
	return NewDomainError("no_server_url", "server URL is required", ExitValidation)
}

func ErrEnvelopeError(code, msg string) *DomainError {
	return NewDomainErrorf(code, "%s", ExitServerError, msg)
}

func ErrEnvelopeDeliveryFailed(reason string) *DomainError {
	return NewDomainErrorf("envelope_failed", "envelope delivery failed: %s", ExitServerError, reason)
}

func ErrInvalidCallAnswer(msg string) *DomainError {
	return NewDomainErrorf("invalid_call_answer", "%s", ExitWebRTC, msg)
}

func ErrInvalidCallOffer(msg string) *DomainError {
	return NewDomainErrorf("invalid_call_offer", "%s", ExitWebRTC, msg)
}

func ErrNotImplemented(msg string) *DomainError {
	return NewDomainErrorf("not_implemented", "%s", ExitGeneralError, msg)
}

func ErrContactNotFound(key string) *DomainError {
	return NewDomainErrorf("contact_not_found", "contact not found: %s", ExitNotFound, key)
}

func ErrNoState() *DomainError {
	return NewDomainError("no_state", "no state found; run init or register first", ExitValidation)
}

func ErrNoSession() *DomainError {
	return NewDomainError("no_session", "session required; run login or register", ExitAuthExpired)
}

func ErrNoServerURL() *DomainError {
	return NewDomainError("no_server_url", "server URL is required", ExitValidation)
}

func ErrInviteExpired(reason string) *DomainError {
	return NewDomainError("invite_expired", "invite is not usable: "+reason, ExitConflict)
}

func ErrTSOffset() *DomainError {
	return NewDomainError("ts_skew", "timestamp skew too large", ExitServerError)
}

func ErrInvalidSignature() *DomainError {
	return NewDomainError("invalid_signature", "signature verification failed", ExitCrypto)
}

func ErrFromMismatch() *DomainError {
	return NewDomainError("from_mismatch", "envelope from does not match signer", ExitCrypto)
}

// IsDomainError проверяет, является ли ошибка DomainError.
func IsDomainError(err error) bool {
	if err == nil {
		return false
	}
	var de *DomainError
	return errors.As(err, &de)
}

// ExitCodeForWSOrREST маппинг серверных кодов ошибок в exit codes (из clierr.go).
func ExitCodeForServerCode(code string) int {
	switch code {
	case "ts_skew", "invalid_signature", "from_mismatch":
		return ExitCrypto
	case "unauthenticated":
		return ExitAuthExpired
	case "forbidden":
		return ExitForbidden
	case "invalid_request", "recipient_unknown", "payload_too_large":
		return ExitServerError
	case "rate_limited":
		return ExitConflict
	default:
		return ExitGeneralError
	}
}
