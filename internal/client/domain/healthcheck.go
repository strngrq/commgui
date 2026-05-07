package domain

import (
	"context"

	"github.com/strngrq/commgui/internal/client/port"
)

// HealthCheck реализует server-info / push-WS пробу и push-listening.
type HealthCheck struct {
	client *Client
}

// Check возвращает статус сервера (server-info + push WS).
func (h *HealthCheck) Check(ctx context.Context, serverOverride string) (*HealthResult, error) {
	c := h.client
	serverURL := serverOverride
	if serverURL == "" && c.state != nil {
		serverURL = chooseServer(c.state)
	}
	if serverURL == "" {
		return nil, ErrMissingServerURL()
	}

	info, err := c.Registrar.fetchServerInfo(ctx, serverURL)
	if err != nil {
		return nil, err
	}

	res := &HealthResult{
		ServerInfo: map[string]any{
			"server_pubkey": info.ServerPubkey,
			"version":       info.Version,
		},
		PushWSOK: false,
	}

	if c.state != nil && c.state.Session.Token != "" {
		pushConn, err := c.ports.Signaling.OpenPush(ctx, serverURL, c.state.Session.Token)
		if err == nil {
			res.PushWSOK = true
			_ = pushConn.Close()
		} else {
			res.PushWSError = err.Error()
		}
	}
	return res, nil
}

// TryRelay проверяет relay-возможности TURN-сервера: подключается,
// создаёт allocation, создаёт permission для peer и проверяет relay.
func (h *HealthCheck) TryRelay(ctx context.Context, opts TurnRelayOpts) (*TurnRelayResult, error) {
	if h.client.ports.Turn == nil {
		return nil, ErrNoState()
	}
	result, err := h.client.ports.Turn.TryRelay(ctx, port.TurnRelayOpts{
		ServerAddr: opts.ServerAddr,
		Username:   opts.Username,
		Credential: opts.Credential,
		PeerAddr:   opts.PeerAddr,
	})
	if err != nil {
		return nil, err
	}
	return &TurnRelayResult{
		OK:          result.OK,
		RelayedAddr: result.RelayedAddr,
		ErrorCode:   result.ErrorCode,
	}, nil
}

// ListenPush ждёт push-wakeup сообщений и вызывает handler для каждого.
func (h *HealthCheck) ListenPush(ctx context.Context, opts PushListenOpts, handler PushWakeupHandler) error {
	c := h.client
	if err := c.requireSession(); err != nil {
		return err
	}

	serverURL := chooseServer(c.state)
	if serverURL == "" {
		return ErrMissingServerURL()
	}

	conn, err := c.ports.Signaling.OpenPush(ctx, serverURL, c.state.Session.Token)
	if err != nil {
		return err
	}
	defer conn.Close()

	var count int
	for opts.MaxEvents == 0 || count < opts.MaxEvents {
		var msg map[string]any
		if err := conn.Read(ctx, &msg); err != nil {
			return err
		}
		typ, _ := msg["type"].(string)
		if typ != "wakeup" {
			continue
		}
		wid, _ := msg["wakeup_id"].(string)
		if wid != "" {
			_ = conn.Write(ctx, map[string]string{"type": "wakeup.ack", "wakeup_id": wid})
		}
		if handler != nil {
			data, _ := msg["data"].(map[string]any)
			handler(PushWakeup{
				Type:       "wakeup",
				Kind:       strVal(msg, "kind"),
				EnvelopeID: strVal(msg, "envelope_id"),
				WakeupID:   wid,
				Data:       data,
			})
		}
		count++
	}
	return nil
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
