//go:build noaudio

package audio

import (
	"context"
	"fmt"

	"github.com/strngrq/commgui/internal/client/port"
)

func newRealPipeline(_ context.Context, _ port.AudioOpts) (port.AudioPipeline, error) {
	return nil, fmt.Errorf("--audio=real is unavailable in noaudio builds")
}
