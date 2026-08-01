package main

import (
	"math"
	"testing"
)

func TestPeakHoldRisesInstantlyAndDecays(t *testing.T) {
	var peaks [32]float64
	bands := [32]float64{}
	bands[0] = 0.9

	decayPeaks(&peaks, bands)
	if peaks[0] != 0.9 {
		t.Fatalf("peak did not follow the band up: %.3f, want 0.9", peaks[0])
	}

	bands[0] = 0.1
	decayPeaks(&peaks, bands)
	if peaks[0] >= 0.9 {
		t.Errorf("peak %.3f did not decay", peaks[0])
	}
	if peaks[0] <= 0.1 {
		t.Errorf("peak %.3f decayed past the band in a single frame", peaks[0])
	}
}

func TestPeakHoldNeverGoesNegative(t *testing.T) {
	var peaks [32]float64
	var bands [32]float64
	for i := 0; i < 500; i++ {
		decayPeaks(&peaks, bands)
	}
	for i, p := range peaks {
		if p < 0 {
			t.Fatalf("peaks[%d] = %.3f, want >= 0", i, p)
		}
	}
}

func TestSimulatedBandsStayInRange(t *testing.T) {
	for _, playing := range []bool{true, false} {
		for step := 0; step < 200; step++ {
			bands := simulatedBands(float64(step)/30, playing)
			for i, v := range bands {
				if v < 0 || v > 1 || math.IsNaN(v) {
					t.Fatalf("simulatedBands(playing=%v)[%d] = %v, want 0..1", playing, i, v)
				}
			}
		}
	}
}

func TestSimulatedBandsCollapseWhenPaused(t *testing.T) {
	playingSum, pausedSum := 0.0, 0.0
	for step := 0; step < 60; step++ {
		p := simulatedBands(float64(step)/30, true)
		q := simulatedBands(float64(step)/30, false)
		for i := range p {
			playingSum += p[i]
			pausedSum += q[i]
		}
	}
	if pausedSum >= playingSum*0.5 {
		t.Errorf("paused bands (%.2f) did not collapse below playing (%.2f)", pausedSum, playingSum)
	}
}

func TestWaveRecordsAtTheRightBucket(t *testing.T) {
	var w wave
	w.record(0.0, 0.5)
	if w.samples[0] != 0.5 {
		t.Errorf("samples[0] = %.2f, want 0.50", w.samples[0])
	}
	w.record(1.0, 0.7) // frac 1.0 must clamp into the last bucket, not overflow
	if w.samples[waveBuckets-1] != 0.7 {
		t.Errorf("samples[last] = %.2f, want 0.70", w.samples[waveBuckets-1])
	}
	w.record(0.5, 0.3)
	if w.samples[waveBuckets/2] != 0.3 {
		t.Errorf("samples[mid] = %.2f, want 0.30", w.samples[waveBuckets/2])
	}
}

func TestWaveKeepsThePeakPerBucket(t *testing.T) {
	var w wave
	w.record(0.25, 0.4)
	w.record(0.25, 0.9)
	w.record(0.25, 0.2)
	if w.samples[waveBuckets/4] != 0.9 {
		t.Errorf("bucket = %.2f, want the peak 0.90", w.samples[waveBuckets/4])
	}
}

func TestWaveResetClears(t *testing.T) {
	var w wave
	w.record(0.5, 0.8)
	w.reset()
	for i, v := range w.samples {
		if v != 0 {
			t.Fatalf("samples[%d] = %.2f after reset, want 0", i, v)
		}
	}
}

func TestWaveColumnAggregatesRange(t *testing.T) {
	var w wave
	w.record(0.0, 0.2)
	w.record(0.01, 0.6)
	// The first of 10 columns spans buckets 0..waveBuckets/10, so it must
	// report the peak of both samples.
	if got := w.column(0, 10); got != 0.6 {
		t.Errorf("column(0,10) = %.2f, want 0.60", got)
	}
}

func TestBandsLevelIsMean(t *testing.T) {
	var bands [32]float64
	for i := range bands {
		bands[i] = 0.5
	}
	if got := bandsLevel(bands); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("bandsLevel() = %.4f, want 0.5", got)
	}
}

func TestBassLevelUsesLowBandsOnly(t *testing.T) {
	var bands [32]float64
	bands[0], bands[1], bands[2], bands[3] = 1, 1, 1, 1
	if got := bassLevel(bands); math.Abs(got-1) > 1e-9 {
		t.Errorf("bassLevel(low=1) = %.4f, want 1", got)
	}
	var high [32]float64
	for i := 8; i < 32; i++ {
		high[i] = 1
	}
	if got := bassLevel(high); got != 0 {
		t.Errorf("bassLevel(high only) = %.4f, want 0", got)
	}
}
