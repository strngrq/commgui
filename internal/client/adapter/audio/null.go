package audio

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
)

var _ port.AudioPipeline = (*nullPipeline)(nil)

// nullPipeline — режим "null": без RTP-аудио, обмен только через DataChannel.
type nullPipeline struct {
	source *eofSource
	sink   *countingSink
}

func newNullPipeline() *nullPipeline {
	return &nullPipeline{
		source: newEOFSource(),
		sink:   &countingSink{},
	}
}

func (p *nullPipeline) Kind() string             { return port.AudioKindData }
func (p *nullPipeline) Outbound() port.RTPSource { return p.source }
func (p *nullPipeline) Inbound() port.RTPSink    { return p.sink }
func (p *nullPipeline) Stats() port.AudioStats {
	return port.AudioStats{SamplesReceived: p.sink.Received()}
}
func (p *nullPipeline) Close() error {
	_ = p.source.Close()
	return p.sink.Close()
}

// eofSource всегда возвращает io.EOF — используется в null-режиме и как заглушка.
type eofSource struct {
	once   sync.Once
	closed chan struct{}
}

func newEOFSource() *eofSource {
	return &eofSource{closed: make(chan struct{})}
}

func (s *eofSource) Next(ctx context.Context) ([]byte, time.Duration, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-s.closed:
		return nil, 0, io.EOF
	}
}

func (s *eofSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// countingSink принимает payload и просто считает.
type countingSink struct {
	received int64
}

func (s *countingSink) Push(_ []byte) {
	atomic.AddInt64(&s.received, 1)
}

func (s *countingSink) Received() int {
	return int(atomic.LoadInt64(&s.received))
}

func (s *countingSink) Close() error { return nil }
