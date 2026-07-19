package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func demoState() engine.State {
	mk := func(t, a string, sec int) engine.Track {
		return engine.Track{ID: "1", Title: t, Artist: a, Duration: time.Duration(sec) * time.Second}
	}
	return engine.State{
		Playing: true,
		Pos:     95 * time.Second,
		Dur:     337 * time.Second,
		Volume:  65,
		Now:     mk("Instant Crush", "Daft Punk", 337),
		Queue: []engine.Track{
			mk("Instant Crush", "Daft Punk", 337),
			mk("Nightcall", "Kavinsky", 258),
			mk("Out of Time", "The Weeknd", 214),
			mk("Судно", "Молчат Дома", 141),
		},
		QueuePos: 0,
	}
}

func TestLayoutFits(t *testing.T) {
	sizes := [][2]int{{120, 35}, {80, 24}, {200, 50}, {71, 23}}
	visualizers := []struct {
		opening bool
		live    bool
		source  string
	}{
		{opening: true},
		{live: true, source: "COREAUDIO"},
		{live: true, source: "PIPEWIRE"},
		{},
	}
	for _, s := range sizes {
		for _, searchOpen := range []bool{false, true} {
			for _, visualizer := range visualizers {
				m := model{
					w: s[0], h: s[1], phase: phaseReady, st: demoState(),
					t:          1.7,
					searchOpen: searchOpen,
					sQuery:     "daft punk",
					vizOpening: visualizer.opening,
					vizLive:    visualizer.live,
					vizSource:  visualizer.source,
				}
				v := m.View()
				lines := strings.Split(v, "\n")
				maxw := 0
				for _, l := range lines {
					if lw := lipgloss.Width(l); lw > maxw {
						maxw = lw
					}
				}
				if maxw > s[0] || len(lines) > s[1] {
					t.Errorf(
						"term %dx%d search=%v visualizer=%q -> render %dx%d overflows",
						s[0],
						s[1],
						searchOpen,
						m.visualizerTitle(),
						maxw,
						len(lines),
					)
				}
			}
		}
	}
}
