package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The app refuses to draw below this, so it is the size everything has to fit.
const (
	minCols = 70
	minRows = 22
)

func TestHelpOverlayFitsTheSmallestTerminal(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	m := model{w: minCols, h: minRows}
	lines := strings.Split(m.helpView(), "\n")
	if len(lines) > minRows {
		t.Errorf("help is %d rows in a %d-row terminal — it would scroll away",
			len(lines), minRows)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > minCols {
			t.Errorf("help row %d is %d cells wide, over the %d-column minimum",
				i, got, minCols)
		}
	}
}

func TestHelpShowsEveryEntryWhole(t *testing.T) {
	// The whole point of the overlay is that nothing gets cut off. pad truncates
	// silently, so a longer entry would quietly lose its tail rather than fail.
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	out := model{w: minCols, h: minRows}.helpView()
	for _, section := range helpSections {
		if !strings.Contains(out, section.title) {
			t.Errorf("section %q missing from the overlay", section.title)
		}
		for _, e := range section.entries {
			if !strings.Contains(out, e.action) {
				t.Errorf("entry %q (%s) is truncated at %d columns", e.action, e.keys, minCols)
			}
		}
	}
}

func TestTransportHintsAreNeverCutInHalf(t *testing.T) {
	// This is the defect the overlay exists for, and measuring the row width
	// will not find it: transportPanel pads every row to the panel, so an
	// overlong hint line comes back the right width with its tail gone. Check
	// the text instead — whichever form is chosen has to appear whole.
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	for _, w := range []int{minCols - 2, 100, 160} {
		for _, focus := range []int{focusQueue, focusRecent, focusPlayer} {
			out := model{w: w + 2, h: minRows, focus: focus}.transportPanel(w)
			full := strings.TrimSpace(transportHints(focus))
			switch {
			case strings.Contains(out, full):
			case strings.Contains(out, strings.TrimSpace(shortHints)):
			default:
				t.Errorf("w=%d focus=%d: neither %q nor %q survived the render:\n%s",
					w, focus, full, strings.TrimSpace(shortHints), out)
			}
		}
	}
}

func TestFitHintsFallsBackRatherThanTruncating(t *testing.T) {
	full := transportHints(focusPlayer)
	if got := fitHints(full, lipgloss.Width(full)); got != full {
		t.Errorf("exact fit was rejected: %q", got)
	}
	if got := fitHints(full, lipgloss.Width(full)-1); got != shortHints {
		t.Errorf("one cell short gave %q, want the short form", got)
	}
	if got := fitHints(full, lipgloss.Width(shortHints)-1); got != "" {
		t.Errorf("no room gave %q, want nothing at all", got)
	}
}

func TestHelpKeyOpensAndAnyKeyCloses(t *testing.T) {
	m := model{phase: phaseReady, w: minCols, h: minRows}
	next, _ := m.updateKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	opened := next.(model)
	if !opened.helpOpen {
		t.Fatal("? did not open the help overlay")
	}

	// Any key closes it, and must not also run its own command — reading the
	// list should never fire the thing you were looking up.
	opened.vizMode = vizBars
	closed, _ := opened.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	after := closed.(model)
	if after.helpOpen {
		t.Error("a key press did not dismiss the help overlay")
	}
	if after.vizMode != vizBars {
		t.Errorf("the dismissing key also cycled the visualizer to %d", after.vizMode)
	}
}
