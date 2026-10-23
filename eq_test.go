package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
	for _, size := range [][2]int{{52, 12}, {30, 8}, {90, 20}} {
		w, h := size[0], size[1]
		lines := strings.Split(eqPanel(w, h, eqPresets[1].gains, 1), "\n")
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

func TestEQPanelLabelRowBracketsTheSelectedBand(t *testing.T) {
	lastRow := func(w, sel int) string {
		lines := strings.Split(eqPanel(w, 12, eqPresets[0].gains, sel), "\n")
		return lines[len(lines)-1]
	}
	if last := lastRow(52, 1); !strings.Contains(last, "[64]") {
		t.Fatalf("label row %q does not bracket the selected band", last)
	}
	// "[250]" would overflow a 4-wide column and shift every label after it.
	if last := lastRow(52, 3); strings.Contains(last, "[") || !strings.Contains(last, " 250 ") {
		t.Fatalf("label row %q brackets a label wider than its column", last)
	}
	if last := lastRow(30, 1); !strings.Contains(last, "▲") || strings.ContainsAny(last, "0123456789") {
		t.Fatalf("narrow label row %q should only point at the selected band", last)
	}
}

// Flat gains put every band at the same height, so every column's cell (plus
// its trailing gap) must render byte-identical on every bar row.
func TestEQBarRowFlatGainsGiveEveryColumnTheSameCell(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	const colW, barRows = 4, 6
	z := barRows / 2
	var heights [10]float64
	for i := range heights {
		heights[i] = eqBarHeight(0, z, barRows) // Flat: every gain 0
	}
	for r := 0; r < barRows; r++ {
		level := float64(barRows - r)
		row := eqBarRow(100, colW, heights, level, barRows, r == barRows-z-1)
		body := []rune(strings.TrimPrefix(row, " "))
		cell := func(i int) string { return string(body[i*(colW+1) : (i+1)*(colW+1)]) }
		want := cell(0)
		for i := 1; i < 10; i++ {
			if got := cell(i); got != want {
				t.Fatalf("row %d: column %d = %q, column 0 = %q, want identical under flat gains",
					r, i, got, want)
			}
		}
	}
}
