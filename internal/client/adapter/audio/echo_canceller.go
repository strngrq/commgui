package audio

// echoCanceller — акустическое эхоподавление с двухступенчатой семантикой:
// Playback регистрирует кадр reference-сигнала (то, что ушло в динамик),
// Capture обрабатывает кадр микрофона in-place, используя ранее накопленный
// reference. Reset сбрасывает адаптивный фильтр (например, после underrun).
type echoCanceller interface {
	Playback(reference []int16)
	Capture(capture []int16)
	Reset()
	Close()
}

// nullCanceller — заглушка, когда AEC выключен.
type nullCanceller struct{}

func (nullCanceller) Playback([]int16) {}
func (nullCanceller) Capture([]int16)  {}
func (nullCanceller) Reset()           {}
func (nullCanceller) Close()           {}
