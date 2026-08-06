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
	vizTorus
	vizSphere
	vizModes
)

const (
	kickMemory  = 0.03 // how fast the baseline forgets a beat, per 1/30s tick
	kickCatchUp = 0.25 // how fast it follows a change of level instead
	kickJump    = 0.25 // a rise bigger than this is a level change, not a beat
	kickGain    = 9.0  // overshoot needed for a full-strength kick
	kickFloor   = 0.02 // ripple below this is not a beat, just the mix breathing
	kickDecay   = 0.85 // how fast a kick falls away between beats
)

// bassKick turns the bass level into a beat. Driving anything from the level
// itself does not work: the bands are normalized over a 63 dB window, so a kick
// riding 6 dB above the sustained bass moves the number by a tenth, and across a
// whole track the low end sits high and nearly flat. What reads as a beat is the
// overshoot above the running average, which rests at zero and spikes on a hit.
//
// The baseline has to be slow, or it swallows the very beat it is there to
// measure — a hit lasts about eight frames once the analyzer's release and the
// UI smoothing have had their say, so the average must remember far longer than
// that. But a slow average also needs three seconds to climb from silence when
// a track starts, and until it arrives every frame reads as one huge beat. So a
// big step is treated as what it is, a change of level rather than a beat, and
// followed quickly; the same goes for the bass falling away.
//
// Returns the updated baseline and kick. Instant to rise, quick to fall.
func bassKick(baseline, kick, bass float64) (float64, float64) {
	gap := bass - baseline
	memory := kickMemory
	if gap > kickJump || gap < 0 {
		memory = kickCatchUp
	}
	baseline += gap * memory

	kick *= kickDecay
	if over := kickGain * (bass - baseline - kickFloor); over > kick {
		kick = math.Min(1, over)
	}
	return baseline, math.Max(0, kick)
}

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

	return paintCells(cells, shade, w, h)
}

// paintCells colors a rendered grid and joins it into panel rows. Shade is an
// index into orbRamp; it picks the accent step, so brighter parts of the shape
// come forward. Same three-step ramp the bars use, so every mode reads as one
// family and the theme key keeps working untouched.
func paintCells(cells []byte, shade []int, w, h int) string {
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

// The sphere is the HUD-globe look: a wireframe cage of latitude rings and
// meridians, drawn as continuous lines and shaded by depth so the near side
// glows and the far side hangs behind it as a ghost. Nothing is culled — seeing
// the back of the cage through the front is what makes it read as a shell
// rather than a disc.
//
// Lines rather than a cloud of points: a cloud puts near and far samples in
// neighbouring cells, and the bright/dim speckle that produces reads as noise
// at terminal resolution. Continuous lines keep each depth in one run of cells.
const (
	sphereLats     = 5    // latitude rings, poles excluded
	sphereMeridian = 10   // meridians
	sphereSamples  = 400  // points traced along every ring and meridian
	sphereDepth    = 4.0  // viewer distance
	sphereBulge    = 0.30 // per-band corrugation
	sphereSwell    = 0.55 // how far the bass pumps the whole globe
	sphereSpin     = 0.35 // the globe turns slower than the torus

	// The projection is sized from the largest the globe can ever be, not from
	// its size right now. Refitting it every frame divides the swell straight
	// back out and the pump becomes invisible.
	sphereMaxExtent = (1 + sphereSwell) * (1 + sphereBulge)
)

// sphereRadius deforms the cage by the spectrum. Latitude maps to frequency
// symmetrically — bass at the equator, treble at the poles — so a kick swells
// the waist instead of tipping the shape to one side.
func sphereRadius(sinLat float64, bands [32]float64) float64 {
	band := min(len(bands)-1, int(math.Abs(sinLat)*float64(len(bands))))
	return 1 + sphereBulge*bands[band]
}

// sphereShade maps a point's depth to a light level, nearest brightest. Squared
// so the far half collapses into the bottom of the ramp: under a linear falloff
// the back of the cage stays nearly as bright as the front and the two read as
// one solid crust instead of a shell you can see into.
func sphereShade(z, extent float64) int {
	near := (extent - z) / (2 * extent)
	return max(0, min(len(orbRamp)-1, int(near*near*float64(len(orbRamp)))))
}

func spherePanel(w, h int, spin, wobble, kick float64, bands [32]float64) string {
	if w < 4 || h < 2 {
		return strings.Repeat(" ", max(0, w))
	}

	tilt := 0.30 * math.Sin(wobble) // a lean, not a tumble: the poles stay poles
	sinTilt, cosTilt := math.Sincos(tilt)
	sinSpin, cosSpin := math.Sincos(spin * sphereSpin)

	// The beat pumps the globe rather than turning it: this one is the pulse.
	breathe := 1 + sphereSwell*max(0, min(1, kick))
	// Shade against the loudest band actually present, not the theoretical
	// maximum: otherwise quiet passages only ever use the bottom of the light
	// ramp and the near face never lights up.
	loudest := 0.0
	for _, v := range bands {
		loudest = math.Max(loudest, v)
	}
	extent := breathe * (1 + sphereBulge*loudest)
	scale := math.Min(0.45*float64(h), 0.225*float64(w)) * sphereDepth / sphereMaxExtent

	cells := make([]byte, w*h)
	shade := make([]int, w*h)
	invZ := make([]float64, w*h)
	for i := range cells {
		cells[i] = ' '
	}

	plot := func(lat, lon float64) {
		sinLat, cosLat := math.Sincos(lat)
		sinLon, cosLon := math.Sincos(lon)
		r := breathe * sphereRadius(sinLat, bands)

		x, y, z := r*cosLat*cosLon, r*sinLat, r*cosLat*sinLon
		// Spin about the polar axis, then lean the axis toward the viewer.
		x, z = x*cosSpin+z*sinSpin, z*cosSpin-x*sinSpin
		y, z = y*cosTilt-z*sinTilt, y*sinTilt+z*cosTilt

		depth := sphereDepth + z
		if depth <= 0 {
			return
		}
		ooz := 1 / depth
		col := w/2 + int(2*scale*ooz*x)
		row := h/2 - int(scale*ooz*y)
		if col < 0 || col >= w || row < 0 || row >= h {
			return
		}
		idx := row*w + col
		if ooz <= invZ[idx] {
			return // something nearer already owns this cell
		}
		invZ[idx] = ooz
		shade[idx] = sphereShade(z, extent)
		cells[idx] = orbRamp[shade[idx]]
	}

	for ring := 1; ring <= sphereLats; ring++ {
		lat := -math.Pi/2 + math.Pi*float64(ring)/(sphereLats+1)
		for i := 0; i < sphereSamples; i++ {
			plot(lat, 2*math.Pi*float64(i)/sphereSamples)
		}
	}
	for meridian := 0; meridian < sphereMeridian; meridian++ {
		lon := 2 * math.Pi * float64(meridian) / sphereMeridian
		for i := 0; i < sphereSamples; i++ {
			plot(-math.Pi/2+math.Pi*float64(i)/(sphereSamples-1), lon)
		}
	}

	return paintCells(cells, shade, w, h)
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

var vizModeNames = [vizModes]string{"bars", "torus", "sphere"}

func loadVizMode() int {
	p := vizModeFile()
	if p == "" {
		return vizBars
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return vizBars
	}
	name := strings.TrimSpace(string(b))
	if name == "orb" {
		name = "torus" // the torus shipped alone, under the generic name
	}
	for mode, known := range vizModeNames {
		if known == name {
			return mode
		}
	}
	return vizBars
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
	if mode < 0 || mode >= vizModes {
		return vizModeNames[vizBars]
	}
	return vizModeNames[mode]
}
