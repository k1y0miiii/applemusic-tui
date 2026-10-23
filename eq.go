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

// eqBandLabel is the short per-column label in the EQ panel's body: "32",
// "64", …, "1k", …, "16k".
func eqBandLabel(i int) string {
	f := eqFreqs[i]
	if f >= 1000 {
		return fmt.Sprintf("%dk", f/1000)
	}
	return fmt.Sprintf("%d", f)
}

// eqBandFreqLabel is the long form for the title: "32 HZ", …, "1 KHZ", ….
func eqBandFreqLabel(i int) string {
	f := eqFreqs[i]
	if f >= 1000 {
		return fmt.Sprintf("%d KHZ", f/1000)
	}
	return fmt.Sprintf("%d HZ", f)
}

func eqGainLabel(g int) string {
	switch {
	case g > 0:
		return fmt.Sprintf("+%d DB", g)
	case g < 0:
		return fmt.Sprintf("%d DB", g) // already carries its own minus sign
	default:
		return "0 DB"
	}
}

// eqTitle matches the visualizer's "VISUALIZER · LIVE · COREAUDIO" style.
func eqTitle(gains [10]int, sel int) string {
	return fmt.Sprintf("EQUALIZER · %s · %s %s",
		strings.ToUpper(eqPresetName(gains)), eqBandFreqLabel(sel), eqGainLabel(gains[sel]))
}

// eqHints replaces the transport bar's hint line while the EQ panel is shown.
const eqHints = "←→ band · ↑↓ gain · P preset · 0 flat · esc close "

// eqBarHeight maps a ±12 dB gain onto the same row-height units vizPanel's
// bar logic expects: z is the row the 0 dB line sits on (half the bar area),
// barRows the total bar rows. A cut shrinks toward 0 at -12 dB, a boost grows
// toward barRows at +12 dB.
func eqBarHeight(gain, z, barRows int) float64 {
	if gain <= 0 {
		return float64(z) + float64(gain)/12*float64(z)
	}
	return float64(z) + float64(gain)/12*float64(barRows-z)
}

// centerInWidth pads s to w, centered; s itself is returned unpadded if it
// does not fit — the caller's final pad() to the panel width still clips it.
func centerInWidth(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	left := (w - sw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-sw-left)
}

func eqLabelRow(w, colW, sel int) string {
	// Columns narrower than "125"/"16k" can't hold frequencies without
	// drifting off their bars; the title names the band, so just point at it.
	narrow := colW < 3
	var row strings.Builder
	for i := 0; i < len(eqFreqs); i++ {
		lbl := eqBandLabel(i)
		st := lipgloss.NewStyle().Foreground(fgDim)
		if i == sel {
			st = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
			if len(lbl)+2 <= colW {
				lbl = "[" + lbl + "]"
			}
		}
		if narrow {
			lbl = ""
			if i == sel {
				lbl = "▲"
			}
		}
		row.WriteString(st.Render(centerInWidth(lbl, colW)))
		row.WriteString(" ")
	}
	return pad(" "+row.String(), w)
}

// eqBarRow draws one row of all 10 bands' bars, colored and partial-topped
// the same way vizPanel does (see barCellAt/barZoneColor). isRefLine draws
// the 0 dB dashed guide across any column that has no bar at this row.
func eqBarRow(w, colW int, heights [10]float64, level float64, barRows int, isRefLine bool) string {
	st := lipgloss.NewStyle().Foreground(barZoneColor(level, barRows))
	dashSt := lipgloss.NewStyle().Foreground(fgDim)
	gap := " "
	if isRefLine {
		gap = dashSt.Render("┄")
	}
	var row strings.Builder
	for i := 0; i < len(heights); i++ {
		switch kind, glyph := barCellAt(heights[i], level, 0, false); kind {
		case barCellFull:
			row.WriteString(st.Render(strings.Repeat("█", colW)))
		case barCellPartial:
			row.WriteString(st.Render(strings.Repeat(string(glyph), colW)))
		default:
			if isRefLine {
				row.WriteString(dashSt.Render(strings.Repeat("┄", colW)))
			} else {
				row.WriteString(strings.Repeat(" ", colW))
			}
		}
		row.WriteString(gap)
	}
	return pad(" "+row.String(), w)
}

// eqPanel fills the same w×(h-1) body the visualizer normally occupies: a
// grid of 10 bars, one per band, growing from a half-height 0 dB line, with a
// label row (selected band bracketed) along the bottom.
func eqPanel(w, h int, gains [10]int, sel int) string {
	totalRows := max(1, h-1)
	colW := max(2, (w-1)/10-1)
	if totalRows == 1 {
		return eqLabelRow(w, colW, sel)
	}

	barRows := totalRows - 1
	z := barRows / 2
	refLine := barRows - z - 1
	var heights [10]float64
	for i, g := range gains {
		heights[i] = eqBarHeight(g, z, barRows)
	}

	var b strings.Builder
	for r := 0; r < barRows; r++ {
		level := float64(barRows - r)
		b.WriteString(eqBarRow(w, colW, heights, level, barRows, r == refLine))
		b.WriteString("\n")
	}
	b.WriteString(eqLabelRow(w, colW, sel))
	return b.String()
}
