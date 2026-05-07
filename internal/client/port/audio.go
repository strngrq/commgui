package port

import (
	"context"
	"time"
)

const (
	// AudioKindAudio — pipeline даёт RTP-аудио (нужен Opus AudioTrack).
	AudioKindAudio = "audio"
	// AudioKindData — pipeline не использует RTP, нужен только DataChannel.
	AudioKindData = "data"
)

// AudioDevice — одно аудио-устройство (микрофон или вывод).
type AudioDevice struct {
	ID        string // OS-зависимый идентификатор
	Name      string // человекочитаемое имя
	Kind      string // "input" | "output"
	IsDefault bool
}

// AudioEngine собирает аудио-pipeline для конкретного режима.
type AudioEngine interface {
	ListDevices(ctx context.Context) ([]AudioDevice, error)
	Build(ctx context.Context, opts AudioOpts) (AudioPipeline, error)
}

// AudioOpts — параметры построения аудио-pipeline.
type AudioOpts struct {
	Mode           string        // "null", "tone", "file:<path>", "real"
	RecordPath     string        // куда писать принятый аудио (если задан и Kind=audio)
	MaxDuration    time.Duration // обрыв исходящей дорожки по времени; 0 = без лимита
	InputDeviceID  string        // ID устройства захвата; "" = default
	OutputDeviceID string        // ID устройства воспроизведения; "" = default

	// PlaybackBufferMs — общий размер playback ring buffer в миллисекундах.
	// 0 = default (~640 мс).
	PlaybackBufferMs int
	// PlaybackPrebufMs — целевая задержка (pre-buffer) в миллисекундах:
	// pipeline копит этот объём перед началом выдачи и после каждого underrun.
	// 0 = default (~200 мс).
	PlaybackPrebufMs int
	// EchoCancellation включает акустическое эхоподавление (AEC) в реальном
	// аудио-pipeline. Когда true, захваченный PCM прогоняется через SpeexDSP
	// echo canceller с reference-сигналом от playback.
	EchoCancellation bool
	// AECTailMs — длина адаптивного фильтра эхоподавителя в миллисекундах.
	// 0 = default (200 мс). Поднимать выше 250 мс смысла обычно нет: фильтр
	// дольше адаптируется и качество подавления падает.
	AECTailMs int
	// OnUnderrun вызывается из мониторинговой goroutine (не из realtime callback)
	// после обнаружения опустошений playback ring. count — суммарное число
	// underrun-эпизодов с момента старта pipeline.
	OnUnderrun func(count int)
}

// AudioPipeline — пара источник + сток. WebRTC-адаптер привязывает Outbound
// к локальному треку, OnTrack от пира — к Inbound.
type AudioPipeline interface {
	// Kind: AudioKindAudio (нужен Opus track) или AudioKindData (только DataChannel).
	Kind() string
	Outbound() RTPSource
	Inbound() RTPSink
	Stats() AudioStats
	Close() error
}

// RTPSource — источник исходящих RTP-payload'ов.
type RTPSource interface {
	// Next блокируется до следующего фрейма; возвращает io.EOF при закрытии источника
	// или ctx.Err() при отмене контекста.
	Next(ctx context.Context) (payload []byte, duration time.Duration, err error)
	Close() error
}

// RTPSink — сток принятых RTP-payload'ов.
type RTPSink interface {
	Push(payload []byte)
	Close() error
}

// AudioStats — счётчики по pipeline.
type AudioStats struct {
	SamplesSent     int
	SamplesReceived int
	AecFrames       int64 // число фреймов, обработанных эхоподавителем (0 = AEC выключен)
}
