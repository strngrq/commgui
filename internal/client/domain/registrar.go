package domain

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
	"github.com/strngrq/commgui/internal/wire"
)

// Registrar реализует bootstrap-операции профиля: Init, Register, Login, Logout.
// Все методы пишут в client.state — после успешного вызова клиент готов
// к остальным операциям, не требуя пересоздания.
type Registrar struct {
	client *Client
}

// Init создаёт новый профиль: генерирует ключ, сохраняет state и identity.key.
func (r *Registrar) Init(ctx context.Context, opts InitOpts) (*InitResult, error) {
	c := r.client
	pub, priv, err := c.ports.Crypto.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	dir := c.ports.State.ProfileDir(c.profile)

	st := &port.State{
		SchemaVersion: 1,
		Server:        port.ServerInfo{URL: opts.ServerURL},
		User: port.UserInfo{
			Name:   opts.Name,
			Pubkey: cryptox.EncodeBase64URL(pub),
		},
	}

	if err := c.ports.State.Save(c.profile, st); err != nil {
		return nil, err
	}

	if err := c.ports.Crypto.SavePrivateKey(dir, priv); err != nil {
		return nil, fmt.Errorf("save private key: %w", err)
	}

	c.setStateAfterRegister(st, priv)

	return &InitResult{
		Profile:     c.profile,
		Pubkey:      st.User.Pubkey,
		Fingerprint: cryptox.Fingerprint(pub, 16),
	}, nil
}

// Register выполняет регистрацию по invite URL.
func (r *Registrar) Register(ctx context.Context, opts RegisterOpts) (*RegisterResult, error) {
	c := r.client

	inviteURL, err := ParseInviteURL(opts.InviteURL)
	if err != nil {
		return nil, err
	}

	server := inviteURL.Server
	if opts.ServerOverride != "" {
		server = strings.TrimRight(opts.ServerOverride, "/")
	}

	info, err := r.fetchServerInfo(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("server-info: %w", err)
	}

	fingerprint, err := serverFingerprint(info.ServerPubkey)
	if err != nil {
		return nil, fmt.Errorf("server pubkey: %w", err)
	}

	pinToCheck := inviteURL.Srv
	if pinToCheck == "" {
		pinToCheck = opts.AcceptServerFingerprint
	}
	if pinToCheck == "" {
		return nil, ErrMissingServerPin()
	}
	if !cryptox.ConstantTimeEqual([]byte(pinToCheck), []byte(fingerprint)) {
		return nil, ErrServerPinMismatch(pinToCheck, fingerprint)
	}

	preview, err := r.fetchInvitePreview(ctx, server, inviteURL.Token)
	if err != nil {
		return nil, fmt.Errorf("invite preview: %w", err)
	}

	if !preview.Valid {
		reason := preview.Reason
		if reason == "" {
			reason = "invalid"
		}
		return nil, ErrInviteNotUsable(reason)
	}

	if opts.PreviewOnly {
		var issuedByName, issuedByPubkey string
		if preview.IssuedByName != nil {
			issuedByName = *preview.IssuedByName
		}
		if preview.IssuedByPubkey != nil {
			issuedByPubkey = *preview.IssuedByPubkey
		}
		return &RegisterResult{
			Valid:          true,
			ServerURL:      server,
			ServerPubkey:   info.ServerPubkey,
			ServerFP:       fingerprint,
			IssuedByName:   issuedByName,
			IssuedByPubkey: issuedByPubkey,
			ExpiresAt:      preview.ExpiresAt,
			RemainingUses:  preview.RemainingUses,
		}, nil
	}

	if opts.Name == "" {
		return nil, ErrMissingNameForRegistration()
	}

	st, _ := c.ports.State.Load(c.profile)
	if st == nil {
		st = &port.State{}
	}
	dir := c.ports.State.ProfileDir(c.profile)
	priv, err := c.ports.Crypto.LoadPrivateKey(dir)
	if err != nil {
		pub, newPriv, genErr := ed25519.GenerateKey(rand.Reader)
		if genErr != nil {
			return nil, genErr
		}
		priv = newPriv
		st.User.Pubkey = cryptox.EncodeBase64URL(pub)
		if saveErr := c.ports.Crypto.SavePrivateKey(dir, priv); saveErr != nil {
			return nil, fmt.Errorf("save private key: %w", saveErr)
		}
	}

	pub := priv.Public().(ed25519.PublicKey)
	nameNFC, err := proto.NormalizeName(opts.Name)
	if err != nil {
		return nil, err
	}

	ts := time.Now().UnixMilli()
	sig := ed25519.Sign(priv, wire.CanonicalRegister(inviteURL.Token, pub, nameNFC, ts))

	req := proto.RegisterRequest{
		InviteToken: inviteURL.Token,
		ClientPub:   cryptox.EncodeBase64URL(pub),
		Name:        nameNFC,
		TS:          ts,
		Signature:   cryptox.EncodeBase64URL(sig),
	}

	var res proto.RegisterResponse
	if err := r.httpPostJSON(ctx, server+"/v1/register", req, &res, ""); err != nil {
		return nil, err
	}

	st.SchemaVersion = 1
	st.Server.URL = server
	st.Server.Pubkey = info.ServerPubkey
	st.User.UserID = res.UserID
	st.User.Name = nameNFC
	st.User.Pubkey = req.ClientPub
	st.Session.Token = res.SessionToken
	st.Session.ExpiresAt = res.ExpiresAt

	if res.Inviter != nil {
		st.Contacts = upsertContact(st.Contacts, Contact{
			UserID:   res.Inviter.UserID,
			Name:     res.Inviter.Name,
			Pubkey:   res.Inviter.Pubkey,
			Verified: true,
			AddedAt:  time.Now().UnixMilli(),
		})
	}

	if err := c.ports.State.Save(c.profile, st); err != nil {
		return nil, err
	}

	c.setStateAfterRegister(st, priv)

	return &RegisterResult{
		UserID:              res.UserID,
		SessionTokenPresent: res.SessionToken != "",
		Inviter:             inviterFromProto(res.Inviter),
	}, nil
}

// Login выполняет аутентификацию: запрашивает challenge, подписывает nonce, сохраняет session token.
func (r *Registrar) Login(ctx context.Context) (*LoginResult, error) {
	c := r.client
	if c.state == nil {
		return nil, ErrNoStateForLogin()
	}
	if c.priv == nil {
		priv, err := c.ports.Crypto.LoadPrivateKey(c.ports.State.ProfileDir(c.profile))
		if err != nil {
			return nil, fmt.Errorf("load private key: %w", err)
		}
		c.priv = priv
	}

	serverURL := chooseServer(c.state)
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}

	var challenge proto.ChallengeResponse
	if err := r.httpPostJSON(ctx, serverURL+"/v1/auth/challenge", proto.ChallengeRequest{UserID: c.state.User.UserID}, &challenge, ""); err != nil {
		return nil, fmt.Errorf("challenge: %w", err)
	}

	nonce, err := cryptox.DecodeBase64URL(challenge.Nonce, 32)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}

	sig := ed25519.Sign(c.priv, wire.CanonicalLogin(c.state.User.UserID, nonce))

	var res proto.SessionResponse
	if err := r.httpPostJSON(ctx, serverURL+"/v1/sessions", proto.SessionRequest{
		UserID:    c.state.User.UserID,
		Nonce:     challenge.Nonce,
		Signature: cryptox.EncodeBase64URL(sig),
	}, &res, ""); err != nil {
		return nil, fmt.Errorf("sessions: %w", err)
	}

	c.state.Session.Token = res.SessionToken
	c.state.Session.ExpiresAt = res.ExpiresAt
	if err := c.ports.State.Save(c.profile, c.state); err != nil {
		return nil, fmt.Errorf("save state: %w", err)
	}

	return &LoginResult{SessionTokenPresent: true}, nil
}

// Logout уничтожает текущую сессию на сервере и очищает токен в state.
func (r *Registrar) Logout(ctx context.Context) error {
	c := r.client
	if c.state == nil {
		return ErrNoState()
	}

	if c.state.Session.Token != "" {
		serverURL := chooseServer(c.state)
		body, _ := json.Marshal(nil)
		req := port.HTTPRequest{
			Method: "DELETE",
			URL:    serverURL + "/v1/sessions/current",
			Header: map[string]string{
				"Authorization": "Bearer " + c.state.Session.Token,
			},
			Body: strings.NewReader(string(body)),
		}

		_, err := r.signedDo(&req)
		_ = err
	}

	c.state.Session.Token = ""
	c.state.Session.ExpiresAt = 0
	return c.ports.State.Save(c.profile, c.state)
}

// FetchTurnCredentials получает TURN-credentials с сервера.
// При opts.NoWSUpgrade == false открывает push WS-соединение, чтобы IP клиента
// попал в TURN allowlist сервера. При true — пропускает WS (для тестов peer-allowlist).
func (r *Registrar) FetchTurnCredentials(ctx context.Context, opts FetchTurnCredentialsOpts) (*TurnCredentials, error) {
	c := r.client
	if err := c.requireSession(); err != nil {
		return nil, err
	}

	serverURL := chooseServer(c.state)
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}

	if !opts.NoWSUpgrade {
		pushConn, wsErr := c.ports.Signaling.OpenPush(ctx, serverURL, c.state.Session.Token)
		if wsErr == nil && pushConn != nil {
			_ = pushConn.Close()
		}
	}

	req := port.HTTPRequest{
		Method: "GET",
		URL:    serverURL + "/v1/turn/credentials",
		Header: map[string]string{
			"Authorization": "Bearer " + c.state.Session.Token,
		},
	}

	resp, err := c.ports.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var creds TurnCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// --- internal helpers (server-info, invite-preview, signed HTTP) ---

func (r *Registrar) fetchServerInfo(ctx context.Context, serverURL string) (*proto.ServerInfo, error) {
	resp, err := r.client.ports.HTTP.Do(port.HTTPRequest{Method: "GET", URL: serverURL + "/v1/server-info"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	var info proto.ServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (r *Registrar) fetchInvitePreview(ctx context.Context, serverURL, token string) (*proto.InvitePreviewResponse, error) {
	resp, err := r.client.ports.HTTP.Do(port.HTTPRequest{Method: "GET", URL: serverURL + "/v1/invites/" + token + "/preview"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	var preview proto.InvitePreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		return nil, err
	}
	return &preview, nil
}

func (r *Registrar) httpPostJSON(ctx context.Context, urlStr string, in, out any, token string) error {
	body, _ := json.Marshal(in)
	req := port.HTTPRequest{
		Method: "POST",
		URL:    urlStr,
		Header: map[string]string{"Content-Type": "application/json"},
		Body:   strings.NewReader(string(body)),
	}
	if token != "" {
		req.Header["Authorization"] = "Bearer " + token
	}

	resp, err := r.client.ports.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var er proto.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		if er.Error.Code != "" {
			return errors.New(er.Error.Code + ": " + er.Error.Message)
		}
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (r *Registrar) signedDo(req *port.HTTPRequest) (*port.HTTPResponse, error) {
	c := r.client
	priv := c.priv
	if priv == nil {
		var err error
		priv, err = c.ports.Crypto.LoadPrivateKey(c.ports.State.ProfileDir(c.profile))
		if err != nil {
			return nil, fmt.Errorf("load private key: %w", err)
		}
	}

	ts := time.Now().UnixMilli()
	if req.Header == nil {
		req.Header = map[string]string{}
	}
	req.Header["X-Ts"] = fmt.Sprintf("%d", ts)

	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body = strings.NewReader(string(bodyBytes))
	}

	u, _ := url.Parse(req.URL)
	requestURI := u.RequestURI()

	sig := c.ports.Crypto.Sign(priv, wire.CanonicalRequest(req.Method, requestURI, ts, bodyBytes))
	req.Header["X-Sig"] = cryptox.EncodeBase64URL(sig)

	return c.ports.HTTP.Do(*req)
}

// --- pure helpers used by Registrar ---

func chooseServer(st *port.State) string {
	if st == nil || st.Server.URL == "" {
		return ""
	}
	return strings.TrimRight(st.Server.URL, "/")
}

func serverFingerprint(pubkeyBase64URL string) (string, error) {
	pub, err := cryptox.DecodeBase64URL(pubkeyBase64URL, ed25519.PublicKeySize)
	if err != nil {
		return "", err
	}
	return cryptox.Fingerprint(pub, 16), nil
}

func upsertContact(list []Contact, c Contact) []Contact {
	for i := range list {
		if list[i].UserID == c.UserID || (c.Alias != "" && list[i].Alias == c.Alias) {
			list[i] = c
			return list
		}
	}
	return append(list, c)
}

func inviterFromProto(inv *proto.PublicUser) *InviterInfo {
	if inv == nil {
		return nil
	}
	return &InviterInfo{
		UserID: inv.UserID,
		Name:   inv.Name,
		Pubkey: inv.Pubkey,
	}
}
