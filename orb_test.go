package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func fullBands(v float64) [32]float64 {
	var b [32]float64
	for i := range b {
		b[i] = v
	}
	return b
}

func TestOrbPanelFillsExactlyTheGivenBox(t *testing.T) {
	for _, size := range [][2]int{{60, 18}, {24, 8}, {120, 40}} {
		w, h := size[0], size[1]
		lines := strings.Split(orbPanel(w, h, 1.1, 0.4, fullBands(0.5)), "\n")
		if len(lines) != h {
			t.Fatalf("orbPanel(%d,%d) drew %d rows, want %d", w, h, len(lines), h)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("orbPanel(%d,%d) row %d is %d wide, want %d", w, h, i, got, w)
			}
		}
	}
}

func TestOrbPanelDrawsAShadedTorusWithAHole(t *testing.T) {
	// A torus seen off-axis is lit on part of its surface and open in the
	// middle. A blank panel means the projection missed the box; a solid block
	// means the z-buffer or the lighting term stopped culling anything.
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii) // no ANSI, so the runes are countable

	w, h := 60, 20
	rows := strings.Split(orbPanel(w, h, 0.9, 0.2, fullBands(0.3)), "\n")

	lit := 0
	holes := 0
	for _, row := range rows {
		lit += w - strings.Count(row, " ")
		// A gap bounded by lit cells on both sides: the hole seen edge-on.
		if strings.Contains(strings.TrimSpace(row), "  ") {
			holes++
		}
	}
	if lit < 100 {
		t.Fatalf("orb drew %d lit cells in a %dx%d box, want a visible torus", lit, w, h)
	}
	if lit > w*h/2 {
		t.Fatalf("orb drew %d of %d cells — nothing is being culled", lit, w*h)
	}
	if holes == 0 {
		t.Error("no row has an enclosed gap — the torus lost its hole")
	}
	if !strings.ContainsRune(strings.Join(rows, ""), rune(orbRamp[len(orbRamp)-1])) {
		t.Error("orb has no highlight — the light ramp never reaches its top")
	}
}

func TestOrbTubeRadiiFollowTheSpectrum(t *testing.T) {
	var bands [32]float64
	bands[0] = 1 // only the bass band is loud
	radii := orbTubeRadii(320, bands)
	if radii[0] <= radii[160] {
		t.Fatalf("loud band gave radius %.3f, silent band %.3f — tube ignores the spectrum",
			radii[0], radii[160])
	}
	for i, r := range radii {
		if r <= 0 {
			t.Fatalf("radii[%d] = %.3f, want positive", i, r)
		}
	}
}

func TestOrbSpinsFasterOnBass(t *testing.T) {
	quiet, _ := orbAdvance(0, 0, 0)
	loud, _ := orbAdvance(0, 0, 1)
	if loud <= quiet {
		t.Fatalf("bass kick did not speed the spin up: %.4f vs %.4f", loud, quiet)
	}
}

func TestOrbAnglesStayBoundedOverALongRun(t *testing.T) {
	spin, wobble := 0.0, 0.0
	for i := 0; i < 100_000; i++ {
		spin, wobble = orbAdvance(spin, wobble, 1)
	}
	if spin < 0 || spin > 7 || wobble < 0 || wobble > 7 {
		t.Fatalf("angles drifted out of one turn: spin=%.3f wobble=%.3f", spin, wobble)
	}
}

func TestVizModeRoundTrips(t *testing.T) {
	t.Setenv("AMTUI_CONFIG_DIR", t.TempDir())
	if got := loadVizMode(); got != vizBars {
		t.Fatalf("fresh config dir gave mode %d, want bars", got)
	}
	saveVizMode(vizTorus)
	if got := loadVizMode(); got != vizTorus {
		t.Fatalf("saved orb, loaded %d", got)
	}
	saveVizMode(vizBars)
	if got := loadVizMode(); got != vizBars {
		t.Fatalf("saved bars, loaded %d", got)
	}
}

func TestSpherePanelFillsExactlyTheGivenBox(t *testing.T) {
	for _, size := range [][2]int{{60, 18}, {24, 8}, {120, 40}} {
		w, h := size[0], size[1]
		lines := strings.Split(spherePanel(w, h, 0.8, 0.6, fullBands(0.5)), "\n")
		if len(lines) != h {
			t.Fatalf("spherePanel(%d,%d) drew %d rows, want %d", w, h, len(lines), h)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("spherePanel(%d,%d) row %d is %d wide, want %d", w, h, i, got, w)
			}
		}
	}
}

func TestSphereShadeIsBrightestNearest(t *testing.T) {
	// Tested on the mapping rather than on the rendered panel: both faces of the
	// cage project onto the same disc, so no region of the image tells the two
	// apart on its own — the visible gradient is a mix of near lines and far
	// ones showing through the gaps between them.
	const extent = 1.4
	if got, want := sphereShade(-extent, extent), len(orbRamp)-1; got != want {
		t.Errorf("nearest point shaded %d, want the top of the ramp %d", got, want)
	}
	if got := sphereShade(extent, extent); got != 0 {
		t.Errorf("farthest point shaded %d, want the bottom of the ramp 0", got)
	}
	prev := len(orbRamp)
	for step := 0; step <= 40; step++ {
		z := -extent + 2*extent*float64(step)/40
		got := sphereShade(z, extent)
		if got > prev {
			t.Fatalf("shade rose to %d at z=%.3f while moving away — ramp is inverted", got, z)
		}
		if got < 0 || got >= len(orbRamp) {
			t.Fatalf("shade %d at z=%.3f is outside the ramp", got, z)
		}
		prev = got
	}
	// The far half has to stay dim, or the back of the cage competes with the
	// front and the shell reads as a solid crust.
	if got := sphereShade(0, extent); got > len(orbRamp)/3 {
		t.Errorf("the silhouette shades to %d, too bright for a see-through cage", got)
	}
}

func TestSphereStaysSeeThrough(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	w, h := 60, 18
	out := spherePanel(w, h, 0.8, 0.6, fullBands(0.4))
	lit := 0
	for _, row := range strings.Split(out, "\n") {
		lit += w - strings.Count(row, " ")
	}
	if lit < 100 {
		t.Fatalf("sphere drew %d lit cells in a %dx%d box, want a visible cage", lit, w, h)
	}
	if lit > w*h/2 {
		t.Fatalf("sphere drew %d of %d cells — the cage filled in solid", lit, w*h)
	}
}

func TestSphereRadiusSwellsAtTheEquatorOnBass(t *testing.T) {
	var bass [32]float64
	bass[0] = 1 // only the lowest band is loud
	if equator, pole := sphereRadius(0, bass), sphereRadius(1, bass); equator <= pole {
		t.Fatalf("bass gave equator %.3f and pole %.3f, want the waist to swell", equator, pole)
	}
	for _, sinLat := range []float64{-1, -0.5, 0, 0.5, 1} {
		if r := sphereRadius(sinLat, fullBands(1)); r <= 0 {
			t.Fatalf("sphereRadius(%.1f) = %.3f, want positive", sinLat, r)
		}
	}
	// Symmetric in latitude: north and south must deform alike, or the globe
	// tips instead of pulsing.
	for _, sinLat := range []float64{0.25, 0.5, 0.9} {
		if a, b := sphereRadius(sinLat, bass), sphereRadius(-sinLat, bass); a != b {
			t.Errorf("sphereRadius(±%.2f) = %.4f vs %.4f, want symmetry", sinLat, a, b)
		}
	}
}

func TestVizModesCycleThroughEveryShape(t *testing.T) {
	seen := map[string]bool{}
	for mode := 0; mode < vizModes; mode++ {
		name := vizModeName(mode)
		if name == "" || seen[name] {
			t.Fatalf("mode %d has a missing or duplicate name %q", mode, name)
		}
		seen[name] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %d modes, want bars, torus and sphere", len(seen))
	}
}

func TestVizModeUpgradesTheOldOrbName(t *testing.T) {
	// The torus shipped alone under the name "orb"; anyone who pressed v before
	// the sphere existed has that written to disk.
	dir := t.TempDir()
	t.Setenv("AMTUI_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "vizmode"), []byte("orb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadVizMode(); got != vizTorus {
		t.Fatalf("old \"orb\" file loaded as mode %d, want torus", got)
	}
}
