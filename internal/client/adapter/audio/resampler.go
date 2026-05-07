package audio

// resample16to48 повышает частоту дискретизации 16kHz → 48kHz линейной интерполяцией.
// Используется в file-режиме для WAV с частотой 16kHz.
func resample16to48(input []int16) []int16 {
	output := make([]int16, len(input)*3)
	for i := range output {
		srcPos := float64(i) / 3.0
		lo := int(srcPos)
		hi := lo + 1
		if hi >= len(input) {
			output[i] = input[lo]
		} else {
			frac := srcPos - float64(lo)
			output[i] = int16(float64(input[lo])*(1-frac) + float64(input[hi])*frac)
		}
	}
	return output
}
