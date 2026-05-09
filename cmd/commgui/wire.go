package main

import (
	"net/http"

	"github.com/strngrq/commgui/internal/client/adapter/audio"
	"github.com/strngrq/commgui/internal/client/adapter/crypto"
	httpAdapter "github.com/strngrq/commgui/internal/client/adapter/http"
	"github.com/strngrq/commgui/internal/client/adapter/signaling"
	"github.com/strngrq/commgui/internal/client/adapter/state"
	turnAdapter "github.com/strngrq/commgui/internal/client/adapter/turn"
	"github.com/strngrq/commgui/internal/client/adapter/webrtc"
	"github.com/strngrq/commgui/internal/client/domain"
)

// defaultPorts собирает Ports со всеми реальными адаптерами.
// Идентично cmd/commclient/wire.go.
func defaultPorts() domain.Ports {
	httpClient := httpAdapter.NewClient()
	return domain.Ports{
		State:     state.NewFileStore(),
		Crypto:    crypto.NewEd25519Provider(),
		HTTP:      httpClient,
		Signaling: signaling.NewWebSocketClient(func() *http.Client { return httpClient.HTTPClient() }),
		WebRTC:    webrtc.NewPionManager(),
		Audio:     audio.NewEngine(),
		Turn:      turnAdapter.NewClient(),
	}
}

// newClient создаёт Client для профиля. Если профиль не инициализирован,
// state будет nil — bootstrap-методы всё равно работают.
func newClient(profile string) *domain.Client {
	return domain.NewClient(profile, defaultPorts())
}
