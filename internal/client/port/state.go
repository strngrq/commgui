package port

// State — полное состояние профиля клиента.
type State struct {
	SchemaVersion int         `json:"schema_version"`
	Server        ServerInfo  `json:"server"`
	User          UserInfo    `json:"user"`
	Session       SessionInfo `json:"session"`
	Contacts      []Contact   `json:"contacts"`
}

// ServerInfo — информация о сервере.
type ServerInfo struct {
	URL         string `json:"url"`
	Pubkey      string `json:"pubkey"`
	Fingerprint string `json:"fingerprint"`
}

// UserInfo — данные пользователя.
type UserInfo struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Pubkey string `json:"pubkey"`
}

// SessionInfo — данные сессии.
type SessionInfo struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// Contact — запись в адресной книге.
type Contact struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Pubkey   string `json:"pubkey"`
	Alias    string `json:"alias"`
	Verified bool   `json:"verified"`
	AddedAt  int64  `json:"added_at"`
}

// StateStore — абстракция хранилища состояния профиля.
type StateStore interface {
	Load(profile string) (*State, error)
	Save(profile string, st *State) error
	ProfileDir(profile string) string
	Delete(profile string) error
}
