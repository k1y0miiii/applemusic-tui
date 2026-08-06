package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func mouseModel(w, h, tracks int) model {
	q := make([]engine.Track, tracks)
	for i := range q {
		q[i] = engine.Track{
			Title:    fmt.Sprintf("Track%02d", i),
			Artist:   "Artist",
			Duration: 3 * time.Minute,
		}
	}
	return model{
		phase: phaseReady, w: w, h: h,
		st: engine.State{Queue: q, Dur: 4 * time.Minute, Pos: time.Minute},
	}
}

func click(m model, x, y int) model {
	next, _ := m.updateMouse(tea.MouseMsg{
		X: x, Y: y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	return next.(model)
}

func TestClickSelectsTheTrackDrawnOnThatRow(t *testing.T) {
	// The point of the whole layout struct: the hit-test has to agree with what
	// is actually on screen. So find each title in the rendered frame and click
	// the row it landed on.
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	m := mouseModel(100, 34, 40)
	rows := strings.Split(m.View(), "\n")

	checked := 0
	for want := 0; want < 40; want++ {
		title := fmt.Sprintf("Track%02d", want)
		for y, row := range rows {
			if !strings.Contains(row, title) {
				continue
			}
			if got := click(m, 5, y); got.selIdx != want {
				t.Errorf("%s is drawn on row %d, but clicking there selected %d",
					title, y, got.selIdx)
			}
			checked++
			break
		}
	}
	if checked < 5 {
		t.Fatalf("only %d titles were visible in the frame, too few to prove anything", checked)
	}
}

// barSpan finds the progress bar in a rendered frame by its glyphs, so the
// geometry is checked against the picture rather than against itself.
func barSpan(t *testing.T, m model) (first, last int) {
	t.Helper()
	rows := strings.Split(m.View(), "\n")
	line := []rune(rows[m.h-2])
	first, last = -1, -1
	for i, r := range line {
		if r == '─' || r == '━' || strings.ContainsRune("▁▂▃▄▅▆▇█", r) {
			// " ▄▄ " prefixes the line with two of the same block glyphs; the
			// bar is the long run, so skip anything in the first few cells.
			if i < 5 {
				continue
			}
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		t.Fatalf("no progress bar found in %q", string(line))
	}
	return first, last
}

func TestProgressGeometryMatchesTheDrawnBar(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	for _, size := range [][2]int{{100, 34}, {70, 22}, {160, 48}} {
		m := mouseModel(size[0], size[1], 5)
		barX, barW := m.progressGeometry(m.w - 2)
		first, last := barSpan(t, m)
		// +1 for the panel border the bar sits inside.
		if first != barX+1 {
			t.Errorf("%dx%d: bar is drawn from column %d, geometry says %d",
				m.w, m.h, first, barX+1)
		}
		if got := last - first + 1; got != barW {
			t.Errorf("%dx%d: bar is %d cells wide, geometry says %d", m.w, m.h, got, barW)
		}
	}
}

func TestClickOnTheProgressBarSeeksThere(t *testing.T) {
	m := mouseModel(100, 34, 5)
	barX, barW := m.progressGeometry(m.w - 2)

	for _, frac := range []float64{0, 0.25, 0.5, 0.9} {
		x := 1 + barX + int(frac*float64(barW))
		got := click(m, x, m.h-2)
		want := time.Duration(frac * float64(m.st.Dur))
		// One cell of the bar is the resolution the user gets.
		if delta := got.st.Pos - want; delta > m.st.Dur/time.Duration(barW) || -delta > m.st.Dur/time.Duration(barW) {
			t.Errorf("click at %.0f%% of the bar seeked to %v, want about %v",
				frac*100, got.st.Pos, want)
		}
	}
}

func TestClickOffTheBarDoesNotSeek(t *testing.T) {
	m := mouseModel(100, 34, 5)
	barX, barW := m.progressGeometry(m.w - 2)
	for _, x := range []int{1, 1 + barX - 1, 1 + barX + barW, m.w - 2} {
		if got := click(m, x, m.h-2); got.st.Pos != m.st.Pos {
			t.Errorf("click at x=%d, outside the bar, moved the position to %v", x, got.st.Pos)
		}
	}
}

func TestWheelMovesTheQueueSelection(t *testing.T) {
	m := mouseModel(100, 34, 40)
	m.selIdx = 10
	lay := m.layout()

	wheel := func(m model, button tea.MouseButton) model {
		next, _ := m.updateMouse(tea.MouseMsg{
			X: 5, Y: lay.queueTop() + 1,
			Button: button, Action: tea.MouseActionPress,
		})
		return next.(model)
	}

	if got := wheel(m, tea.MouseButtonWheelDown); got.selIdx != 11 {
		t.Errorf("wheel down moved the selection to %d, want 11", got.selIdx)
	}
	if got := wheel(m, tea.MouseButtonWheelUp); got.selIdx != 9 {
		t.Errorf("wheel up moved the selection to %d, want 9", got.selIdx)
	}

	// And it must not run off either end.
	m.selIdx = 0
	if got := wheel(m, tea.MouseButtonWheelUp); got.selIdx != 0 {
		t.Errorf("wheel up at the top gave %d, want 0", got.selIdx)
	}
	m.selIdx = 39
	if got := wheel(m, tea.MouseButtonWheelDown); got.selIdx != 39 {
		t.Errorf("wheel down at the bottom gave %d, want 39", got.selIdx)
	}
}

func TestMouseIsIgnoredBehindOverlays(t *testing.T) {
	base := mouseModel(100, 34, 40)
	base.selIdx = 7
	for name, m := range map[string]model{
		"help":    func() model { c := base; c.helpOpen = true; return c }(),
		"search":  func() model { c := base; c.searchOpen = true; return c }(),
		"booting": func() model { c := base; c.phase = 0; return c }(),
	} {
		if got := click(m, 5, m.layout().queueTop()); got.selIdx != 7 {
			t.Errorf("%s: a click reached the queue underneath and selected %d", name, got.selIdx)
		}
	}
}

func TestLayoutMatchesTheRenderedFrame(t *testing.T) {
	// layout() exists so View and the hit-test cannot drift. Check its numbers
	// against a real frame rather than against themselves.
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)

	for _, size := range [][2]int{{100, 34}, {70, 22}, {160, 48}} {
		m := mouseModel(size[0], size[1], 40)
		rows := strings.Split(m.View(), "\n")
		lay := m.layout()

		if len(rows) != m.h {
			t.Errorf("%dx%d: frame is %d rows", m.w, m.h, len(rows))
		}
		// The first queue entry has to be on the row layout says it is.
		if top := lay.queueTop(); top >= len(rows) || !strings.Contains(rows[top], "Track00") {
			t.Errorf("%dx%d: queueTop()=%d does not hold the first track: %q",
				m.w, m.h, top, rows[min(top, len(rows)-1)])
		}
	}
}
