package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// eqFreqs are the 10 band centers (Hz), lowest to highest. Band 0 is a
// low-shelf, band 9 a high-shelf, the rest peaking — see engine.SetEQ.
var eqFreqs = [10]int{32, 64, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

type eqPreset struct {
	name  string
	gains [10]int
}

// eqPresets is cycled by the P key, in this order. Flat first so pressing P
// from a custom mix always reaches "no EQ" within one lap.
var eqPresets = []eqPreset{
	{"Flat", [10]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
	{"Bass Boost", [10]int{6, 5, 4, 2, 0, 0, 0, 0, 0, 0}},
	{"Treble Boost", [10]int{0, 0, 0, 0, 0, 1, 2, 4, 5, 6}},
	{"Vocal", [10]int{-2, -2, -1, 1, 3, 4, 3, 1, 0, -1}},
	{"Loudness", [10]int{5, 4, 2, 0, -1, -1, 0, 2, 4, 5}},
	{"Rock", [10]int{4, 3, 2, 0, -1, -1, 1, 3, 4, 4}},
}

// eqPresetName matches gains against the named presets; anything else is
// "Custom", which also covers every hand-tweaked single band.
func eqPresetName(gains [10]int) string {
	for _, p := range eqPresets {
		if p.gains == gains {
			return p.name
		}
	}
	return "Custom"
}

// nextEQPreset cycles to the next preset in eqPresets. A Custom mix restarts
// the cycle at Flat rather than guessing which preset is "nearest".
func nextEQPreset(gains [10]int) [10]int {
	for i, p := range eqPresets {
		if p.gains == gains {
			return eqPresets[(i+1)%len(eqPresets)].gains
		}
	}
	return eqPresets[0].gains
}

func eqIsFlat(gains [10]int) bool { return gains == eqPresets[0].gains }

func clampEQGain(v int) int { return min(max(v, -12), 12) }

// The EQ gains live in their own one-line file, same reasoning as the theme
// and visualizer mode: writing it back must never clobber config.toml.
func eqFile() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "eq")
}

func loadEQGains() [10]int {
	var gains [10]int
	p := eqFile()
	if p == "" {
		return gains
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return gains
	}
	fields := strings.Fields(string(b))
	if len(fields) < 10 {
		return [10]int{} // corrupt/short file — flat is the safe fallback
	}
	for i := 0; i < 10; i++ {
		v, err := strconv.Atoi(fields[i])
		if err != nil {
			return [10]int{}
		}
		gains[i] = clampEQGain(v)
	}
	return gains
}

// saveEQGains is best-effort: a read-only config dir must not break the UI.
func saveEQGains(gains [10]int) {
	p := eqFile()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	parts := make([]string, len(gains))
	for i, g := range gains {
		parts[i] = strconv.Itoa(g)
	}
	_ = os.WriteFile(p, []byte(strings.Join(parts, " ")+"\n"), 0o644)
}

func eqBandLabel(i int) string {
	f := eqFreqs[i]
	if f >= 1000 {
		return fmt.Sprintf("%dk", f/1000)
	}
	return fmt.Sprintf("%d", f)
}

// eqBar renders one band's ±12 dB gain as a horizontal bar centered on a 0 dB
// line: it grows right for boost, left for cut, with eighth-block partials on
// the boost side for sub-cell precision. The cut side only gets a right half-block
// partial — Unicode has no right-anchored eighths to mirror the boost ones,
// and a second lookup table was not worth it for a cosmetic bar.
func eqBar(barW, gain int) string {
	if barW < 3 {
		return strings.Repeat(" ", max(0, barW))
	}
	center := barW / 2
	left := []rune(strings.Repeat(" ", center))
	right := []rune(strings.Repeat(" ", barW-center-1))
	const maxDB = 12.0
	mag := float64(gain) / maxDB
	eighths := []rune(" ▏▎▍▌▋▊▉█")
	switch {
	case mag > 0:
		if mag > 1 {
			mag = 1
		}
		cells := mag * float64(len(right))
		full := int(cells)
		for i := 0; i < full && i < len(right); i++ {
			right[i] = '█'
		}
		if full < len(right) {
			if idx := int((cells-float64(full))*8 + 0.5); idx > 0 {
				right[full] = eighths[idx]
			}
		}
	case mag < 0:
		if mag < -1 {
			mag = -1
		}
		cells := -mag * float64(len(left))
		full := int(cells)
		for i := 0; i < full && i < len(left); i++ {
			left[len(left)-1-i] = '█'
		}
		if full < len(left) && cells-float64(full) >= 0.5 {
			left[len(left)-1-full] = '▐'
		}
	}
	return string(left) + "│" + string(right)
}

func eqRow(w, gain int, label string, selected bool) string {
	lbl := fmt.Sprintf("%4s", label)
	val := fmt.Sprintf("%+3d", gain)
	barW := w - lipgloss.Width(lbl) - lipgloss.Width(val) - 2
	bar := eqBar(barW, gain)
	fg, bold := fgDim, false
	if selected {
		fg, bold = accentHi, true
	}
	st := lipgloss.NewStyle().Foreground(fg).Bold(bold)
	return st.Render(pad(lbl+" "+bar+" "+val, w))
}

// eqPanel fills the same w×(h-1) body the visualizer normally occupies (see
// vizPanel/lyricsPanel), one row per band, scrolled to keep the selected band
// on screen when the panel is shorter than 10 rows.
func eqPanel(w, h int, gains [10]int, sel int) string {
	rows := max(1, h-1)
	n := len(gains)
	visible := min(rows, n)
	start := max(0, min(sel-visible/2, n-visible))

	var b strings.Builder
	for r := 0; r < rows; r++ {
		i := start + r
		if i < n {
			b.WriteString(eqRow(w, gains[i], eqBandLabel(i), i == sel))
		} else {
			b.WriteString(pad("", w))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
