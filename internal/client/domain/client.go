package domain

import (
	"crypto/ed25519"
	"errors"

	"github.com/strngrq/commgui/internal/client/port"
)

// Ports — набор всех портов, нужных Client'у.
type Ports struct {
	State     port.StateStore
	Crypto    port.CryptoProvider
	HTTP      port.HTTPClient
	Signaling port.SignalingClient
	WebRTC    port.WebRTCManager
	Audio     port.AudioEngine
	Turn      port.TurnClient
}

// Client — фасад над поддоменами клиента. Пристёгнут к одному профилю,
// загружает state и приватный ключ один раз в конструкторе. Поддомены
// используют *Client для доступа к актуальному state (важно после Login,
// который обновляет session token).
type Client struct {
	profile string
	ports   Ports

	state *port.State
	priv  ed25519.PrivateKey

	Registrar  *Registrar
	Caller     *Caller
	Contacts   *ContactBook
	EnvelopeIO *EnvelopeIO
	Health     *HealthCheck
}

// NewClient создаёт Client для профиля. Если профиль ещё не инициализирован,
// возвращается клиент с state=nil — методы, требующие state, вернут ErrNoState.
// Это позволяет одному и тому же объекту использоваться для bootstrap-операций
// (Init, Register, RegisterPreview) и для пост-bootstrap операций (Login, Call, …).
func NewClient(profile string, ports Ports) *Client {
	c := &Client{profile: profile, ports: ports}
	if st, err := ports.State.Load(profile); err == nil && st != nil {
		c.state = st
		if priv, err := ports.Crypto.LoadPrivateKey(ports.State.ProfileDir(profile)); err == nil {
			c.priv = priv
		}
	}
	c.Registrar = &Registrar{client: c}
	c.Contacts = &ContactBook{client: c}
	c.EnvelopeIO = &EnvelopeIO{client: c}
	c.Health = &HealthCheck{client: c}
	c.Caller = &Caller{
		client:    c,
		signaling: ports.Signaling,
		webrtc:    ports.WebRTC,
		audio:     ports.Audio,
	}
	return c
}

// Profile возвращает имя профиля.
func (c *Client) Profile() string { return c.profile }

// State возвращает текущий state. Может быть nil, если профиль не инициализирован.
func (c *Client) State() *port.State { return c.state }

// PrivateKey возвращает приватный ключ профиля. Может быть nil, если профиль не инициализирован.
func (c *Client) PrivateKey() ed25519.PrivateKey { return c.priv }

// Ports возвращает набор портов, чтобы поддомены могли их использовать.
func (c *Client) Ports() Ports { return c.ports }

// requireState возвращает ошибку, если state не загружен.
func (c *Client) requireState() error {
	if c.state == nil {
		return ErrNoState()
	}
	return nil
}

// requireSession проверяет, что есть state и session token.
func (c *Client) requireSession() error {
	if err := c.requireState(); err != nil {
		return err
	}
	if c.state.Session.Token == "" {
		return ErrAuthExpired("session token missing")
	}
	return nil
}

// setStateAfterRegister обновляет state и priv после успешной регистрации/логина.
// Вызывается из Registrar.
func (c *Client) setStateAfterRegister(st *port.State, priv ed25519.PrivateKey) {
	c.state = st
	c.priv = priv
}

// reloadState перечитывает state с диска (после mutating operations Registrar/ContactBook).
func (c *Client) reloadState() {
	if st, err := c.ports.State.Load(c.profile); err == nil && st != nil {
		c.state = st
	}
}

// Sentinel — exported errors helpers.
var (
	ErrClientNotInitialized = errors.New("client not initialized")
)
