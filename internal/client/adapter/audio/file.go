//go:build !noaudio

package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/strngrq/commgui/internal/client/port"

	"github.com/go-audio/wav"
)

var _ port.AudioPipeline = (*filePipeline)(nil)

// filePipeline — режим "file[:<path>]": читает WAV-файл, ресемплирует если 16kHz,
// кодирует в Opus и отправляет. Если путь не указан, outbound не шлёт ничего.
// Входящий RTP опционально пишется в WAV-файл.
type filePipeline struct {
	src  port.RTPSource
	sink port.RTPSink
	rec  *wavRecorder
}

func newFilePipeline(path string, opts port.AudioOpts) (port.AudioPipeline, error) {
	p := &filePipeline{}

	if path != "" {
		enc, err := newOpusEncoder()
		if err != nil {
			return nil, err
		}
		pcm, sampleRate, err := readWAVPCM(path)
		if err != nil {
			return nil, err
		}
		if sampleRate == 16000 {
			pcm = resample16to48(pcm)
		}
		p.src = newFileSource(enc, pcm, opts.MaxDuration)
	} else {
		p.src = newEOFSource()
	}

	if opts.RecordPath != "" {
		rec, recErr := newWAVRecorder(opts.RecordPath)
		if recErr != nil {
			return nil, recErr
		}
		p.rec = rec
		p.sink = rec
	} else {
		p.sink = &countingSink{}
	}
	return p, nil
}

func (p *filePipeline) Kind() string             { return port.AudioKindAudio }
func (p *filePipeline) Outbound() port.RTPSource { return p.src }
func (p *filePipeline) Inbound() port.RTPSink    { return p.sink }
func (p *filePipeline) Stats() port.AudioStats {
	var sent int
	if fs, ok := p.src.(*fileSource); ok {
		sent = fs.sentCount()
	}
	stats := port.AudioStats{SamplesSent: sent}
	if p.rec != nil {
		stats.SamplesReceived = p.rec.receivedCount()
	} else if cs, ok := p.sink.(*countingSink); ok {
		stats.SamplesReceived = cs.Received()
	}
	return stats
}
func (p *filePipeline) Close() error {
	_ = p.src.Close()
	return p.sink.Close()
}

// fileSource выдаёт PCM из предзагруженного WAV, кодирует в Opus.
type fileSource struct {
	enc         *opusEncoder
	pcm         []int16
	pos         int
	maxDuration time.Duration
	started     time.Time
	closed      chan struct{}
	once        sync.Once
	sent        int64
}

func newFileSource(enc *opusEncoder, pcm []int16, maxDur time.Duration) *fileSource {
	return &fileSource{
		enc:         enc,
		pcm:         pcm,
		maxDuration: maxDur,
		started:     time.Now(),
		closed:      make(chan struct{}),
	}
}

func (s *fileSource) Next(ctx context.Context) ([]byte, time.Duration, error) {
	if s.pos >= len(s.pcm) {
		return nil, 0, io.EOF
	}
	if s.maxDuration > 0 && time.Since(s.started) >= s.maxDuration {
		return nil, 0, io.EOF
	}
	end := s.pos + opusFrameSize
	frame := make([]int16, opusFrameSize)
	copy(frame, s.pcm[s.pos:min(end, len(s.pcm))])
	s.pos = end

	payload, err := s.enc.Encode(frame)
	if err != nil {
		return nil, 0, err
	}
	atomic.AddInt64(&s.sent, 1)
	return payload, opusFrameDuration * time.Nanosecond, nil
}

func (s *fileSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *fileSource) sentCount() int { return int(atomic.LoadInt64(&s.sent)) }

// readWAVPCM читает WAV-файл, возвращает PCM-сэмплы и частоту дискретизации.
func readWAVPCM(path string) ([]int16, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	dec.ReadInfo()
	if !dec.IsValidFile() {
		return nil, 0, fmt.Errorf("%s is not a valid WAV file", path)
	}
	if dec.NumChans != 1 {
		return nil, 0, fmt.Errorf("WAV must be mono, got %d channels", dec.NumChans)
	}
	if dec.SampleRate != 16000 && dec.SampleRate != 48000 {
		return nil, 0, fmt.Errorf("WAV sample rate must be 16kHz or 48kHz, got %d", dec.SampleRate)
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read WAV PCM: %w", err)
	}
	// buf.Data — []int с уже декодированными сэмплами, диапазон зависит
	// от SourceBitDepth. Приводим к int16 в зависимости от исходной разрядности.
	bitDepth := buf.SourceBitDepth
	if bitDepth == 0 {
		bitDepth = int(dec.BitDepth)
	}
	pcm := make([]int16, len(buf.Data))
	switch bitDepth {
	case 16:
		for i, v := range buf.Data {
			pcm[i] = int16(v)
		}
	case 24:
		for i, v := range buf.Data {
			pcm[i] = int16(v >> 8)
		}
	case 32:
		for i, v := range buf.Data {
			pcm[i] = int16(v >> 16)
		}
	case 8:
		// 8-bit WAV — unsigned, центр 128.
		for i, v := range buf.Data {
			pcm[i] = int16((v - 128) << 8)
		}
	default:
		return nil, 0, fmt.Errorf("unsupported WAV bit depth %d (need 8/16/24/32)", bitDepth)
	}
	return pcm, int(dec.SampleRate), nil
}
