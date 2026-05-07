//go:build !noaudio

package audio

// #cgo linux LDFLAGS: -Wl,-Bstatic -lopus -Wl,-Bdynamic -lm
/*
#include <opus/opus.h>

// macOS/windows linker flags are supplied via CGO_LDFLAGS env var at build time.

static OpusEncoder* new_encoder(int sampleRate, int channels, int app, int *err) {
	return opus_encoder_create(sampleRate, channels, app, err);
}

static OpusDecoder* new_decoder(int sampleRate, int channels, int *err) {
	return opus_decoder_create(sampleRate, channels, err);
}

static int encode_pcm(OpusEncoder *enc, const short *pcm, int frameSize, unsigned char *data, int maxData) {
	return opus_encode(enc, pcm, frameSize, data, maxData);
}

static int decode_payload(OpusDecoder *dec, const unsigned char *data, int len, short *pcm, int frameSize) {
	return opus_decode(dec, data, len, pcm, frameSize, 0);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	opusSampleRate    = 48000
	opusChannels      = 1
	opusFrameSize     = 960 // 20ms при 48kHz
	opusFrameDuration = 20_000_000 // 20ms в наносекундах
	opusMaxPayload    = 1275 // максимальный размер Opus-фрейма для 48kHz mono

	opusAppVoIP = 2048 // OPUS_APPLICATION_VOIP
)

// opusEncoder — обёртка над libopus encoder через CGo.
type opusEncoder struct {
	enc *C.OpusEncoder
}

func newOpusEncoder() (*opusEncoder, error) {
	var err C.int
	enc := C.new_encoder(C.int(opusSampleRate), C.int(opusChannels), C.int(opusAppVoIP), &err)
	if err != 0 {
		return nil, fmt.Errorf("opus encoder create: error %d", int(err))
	}
	return &opusEncoder{enc: enc}, nil
}

func (e *opusEncoder) Encode(pcm []int16) ([]byte, error) {
	payload := make([]byte, opusMaxPayload)
	n := C.encode_pcm(
		e.enc,
		(*C.short)(unsafe.Pointer(&pcm[0])),
		C.int(opusFrameSize),
		(*C.uchar)(unsafe.Pointer(&payload[0])),
		C.int(opusMaxPayload),
	)
	if n < 0 {
		return nil, fmt.Errorf("opus encode: error %d", int(n))
	}
	return payload[:int(n)], nil
}

func (e *opusEncoder) Close() {
	if e.enc != nil {
		C.opus_encoder_destroy(e.enc)
		e.enc = nil
	}
}

// opusDecoder — обёртка над libopus decoder через CGo.
type opusDecoder struct {
	dec *C.OpusDecoder
}

func newOpusDecoder() (*opusDecoder, error) {
	var err C.int
	dec := C.new_decoder(C.int(opusSampleRate), C.int(opusChannels), &err)
	if err != 0 {
		return nil, fmt.Errorf("opus decoder create: error %d", int(err))
	}
	return &opusDecoder{dec: dec}, nil
}

func (d *opusDecoder) Decode(data []byte) ([]int16, error) {
	pcm := make([]int16, opusFrameSize)
	n := C.decode_payload(
		d.dec,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		(*C.short)(unsafe.Pointer(&pcm[0])),
		C.int(opusFrameSize),
	)
	if n < 0 {
		return nil, fmt.Errorf("opus decode: error %d", int(n))
	}
	return pcm[:int(n)], nil
}

func (d *opusDecoder) Close() {
	if d.dec != nil {
		C.opus_decoder_destroy(d.dec)
		d.dec = nil
	}
}
