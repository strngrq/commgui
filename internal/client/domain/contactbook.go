package domain

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
)

// ContactBook реализует управление профилем, контактами и инвайтами.
type ContactBook struct {
	client *Client
}

// Profile возвращает информацию о текущем профиле.
func (b *ContactBook) Profile(serverOverride string) (*ProfileInfo, error) {
	c := b.client
	if err := c.requireState(); err != nil {
		return nil, err
	}
	serverURL := serverOverride
	if serverURL == "" {
		serverURL = chooseServer(c.state)
	}
	contactURL := BuildContactURL(serverURL, c.state.User.UserID, c.state.User.Pubkey, c.state.User.Name)
	return &ProfileInfo{
		Profile:    c.profile,
		UserID:     c.state.User.UserID,
		Name:       c.state.User.Name,
		Pubkey:     c.state.User.Pubkey,
		ContactURL: contactURL,
	}, nil
}

// List возвращает список контактов профиля.
func (b *ContactBook) List() ([]Contact, error) {
	if err := b.client.requireState(); err != nil {
		return nil, err
	}
	return b.client.state.Contacts, nil
}

// Add добавляет контакт по privcall:// URL.
func (b *ContactBook) Add(ctx context.Context, opts AddContactOpts) (*Contact, error) {
	c := b.client
	if err := c.requireState(); err != nil {
		return nil, err
	}
	contact, err := ParseContactURL(opts.ContactURL, opts.Alias, opts.AutoVerify)
	if err != nil {
		return nil, err
	}
	c.state.Contacts = upsertContact(c.state.Contacts, contact)
	if err := c.ports.State.Save(c.profile, c.state); err != nil {
		return nil, err
	}
	return &contact, nil
}

// Upsert вставляет/обновляет контакт напрямую (без парсинга URL).
// Используется для прикладной логики типа обработки invite_used wakeup.
func (b *ContactBook) Upsert(ctx context.Context, c Contact) error {
	cl := b.client
	if err := cl.requireState(); err != nil {
		return err
	}
	cl.state.Contacts = upsertContact(cl.state.Contacts, c)
	return cl.ports.State.Save(cl.profile, cl.state)
}

// Remove удаляет контакт по UserID или alias.
func (b *ContactBook) Remove(ctx context.Context, idOrAlias string) error {
	c := b.client
	if err := c.requireState(); err != nil {
		return err
	}
	c.state.Contacts = removeContact(c.state.Contacts, idOrAlias)
	return c.ports.State.Save(c.profile, c.state)
}

// SafetyNumber возвращает safety number для контакта.
func (b *ContactBook) SafetyNumber(idOrAlias string) (string, error) {
	c := b.client
	if err := c.requireState(); err != nil {
		return "", err
	}
	contact, ok := findContact(c.state, idOrAlias)
	if !ok {
		return "", ErrContactNotFound(idOrAlias)
	}
	own, err := cryptox.DecodeBase64URL(c.state.User.Pubkey, ed25519.PublicKeySize)
	if err != nil {
		return "", err
	}
	peer, err := cryptox.DecodeBase64URL(contact.Pubkey, ed25519.PublicKeySize)
	if err != nil {
		return "", err
	}
	return cryptox.SafetyNumber(own, peer), nil
}

// CreateInvite создаёт инвайт на сервере.
func (b *ContactBook) CreateInvite(ctx context.Context, opts CreateInviteOpts) (*InviteResult, error) {
	c := b.client
	if err := c.requireSession(); err != nil {
		return nil, err
	}

	serverURL := chooseServer(c.state)
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}

	body, _ := json.Marshal(proto.InviteCreateRequest{
		TTLSeconds: int64(opts.TTL / time.Second),
		MaxUses:    opts.MaxUses,
		Label:      opts.Label,
	})

	req := port.HTTPRequest{
		Method: "POST",
		URL:    serverURL + "/v1/invites",
		Header: map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + c.state.Session.Token,
		},
		Body: strings.NewReader(string(body)),
	}

	resp, err := c.Registrar.signedDo(&req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var er proto.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		if er.Error.Code != "" {
			return nil, fmt.Errorf("%s: %s", er.Error.Code, er.Error.Message)
		}
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var res proto.InviteResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &InviteResult{Token: res.Token, URL: res.URL, QRPayload: res.QRPayload}, nil
}

// ListInvites возвращает инвайты пользователя с фильтром по статусу.
func (b *ContactBook) ListInvites(ctx context.Context, opts ListInvitesOpts) ([]Invite, error) {
	c := b.client
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	serverURL := chooseServer(c.state)
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}

	status := opts.Status
	if status == "" {
		status = "active"
	}
	reqURL := serverURL + "/v1/invites?status=" + url.QueryEscape(status)

	req := port.HTTPRequest{
		Method: "GET",
		URL:    reqURL,
		Header: map[string]string{"Authorization": "Bearer " + c.state.Session.Token},
	}

	resp, err := c.ports.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	invites := []Invite{}
	if raw, ok := out["invites"]; ok {
		data, _ := json.Marshal(raw)
		_ = json.Unmarshal(data, &invites)
	}
	return invites, nil
}

// RevokeInvite отзывает инвайт по токену.
func (b *ContactBook) RevokeInvite(ctx context.Context, token string) error {
	c := b.client
	if err := c.requireSession(); err != nil {
		return err
	}
	serverURL := chooseServer(c.state)
	if serverURL == "" {
		return ErrMissingServerURL()
	}

	req := port.HTTPRequest{
		Method: "DELETE",
		URL:    serverURL + "/v1/invites/" + token,
		Header: map[string]string{"Authorization": "Bearer " + c.state.Session.Token},
	}

	resp, err := c.Registrar.signedDo(&req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return nil
}

// --- pure helpers used by ContactBook ---

// BuildContactURL строит privcall://add#... URL для отображения/QR.
func BuildContactURL(server, userID, pubkey, name string) string {
	fp := ""
	if raw, err := cryptox.DecodeBase64URL(pubkey, ed25519.PublicKeySize); err == nil {
		fp = cryptox.ContactFingerprint(raw)
	}
	return "privcall://add#v=1&s=" + url.QueryEscape(server) + "&u=" + url.QueryEscape(userID) + "&k=" + url.QueryEscape(pubkey) + "&n=" + url.QueryEscape(name) + "&fp=" + url.QueryEscape(fp)
}

func findContact(st *port.State, key string) (Contact, bool) {
	if st == nil {
		return Contact{}, false
	}
	for _, c := range st.Contacts {
		if c.UserID == key || c.Alias == key {
			return c, true
		}
	}
	return Contact{}, false
}

func removeContact(list []Contact, key string) []Contact {
	out := list[:0]
	for _, c := range list {
		if c.UserID != key && c.Alias != key {
			out = append(out, c)
		}
	}
	return out
}
