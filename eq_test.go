package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestEQPresetNameMatchesExactlyOrCustom(t *testing.T) {
	if got := eqPresetName([10]int{}); got != "Flat" {
		t.Fatalf("flat gains named %q, want Flat", got)
	}
	if got := eqPresetName(eqPresets[1].gains); got != "Bass Boost" {
		t.Fatalf("bass boost gains named %q", got)
	}
	custom := eqPresets[1].gains
	custom[9]++ // one band off from a known preset
	if got := eqPresetName(custom); got != "Custom" {
		t.Fatalf("tweaked preset named %q, want Custom", got)
	}
}

func TestNextEQPresetCyclesAndRestartsFromCustom(t *testing.T) {
	gains := eqPresets[0].gains
	for i := 1; i < len(eqPresets); i++ {
		gains = nextEQPreset(gains)
		if gains != eqPresets[i].gains {
			t.Fatalf("step %d = %v, want %v", i, gains, eqPresets[i].gains)
		}
	}
	if got := nextEQPreset(gains); got != eqPresets[0].gains {
		t.Fatalf("cycle did not wrap to Flat: %v", got)
	}
	if got := nextEQPreset([10]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}); got != eqPresets[0].gains {
		t.Fatalf("custom mix did not restart at Flat: %v", got)
	}
}

func TestClampEQGainStaysInRange(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0}, {12, 12}, {13, 12}, {-12, -12}, {-13, -12}, {1000, 12}, {-1000, -12},
	}
	for _, c := range cases {
		if got := clampEQGain(c.in); got != c.want {
			t.Errorf("clampEQGain(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEQGainsFileRoundTrips(t *testing.T) {
	t.Setenv("AMTUI_CONFIG_DIR", t.TempDir())
	if got := loadEQGains(); got != [10]int{} {
		t.Fatalf("fresh config dir gave %v, want flat", got)
	}
	gains := [10]int{6, 5, 4, 2, 0, 0, 0, 0, 0, -3}
	saveEQGains(gains)
	if got := loadEQGains(); got != gains {
		t.Fatalf("saved %v, loaded %v", gains, got)
	}
}

func TestEQPanelFillsExactlyTheGivenBox(t *testing.T) {
	for _, size := range [][2]int{{60, 12}, {24, 4}, {120, 20}} {
		w, h := size[0], size[1]
		lines := strings.Split(eqPanel(w, h, eqPresets[1].gains, 3), "\n")
		if len(lines) != h-1 {
			t.Fatalf("eqPanel(%d,%d) drew %d rows, want %d", w, h, len(lines), h-1)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("eqPanel(%d,%d) row %d is %d wide, want %d", w, h, i, got, w)
			}
		}
	}
}
