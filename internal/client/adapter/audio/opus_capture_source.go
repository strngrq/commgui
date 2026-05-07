//go:build !noaudio

package audio

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
)

var _ port.RTPSource = (*opusCaptureSource)(nil)

// opusCaptureSource — RTPSource: читает PCM из captureRing, кодирует в Opus.
// Используется в real-режиме.
type opusCaptureSource struct {
	enc         *opusEncoder
	ring        *ringBuffer
	notify      chan struct{} // сигнал от malgo callback: новые PCM в ring
	maxDuration time.Duration
	started     time.Time
	closed      chan struct{}
	once        sync.Once
	sent        int64
}

func newOpusCaptureSource(enc *opusEncoder, ring *ringBuffer, notify chan struct{}, maxDur time.Duration) *opusCaptureSource {
	return &opusCaptureSource{
		enc:         enc,
		ring:        ring,
		notify:      notify,
		maxDuration: maxDur,
		started:     time.Now(),
		closed:      make(chan struct{}),
	}
}

func (s *opusCaptureSource) Next(ctx context.Context) ([]byte, time.Duration, error) {
	if s.maxDuration > 0 && time.Since(s.started) >= s.maxDuration {
		return nil, 0, io.EOF
	}
	for {
		if s.ring.Available() >= opusFrameSize {
			pcm := make([]int16, opusFrameSize)
			s.ring.Read(pcm)
			payload, err := s.enc.Encode(pcm)
			if err != nil {
				return nil, 0, err
			}
			atomic.AddInt64(&s.sent, 1)
			return payload, opusFrameDuration * time.Nanosecond, nil
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-s.closed:
			return nil, 0, io.EOF
		case <-s.notify:
			// новые PCM, продолжаем цикл
		}
	}
}

func (s *opusCaptureSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *opusCaptureSource) sentCount() int { return int(atomic.LoadInt64(&s.sent)) }
