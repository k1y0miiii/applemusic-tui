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
