//go:build !noaudio

package audio

import (
	"context"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
)

var _ port.AudioPipeline = (*tonePipeline)(nil)

// tonePipeline — режим "tone": исходящая синусоида 440Hz, кодированная в Opus.
type tonePipeline struct {
	src  *toneSource
	sink *countingSink
}

func newTonePipeline(opts port.AudioOpts) (port.AudioPipeline, error) {
	enc, err := newOpusEncoder()
	if err != nil {
		return nil, err
	}
	return &tonePipeline{
		src:  newToneSource(enc, opts.MaxDuration),
		sink: &countingSink{},
	}, nil
}

func (p *tonePipeline) Kind() string             { return port.AudioKindAudio }
func (p *tonePipeline) Outbound() port.RTPSource { return p.src }
func (p *tonePipeline) Inbound() port.RTPSink    { return p.sink }
func (p *tonePipeline) Stats() port.AudioStats {
	return port.AudioStats{
		SamplesSent:     p.src.sentCount(),
		SamplesReceived: p.sink.Received(),
	}
}
func (p *tonePipeline) Close() error {
	_ = p.src.Close()
	return p.sink.Close()
}

// toneSource генерирует синусоиду 440Hz, кодирует в Opus.
type toneSource struct {
	enc         *opusEncoder
	phase       uint64
	maxDuration time.Duration
	started     time.Time
	closed      chan struct{}
	once        sync.Once
	sent        int64
}

func newToneSource(enc *opusEncoder, maxDur time.Duration) *toneSource {
	return &toneSource{
		enc:         enc,
		maxDuration: maxDur,
		started:     time.Now(),
		closed:      make(chan struct{}),
	}
}

func (s *toneSource) Next(ctx context.Context) ([]byte, time.Duration, error) {
	if s.maxDuration > 0 && time.Since(s.started) >= s.maxDuration {
		return nil, 0, io.EOF
	}
	pcm := make([]int16, opusFrameSize)
	for i := range pcm {
		t := float64(s.phase+uint64(i)) / opusSampleRate
		pcm[i] = int16(math.Sin(2*math.Pi*440*t) * 32767)
	}
	s.phase += uint64(opusFrameSize)

	payload, err := s.enc.Encode(pcm)
	if err != nil {
		return nil, 0, err
	}
	atomic.AddInt64(&s.sent, 1)
	return payload, opusFrameDuration * time.Nanosecond, nil
}

func (s *toneSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *toneSource) sentCount() int { return int(atomic.LoadInt64(&s.sent)) }
