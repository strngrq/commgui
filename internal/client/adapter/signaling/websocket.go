package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/strngrq/commgui/internal/client/port"

	"nhooyr.io/websocket"
)

const (
	signalPingInterval = 30 * time.Second
	pushPingInterval   = 60 * time.Second
	pingTimeout        = 10 * time.Second
)

type WebSocketClient struct {
	httpClientProvider func() *http.Client
}

func NewWebSocketClient(p func() *http.Client) *WebSocketClient {
	if p == nil {
		p = func() *http.Client { return &http.Client{} }
	}
	return &WebSocketClient{httpClientProvider: p}
}

func (w *WebSocketClient) OpenSignal(ctx context.Context, serverURL, token string) (port.SignalConn, error) {
	conn, err := w.dialWS(ctx, serverURL, "/v1/ws/signal", "commsrv.signal.v2", token)
	if err != nil {
		return nil, err
	}
	if err := handshakeSignal(ctx, conn); err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
		return nil, err
	}
	pingCtx, cancel := context.WithCancel(ctx)
	wc := &wsConn{conn: conn, cancel: cancel}
	go pingLoop(pingCtx, conn, signalPingInterval, pingTimeout)
	return wc, nil
}

func (w *WebSocketClient) OpenPush(ctx context.Context, serverURL, token string) (port.PushConn, error) {
	conn, err := w.dialWS(ctx, serverURL, "/v1/ws/push", "commsrv.push.v2", token)
	if err != nil {
		return nil, err
	}
	if err := ackPushWakeup(ctx, conn); err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
		return nil, err
	}
	pingCtx, cancel := context.WithCancel(ctx)
	wc := &wsConn{conn: conn, cancel: cancel}
	go pingLoop(pingCtx, conn, pushPingInterval, pingTimeout)
	return wc, nil
}

func (w *WebSocketClient) dialWS(ctx context.Context, serverURL, path, subproto, token string) (*websocket.Conn, error) {
	wsURL := strings.TrimRight(serverURL, "/") + path
	switch {
	case strings.HasPrefix(wsURL, "https://"):
		wsURL = "wss://" + wsURL[len("https://"):]
	case strings.HasPrefix(wsURL, "http://"):
		wsURL = "ws://" + wsURL[len("http://"):]
	default:
		return nil, fmt.Errorf("server URL must start with https:// or http://, got %q", serverURL)
	}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient:   w.httpClientProvider(),
		HTTPHeader:   headers,
		Subprotocols: []string{subproto},
	})
	return conn, err
}

func handshakeSignal(ctx context.Context, conn *websocket.Conn) error {
	var hello map[string]any
	if err := wsRead(ctx, conn, &hello); err != nil {
		return err
	}
	if t, _ := hello["type"].(string); t != "hello" {
		return fmt.Errorf("signal: expected hello, got %#v", hello)
	}
	if err := wsWrite(ctx, conn, map[string]any{"type": "ready", "client_ts": timeNowMillis()}); err != nil {
		return err
	}
	var readyOK map[string]any
	if err := wsRead(ctx, conn, &readyOK); err != nil {
		return err
	}
	if t, _ := readyOK["type"].(string); t != "ready.ok" {
		return fmt.Errorf("signal: expected ready.ok, got %#v", readyOK)
	}
	return nil
}

func ackPushWakeup(ctx context.Context, conn *websocket.Conn) error {
	var first map[string]any
	if err := wsRead(ctx, conn, &first); err != nil {
		return err
	}
	if typ, _ := first["type"].(string); typ == "wakeup" {
		if wid, ok := first["wakeup_id"].(string); ok && wid != "" {
			return wsWrite(ctx, conn, map[string]string{"type": "wakeup.ack", "wakeup_id": wid})
		}
	}
	return nil
}

type wsConn struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
}

func (w *wsConn) Read(ctx context.Context, v interface{}) error {
	return wsRead(ctx, w.conn, v)
}

func (w *wsConn) Write(ctx context.Context, v interface{}) error {
	return wsWrite(ctx, w.conn, v)
}

func (w *wsConn) Close() error {
	w.cancel()
	return w.conn.Close(websocket.StatusNormalClosure, "closed")
}

func pingLoop(ctx context.Context, conn *websocket.Conn, interval, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, timeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				// Штатное закрытие: wsConn.Close() отменяет ctx pingLoop'a — Ping
				// вернёт context.Canceled. Это не ping_timeout, не шумим в логи и
				// не пытаемся повторно закрыть соединение.
				if ctx.Err() != nil {
					return
				}
				log.Printf("websocket ping timeout: %v", err)
				_ = conn.Close(websocket.StatusInternalError, "ping_timeout")
				return
			}
		}
	}
}

func wsRead(ctx context.Context, conn *websocket.Conn, target any) error {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func wsWrite(ctx context.Context, conn *websocket.Conn, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

func timeNowMillis() int64 {
	return time.Now().UnixMilli()
}
