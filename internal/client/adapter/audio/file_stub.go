//go:build noaudio

package audio

import (
	"errors"

	"github.com/strngrq/commgui/internal/client/port"
)

func newFilePipeline(string, port.AudioOpts) (port.AudioPipeline, error) {
	return nil, errors.New("file audio mode requires audio support (noaudio build tag disables it)")
}
