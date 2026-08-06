package main

import (
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := loadVizMode(); got != vizBars {
		t.Fatalf("fresh config dir gave mode %d, want bars", got)
	}
	saveVizMode(vizOrb)
	if got := loadVizMode(); got != vizOrb {
		t.Fatalf("saved orb, loaded %d", got)
	}
	saveVizMode(vizBars)
	if got := loadVizMode(); got != vizBars {
		t.Fatalf("saved bars, loaded %d", got)
	}
}
