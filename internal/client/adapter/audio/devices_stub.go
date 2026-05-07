//go:build noaudio

package audio

import (
	"context"

	"github.com/strngrq/commgui/internal/client/port"
)

// ListDevices возвращает пустой список в noaudio-сборках.
func (e *Engine) ListDevices(ctx context.Context) ([]port.AudioDevice, error) {
	return nil, nil
}
