package audio

import "sync/atomic"

// ringBuffer — lock-free SPSC ring buffer для int16 PCM-сэмплов.
// Producer (один) пишет через Write, Consumer (один) читает через Read.
// Индексы всегда растут, маскирование происходит при доступе к слайсу.
type ringBuffer struct {
	buf  []int16
	mask int
	rIdx atomic.Uint64
	wIdx atomic.Uint64
}

func newRingBuffer(capacity int) *ringBuffer {
	size := 1
	for size < capacity {
		size <<= 1
	}
	return &ringBuffer{
		buf:  make([]int16, size),
		mask: size - 1,
	}
}

func (rb *ringBuffer) Write(src []int16) int {
	w := rb.wIdx.Load()
	r := rb.rIdx.Load()
	size := uint64(len(rb.buf))

	avail := size - (w - r)
	if avail == 0 {
		return 0
	}
	n := uint64(len(src))
	if n > avail {
		n = avail
	}
	for i := uint64(0); i < n; i++ {
		rb.buf[(w+i)&uint64(rb.mask)] = src[i]
	}
	rb.wIdx.Store(w + n)
	return int(n)
}

func (rb *ringBuffer) Read(dst []int16) int {
	r := rb.rIdx.Load()
	w := rb.wIdx.Load()

	avail := w - r
	if avail == 0 {
		return 0
	}
	n := uint64(len(dst))
	if n > avail {
		n = avail
	}
	for i := uint64(0); i < n; i++ {
		dst[i] = rb.buf[(r+i)&uint64(rb.mask)]
	}
	rb.rIdx.Store(r + n)
	return int(n)
}

func (rb *ringBuffer) Available() int {
	r := rb.rIdx.Load()
	w := rb.wIdx.Load()
	return int(w - r)
}
