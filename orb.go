package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The orb is the donut.c render: sample the torus surface over (theta, phi),
// project it through a z-buffer and shade by the surface normal. What is new
// here is that the tube radius follows the spectrum band sitting at that angle,
// so the ring corrugates with the music instead of only spinning.

const (
	orbRamp  = ".,-~:;=!*#$@"
	orbRing  = 2.0 // hole center to tube center
	orbTube  = 1.0 // tube radius at a silent spectrum
	orbDepth = 5.0 // viewer distance; keeps the whole torus in front of the eye

	// Sample counts, not step sizes: dense enough that the surface never
	// shows gaps at the panel sizes this UI uses.
	orbThetaSamples = 90  // around the tube cross-section
	orbPhiSamples   = 314 // around the ring
)

const (
	vizBars = iota
	vizOrb
)

// orbAdvance moves the two angles on one 1/30s tick. The spin has a steady base
// speed plus a kick on the bass — that kick is what makes it read as turning
// *to* the beat rather than just turning. The wobble tilts the axis, so the
// view drifts between the hole and the edge.
func orbAdvance(spin, wobble, bass float64) (float64, float64) {
	spin += 0.030 + 0.20*bass
	wobble += 0.012 + 0.05*bass
	return math.Mod(spin, 2*math.Pi), math.Mod(wobble, 2*math.Pi)
}

// orbTubeRadii maps the 32 bands onto the ring: each phi sample takes its
// thickness from the band that owns that slice of the circle.
func orbTubeRadii(samples int, bands [32]float64) []float64 {
	radii := make([]float64, samples)
	for i := range radii {
		band := i * len(bands) / samples
		radii[i] = orbTube * (0.55 + 0.85*bands[band])
	}
	return radii
}

func orbPanel(w, h int, spin, wobble float64, bands [32]float64) string {
	if w < 4 || h < 2 {
		return strings.Repeat(" ", max(0, w))
	}

	tilt := 0.85 + 0.55*math.Sin(wobble)
	sinTilt, cosTilt := math.Sincos(tilt)
	sinSpin, cosSpin := math.Sincos(spin)

	// Breathe with the overall loudness on top of the per-band corrugation.
	ring := orbRing * (1 + 0.30*bandsLevel(bands))

	tube := orbTubeRadii(orbPhiSamples, bands)

	// Scale so the widest the torus can get still fits, with a margin. Terminal
	// cells are about twice as tall as wide, hence the 2x on the x axis.
	extent := ring + orbTube*1.4
	scale := math.Min(0.45*float64(h), 0.225*float64(w)) * orbDepth / extent

	cells := make([]byte, w*h)
	shade := make([]int, w*h)
	invZ := make([]float64, w*h)
	for i := range cells {
		cells[i] = ' '
	}

	for ti := 0; ti < orbThetaSamples; ti++ {
		sinTheta, cosTheta := math.Sincos(2 * math.Pi * float64(ti) / orbThetaSamples)
		for pi := 0; pi < orbPhiSamples; pi++ {
			sinPhi, cosPhi := math.Sincos(2 * math.Pi * float64(pi) / orbPhiSamples)

			circleX := ring + tube[pi]*cosTheta
			circleY := tube[pi] * sinTheta

			x := circleX*(cosSpin*cosPhi+sinTilt*sinSpin*sinPhi) - circleY*cosTilt*sinSpin
			y := circleX*(sinSpin*cosPhi-sinTilt*cosSpin*sinPhi) + circleY*cosTilt*cosSpin
			z := orbDepth + cosTilt*circleX*sinPhi + circleY*sinTilt
			if z <= 0 {
				continue
			}
			ooz := 1 / z

			light := cosPhi*cosTheta*sinSpin - cosTilt*cosTheta*sinPhi - sinTilt*sinTheta +
				cosSpin*(cosTilt*sinTheta-cosTheta*sinTilt*sinPhi)
			if light <= 0 {
				continue // facing away from the light, leave it dark
			}

			col := w/2 + int(2*scale*ooz*x)
			row := h/2 - int(scale*ooz*y)
			if col < 0 || col >= w || row < 0 || row >= h {
				continue
			}
			idx := row*w + col
			if ooz <= invZ[idx] {
				continue
			}
			level := min(len(orbRamp)-1, int(light*8))
			invZ[idx] = ooz
			cells[idx] = orbRamp[level]
			shade[idx] = level
		}
	}

	// Same three-step accent ramp the bars use, so both modes read as one family.
	styles := [3]lipgloss.Style{
		lipgloss.NewStyle().Foreground(accentLo),
		lipgloss.NewStyle().Foreground(accent),
		lipgloss.NewStyle().Foreground(accentHi),
	}

	var out strings.Builder
	for row := 0; row < h; row++ {
		var line, run strings.Builder
		cur := -1
		flush := func() {
			if run.Len() == 0 {
				return
			}
			if cur < 0 {
				line.WriteString(run.String())
			} else {
				line.WriteString(styles[cur].Render(run.String()))
			}
			run.Reset()
		}
		for col := 0; col < w; col++ {
			idx := row*w + col
			want := -1
			if cells[idx] != ' ' {
				want = min(2, shade[idx]*3/len(orbRamp))
			}
			if want != cur {
				flush()
				cur = want
			}
			run.WriteByte(cells[idx])
		}
		flush()
		out.WriteString(line.String())
		if row < h-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// The visualizer mode lives in its own one-line file, next to the theme, so
// writing it back never clobbers the comments in config.toml.
func vizModeFile() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "vizmode")
}

func loadVizMode() int {
	p := vizModeFile()
	if p == "" {
		return vizBars
	}
	b, err := os.ReadFile(p)
	if err != nil || strings.TrimSpace(string(b)) != "orb" {
		return vizBars
	}
	return vizOrb
}

// saveVizMode is best-effort: a read-only config dir must not break the UI.
func saveVizMode(mode int) {
	p := vizModeFile()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(vizModeName(mode)+"\n"), 0o644)
}

func vizModeName(mode int) string {
	if mode == vizOrb {
		return "orb"
	}
	return "bars"
}
