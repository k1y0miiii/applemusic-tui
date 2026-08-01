package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func demoRecent(n int) []engine.Track {
	out := make([]engine.Track, n)
	for i := range out {
		out[i] = engine.Track{
			ID:     string(rune('a' + i)),
			Kind:   "album",
			Title:  "Album With A Fairly Long Name",
			Artist: "Some Artist",
			Art:    "https://example.invalid/{w}x{h}.jpg",
		}
	}
	return out
}

func recentModel() model {
	return model{
		w: 120, h: 35, phase: phaseReady, st: demoState(),
		recent: demoRecent(7),
	}
}

// Tab must walk queue → recent → player and wrap back.
func TestTabCyclesThreeZones(t *testing.T) {
	m := recentModel()
	want := []int{focusRecent, focusPlayer, focusQueue}
	for i, w := range want {
		next, _ := m.updateKeys(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(model)
		if m.focus != w {
			t.Fatalf("tab #%d put focus on %d, want %d", i+1, m.focus, w)
		}
	}
}

// With no recently-played entries, or in a terminal too short to draw the grid,
// Tab has to skip the zone rather than focus something invisible.
func TestTabSkipsHiddenRecentGrid(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    model
	}{
		{"no entries", model{w: 120, h: 35, phase: phaseReady, st: demoState()}},
		{"terminal too short", model{w: 120, h: 22, phase: phaseReady, st: demoState(), recent: demoRecent(4)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.m.recentVisible() {
				t.Fatal("grid should not be visible in this case")
			}
			next, _ := tc.m.updateKeys(tea.KeyMsg{Type: tea.KeyTab})
			if got := next.(model).focus; got != focusPlayer {
				t.Fatalf("tab from queue landed on %d, want focusPlayer (%d)", got, focusPlayer)
			}
		})
	}
}

// ←/→ step one tile, ↑/↓ jump a whole row, and both clamp at the ends.
func TestRecentGridNavigationClamps(t *testing.T) {
	m := recentModel()
	m.focus = focusRecent

	step := func(k tea.KeyType) {
		next, _ := m.updateKeys(tea.KeyMsg{Type: k})
		m = next.(model)
	}

	step(tea.KeyLeft)
	if m.recentSel != 0 {
		t.Fatalf("left at the start moved to %d, want 0", m.recentSel)
	}
	step(tea.KeyRight)
	if m.recentSel != 1 {
		t.Fatalf("right gave %d, want 1", m.recentSel)
	}
	step(tea.KeyDown)
	if m.recentSel != 1+recentCols {
		t.Fatalf("down gave %d, want %d", m.recentSel, 1+recentCols)
	}
	step(tea.KeyUp)
	if m.recentSel != 1 {
		t.Fatalf("up gave %d, want 1", m.recentSel)
	}
	for i := 0; i < 20; i++ {
		step(tea.KeyDown)
	}
	if m.recentSel != len(m.recent)-1 {
		t.Fatalf("down past the end gave %d, want %d", m.recentSel, len(m.recent)-1)
	}
}

// ↑/↓ must still drive volume when the player is focused, not the grid.
func TestRecentGridDoesNotStealPlayerKeys(t *testing.T) {
	m := recentModel()
	m.focus = focusPlayer
	m.eng = &engine.Engine{}
	m.st.Volume = 50

	next, _ := m.updateKeys(tea.KeyMsg{Type: tea.KeyUp})
	if got := next.(model); got.st.Volume != 55 || got.recentSel != 0 {
		t.Fatalf("up with player focused: volume=%d sel=%d, want 55 and 0",
			got.st.Volume, got.recentSel)
	}
}

// A catalog search returns no Recent; it must not blank the grid.
func TestCatalogSearchKeepsRecentGrid(t *testing.T) {
	m := recentModel()
	next, _ := m.Update(searchMsg{res: engine.SearchResults{
		Songs: []engine.Track{{ID: "x", Title: "Something"}},
	}})
	if got := len(next.(model).recent); got != len(m.recent) {
		t.Fatalf("catalog search left %d recent entries, want %d", got, len(m.recent))
	}
}

// The grid renders exactly the height it is given and never overruns its width.
func TestRecentPanelFitsItsBox(t *testing.T) {
	for _, w := range []int{28, 40, 56} {
		for _, h := range []int{7, 10, 12} {
			m := recentModel()
			m.focus = focusRecent
			m.recentSel = 3
			body := m.recentPanel(w, h)
			lines := strings.Split(body, "\n")
			if len(lines) != h-1 {
				t.Errorf("recentPanel(%d,%d) drew %d lines, want %d", w, h, len(lines), h-1)
			}
			for i, l := range lines {
				if lw := lipgloss.Width(l); lw != w {
					t.Errorf("recentPanel(%d,%d) line %d is %d wide, want %d", w, h, i, lw, w)
				}
			}
		}
	}
}

// Enter on a tile replaces the queue with that album and starts loading it.
func TestEnterOnTilePlaysTheAlbum(t *testing.T) {
	m := recentModel()
	m.focus = focusRecent
	m.recentSel = 2
	m.eng = &engine.Engine{}

	next, cmd := m.updateKeys(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if cmd == nil {
		t.Fatal("enter on a tile returned no command")
	}
	if got.loading != m.recent[2].Title {
		t.Fatalf("loading = %q, want %q", got.loading, m.recent[2].Title)
	}
	if !got.loadHideQueue {
		t.Error("playing an album replaces the queue, so it should be hidden while loading")
	}
}
