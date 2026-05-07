//go:build !noaudio

package audio

import (
	"sync/atomic"

	"github.com/strngrq/commgui/internal/client/port"
)

var _ port.RTPSink = (*opusPlaybackSink)(nil)

// opusPlaybackSink — RTPSink: декодирует Opus payload в PCM и пишет в playbackRing.
// Используется в real-режиме для вывода входящего RTP на динамик.
type opusPlaybackSink struct {
	dec      *opusDecoder
	ring     *ringBuffer
	received int64
}

func newOpusPlaybackSink(dec *opusDecoder, ring *ringBuffer) *opusPlaybackSink {
	return &opusPlaybackSink{dec: dec, ring: ring}
}

func (s *opusPlaybackSink) Push(payload []byte) {
	atomic.AddInt64(&s.received, 1)
	pcm, err := s.dec.Decode(payload)
	if err != nil {
		return // дропаем повреждённый пакет
	}
	s.ring.Write(pcm)
}

func (s *opusPlaybackSink) Close() error { return nil }

func (s *opusPlaybackSink) receivedCount() int { return int(atomic.LoadInt64(&s.received)) }
