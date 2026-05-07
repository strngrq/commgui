//go:build !noaudio

package audio

// #cgo linux LDFLAGS: -Wl,-Bstatic -lspeexdsp -Wl,-Bdynamic -lm
/*
#include <speex/speex_echo.h>
#include <speex/speex_preprocess.h>

// Создание SpeexEchoState с явной установкой sample rate. Без
// SPEEX_ECHO_SET_SAMPLING_RATE Speex считает sample rate = 8 kHz и его
// внутренние эвристики (DT-detection, leak estimator) дают мусор на 48 kHz.
static SpeexEchoState* speex_aec_init(int frameSize, int filterLength, int sampleRate) {
	SpeexEchoState *st = speex_echo_state_init(frameSize, filterLength);
	if (st == NULL) return NULL;
	int rate = sampleRate;
	speex_echo_ctl(st, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);
	return st;
}

// Препроцессор, привязанный к echo state. Делает нелинейное подавление
// остаточного эхо (NLP) и шумодав. Без этой связки линейный AEC даёт
// 10-25 дБ подавления, остальное эхо слышно.
static SpeexPreprocessState* speex_pp_init(int frameSize, int sampleRate, SpeexEchoState *aec) {
	SpeexPreprocessState *pp = speex_preprocess_state_init(frameSize, sampleRate);
	if (pp == NULL) return NULL;
	speex_preprocess_ctl(pp, SPEEX_PREPROCESS_SET_ECHO_STATE, aec);
	int denoise = 1;
	speex_preprocess_ctl(pp, SPEEX_PREPROCESS_SET_DENOISE, &denoise);
	int echo_suppress = -40;
	speex_preprocess_ctl(pp, SPEEX_PREPROCESS_SET_ECHO_SUPPRESS, &echo_suppress);
	int echo_suppress_active = -15;
	speex_preprocess_ctl(pp, SPEEX_PREPROCESS_SET_ECHO_SUPPRESS_ACTIVE, &echo_suppress_active);
	return pp;
}

static void speex_aec_playback(SpeexEchoState *st, const short *ref) {
	speex_echo_playback(st, ref);
}

static void speex_aec_capture(SpeexEchoState *st, const short *mic, short *out) {
	speex_echo_capture(st, mic, out);
}

static void speex_pp_run(SpeexPreprocessState *pp, short *frame) {
	speex_preprocess_run(pp, frame);
}

static void speex_aec_reset(SpeexEchoState *st) {
	speex_echo_state_reset(st);
}

static void speex_aec_destroy(SpeexEchoState *st) {
	speex_echo_state_destroy(st);
}

static void speex_pp_destroy(SpeexPreprocessState *pp) {
	speex_preprocess_state_destroy(pp);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	aecFrameSize     = 480 // 10 мс при 48 kHz — рекомендованный размер фрейма Speex
	aecDefaultTailMs = 200 // 9600 sample при 48 kHz; 100 мс маловато для ноута с открытыми динамиками
)

// speexCanceller — реализация на SpeexDSP с двухступенчатым API и
// preprocess-цепочкой (NLP + denoise).
type speexCanceller struct {
	state     *C.SpeexEchoState
	pp        *C.SpeexPreprocessState
	frameSize int
	tmp       []int16 // выходной буфер speex_echo_capture, переиспользуем
}

func newSpeexCanceller(tailMs int) (echoCanceller, error) {
	if tailMs <= 0 {
		tailMs = aecDefaultTailMs
	}
	filterLength := tailMs * opusSampleRate / 1000
	state := C.speex_aec_init(C.int(aecFrameSize), C.int(filterLength), C.int(opusSampleRate))
	if state == nil {
		return nil, fmt.Errorf("speex_echo_state_init failed")
	}
	pp := C.speex_pp_init(C.int(aecFrameSize), C.int(opusSampleRate), state)
	if pp == nil {
		C.speex_aec_destroy(state)
		return nil, fmt.Errorf("speex_preprocess_state_init failed")
	}
	return &speexCanceller{
		state:     state,
		pp:        pp,
		frameSize: aecFrameSize,
		tmp:       make([]int16, aecFrameSize),
	}, nil
}

func (s *speexCanceller) Playback(reference []int16) {
	for offset := 0; offset+s.frameSize <= len(reference); offset += s.frameSize {
		C.speex_aec_playback(s.state, (*C.short)(unsafe.Pointer(&reference[offset])))
	}
}

func (s *speexCanceller) Capture(capture []int16) {
	for offset := 0; offset+s.frameSize <= len(capture); offset += s.frameSize {
		C.speex_aec_capture(
			s.state,
			(*C.short)(unsafe.Pointer(&capture[offset])),
			(*C.short)(unsafe.Pointer(&s.tmp[0])),
		)
		C.speex_pp_run(s.pp, (*C.short)(unsafe.Pointer(&s.tmp[0])))
		copy(capture[offset:offset+s.frameSize], s.tmp)
	}
}

func (s *speexCanceller) Reset() {
	if s.state != nil {
		C.speex_aec_reset(s.state)
	}
}

func (s *speexCanceller) Close() {
	if s.pp != nil {
		C.speex_pp_destroy(s.pp)
		s.pp = nil
	}
	if s.state != nil {
		C.speex_aec_destroy(s.state)
		s.state = nil
	}
}
