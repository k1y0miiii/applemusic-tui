package visualizer

import (
	"fmt"
	"math"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	windowFrames = 4_096
	bandCount    = 32

	dbFloor = -75.0
	dbCeil  = -12.0

	attackTime  = 35 * time.Millisecond
	releaseTime = 190 * time.Millisecond
)

type Analyzer struct {
	format Format
	fft    *fourier.FFT

	hann       []float64
	windowSum  float64
	windowENBW float64
	ring       [2][]float64
	scratch    []float64
	coeff      [2][]complex128
	binBand    []int

	write       int
	hopFrames   int
	framesToHop int
	bands       [bandCount]float64
}

func NewAnalyzer(format Format) (*Analyzer, error) {
	if format.SampleRate <= 0 {
		return nil, fmt.Errorf("visualizer: invalid sample rate %d", format.SampleRate)
	}
	if format.Channels != 1 && format.Channels != 2 {
		return nil, fmt.Errorf("visualizer: channels must be mono or stereo, got %d", format.Channels)
	}

	analyzer := &Analyzer{
		format:      format,
		fft:         fourier.NewFFT(windowFrames),
		hann:        make([]float64, windowFrames),
		scratch:     make([]float64, windowFrames),
		binBand:     make([]int, windowFrames/2+1),
		hopFrames:   max(1, format.SampleRate/30),
		framesToHop: max(1, format.SampleRate/30),
	}
	for channel := 0; channel < format.Channels; channel++ {
		analyzer.ring[channel] = make([]float64, windowFrames)
		analyzer.coeff[channel] = make([]complex128, windowFrames/2+1)
	}
	var windowSumSquares float64
	for i := range analyzer.hann {
		value := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(windowFrames-1))
		analyzer.hann[i] = value
		analyzer.windowSum += value
		windowSumSquares += value * value
	}
	analyzer.windowENBW = windowFrames * windowSumSquares /
		(analyzer.windowSum * analyzer.windowSum)

	edges := makeBandEdges()
	for bin := range analyzer.binBand {
		analyzer.binBand[bin] = -1
		if bin == 0 {
			continue
		}
		frequency := float64(bin) * float64(format.SampleRate) / windowFrames
		for band := 0; band < bandCount; band++ {
			if frequency >= edges[band] && frequency < edges[band+1] {
				analyzer.binBand[bin] = band
				break
			}
		}
	}

	return analyzer, nil
}

func (a *Analyzer) Process(interleaved []float32) ([bandCount]float64, error) {
	if len(interleaved)%a.format.Channels != 0 {
		return a.bands, fmt.Errorf(
			"visualizer: %d samples do not contain complete %d-channel frames",
			len(interleaved),
			a.format.Channels,
		)
	}
	for sample, value := range interleaved {
		value := float64(value)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return a.bands, fmt.Errorf("visualizer: PCM sample %d is not finite", sample)
		}
	}

	frames := len(interleaved) / a.format.Channels
	if frames == 0 {
		return a.bands, nil
	}

	for offset := 0; offset < frames; {
		step := min(a.framesToHop, frames-offset)
		start := offset * a.format.Channels
		end := (offset + step) * a.format.Channels
		a.appendFrames(interleaved[start:end], step)
		offset += step
		a.framesToHop -= step
		if a.framesToHop == 0 {
			a.analyzeHop()
			a.framesToHop = a.hopFrames
		}
	}

	return a.bands, nil
}

func (a *Analyzer) analyzeHop() {
	var powers [bandCount]float64
	amplitudeScale := 2 / a.windowSum
	powerScale := 1 / (float64(a.format.Channels) * a.windowENBW)
	for channel := 0; channel < a.format.Channels; channel++ {
		a.windowChannel(channel)
		a.fft.Coefficients(a.coeff[channel], a.scratch)
		for bin := 1; bin < len(a.coeff[channel]); bin++ {
			band := a.binBand[bin]
			if band < 0 {
				continue
			}
			coefficient := a.coeff[channel][bin]
			realPart := real(coefficient)
			imaginaryPart := imag(coefficient)
			amplitudeSquared := (realPart*realPart + imaginaryPart*imaginaryPart) *
				amplitudeScale * amplitudeScale
			powers[band] += amplitudeSquared * powerScale
		}
	}

	elapsedSeconds := float64(a.hopFrames) / float64(a.format.SampleRate)
	attackAlpha := -math.Expm1(-elapsedSeconds / attackTime.Seconds())
	releaseAlpha := -math.Expm1(-elapsedSeconds / releaseTime.Seconds())
	for band, power := range powers {
		target := normalizedDB(power)
		alpha := releaseAlpha
		if target > a.bands[band] {
			alpha = attackAlpha
		}
		a.bands[band] += alpha * (target - a.bands[band])
	}
}

func (a *Analyzer) appendFrames(interleaved []float32, frames int) {
	for frame := 0; frame < frames; frame++ {
		base := frame * a.format.Channels
		for channel := 0; channel < a.format.Channels; channel++ {
			a.ring[channel][a.write] = float64(interleaved[base+channel])
		}
		a.write++
		if a.write == windowFrames {
			a.write = 0
		}
	}
}

func (a *Analyzer) windowChannel(channel int) {
	for i := range windowFrames {
		ringIndex := a.write + i
		if ringIndex >= windowFrames {
			ringIndex -= windowFrames
		}
		a.scratch[i] = a.ring[channel][ringIndex] * a.hann[i]
	}
}

func normalizedDB(power float64) float64 {
	if power <= 0 {
		return 0
	}
	normalized := (10*math.Log10(power) - dbFloor) / (dbCeil - dbFloor)
	return max(0, min(1, normalized))
}

func makeBandEdges() [bandCount + 1]float64 {
	var edges [bandCount + 1]float64
	copy(edges[:], []float64{25, 40, 60, 90, 130, 185, 250})
	ratio := math.Pow(16_000.0/250.0, 1.0/26.0)
	for interval := 1; interval <= 26; interval++ {
		edges[6+interval] = 250 * math.Pow(ratio, float64(interval))
	}
	return edges
}
