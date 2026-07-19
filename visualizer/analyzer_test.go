package visualizer

import (
	"fmt"
	"math"
	"testing"
)

func TestAnalyzerPlacesSinusoidsInExpectedBands(t *testing.T) {
	t.Parallel()

	for _, sampleRate := range []int{44_100, 48_000} {
		for _, frequency := range []float64{35, 60, 100, 1_000, 8_000} {
			sampleRate := sampleRate
			frequency := frequency
			t.Run(fmt.Sprintf("%dHz/%d", int(frequency), sampleRate), func(t *testing.T) {
				t.Parallel()

				analyzer, err := NewAnalyzer(Format{SampleRate: sampleRate, Channels: 1})
				if err != nil {
					t.Fatalf("NewAnalyzer: %v", err)
				}

				bands, err := analyzer.Process(sinePCM(sampleRate, 1, frequency, 0.8, 4_096, false))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}

				got := peakBand(bands)
				want := expectedBand(frequency)
				if got < want-1 || got > want+1 {
					t.Fatalf("peak band = %d, want %d or an adjacent band; bands=%v", got, want, bands)
				}
				if bands[got] < 0.5 {
					t.Fatalf("peak strength = %.3f, want >= 0.5", bands[got])
				}
			})
		}
	}
}

func TestAnalyzerSilenceIsNearZero(t *testing.T) {
	t.Parallel()

	analyzer, err := NewAnalyzer(Format{SampleRate: 48_000, Channels: 2})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	bands, err := analyzer.Process(make([]float32, 4_096*2))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	for i, band := range bands {
		if band > 1e-6 {
			t.Errorf("band %d = %g, want near zero", i, band)
		}
	}
}

func TestAnalyzerDoesNotCancelAntiPhaseStereo(t *testing.T) {
	t.Parallel()

	analyzer, err := NewAnalyzer(Format{SampleRate: 48_000, Channels: 2})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	bands, err := analyzer.Process(sinePCM(48_000, 2, 1_000, 0.8, 4_096, true))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	peak := peakBand(bands)
	if bands[peak] < 0.5 {
		t.Fatalf("anti-phase stereo peak = %.3f in band %d, want >= 0.5", bands[peak], peak)
	}
	want := expectedBand(1_000)
	if peak < want-1 || peak > want+1 {
		t.Fatalf("anti-phase stereo peak band = %d, want %d or an adjacent band", peak, want)
	}
}

func TestAnalyzerBuildsRollingWindowFromChunks(t *testing.T) {
	t.Parallel()

	analyzer, err := NewAnalyzer(Format{SampleRate: 48_000, Channels: 2})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	pcm := sinePCM(48_000, 2, 1_000, 0.8, 4_096, false)
	var bands [32]float64
	for start := 0; start < len(pcm); start += 512 {
		end := min(start+512, len(pcm))
		bands, err = analyzer.Process(pcm[start:end])
		if err != nil {
			t.Fatalf("Process chunk [%d:%d]: %v", start, end, err)
		}
	}

	peak := peakBand(bands)
	want := expectedBand(1_000)
	if peak < want-1 || peak > want+1 {
		t.Fatalf("chunked PCM peak band = %d, want %d or an adjacent band", peak, want)
	}
	if bands[peak] < 0.5 {
		t.Fatalf("chunked PCM peak strength = %.3f, want >= 0.5", bands[peak])
	}
}

func TestAnalyzerOutputIsIndependentOfChunkSize(t *testing.T) {
	t.Parallel()

	format := Format{SampleRate: 48_000, Channels: 1}
	whole, err := NewAnalyzer(format)
	if err != nil {
		t.Fatalf("NewAnalyzer whole: %v", err)
	}
	oneFrame, err := NewAnalyzer(format)
	if err != nil {
		t.Fatalf("NewAnalyzer one frame: %v", err)
	}

	pcm := sinePCM(format.SampleRate, format.Channels, 1_000, 0.1, 4_800, false)
	wholeBands, err := whole.Process(pcm)
	if err != nil {
		t.Fatalf("Process whole: %v", err)
	}

	var oneFrameBands [32]float64
	for frame := 0; frame < len(pcm); frame++ {
		oneFrameBands, err = oneFrame.Process(pcm[frame : frame+1])
		if err != nil {
			t.Fatalf("Process frame %d: %v", frame, err)
		}
	}

	var maxDiff float64
	for band := range wholeBands {
		diff := math.Abs(wholeBands[band] - oneFrameBands[band])
		maxDiff = max(maxDiff, diff)
		if diff > 1e-12 {
			t.Errorf(
				"band %d differs by %g: whole=%g one-frame=%g",
				band,
				diff,
				wholeBands[band],
				oneFrameBands[band],
			)
		}
	}
	t.Logf("maximum whole-vs-one-frame difference: %.12g", maxDiff)
}

func TestAnalyzerCompensatesHannEquivalentNoiseBandwidth(t *testing.T) {
	t.Parallel()

	const (
		sampleRate = 48_000
		bin        = 85
		amplitude  = 0.01
	)
	frequency := float64(bin*sampleRate) / windowFrames
	analyzer, err := NewAnalyzer(Format{SampleRate: sampleRate, Channels: 1})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	bands, err := analyzer.Process(sinePCM(sampleRate, 1, frequency, amplitude, sampleRate, false))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	got := bands[expectedBand(frequency)]
	want := (-40.0 - dbFloor) / (dbCeil - dbFloor)
	if diff := math.Abs(got - want); diff > 1e-3 {
		t.Fatalf("normalized level = %.9f, want %.9f (difference %.9f)", got, want, diff)
	}
	t.Logf("Hann ENBW = %.9f, normalized level = %.9f", analyzer.windowENBW, got)
}

func TestNewAnalyzerRejectsInvalidFormats(t *testing.T) {
	t.Parallel()

	tests := []Format{
		{SampleRate: 0, Channels: 1},
		{SampleRate: -1, Channels: 1},
		{SampleRate: 48_000, Channels: 0},
		{SampleRate: 48_000, Channels: 3},
	}
	for _, format := range tests {
		if _, err := NewAnalyzer(format); err == nil {
			t.Errorf("NewAnalyzer(%+v) succeeded, want error", format)
		}
	}
}

func TestAnalyzerRejectsIncompleteInterleavedFrame(t *testing.T) {
	t.Parallel()

	analyzer, err := NewAnalyzer(Format{SampleRate: 48_000, Channels: 2})
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	if _, err := analyzer.Process([]float32{0.5}); err == nil {
		t.Fatal("Process accepted an incomplete stereo frame")
	}
}

func TestAnalyzerRejectsNonFinitePCMWithoutMutatingState(t *testing.T) {
	t.Parallel()

	values := map[string]float32{
		"NaN":          float32(math.NaN()),
		"positive Inf": float32(math.Inf(1)),
		"negative Inf": float32(math.Inf(-1)),
	}
	for name, nonFinite := range values {
		name := name
		nonFinite := nonFinite
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			format := Format{SampleRate: 48_000, Channels: 1}
			analyzer, err := NewAnalyzer(format)
			if err != nil {
				t.Fatalf("NewAnalyzer: %v", err)
			}
			control, err := NewAnalyzer(format)
			if err != nil {
				t.Fatalf("NewAnalyzer control: %v", err)
			}

			invalid := sinePCM(format.SampleRate, format.Channels, 1_000, 0.1, 1_600, false)
			invalid[len(invalid)/2] = nonFinite
			if _, err := analyzer.Process(invalid); err == nil {
				t.Fatalf("Process accepted %s PCM", name)
			}

			valid := sinePCM(format.SampleRate, format.Channels, 1_000, 0.1, 1_600, false)
			got, err := analyzer.Process(valid)
			if err != nil {
				t.Fatalf("Process valid PCM: %v", err)
			}
			want, err := control.Process(valid)
			if err != nil {
				t.Fatalf("Process control PCM: %v", err)
			}
			if got != want {
				t.Fatalf("state changed after rejected %s PCM:\ngot  %v\nwant %v", name, got, want)
			}
		})
	}
}

func sinePCM(sampleRate, channels int, frequency, amplitude float64, frames int, antiPhase bool) []float32 {
	pcm := make([]float32, frames*channels)
	for frame := 0; frame < frames; frame++ {
		sample := float32(amplitude * math.Sin(2*math.Pi*frequency*float64(frame)/float64(sampleRate)))
		for channel := 0; channel < channels; channel++ {
			value := sample
			if antiPhase && channel == 1 {
				value = -value
			}
			pcm[frame*channels+channel] = value
		}
	}
	return pcm
}

func peakBand(bands [32]float64) int {
	peak := 0
	for i := 1; i < len(bands); i++ {
		if bands[i] > bands[peak] {
			peak = i
		}
	}
	return peak
}

func expectedBand(frequency float64) int {
	var edges [33]float64
	copy(edges[:], []float64{25, 40, 60, 90, 130, 185, 250})
	ratio := math.Pow(16_000.0/250.0, 1.0/26.0)
	for i := 1; i <= 26; i++ {
		edges[6+i] = 250 * math.Pow(ratio, float64(i))
	}
	for band := range 32 {
		if frequency >= edges[band] && frequency < edges[band+1] {
			return band
		}
	}
	return -1
}
