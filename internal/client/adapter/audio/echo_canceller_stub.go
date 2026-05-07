//go:build noaudio

package audio

import "fmt"

func newSpeexCanceller(_ int) (echoCanceller, error) {
	return nil, fmt.Errorf("speexdsp echo canceller unavailable in noaudio builds")
}
