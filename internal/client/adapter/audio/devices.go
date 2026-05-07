//go:build !noaudio

package audio

import (
	"context"

	"github.com/strngrq/commgui/internal/client/port"

	"github.com/gen2brain/malgo"
)

// ListDevices перечисляет доступные аудио-устройства.
func (e *Engine) ListDevices(ctx context.Context) ([]port.AudioDevice, error) {
	mctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, err
	}
	defer mctx.Free()

	var devices []port.AudioDevice

	inputs, err := mctx.Devices(malgo.Capture)
	if err != nil {
		return nil, err
	}
	for _, d := range inputs {
		devices = append(devices, port.AudioDevice{
			ID:        d.ID.String(),
			Name:      d.Name(),
			Kind:      "input",
			IsDefault: d.IsDefault != 0,
		})
	}

	outputs, err := mctx.Devices(malgo.Playback)
	if err != nil {
		return nil, err
	}
	for _, d := range outputs {
		devices = append(devices, port.AudioDevice{
			ID:        d.ID.String(),
			Name:      d.Name(),
			Kind:      "output",
			IsDefault: d.IsDefault != 0,
		})
	}

	return devices, nil
}
