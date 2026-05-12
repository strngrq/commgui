package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
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
	wc := newWSConn(conn, cancel)
	go pingLoop(pingCtx, conn, wc.id, signalPingInterval, pingTimeout)
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
	wc := newWSConn(conn, cancel)
	go pingLoop(pingCtx, conn, wc.id, pushPingInterval, pingTimeout)
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
	id       uint64
	conn     *websocket.Conn
	cancel   context.CancelFunc
	readers  int32 // atomic: число горутин, находящихся в Read()
}

var wsConnNextID uint64

func newWSConn(conn *websocket.Conn, cancel context.CancelFunc) *wsConn {
	wsConnNextID++
	return &wsConn{id: wsConnNextID, conn: conn, cancel: cancel}
}

func (w *wsConn) Read(ctx context.Context, v interface{}) error {
	n := atomic.AddInt32(&w.readers, 1)
	if n > 1 {
		buf := make([]byte, 4096)
		buf = buf[:runtime.Stack(buf, false)]
		log.Printf("[ws:%d] CONCURRENT READ: %d goroutines in Read() — stack:\n%s", w.id, n, buf)
	}
	err := wsRead(ctx, w.conn, v)
	atomic.AddInt32(&w.readers, -1)
	if err != nil && ctx.Err() == nil {
		log.Printf("[ws:%d] websocket read error: %v", w.id, err)
	}
	return err
}

func (w *wsConn) Write(ctx context.Context, v interface{}) error {
	return wsWrite(ctx, w.conn, v)
}

func (w *wsConn) Close() error {
	w.cancel()
	return w.conn.Close(websocket.StatusNormalClosure, "closed")
}

func pingLoop(ctx context.Context, conn *websocket.Conn, wsID uint64, interval, timeout time.Duration) {
	log.Printf("pingLoop [ws:%d] started (interval=%v)", wsID, interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("pingLoop [ws:%d] stopped", wsID)
			return
		case <-ticker.C:
			log.Printf("pingLoop [ws:%d] sending Ping", wsID)
			pctx, cancel := context.WithTimeout(ctx, timeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("pingLoop [ws:%d] ping timeout: %v", wsID, err)
				_ = conn.Close(websocket.StatusInternalError, "ping_timeout")
				return
			}
			log.Printf("pingLoop [ws:%d] pong received", wsID)
		}
	}
}

func wsRead(ctx context.Context, conn *websocket.Conn, target any) error {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		log.Printf("websocket json unmarshal error: %v (raw=%q)", err, truncateBytes(raw, 120))
		return err
	}
	return nil
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

func truncateBytes(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
