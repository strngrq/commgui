package port

import "context"

// TurnClient проверяет relay-возможности TURN-сервера.
type TurnClient interface {
	TryRelay(ctx context.Context, opts TurnRelayOpts) (*TurnRelayResult, error)
}

// TurnRelayOpts — параметры проверки TURN relay.
type TurnRelayOpts struct {
	ServerAddr string // TURN server host:port
	Username   string
	Credential string
	PeerAddr   string // peer ip:port для проверки permission
}

// TurnRelayResult — результат проверки TURN relay.
type TurnRelayResult struct {
	OK          bool   `json:"ok"`
	RelayedAddr string `json:"relayed_addr,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"` // "turn_forbidden_peer" при отказе permission
}
