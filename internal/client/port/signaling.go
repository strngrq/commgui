package port

import (
	"context"
)

// SignalingClient — абстракция signaling-соединений (WebSocket).
type SignalingClient interface {
	OpenSignal(ctx context.Context, serverURL, token string) (SignalConn, error)
	OpenPush(ctx context.Context, serverURL, token string) (PushConn, error)
}

// SignalConn — соединение для signaling (/ws/signal).
type SignalConn interface {
	Read(ctx context.Context, v interface{}) error
	Write(ctx context.Context, v interface{}) error
	Close() error
}

// PushConn — соединение для push (/ws/push).
type PushConn interface {
	Read(ctx context.Context, v interface{}) error
	Write(ctx context.Context, v interface{}) error
	Close() error
}
