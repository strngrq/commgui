package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/strngrq/commgui/internal/client/domain"
	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"nhooyr.io/websocket"
)

// closeCodeSignalReplaced — серверный код вытеснения signal-WS при появлении
// нового /ws/signal от того же user_id (002 §3.3). Получив его, listener
// должен молчаливо умереть и НЕ реконнектиться: серверный комментарий явно
// предупреждает о tight loop'е, который выселит signal активного звонка.
const closeCodeSignalReplaced websocket.StatusCode = 4002

// App — Wails-биндинги для GUI. Все методы, экспортируемые на фронтенд,
// живут здесь. Контекст устанавливается в startup().
type App struct {
	ctx     context.Context
	client  *domain.Client
	bus     *eventBus
	profile string

	mu              sync.Mutex
	activeCall      domain.CallSession
	pendingIncoming       *domain.IncomingCall
	pendingIncomingTimer  *time.Timer
	listener        domain.Listener
	listening       bool
	listenerCtx     context.Context
	listenerCancel  context.CancelFunc
	pushListening   bool
	pushHealthy     bool
	pushCancel      context.CancelFunc
	muted           bool
	outputVolume    int // 0..100
	audioPrefs      AudioPrefsDTO
	callLog         []CallLogEntryDTO
	debugLogMu      sync.Mutex
	debugLogFile    *os.File
}

func (a *App) recordNetHealthFailure(source string, err error) {
	nh := a.client.NetHealth()
	if nh.RecordFailure() {
		a.mu.Lock()
		hasActiveCall := a.activeCall != nil
		a.mu.Unlock()
		if hasActiveCall {
			nh.DeferRebuild()
			a.logDebug("net_health_rebuild_deferred", nil)
		} else {
			nh.MarkRebuilt()
			a.logDebug("net_health_rebuild", nil)
			a.client.RebuildTransport()
		}
	}
}

func (a *App) recordNetHealthSuccess() {
	a.client.NetHealth().RecordSuccess()
}

func (a *App) doRebuildTransport() {
	a.client.NetHealth().MarkRebuilt()
	a.logDebug("net_health_rebuild", nil)
	a.client.RebuildTransport()
}

// ==============================================================================
// DTOs
// ==============================================================================

type BootstrapResult struct {
	HasIdentity bool        `json:"hasIdentity"`
	Profile     *ProfileDTO `json:"profile,omitempty"`
}

type ProfileDTO struct {
	Profile string `json:"profile"`
	UserID  string `json:"user_id"`
	Name    string `json:"name"`
	Server  string `json:"server"`
}

type InvitePreviewDTO struct {
	Valid          bool   `json:"valid"`
	ServerURL      string `json:"server_url"`
	ServerFP       string `json:"server_fp"`
	IssuedByName   string `json:"issued_by_name"`
	IssuedByPubkey string `json:"issued_by_pubkey"`
	ExpiresAt      int64  `json:"expires_at"`
	RemainingUses  int    `json:"remaining_uses"`
}

type RegisterResultDTO struct {
	UserID              string `json:"user_id"`
	SessionTokenPresent bool   `json:"session_token_present"`
}

type ContactDTO struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Pubkey   string `json:"pubkey"`
	Alias    string `json:"alias"`
	Verified bool   `json:"verified"`
}

type ContactPreviewDTO struct {
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Pubkey      string `json:"pubkey"`
	Server      string `json:"server"`
	Fingerprint string `json:"fingerprint"`
}

type InviteDTO struct {
	Token     string `json:"token"`
	Label     string `json:"label"`
	MaxUses   int    `json:"max_uses"`
	UsedCount int    `json:"used_count"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	RevokedAt *int64 `json:"revoked_at,omitempty"`
}

type CreateInviteOpts struct {
	Label   string `json:"label"`
	TTL     string `json:"ttl"`     // "1h", "24h", "7d"
	MaxUses int    `json:"maxUses"` // 1, 3, 10
}

type InviteCreatedDTO struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

type ProfileFullDTO struct {
	Profile    string `json:"profile"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Server     string `json:"server"`
	ContactURL string `json:"contact_url"`
}

type MyContactDTO struct {
	ContactURL string `json:"contact_url"`
	QRPngB64   string `json:"qr_png_b64"`
}

type AudioDeviceDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "input" | "output"
	IsDefault bool   `json:"isDefault"`
}

type AudioDevicesListDTO struct {
	Input  []AudioDeviceDTO `json:"input"`
	Output []AudioDeviceDTO `json:"output"`
}

type AudioPrefsDTO struct {
	InputDeviceID    string `json:"inputDeviceId"`
	OutputDeviceID   string `json:"outputDeviceId"`
	PlaybackBufferMs int    `json:"playbackBufferMs"`
	PlaybackPrebufMs int    `json:"playbackPrebufMs"`
	EchoCancellation bool   `json:"echoCancellation"`
	AECTailMs        int    `json:"aecTailMs"`
	DebugLogEnabled  bool   `json:"debugLogEnabled"`
}

const (
	defaultPlaybackBufferMs = 640
	defaultPlaybackPrebufMs = 200
	defaultAECTailMs        = 200
)

type ServerStatusDTO struct {
	ServerURL string `json:"server_url"`
	ServerFP  string `json:"server_fp"`
	PushWS    bool   `json:"push_ws"`
	Version   string `json:"version"`
}

type CallLogEntryDTO struct {
	CallID     string `json:"call_id"`
	PeerUserID string `json:"peer_user_id"`
	PeerName   string `json:"peer_name"`
	Direction  string `json:"direction"` // "in" | "out"
	Outcome    string `json:"outcome"`
	DurationMs int64  `json:"duration_ms"`
	StartedAt  int64  `json:"started_at"`
}

type CallResultDTO struct {
	CallID string `json:"callId"`
}

type IncomingCallDTO struct {
	CallID   string     `json:"callId"`
	From     ContactDTO `json:"from"`
}

// ==============================================================================
// startup
// ==============================================================================

func NewApp() *App {
	return &App{profile: "default"}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.client = newClient(a.profile)
	a.bus = newEventBus(ctx)
	a.bus.onCallEnded = a.saveCallLogEntry
	a.bus.onCallClosed = a.handleCallClosed
	a.bus.logFn = a.logDebug
	a.loadCallLog()
	a.loadAudioPrefs()
	a.initDebugLog()
}

// handleCallClosed вызывается eventBus после CallEventClosed: освобождает
// слот activeCall и перезапускает listener.
// Listener restart в отдельной goroutine — чтобы "call-ended" ушёл во фронтенд
// без задержки на открытие WebSocket.
func (a *App) handleCallClosed(failureReason string) {
	nh := a.client.NetHealth()
	// Записываем signal_lost и проверяем отложенный rebuild через общий трекер.
	if failureReason == "signal_lost" {
		a.logDebug("net_health_fail", map[string]any{"source": "callSession", "error": "signal_lost"})
	}
	if nh.HandleCallClosed(failureReason) {
		a.doRebuildTransport()
	}

	a.mu.Lock()
	a.activeCall = nil
	a.mu.Unlock()

	go a.restartListener()
}

func (a *App) debugLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "commclient", "profiles", a.profile, "debug.log")
}

func (a *App) initDebugLog() {
	a.debugLogMu.Lock()
	defer a.debugLogMu.Unlock()
	if a.debugLogFile != nil {
		_ = a.debugLogFile.Close()
		a.debugLogFile = nil
	}
	a.mu.Lock()
	enabled := a.audioPrefs.DebugLogEnabled
	a.mu.Unlock()
	if !enabled {
		return
	}
	dir := filepath.Dir(a.debugLogPath())
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(a.debugLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	a.debugLogFile = f
	entry := map[string]any{
		"ts":      time.Now().UnixMilli(),
		"event":   "session_start",
		"profile": a.profile,
	}
	b, _ := json.Marshal(entry)
	fmt.Fprintf(a.debugLogFile, "%s\n", b)
}

func (a *App) logDebug(event string, data map[string]any) {
	a.debugLogMu.Lock()
	defer a.debugLogMu.Unlock()
	if a.debugLogFile == nil {
		return
	}
	now := time.Now()
	entry := map[string]any{"ts": now.UnixMilli(), "event": event}
	for k, v := range data {
		entry[k] = v
	}
	b, _ := json.Marshal(entry)
	fmt.Fprintf(a.debugLogFile, "%s %s\n", now.Format("2006-01-02 15:04:05.000"), b)
}

// ==============================================================================
// bootstrap / identity
// ==============================================================================

func (a *App) Bootstrap() BootstrapResult {
	st := a.client.State()
	if st == nil {
		return BootstrapResult{HasIdentity: false}
	}
	a.ensureListener()
	a.ensurePushListener()
	return BootstrapResult{
		HasIdentity: true,
		Profile: &ProfileDTO{
			Profile: a.profile,
			UserID:  st.User.UserID,
			Name:    st.User.Name,
			Server:  chooseServerURL(st),
		},
	}
}

func (a *App) PreviewInvite(url string) (InvitePreviewDTO, error) {
	res, err := a.client.Registrar.Register(a.ctx, domain.RegisterOpts{
		InviteURL:   url,
		PreviewOnly: true,
	})
	if err != nil {
		return InvitePreviewDTO{}, err
	}
	return InvitePreviewDTO{
		Valid:          res.Valid,
		ServerURL:      res.ServerURL,
		ServerFP:       res.ServerFP,
		IssuedByName:   res.IssuedByName,
		IssuedByPubkey: res.IssuedByPubkey,
		ExpiresAt:      res.ExpiresAt,
		RemainingUses:  res.RemainingUses,
	}, nil
}

func (a *App) Register(url, name string) (RegisterResultDTO, error) {
	res, err := a.client.Registrar.Register(a.ctx, domain.RegisterOpts{
		InviteURL: url,
		Name:      name,
	})
	if err != nil {
		return RegisterResultDTO{}, err
	}
	a.ensureListener()
	a.ensurePushListener()
	return RegisterResultDTO{
		UserID:              res.UserID,
		SessionTokenPresent: res.SessionTokenPresent,
	}, nil
}

// ==============================================================================
// contacts
// ==============================================================================

func (a *App) ListContacts() ([]ContactDTO, error) {
	list, err := a.client.Contacts.List()
	if err != nil {
		return nil, err
	}
	out := make([]ContactDTO, len(list))
	for i, c := range list {
		out[i] = ContactDTO{
			UserID:   c.UserID,
			Name:     c.Name,
			Pubkey:   c.Pubkey,
			Alias:    c.Alias,
			Verified: c.Verified,
		}
	}
	return out, nil
}

func (a *App) PreviewContact(rawURL string) (ContactPreviewDTO, error) {
	contact, err := domain.ParseContactURL(rawURL, "", false)
	if err != nil {
		return ContactPreviewDTO{}, err
	}
	fp := ""
	if pub, err := cryptox.DecodeBase64URL(contact.Pubkey, ed25519.PublicKeySize); err == nil {
		fp = cryptox.Fingerprint(pub, 8)
	}
	server := extractServerFromURL(rawURL)
	return ContactPreviewDTO{
		UserID:      contact.UserID,
		Name:        contact.Name,
		Pubkey:      contact.Pubkey,
		Server:      server,
		Fingerprint: fp,
	}, nil
}

func (a *App) AddContact(url, alias string) (ContactDTO, error) {
	c, err := a.client.Contacts.Add(a.ctx, domain.AddContactOpts{
		ContactURL: url,
		Alias:      alias,
	})
	if err != nil {
		return ContactDTO{}, err
	}
	return ContactDTO{
		UserID:   c.UserID,
		Name:     c.Name,
		Pubkey:   c.Pubkey,
		Alias:    c.Alias,
		Verified: c.Verified,
	}, nil
}

func (a *App) RenameContact(id, alias string) error {
	st := a.client.State()
	if st == nil {
		return domain.ErrNoState()
	}
	for i := range st.Contacts {
		if st.Contacts[i].UserID == id || st.Contacts[i].Alias == id {
			st.Contacts[i].Alias = alias
			return a.client.Ports().State.Save(a.profile, st)
		}
	}
	return domain.ErrContactNotFound(id)
}

func (a *App) RemoveContact(id string) error {
	return a.client.Contacts.Remove(a.ctx, id)
}

func (a *App) SafetyNumber(id string) (string, error) {
	return a.client.Contacts.SafetyNumber(id)
}

func (a *App) MarkVerified(id string, ok bool) error {
	st := a.client.State()
	if st == nil {
		return domain.ErrNoState()
	}
	for i := range st.Contacts {
		if st.Contacts[i].UserID == id || st.Contacts[i].Alias == id {
			st.Contacts[i].Verified = ok
			return a.client.Ports().State.Save(a.profile, st)
		}
	}
	return domain.ErrContactNotFound(id)
}

// ==============================================================================
// invites
// ==============================================================================

func (a *App) ListInvites(status string) ([]InviteDTO, error) {
	list, err := a.client.Contacts.ListInvites(a.ctx, domain.ListInvitesOpts{Status: status})
	if err != nil {
		return nil, err
	}
	out := make([]InviteDTO, len(list))
	for i, inv := range list {
		out[i] = InviteDTO{
			Token:     inv.Token,
			Label:     inv.Label,
			MaxUses:   inv.MaxUses,
			UsedCount: inv.UsedCount,
			CreatedAt: inv.CreatedAt,
			ExpiresAt: inv.ExpiresAt,
			RevokedAt: inv.RevokedAt,
		}
	}
	return out, nil
}

func (a *App) CreateInvite(opts CreateInviteOpts) (InviteCreatedDTO, error) {
	ttl, err := parseTTL(opts.TTL)
	if err != nil {
		return InviteCreatedDTO{}, err
	}
	res, err := a.client.Contacts.CreateInvite(a.ctx, domain.CreateInviteOpts{
		Label:   opts.Label,
		TTL:     ttl,
		MaxUses: opts.MaxUses,
	})
	if err != nil {
		return InviteCreatedDTO{}, err
	}
	return InviteCreatedDTO{Token: res.Token, URL: res.URL}, nil
}

func (a *App) RevokeInvite(token string) error {
	return a.client.Contacts.RevokeInvite(a.ctx, token)
}

// ==============================================================================
// profile
// ==============================================================================

func (a *App) Profile() (ProfileFullDTO, error) {
	info, err := a.client.Contacts.Profile("")
	if err != nil {
		return ProfileFullDTO{}, err
	}
	st := a.client.State()
	server := ""
	if st != nil {
		server = chooseServerURL(st)
	}
	return ProfileFullDTO{
		Profile:    info.Profile,
		UserID:     info.UserID,
		Name:       info.Name,
		Server:     server,
		ContactURL: info.ContactURL,
	}, nil
}

func (a *App) Rename(name string) error {
	st := a.client.State()
	if st == nil {
		return domain.ErrNoState()
	}
	st.User.Name = name
	return a.client.Ports().State.Save(a.profile, st)
}

func (a *App) MyContact() (MyContactDTO, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	inv, err := a.client.Contacts.CreateInvite(ctx, domain.CreateInviteOpts{
		TTL:     1 * time.Hour,
		MaxUses: 1,
	})
	if err != nil {
		return MyContactDTO{}, err
	}
	qrPNG, err := qrcode.Encode(inv.URL, qrcode.Medium, 256)
	if err != nil {
		return MyContactDTO{}, err
	}
	return MyContactDTO{
		ContactURL: inv.URL,
		QRPngB64:   base64.StdEncoding.EncodeToString(qrPNG),
	}, nil
}

func (a *App) DeleteProfile() error {
	a.stopListener()
	a.stopPushListener()

	// Инвалидируем сессию на сервере (best effort).
	_ = a.client.Registrar.Logout(a.ctx)

	// Удаляем все локальные данные профиля.
	dir := a.client.Ports().State.ProfileDir(a.profile)
	_ = os.RemoveAll(dir)

	// Сбрасываем in-memory состояние.
	a.client = newClient(a.profile)
	a.mu.Lock()
	a.activeCall = nil
	a.pendingIncoming = nil
	a.listener = nil
	a.listening = false
	a.muted = false
	a.callLog = nil
	a.outputVolume = 0
	a.mu.Unlock()

	return nil
}

// ==============================================================================
// settings / server
// ==============================================================================

func (a *App) ServerStatus() (ServerStatusDTO, error) {
	st := a.client.State()
	serverURL := chooseServerURL(st)
	serverFP := ""
	if st != nil && st.Server.Pubkey != "" {
		if pub, err := cryptox.DecodeBase64URL(st.Server.Pubkey, ed25519.PublicKeySize); err == nil {
			serverFP = cryptox.Fingerprint(pub, 16)
		}
	}

	// CheckLite вместо Check: пробное /ws/push в Health.Check выбило бы
	// наш собственный push-listener (4003 replaced), а сервер в журнале
	// клиента это видится как push_listener_disconnect.
	res, err := a.client.Health.CheckLite(a.ctx, "")
	a.mu.Lock()
	pushAlive := a.pushHealthy
	a.mu.Unlock()
	if err != nil {
		return ServerStatusDTO{
			ServerURL: serverURL,
			ServerFP:  serverFP,
			PushWS:    pushAlive,
		}, nil
	}

	version := ""
	if v, ok := res.ServerInfo["version"].(string); ok {
		version = v
	}
	return ServerStatusDTO{
		ServerURL: serverURL,
		ServerFP:  serverFP,
		PushWS:    pushAlive,
		Version:   version,
	}, nil
}

func (a *App) ListAudioDevices() (AudioDevicesListDTO, error) {
	devices, err := a.client.Ports().Audio.ListDevices(a.ctx)
	if err != nil {
		return AudioDevicesListDTO{}, err
	}
	out := AudioDevicesListDTO{}
	for _, d := range devices {
		dto := AudioDeviceDTO{
			ID:        d.ID,
			Name:      d.Name,
			Kind:      d.Kind,
			IsDefault: d.IsDefault,
		}
		if d.Kind == "input" {
			out.Input = append(out.Input, dto)
		} else {
			out.Output = append(out.Output, dto)
		}
	}
	return out, nil
}

func (a *App) SaveAudioPreferences(p AudioPrefsDTO) error {
	a.mu.Lock()
	prev := a.audioPrefs
	a.audioPrefs = p
	a.mu.Unlock()
	a.persistAudioPrefs()
	if prev.DebugLogEnabled != p.DebugLogEnabled {
		a.initDebugLog()
	}
	return nil
}

func (a *App) GetAudioPreferences() AudioPrefsDTO {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.audioPrefs
}

func (a *App) audioPrefsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "commclient", "profiles", a.profile, "audio_prefs.json")
}

func (a *App) loadAudioPrefs() {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := os.ReadFile(a.audioPrefsPath())
	if err != nil {
		// дефолты для новой установки
		a.audioPrefs.PlaybackBufferMs = defaultPlaybackBufferMs
		a.audioPrefs.PlaybackPrebufMs = defaultPlaybackPrebufMs
		a.audioPrefs.EchoCancellation = true
		a.audioPrefs.AECTailMs = defaultAECTailMs
		return
	}
	_ = json.Unmarshal(data, &a.audioPrefs)
	if a.audioPrefs.PlaybackBufferMs <= 0 {
		a.audioPrefs.PlaybackBufferMs = defaultPlaybackBufferMs
	}
	if a.audioPrefs.PlaybackPrebufMs <= 0 {
		a.audioPrefs.PlaybackPrebufMs = defaultPlaybackPrebufMs
	}
	if a.audioPrefs.AECTailMs <= 0 {
		a.audioPrefs.AECTailMs = defaultAECTailMs
	}
	// Для старых конфигов без поля echoCancellation — включаем по умолчанию
	if !a.audioPrefs.EchoCancellation && !hasField(data, "echoCancellation") {
		a.audioPrefs.EchoCancellation = true
	}
}

func (a *App) persistAudioPrefs() {
	a.mu.Lock()
	prefs := a.audioPrefs
	a.mu.Unlock()
	dir := filepath.Dir(a.audioPrefsPath())
	os.MkdirAll(dir, 0o700)
	data, _ := json.MarshalIndent(prefs, "", "  ")
	_ = os.WriteFile(a.audioPrefsPath(), data, 0o600)
}

// hasField проверяет наличие ключа в JSON-объекте (без полного парсинга).
func hasField(data []byte, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func (a *App) onPlaybackUnderrun(count int) {
	a.logDebug("audio_playback_underrun", map[string]any{"count": count})
}

func (a *App) SetRingtone(on bool) error {
	_ = on
	return nil // v2
}

func (a *App) TestMic(seconds int) error {
	_ = seconds
	return nil // v2 — требует AudioPipeline с real-режимом
}

// ==============================================================================
// calls
// ==============================================================================

// StartCall инициирует исходящий звонок контакту.
func (a *App) StartCall(contactId string) (CallResultDTO, error) {
	a.logDebug("start_call_enter", map[string]any{"contact": contactId})
	a.mu.Lock()
	a.logDebug("start_call_locked", map[string]any{"contact": contactId, "active_call_exists": a.activeCall != nil})
	defer a.mu.Unlock()

	if a.activeCall != nil {
		a.logDebug("start_call_busy", map[string]any{"contact": contactId, "active_id": a.activeCall.ID()})
		return CallResultDTO{}, errors.New("call already active")
	}

	a.logDebug("start_call_resolving", map[string]any{"contact": contactId})
	contact, err := a.client.Caller.ResolveContact(contactId)
	if err != nil {
		a.logDebug("start_call_resolve_error", map[string]any{"contact": contactId, "error": err.Error()})
		return CallResultDTO{}, err
	}
	a.logDebug("start_call_resolved", map[string]any{"contact": contactId, "peer_id": contact.UserID, "peer_name": displayName(contact)})

	audioMode := "real"
	recordPath := ""

	a.logDebug("start_call_outgoing_begin", map[string]any{"contact": contactId})
	// Серверная политика: один /ws/signal на user_id; новое соединение вытесняет
	// старое (4002 conflict). Если оставить listener активным, его pumpListener
	// получит 4002 и начнёт реконнектиться, выселяя наш же outgoing-signal —
	// звонок упадёт через ~3с с ice_state=new и rtp_received=0. Останавливаем
	// listener до Outgoing; handleCallClosed поднимет его после завершения.
	a.stopListenerLocked()
	sess, err := a.client.Caller.Outgoing(a.ctx, domain.OutgoingOpts{
		Contact:          contact,
		AudioMode:        audioMode,
		InputDeviceID:    a.audioPrefs.InputDeviceID,
		OutputDeviceID:   a.audioPrefs.OutputDeviceID,
		RecordPath:       recordPath,
		MaxDuration:      0,
		Ringtime:         30 * time.Second,
		PlaybackBufferMs: a.audioPrefs.PlaybackBufferMs,
		PlaybackPrebufMs: a.audioPrefs.PlaybackPrebufMs,
		EchoCancellation: a.audioPrefs.EchoCancellation,
		AECTailMs:        a.audioPrefs.AECTailMs,
		OnUnderrun:       a.onPlaybackUnderrun,
		OnForeignOffer:   a.onForeignOfferForActiveSession,
	})
	if err != nil {
		a.logDebug("outgoing_call_error", map[string]any{"error": err.Error(), "contact": contactId})
		// Outgoing провалился — handleCallClosed не сработает, поднимаем listener сами.
		a.ensureListenerLocked()
		return CallResultDTO{}, err
	}

	a.logDebug("outgoing_call_started", map[string]any{
		"call_id":             sess.ID(),
		"peer_id":              contact.UserID,
		"peer_name":            displayName(contact),
		"audio_mode":           audioMode,
		"input_device_id":      a.audioPrefs.InputDeviceID,
		"output_device_id":     a.audioPrefs.OutputDeviceID,
		"playback_buffer_ms":   a.audioPrefs.PlaybackBufferMs,
		"playback_prebuf_ms":   a.audioPrefs.PlaybackPrebufMs,
		"echo_cancellation":    a.audioPrefs.EchoCancellation,
	})
	a.activeCall = sess
	a.bus.watchSession(sess, contact.UserID, displayName(contact), "out")
	a.logDebug("start_call_done", map[string]any{"call_id": sess.ID()})
	return CallResultDTO{CallID: sess.ID()}, nil
}

// AcceptCall принимает входящий звонок.
func (a *App) AcceptCall(callId string) error {
	a.mu.Lock()
	in := a.pendingIncoming
	a.pendingIncoming = nil; a.stopPendingTimerLocked()
	a.mu.Unlock()

	if in == nil {
		return errors.New("no pending incoming call")
	}
	if in.CallID != callId {
		return errors.New("call id mismatch")
	}

	audioMode := "real"
	recordPath := ""

	sess, err := in.Accept(a.ctx, domain.AcceptOpts{
		AudioMode:        audioMode,
		InputDeviceID:    a.audioPrefs.InputDeviceID,
		OutputDeviceID:   a.audioPrefs.OutputDeviceID,
		RecordPath:       recordPath,
		MaxDuration:      0,
		PlaybackBufferMs: a.audioPrefs.PlaybackBufferMs,
		PlaybackPrebufMs: a.audioPrefs.PlaybackPrebufMs,
		EchoCancellation: a.audioPrefs.EchoCancellation,
		AECTailMs:        a.audioPrefs.AECTailMs,
		OnUnderrun:       a.onPlaybackUnderrun,
	})
	if err != nil {
		a.restartListener()
		a.logDebug("accept_call_error", map[string]any{"error": err.Error(), "call_id": callId})
		return err
	}

	a.logDebug("incoming_call_accepted", map[string]any{
		"call_id":              sess.ID(),
		"peer_id":              in.From.UserID,
		"peer_name":            displayName(in.From),
		"audio_mode":           audioMode,
		"input_device_id":      a.audioPrefs.InputDeviceID,
		"output_device_id":     a.audioPrefs.OutputDeviceID,
		"playback_buffer_ms":   a.audioPrefs.PlaybackBufferMs,
		"playback_prebuf_ms":   a.audioPrefs.PlaybackPrebufMs,
	})
	a.mu.Lock()
	a.activeCall = sess
	a.mu.Unlock()
	a.bus.watchSession(sess, in.From.UserID, displayName(in.From), "in")
	return nil
}

// DeclineCall отклоняет входящий звонок.
func (a *App) DeclineCall(callId string) error {
	a.mu.Lock()
	in := a.pendingIncoming
	a.pendingIncoming = nil; a.stopPendingTimerLocked()
	a.mu.Unlock()

	if in == nil {
		return errors.New("no pending incoming call")
	}
	if in.CallID != callId {
		return errors.New("call id mismatch")
	}

	a.logDebug("decline_call", map[string]any{"call_id": callId})
	if err := in.Decline(a.ctx); err != nil {
		a.logDebug("decline_call_error", map[string]any{"call_id": callId, "error": err.Error()})
	}
	a.logIncomingNotAnswered(in, "declined")
	a.restartListener()
	return nil
}

// stopPendingTimerLocked останавливает таймер auto-decline. Вызывается под a.mu.
func (a *App) stopPendingTimerLocked() {
	if a.pendingIncomingTimer != nil {
		a.pendingIncomingTimer.Stop()
		a.pendingIncomingTimer = nil
	}
}

// autoDeclinePending — авто-отклонение входящего по таймеру (§12.2).
func (a *App) autoDeclinePending(callID string) {
	a.mu.Lock()
	in := a.pendingIncoming
	if in == nil || in.CallID != callID {
		a.mu.Unlock()
		return
	}
	a.pendingIncoming = nil
	a.pendingIncomingTimer = nil
	a.mu.Unlock()

	a.logDebug("incoming_call_auto_declined", map[string]any{"call_id": callID})
	_ = in.DeclineWithReason(a.ctx, "missed")
	a.logIncomingNotAnswered(in, "missed")
	a.restartListener()
}

// shouldYieldToForeignOffer — лексикографическое сравнение callID для glare (§6a).
// Меньший callID выигрывает.
func shouldYieldToForeignOffer(localID, foreignID string) bool {
	return localID > foreignID
}

// onForeignOfferForActiveSession — обработчик glare; передаётся в
// callSessionConfig.onForeignOffer при создании Outgoing-сессии.
func (a *App) onForeignOfferForActiveSession(fo domain.ForeignOffer) {
	a.mu.Lock()
	sess := a.activeCall
	a.mu.Unlock()
	if sess == nil {
		return
	}
	if sess.State() != port.CallStateCalling {
		return
	}
	if fo.From.UserID != sess.Peer().UserID {
		return
	}
	if !shouldYieldToForeignOffer(sess.ID(), fo.CallID) {
		a.logDebug("glare_won", map[string]any{
			"local_call_id":   sess.ID(),
			"foreign_call_id": fo.CallID,
		})
		return
	}
	a.logDebug("glare_yielding", map[string]any{
		"local_call_id":   sess.ID(),
		"foreign_call_id": fo.CallID,
	})
	sigConn, err := sess.YieldSigConn()
	if err != nil {
		a.logDebug("glare_yield_error", map[string]any{"error": err.Error()})
		return
	}
	a.mu.Lock()
	a.activeCall = nil
	a.mu.Unlock()

	// Accept в горутине — не блокируем pumpSignaling (§6a #2).
	go func() {
		in := a.client.Caller.IncomingFromForeignOffer(a.ctx, sigConn, fo)
		sess2, err := in.Accept(a.ctx, domain.AcceptOpts{
			AudioMode:        "real",
			InputDeviceID:    a.audioPrefs.InputDeviceID,
			OutputDeviceID:   a.audioPrefs.OutputDeviceID,
			PlaybackBufferMs: a.audioPrefs.PlaybackBufferMs,
			PlaybackPrebufMs: a.audioPrefs.PlaybackPrebufMs,
			EchoCancellation: a.audioPrefs.EchoCancellation,
			AECTailMs:        a.audioPrefs.AECTailMs,
			OnUnderrun:       a.onPlaybackUnderrun,
		})
		if err != nil {
			a.logDebug("glare_accept_error", map[string]any{"error": err.Error()})
			a.restartListener()
			return
		}
		a.mu.Lock()
		a.activeCall = sess2
		a.mu.Unlock()
		a.bus.watchSession(sess2, fo.From.UserID, displayName(fo.From), "in-glare")
	}()
}

// HangUp завершает активный звонок.
func (a *App) HangUp() error {
	a.mu.Lock()
	sess := a.activeCall
	a.activeCall = nil
	a.mu.Unlock()

	if sess == nil {
		return errors.New("no active call")
	}
	a.logDebug("hangup", map[string]any{"call_id": sess.ID()})
	hangupCtx, hangupCancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer hangupCancel()
	if err := sess.Hangup(hangupCtx); err != nil {
		a.logDebug("hangup_error", map[string]any{"call_id": sess.ID(), "error": err.Error()})
		return err
	}
	return nil
}

// ToggleMute переключает mute микрофона.
func (a *App) ToggleMute() (bool, error) {
	a.mu.Lock()
	a.muted = !a.muted
	muted := a.muted
	a.mu.Unlock()
	return muted, nil
}

// SetOutputVolume устанавливает громкость воспроизведения (0..100).
func (a *App) SetOutputVolume(v int) error {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	a.mu.Lock()
	a.outputVolume = v
	a.mu.Unlock()
	return nil
}

// ==============================================================================
// call history
// ==============================================================================

func (a *App) ListCalls(limit, offset int) ([]CallLogEntryDTO, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	log := a.callLog
	if offset >= len(log) {
		return []CallLogEntryDTO{}, nil
	}
	end := offset + limit
	if end > len(log) {
		end = len(log)
	}
	return log[offset:end], nil
}

func (a *App) callLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "commclient", "profiles", a.profile, "calls.json")
}

func (a *App) loadCallLog() {
	data, err := os.ReadFile(a.callLogPath())
	if err != nil {
		return
	}
	var log []CallLogEntryDTO
	if err := json.Unmarshal(data, &log); err != nil {
		return
	}
	a.callLog = log
}

func (a *App) saveCallLog() {
	dir := filepath.Dir(a.callLogPath())
	os.MkdirAll(dir, 0o700)
	data, _ := json.MarshalIndent(a.callLog, "", "  ")
	os.WriteFile(a.callLogPath(), data, 0o600)
}

func (a *App) saveCallLogEntry(entry CallLogEntryDTO) {
	a.mu.Lock()
	a.callLog = append([]CallLogEntryDTO{entry}, a.callLog...)
	if len(a.callLog) > 500 {
		a.callLog = a.callLog[:500]
	}
	a.mu.Unlock()
	a.saveCallLog()
}

// logIncomingNotAnswered пишет запись в историю звонков для входящего, который
// не дошёл до сессии (declined пользователем, auto-rejected с busy). outcome —
// "declined" или "missed" — UI окрашивает их одинаково, но семантика разная.
func (a *App) logIncomingNotAnswered(in *domain.IncomingCall, outcome string) {
	if in == nil {
		return
	}
	a.saveCallLogEntry(CallLogEntryDTO{
		CallID:     in.CallID,
		PeerUserID: in.From.UserID,
		PeerName:   displayName(in.From),
		Direction:  "in",
		Outcome:    outcome,
		DurationMs: 0,
		StartedAt:  time.Now().UnixMilli(),
	})
}

// ==============================================================================
// utilities
// ==============================================================================

func (a *App) Clipboard(text string) error {
	return runtime.ClipboardSetText(a.ctx, text)
}

// ==============================================================================
// listener lifecycle
// ==============================================================================

// ensureListener запускает фоновую goroutine, держащую listener входящих
// звонков. Goroutine сама реконнектит соединение при разрывах с backoff и
// выходит после доставки одного incoming-call (listener одноразовый).
// Идемпотентно: повторный вызов при активном listener'е — no-op.
func (a *App) ensureListener() {
	a.mu.Lock()
	a.ensureListenerLocked()
	a.mu.Unlock()
}

// ensureListenerLocked — версия для caller'ов, которые уже держат a.mu
// (например, StartCall). Не берёт mutex повторно (sync.Mutex не reentrant).
func (a *App) ensureListenerLocked() {
	if a.listening {
		return
	}
	st := a.client.State()
	if st == nil || st.Session.Token == "" {
		a.logDebug("listener_skip", map[string]any{"reason": "no_session"})
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.listenerCtx = ctx
	a.listenerCancel = cancel
	a.listening = true
	a.logDebug("listener_started", nil)
	go a.pumpListener(ctx)
}

// stopListener останавливает прослушивание. Goroutine pumpListener увидит
// отменённый ctx и завершится; defer внутри неё очистит листенер только
// если контекст всё ещё активный (защита от перезатирания состояния новой
// pumpListener-goroutine, запущенной параллельно).
func (a *App) stopListener() {
	a.logDebug("listener_stop", nil)
	a.mu.Lock()
	a.stopListenerLocked()
	a.mu.Unlock()
}

// stopListenerLocked — версия для caller'ов, которые уже держат a.mu.
func (a *App) stopListenerLocked() {
	if a.listenerCancel != nil {
		a.listenerCancel()
		a.listenerCancel = nil
	}
	if a.listener != nil {
		a.listener.Close()
		a.listener = nil
	}
	a.listenerCtx = nil
	a.listening = false
}

// restartListener перезапускает прослушивание после accept/decline/закрытия
// звонка либо после push wakeup "incoming_call".
func (a *App) restartListener() {
	a.logDebug("listener_restart", nil)
	a.stopListener()
	a.ensureListener()
}

// pumpListener держит signaling-listener активным: при разрыве переоткрывает
// его с экспоненциальным backoff. Завершается, когда либо доставлен входящий
// звонок (listener одноразовый), либо отменён ctx через stopListener.
func (a *App) pumpListener(ctx context.Context) {
	const minBackoff = 1 * time.Second
	const maxBackoff = 30 * time.Second
	backoff := minBackoff

	defer func() {
		a.mu.Lock()
		// Снимаем состояние только если мы — текущая активная goroutine.
		// Параллельный stopListener мог уже подменить ctx новой pumpListener'ой.
		if a.listenerCtx == ctx {
			if a.listener != nil {
				a.listener.Close()
				a.listener = nil
			}
			if a.listenerCancel != nil {
				a.listenerCancel()
				a.listenerCancel = nil
			}
			a.listenerCtx = nil
			a.listening = false
		}
		a.mu.Unlock()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		listener, err := a.client.Caller.Listen(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.logDebug("listener_error", map[string]any{
				"error":      err.Error(),
				"backoff_ms": backoff.Milliseconds(),
			})
			a.bus.emit("listener-error", err.Error())
			a.recordNetHealthFailure("pumpListener", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		a.mu.Lock()
		a.listener = listener
		a.mu.Unlock()
		backoff = minBackoff
	a.recordNetHealthSuccess()

		delivered, displaced := a.runListener(ctx, listener)
		a.mu.Lock()
		a.listener = nil
		a.mu.Unlock()
		if delivered || displaced || ctx.Err() != nil {
			// displaced=true: сервер вытеснил наш signal по 4002 (другой /ws/signal
			// от того же user_id, обычно — наш собственный outgoing-call). Tight-loop
			// реконнект вытеснил бы signal активного звонка — выходим и ждём явный
			// ensureListener (через handleCallClosed после завершения звонка).
			return
		}
		// Соединение оборвалось без доставки — небольшой backoff и снова.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runListener читает один incoming-call из listener'а либо ждёт его обрыва.
// Возвращает (delivered, displaced):
//   - delivered=true — успешно получен incoming-call, listener одноразово закрыт
//   - displaced=true — сервер вытеснил signal с close-кодом 4002 (conflict)
//   - оба false — обычная ошибка/обрыв/отмена ctx, можно реконнектить с backoff
func (a *App) runListener(ctx context.Context, listener domain.Listener) (delivered, displaced bool) {
	for {
		select {
		case <-ctx.Done():
			return false, false
		case in, ok := <-listener.Incoming():
			if !ok {
				a.logDebug("listener_closed", nil)
				return false, false
			}

			// SC-25: если уже есть активный звонок — auto-reject с busy.
			a.mu.Lock()
			activeCall := a.activeCall
			a.mu.Unlock()
			if activeCall != nil {
				a.logDebug("incoming_call_busy", map[string]any{
					"call_id":        in.CallID,
					"active_call_id": activeCall.ID(),
				})
				_ = in.DeclineWithReason(a.ctx, "busy")
				a.logIncomingNotAnswered(&in, "missed")
				continue
			}

			a.mu.Lock()
			a.pendingIncoming = &in
			if a.pendingIncomingTimer != nil {
				a.pendingIncomingTimer.Stop()
			}
			a.pendingIncomingTimer = time.AfterFunc(35*time.Second, func() {
				a.autoDeclinePending(in.CallID)
			})
			a.mu.Unlock()

			a.logDebug("incoming_call_arrived", map[string]any{
				"call_id": in.CallID,
				"from_id": in.From.UserID,
				"from":    displayName(in.From),
			})
			a.bus.emit("incoming-call", IncomingCallDTO{
				CallID: in.CallID,
				From: ContactDTO{
					UserID:   in.From.UserID,
					Name:     displayName(in.From),
					Pubkey:   in.From.Pubkey,
					Alias:    in.From.Alias,
					Verified: in.From.Verified,
				},
			})

			// SC-09 отключён тактически: WatchCancel запускал второй Read на тот же
			// signaling WS, что несовместимо с nhooyr/websocket — после Accept
			// pumpSignaling сразу падал с ошибкой через ~90 мс. UX-регресс: если
			// звонящий повесит до Accept, incoming-экран не уйдёт сам — пользователь
			// должен нажать Отклонить. Архитектурный фикс — слить WatchCancel и
			// pumpSignaling в одну goroutine.

			return true, false

		case err, ok := <-listener.Errors():
			if !ok {
				return false, false
			}
			a.logDebug("listener_error", map[string]any{"error": err.Error()})
			a.bus.emit("listener-error", err.Error())
			if websocket.CloseStatus(err) == closeCodeSignalReplaced {
				return false, true
			}
			return false, false
		}
	}
}

// ==============================================================================
// push listener (server wakeups: invite_used, …)
// ==============================================================================

// ensurePushListener запускает фоновое прослушивание push-WS. Идемпотентно.
func (a *App) ensurePushListener() {
	a.mu.Lock()
	if a.pushListening {
		a.mu.Unlock()
		return
	}
	st := a.client.State()
	if st == nil || st.Session.Token == "" {
		a.mu.Unlock()
		a.logDebug("push_listener_skip", map[string]any{"reason": "no_session"})
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.pushCancel = cancel
	a.pushListening = true
	a.mu.Unlock()
	a.logDebug("push_listener_started", nil)
	go a.pumpPush(ctx)
}

func (a *App) stopPushListener() {
	a.mu.Lock()
	if a.pushCancel != nil {
		a.pushCancel()
		a.pushCancel = nil
	}
	a.pushListening = false
	a.pushHealthy = false
	a.mu.Unlock()
	a.logDebug("push_listener_stop", nil)
}

// pumpPush держит постоянное push-WS подключение с retry/backoff.
func (a *App) pumpPush(ctx context.Context) {
	const minBackoff = 1 * time.Second
	const maxBackoff = 30 * time.Second
	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		a.pushHealthy = true
		a.mu.Unlock()
		err := a.client.Health.ListenPush(ctx, domain.PushListenOpts{}, a.handlePushWakeup)
		a.mu.Lock()
		a.pushHealthy = false
		a.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.logDebug("push_listener_disconnect", map[string]any{
				"error":      err.Error(),
				"backoff_ms": backoff.Milliseconds(),
			})
			a.recordNetHealthFailure("pumpPush", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (a *App) handlePushWakeup(w domain.PushWakeup) {
	a.recordNetHealthSuccess()
	a.logDebug("push_wakeup", map[string]any{
		"kind":      w.Kind,
		"wakeup_id": w.WakeupID,
	})
	switch w.Kind {
	case "invite_used":
		a.handleInviteUsed(w.Data)
	case "incoming_call":
		// Wakeup означает, что у сервера есть pending envelope для нас, но
		// в момент send наш signaling WS был закрыт. Переоткрываем listener:
		// его handshake (`ready`) вызовет drainPending на сервере и envelope
		// придёт в pumpIncoming.
		a.mu.Lock()
		hasActive := a.activeCall != nil
		hasPending := a.pendingIncoming != nil
		a.mu.Unlock()
		if hasActive || hasPending {
			a.logDebug("push_wakeup_skip_restart", map[string]any{
				"reason":     "busy",
				"has_active": hasActive,
				"has_pending": hasPending,
			})
			return
		}
		go a.restartListener()
	}
}

// handleInviteUsed обрабатывает wakeup-событие "получатель воспользовался моим инвайтом":
// сохраняет нового пользователя в адресную книгу и уведомляет фронт.
func (a *App) handleInviteUsed(data map[string]any) {
	if data == nil {
		a.logDebug("invite_used_skip", map[string]any{"reason": "no_data"})
		return
	}
	userID, _ := data["user_id"].(string)
	name, _ := data["name"].(string)
	pubkey, _ := data["pubkey"].(string)
	if userID == "" || pubkey == "" {
		a.logDebug("invite_used_skip", map[string]any{
			"reason": "missing_fields",
			"data":   data,
		})
		return
	}
	contact := port.Contact{
		UserID:   userID,
		Name:     name,
		Pubkey:   pubkey,
		Verified: true,
		AddedAt:  time.Now().UnixMilli(),
	}
	if err := a.client.Contacts.Upsert(a.ctx, contact); err != nil {
		a.logDebug("invite_used_upsert_error", map[string]any{"error": err.Error()})
		return
	}
	a.logDebug("invite_used_contact_added", map[string]any{
		"user_id": userID,
		"name":    name,
	})
	a.bus.emit("contact-added", ContactDTO{
		UserID:   contact.UserID,
		Name:     contact.Name,
		Pubkey:   contact.Pubkey,
		Alias:    contact.Alias,
		Verified: contact.Verified,
	})
}

// ==============================================================================
// helpers
// ==============================================================================

// displayName возвращает alias, если задан, иначе name.
func displayName(c port.Contact) string {
	if c.Alias != "" {
		return c.Alias
	}
	return c.Name
}

func chooseServerURL(st *port.State) string {
	if st == nil || st.Server.URL == "" {
		return ""
	}
	return strings.TrimRight(st.Server.URL, "/")
}

func extractServerFromURL(rawURL string) string {
	parts := strings.Split(rawURL, "#")
	if len(parts) != 2 {
		return ""
	}
	values, err := url.ParseQuery(parts[1])
	if err != nil {
		return ""
	}
	s, _ := url.QueryUnescape(values.Get("s"))
	return s
}

func parseTTL(s string) (time.Duration, error) {
	switch s {
	case "1h":
		return time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	default:
		return 24 * time.Hour, nil
	}
}
