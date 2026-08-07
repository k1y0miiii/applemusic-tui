package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Clicking only works if the hit-test agrees with the renderer down to the
// cell, so the panel arithmetic lives here once and View reads it rather than
// recomputing it. Same for the progress bar: transportPanel takes its width
// from progressGeometry, so a click cannot land where the bar is not drawn.

type layout struct {
	topH   int // rows above the transport
	leftW  int // full width of the queue column, borders included
	rightW int
	qw, qh int // queue panel content size
	recH   int // full height of the recently-played box, 0 when hidden
	vw, vh int // visualizer panel
	lh     int // lyrics panel content height
}

func (m model) layout() layout {
	l := layout{topH: m.h - 4}
	l.leftW = m.w * 42 / 100
	l.rightW = m.w - l.leftW
	l.qw = l.leftW - 2
	if len(m.recent) > 0 {
		l.recH = recentPanelHeight(l.topH)
	}
	l.qh = l.topH - 2 - l.recH
	l.vh = l.topH * 62 / 100
	l.vw = l.rightW - 2
	l.lh = l.topH - l.vh - 2
	return l
}

// queueRows is how many tracks the queue panel shows at once, and queueTop the
// screen row the first of them is drawn on. panel() spends one row on the
// border and one on its title before the body starts.
func (l layout) queueRows() int { return l.qh - 1 }
func (l layout) queueTop() int  { return 2 }

// recentTop is the screen row the cover grid starts on, directly below the
// queue box.
func (l layout) recentTop() int { return l.qh + 2 + 2 }

// progressGeometry returns the column the progress bar starts at within the
// transport's content, and its width. transportPanel lays the second line out
// as " ▄▄ " then the play icon then the elapsed time, which is six cells plus
// the clock before the bar begins.
func (m model) progressGeometry(w int) (x, width int) {
	times := fmtTime(m.st.Pos) + " "
	timee := " " + fmtTime(m.st.Dur)
	shuf, rep := "off", [3]string{"off", "one", "all"}[min(m.st.Repeat, 2)]
	if m.st.Shuffle {
		shuf = "on"
	}
	right := fmt.Sprintf("⇄ %s  ↻ %s  vol ", shuf, rep) +
		strings.Repeat("▮", 6) + fmt.Sprintf(" %d%% ", m.st.Volume)
	barW := max(10, w-lipgloss.Width(times)-lipgloss.Width(timee)-lipgloss.Width(right)-8)
	return 6 + lipgloss.Width(times), barW
}

// recentTileAt maps a column in the cover grid to an entry, mirroring the tile
// sizing in recentPanel. Returns -1 when the click missed a tile, landing in a
// gap or past the last cover.
func (m model) recentTileAt(x, panelW int) int {
	const minGap = 2
	rows := m.layout().recH - 3 // panel body height
	artRows := rows - 2
	artCols := artRows * 2
	if maxCols := (panelW - (recentCols+1)*minGap) / recentCols; artCols > maxCols {
		artCols = maxCols - maxCols%2
		artRows = artCols / 2
	}
	if artCols < 6 || artRows < 2 {
		return -1
	}
	gap := (panelW - recentCols*artCols) / (recentCols + 1)
	start := m.recentSel / recentCols * recentCols
	for c := 0; c < recentCols; c++ {
		left := gap*(c+1) + artCols*c
		if x >= left && x < left+artCols {
			if i := start + c; i < len(m.recent) {
				return i
			}
			return -1
		}
	}
	return -1
}

func (m model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.phase != phaseReady || m.helpOpen || m.acctOpen || m.searchOpen {
		return m, nil
	}
	lay := m.layout()
	inQueue := msg.X < lay.leftW && msg.Y >= lay.queueTop() &&
		msg.Y < lay.queueTop()+lay.queueRows()
	inRecent := lay.recH > 0 && msg.X < lay.leftW &&
		msg.Y >= lay.recentTop() && msg.Y < lay.topH

	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		step := 1
		if msg.Button == tea.MouseButtonWheelUp {
			step = -1
		}
		switch {
		case inQueue:
			m.focus = focusQueue
			m.selIdx = max(0, min(m.selIdx+step, len(m.st.Queue)-1))
		case inRecent:
			m.focus = focusRecent
			m.recentSel = max(0, min(m.recentSel+step*recentCols, len(m.recent)-1))
		}
		return m, nil

	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.click(msg, lay, inQueue, inRecent)
	}
	return m, nil
}

func (m model) click(msg tea.MouseMsg, lay layout, inQueue, inRecent bool) (tea.Model, tea.Cmd) {
	eng := m.eng
	switch {
	case inQueue && len(m.st.Queue) > 0:
		// Same windowing queuePanel uses, so the row under the pointer is the
		// track that was drawn there.
		rows := lay.queueRows()
		start := max(0, min(m.selIdx-rows/2, len(m.st.Queue)-rows))
		i := start + msg.Y - lay.queueTop()
		if i < 0 || i >= len(m.st.Queue) {
			return m, nil
		}
		m.focus, m.selIdx = focusQueue, i
		if m.audioInitializing() {
			m.note, m.noteAt = audioInitializingText, m.t
			return m, nil
		}
		m.loading = m.st.Queue[i].Title
		m.loadSnap, m.loadStart, m.loadHideQueue = snap(m.st), m.t, false
		return m, doCmd(func() error { return eng.JumpTo(i) })

	case inRecent:
		i := m.recentTileAt(msg.X-1, lay.qw)
		if i < 0 {
			return m, nil
		}
		m.focus, m.recentSel = focusRecent, i
		if m.audioInitializing() {
			m.note, m.noteAt = audioInitializingText, m.t
			return m, nil
		}
		tr := m.recent[i]
		m.loading, m.loadSnap, m.loadStart = tr.Title, snap(m.st), m.t
		m.loadHideQueue = true
		m.beginAudioInitialization()
		kind, id := tr.Kind, tr.ID
		return m, doCmd(func() error { return eng.Play(kind, id) })

	case msg.Y == m.h-2 && m.st.Dur > 0:
		// The transport's second line. Seeking is the one thing a pointer does
		// better than a keyboard, so it is worth the geometry.
		barX, barW := m.progressGeometry(m.w - 2)
		x := msg.X - 1 - barX
		if x < 0 || x >= barW {
			return m, nil
		}
		m.focus = focusPlayer
		m.st.Pos = time.Duration(float64(m.st.Dur) * float64(x) / float64(barW))
		m.wv.reset() // the recorded waveform no longer matches the new position
		pos := m.st.Pos
		return m, doCmd(func() error { return eng.SeekTo(pos) })
	}
	return m, nil
}
