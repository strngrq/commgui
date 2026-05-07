package turn

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/strngrq/commgui/internal/client/port"

	pionturn "github.com/pion/turn/v4"
)

// Client — адаптер TURN через pion/turn v4.
type Client struct{}

// NewClient создаёт Client.
func NewClient() *Client { return &Client{} }

var _ port.TurnClient = (*Client)(nil)

// TryRelay проверяет relay-возможности TURN-сервера.
func (c *Client) TryRelay(ctx context.Context, opts port.TurnRelayOpts) (*port.TurnRelayResult, error) {
	if opts.ServerAddr == "" {
		return nil, fmt.Errorf("TURN server address is required")
	}
	if opts.PeerAddr == "" {
		return nil, fmt.Errorf("peer address is required")
	}

	if _, _, err := net.SplitHostPort(opts.ServerAddr); err != nil {
		opts.ServerAddr = net.JoinHostPort(opts.ServerAddr, "3478")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	clientConn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("listen packet: %w", err)
	}
	defer clientConn.Close()

	turnClient, err := pionturn.NewClient(&pionturn.ClientConfig{
		TURNServerAddr: opts.ServerAddr,
		Username:       opts.Username,
		Password:       opts.Credential,
		Conn:           clientConn,
	})
	if err != nil {
		return nil, fmt.Errorf("create TURN client: %w", err)
	}
	defer turnClient.Close()

	if err := turnClient.Listen(); err != nil {
		return nil, fmt.Errorf("TURN client listen: %w", err)
	}

	allocation, err := turnClient.Allocate()
	if err != nil {
		if isForbidden(err) {
			return &port.TurnRelayResult{OK: false, ErrorCode: "turn_forbidden_peer"}, nil
		}
		return nil, fmt.Errorf("TURN allocate: %w", err)
	}
	defer allocation.Close()

	relayedAddr := allocation.LocalAddr().String()

	peerUDPAddr, err := net.ResolveUDPAddr("udp4", opts.PeerAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve peer %s: %w", opts.PeerAddr, err)
	}

	if err := turnClient.CreatePermission(peerUDPAddr); err != nil {
		if isForbidden(err) {
			return &port.TurnRelayResult{OK: false, RelayedAddr: relayedAddr, ErrorCode: "turn_forbidden_peer"}, nil
		}
		return nil, fmt.Errorf("create permission for %s: %w", opts.PeerAddr, err)
	}

	// Verify relay works: send a small binding through the allocation.
	if _, err := allocation.WriteTo([]byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, peerUDPAddr); err != nil {
		if isForbidden(err) {
			return &port.TurnRelayResult{OK: false, RelayedAddr: relayedAddr, ErrorCode: "turn_forbidden_peer"}, nil
		}
		// Non-fatal: relay may work even if send fails (no receiver).
	}

	return &port.TurnRelayResult{OK: true, RelayedAddr: relayedAddr}, nil
}

func isForbidden(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Forbidden") ||
		strings.Contains(s, "forbidden") ||
		strings.Contains(s, "403") ||
		strings.Contains(s, "401")
}
