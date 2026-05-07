//go:build !noaudio

package audio

import (
	"context"
	"encoding/hex"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/strngrq/commgui/internal/client/port"

	"github.com/gen2brain/malgo"
)

var _ port.AudioPipeline = (*realPipeline)(nil)

// ---------------------------------------------------------------------------
// Состояние аудио-колбека. Вместо замыкания, которое Go 1.24 запрещает
// передавать в C (cgo argument has Go pointer to unpinned Go pointer),
// храним состояние в package-level переменных, а в malgo.DeviceCallbacks
// передаём обычную функцию (не closure). Одновременно активен только
// один пайплайн (гарантируется StartCall проверкой a.activeCall != nil).
// ---------------------------------------------------------------------------
var (
	cbCaptureRing  *ringBuffer
	cbPlaybackRing *ringBuffer
	cbNotify       chan struct{}
	cbPipeline     *realPipeline
	cbAec          echoCanceller
)

func setAudioCallbackState(capRing, playRing *ringBuffer, notify chan struct{}, p *realPipeline, aec echoCanceller) {
	cbCaptureRing = capRing
	cbPlaybackRing = playRing
	cbNotify = notify
	cbPipeline = p
	cbAec = aec
}

func clearAudioCallbackState() {
	cbCaptureRing = nil
	cbPlaybackRing = nil
	cbNotify = nil
	cbPipeline = nil
	cbAec = nil
}

// audioDataCallback — malgo.Data callback. Не замыкание: все данные берёт
// из package-level переменных, установленных перед InitDevice.
//
// Порядок обработки:
//   1. Заполняем output из playbackRing (или нулями в prebuf-фазе).
//   2. Если играем реальные данные — отдаём output как reference в AEC через
//      Playback. В prebuf-фазе (нули в output) AEC не дёргаем, чтобы фильтр
//      не учился на тишине.
//   3. Микрофон прогоняем через AEC.Capture in-place, потом пишем в
//      captureRing. Если префиксы output/input расходятся по длине — AEC
//      получает только общий префикс, остальное идёт в ring как есть.
//   4. На переходе prebuf=true → false делаем Reset фильтра: после
//      underrun synchronization ref↔mic потеряна, продолжать с накопленным
//      состоянием контрпродуктивно.
func audioDataCallback(output, input []byte, frameCount uint32) {
	refSamples := len(output) / 2
	micSamples := len(input) / 2

	var dst []int16
	if refSamples > 0 {
		dst = unsafe.Slice((*int16)(unsafe.Pointer(&output[0])), refSamples)
	}

	// playback: заполняем output, AEC учитывает только реальные данные
	prebuf := cbPipeline.playbackPrebuf.Load()
	if refSamples > 0 {
		if prebuf && cbPlaybackRing.Available() >= cbPipeline.prebufSamples {
			cbPipeline.playbackPrebuf.Store(false)
			cbAec.Reset()
			prebuf = false
		}

		if prebuf {
			for i := 0; i < refSamples; i++ {
				dst[i] = 0
			}
		} else {
			n := cbPlaybackRing.Read(dst)
			for i := n; i < refSamples; i++ {
				dst[i] = 0
			}
			if n < refSamples {
				cbPipeline.playbackPrebuf.Store(true)
				cbPipeline.underruns.Add(1)
				prebuf = true
			}
		}
	}

	aecActive := !prebuf && refSamples > 0 && micSamples > 0
	aecLen := refSamples
	if micSamples < aecLen {
		aecLen = micSamples
	}

	if aecActive {
		cbAec.Playback(dst[:aecLen])
	}

	// capture
	if micSamples > 0 {
		pcm := unsafe.Slice((*int16)(unsafe.Pointer(&input[0])), micSamples)
		if aecActive {
			cbAec.Capture(pcm[:aecLen])
			cbPipeline.aecFrames.Add(1)
		}
		cbCaptureRing.Write(pcm)
		select {
		case cbNotify <- struct{}{}:
		default:
		}
	}
	atomic.AddUint64(&cbPipeline.captureFrames, uint64(frameCount))
}

// realPipeline — режим "real": захват с микрофона через malgo, Opus-кодирование,
// декодирование входящего Opus и вывод на динамик.
type realPipeline struct {
	src           *opusCaptureSource
	sink          port.RTPSink
	rec           *wavRecorder
	playbackRing  *ringBuffer
	notify        chan struct{}
	closeDev      func() error
	captureFrames uint64
	aec           echoCanceller
	aecFrames     atomic.Int64

	// playback jitter compensation:
	// после старта и после каждого underrun копим prebufSamples сэмплов
	// прежде чем начать выдачу, иначе резкий обрыв звука.
	playbackPrebuf atomic.Bool
	prebufSamples  int
	underruns      atomic.Int64

	monitorStop chan struct{}
}

// Defaults для playback-буферов. 48 kHz моно, фрейм Opus = 20 мс (960 sample).
// playbackRing ~640 мс — запас на сетевой jitter; pre-buffer ~200 мс —
// целевой уровень, ниже которого возврат в prebuffer-режим.
// captureRing ~320 мс — запас на случай задержек pumpOutbound.
const (
	captureRingSamples       = opusFrameSize * 16
	defaultPlaybackBufferMs  = 640
	defaultPlaybackPrebufMs  = 200
	minPlaybackBufferMs      = 80
	maxPlaybackBufferMs      = 4000
	minPlaybackPrebufMs      = 20
)

func msToSamples(ms int) int { return ms * opusSampleRate / 1000 }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func newRealPipeline(_ context.Context, opts port.AudioOpts) (port.AudioPipeline, error) {
	enc, err := newOpusEncoder()
	if err != nil {
		return nil, err
	}
	dec, err := newOpusDecoder()
	if err != nil {
		return nil, err
	}

	bufMs := opts.PlaybackBufferMs
	if bufMs <= 0 {
		bufMs = defaultPlaybackBufferMs
	}
	bufMs = clampInt(bufMs, minPlaybackBufferMs, maxPlaybackBufferMs)
	prebufMs := opts.PlaybackPrebufMs
	if prebufMs <= 0 {
		prebufMs = defaultPlaybackPrebufMs
	}
	prebufMs = clampInt(prebufMs, minPlaybackPrebufMs, bufMs/2)

	captureRing := newRingBuffer(captureRingSamples)
	playbackRing := newRingBuffer(msToSamples(bufMs))
	notify := make(chan struct{}, 1)

	var aec echoCanceller = nullCanceller{}
	if opts.EchoCancellation {
		var err error
		aec, err = newSpeexCanceller(opts.AECTailMs)
		if err != nil {
			enc.Close()
			dec.Close()
			return nil, err
		}
	}

	p := &realPipeline{
		playbackRing:  playbackRing,
		notify:        notify,
		prebufSamples: msToSamples(prebufMs),
		aec:           aec,
		monitorStop:   make(chan struct{}),
	}
	p.playbackPrebuf.Store(true)
	p.src = newOpusCaptureSource(enc, captureRing, notify, opts.MaxDuration)

	if opts.RecordPath != "" {
		rec, recErr := newWAVRecorder(opts.RecordPath)
		if recErr != nil {
			return nil, recErr
		}
		p.rec = rec
		p.sink = rec
	} else {
		p.sink = newOpusPlaybackSink(dec, playbackRing)
	}

	mctx, cerr := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if cerr != nil {
		_ = p.src.Close()
		_ = p.sink.Close()
		p.aec.Close()
		return nil, cerr
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Duplex)
	cfg.SampleRate = 48000
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1

	// Идентификаторы устройств храним на куче с Pin, чтобы Go 1.24
	// пропустил их через cgo (DeviceConfig.toC() передаёт эти указатели
	// в C-структуру, и без Pin'а cgo-рантайм паникует).
	var pinner runtime.Pinner
	if opts.InputDeviceID != "" {
		b, err := hex.DecodeString(opts.InputDeviceID)
		if err == nil {
			id := new(malgo.DeviceID)
			copy(id[:], b)
			pinner.Pin(id)
			cfg.Capture.DeviceID = unsafe.Pointer(id)
		}
	}
	if opts.OutputDeviceID != "" {
		b, err := hex.DecodeString(opts.OutputDeviceID)
		if err == nil {
			id := new(malgo.DeviceID)
			copy(id[:], b)
			pinner.Pin(id)
			cfg.Playback.DeviceID = unsafe.Pointer(id)
		}
	}
	setAudioCallbackState(captureRing, playbackRing, notify, p, aec)
	callbacks := malgo.DeviceCallbacks{Data: audioDataCallback}
	device, derr := malgo.InitDevice(mctx.Context, cfg, callbacks)
	pinner.Unpin() // DeviceID скопированы miniaudio, фиксировать больше не нужно
	if derr != nil {
		clearAudioCallbackState()
		mctx.Free()
		_ = p.src.Close()
		_ = p.sink.Close()
		p.aec.Close()
		return nil, derr
	}
	if err := device.Start(); err != nil {
		clearAudioCallbackState()
		device.Uninit()
		mctx.Free()
		_ = p.src.Close()
		_ = p.sink.Close()
		p.aec.Close()
		return nil, err
	}
	p.closeDev = func() error {
		clearAudioCallbackState()
		_ = device.Stop()
		device.Uninit()
		mctx.Free()
		return nil
	}
	if opts.OnUnderrun != nil {
		go p.runUnderrunMonitor(opts.OnUnderrun)
	}
	return p, nil
}

// runUnderrunMonitor периодически снимает счётчик underrun и вызывает cb,
// если он вырос. Работает вне realtime callback'а, поэтому callback может
// безопасно писать в файл/лог.
func (p *realPipeline) runUnderrunMonitor(cb func(int)) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	var prev int64
	for {
		select {
		case <-p.monitorStop:
			return
		case <-t.C:
			cur := p.underruns.Load()
			if cur > prev {
				cb(int(cur))
				prev = cur
			}
		}
	}
}

func (p *realPipeline) Kind() string             { return port.AudioKindAudio }
func (p *realPipeline) Outbound() port.RTPSource { return p.src }
func (p *realPipeline) Inbound() port.RTPSink    { return p.sink }
func (p *realPipeline) Stats() port.AudioStats {
	stats := port.AudioStats{
		SamplesSent:     p.src.sentCount(),
		AecFrames:       p.aecFrames.Load(),
	}
	if p.rec != nil {
		stats.SamplesReceived = p.rec.receivedCount()
	} else if ps, ok := p.sink.(*opusPlaybackSink); ok {
		stats.SamplesReceived = ps.receivedCount()
	}
	return stats
}
func (p *realPipeline) Close() error {
	select {
	case <-p.monitorStop:
	default:
		close(p.monitorStop)
	}
	_ = p.src.Close()
	if p.closeDev != nil {
		_ = p.closeDev()
	}
	p.aec.Close()
	return p.sink.Close()
}
