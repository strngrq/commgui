package domain

import (
	"crypto/rand"
	"net/url"
	"strings"
	"time"

	"github.com/strngrq/commgui/internal/client/port"

	"github.com/oklog/ulid/v2"
)

// ParseInviteURL parses a privcall://invite#... URL.
// Parameter values are URL-decoded (matching inviteURL which QueryEscape's them).
func ParseInviteURL(invite string) (*InviteURL, error) {
	parts := strings.Split(invite, "#")
	if len(parts) != 2 {
		return nil, ErrInvalidInvite("invalid invite URL")
	}

	values, err := url.ParseQuery(parts[1])
	if err != nil {
		return nil, ErrInvalidInvite("invalid invite URL: " + err.Error())
	}

	server := values.Get("s")
	if server == "" {
		return nil, ErrInvalidInvite("invalid invite URL: missing server")
	}
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}

	return &InviteURL{
		Server: server,
		Token:  values.Get("t"),
		Srv:    values.Get("srv"),
	}, nil
}

// ParseContactURL parses a privcall://add#... URL.
func ParseContactURL(raw, alias string, verified bool) (Contact, error) {
	parts := strings.Split(raw, "#")
	if len(parts) != 2 {
		return Contact{}, ErrInvalidInvite("invalid contact URL")
	}

	values, err := url.ParseQuery(parts[1])
	if err != nil {
		return Contact{}, err
	}

	name, _ := url.QueryUnescape(values.Get("n"))
	return Contact{
		UserID:   values.Get("u"),
		Name:     name,
		Pubkey:   values.Get("k"),
		Alias:    alias,
		Verified: verified,
		AddedAt:  time.Now().UnixMilli(),
	}, nil
}

func NewID() string {
	entropy := ulid.Monotonic(rand.Reader, 0)
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// contactByUserID ищет контакт по UserID. Если не найдено — возвращает Contact{UserID: id}, false,
// чтобы вызывающие могли использовать сам id как fallback.
func contactByUserID(st *port.State, userID string) (Contact, bool) {
	if st == nil {
		return Contact{UserID: userID}, false
	}
	for _, c := range st.Contacts {
		if c.UserID == userID {
			return c, true
		}
	}
	return Contact{UserID: userID}, false
}
