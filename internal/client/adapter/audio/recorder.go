//go:build !noaudio

package audio

import (
	"os"
	"sync"
	"sync/atomic"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// wavRecorder — RTPSink, который декодирует Opus payload в PCM и пишет в WAV-файл.
type wavRecorder struct {
	mu       sync.Mutex
	file     *os.File
	enc      *wav.Encoder
	dec      *opusDecoder
	closed   bool
	received int64
}

func newWAVRecorder(path string) (*wavRecorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	enc := wav.NewEncoder(f, 48000, 16, 1, 1)
	dec, err := newOpusDecoder()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &wavRecorder{file: f, enc: enc, dec: dec}, nil
}

func (r *wavRecorder) Push(payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	atomic.AddInt64(&r.received, 1)

	pcm, err := r.dec.Decode(payload)
	if err != nil {
		return
	}
	intSamples := make([]int, len(pcm))
	for i, v := range pcm {
		intSamples[i] = int(v)
	}
	_ = r.enc.Write(&audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: 48000},
		Data:           intSamples,
		SourceBitDepth: 16,
	})
}

func (r *wavRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	encErr := r.enc.Close()
	fileErr := r.file.Close()
	if encErr != nil {
		return encErr
	}
	return fileErr
}

func (r *wavRecorder) receivedCount() int { return int(atomic.LoadInt64(&r.received)) }
