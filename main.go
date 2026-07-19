package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/k1y0miiii/applemusic-tui/engine"
	"github.com/k1y0miiii/applemusic-tui/lyrics"
	"github.com/k1y0miiii/applemusic-tui/visualizer"
)

const (
	accent                    = lipgloss.Color("#FA233B") // Apple Music red
	accentHi                  = lipgloss.Color("#FB5C74") // Apple Music pink
	accentLo                  = lipgloss.Color("#8A1E30")
	fgBright                  = lipgloss.Color("#F2F2F7")
	fgDim                     = lipgloss.Color("#8E8E93")
	fgFaint                   = lipgloss.Color("#5A5A5E")
	borderDim                 = lipgloss.Color("#3A3A3C")
	selBg                     = lipgloss.Color("#2C2C2E")
	audioInitializingText     = "initializing Apple Music audio…"
	audioInitializingWarning  = "Apple Music is still initializing · R reload"
	visualizerUnavailableNote = "visualizer unavailable · simulated"
	visualizerFrameFreshness  = 400 * time.Millisecond
)

type phase int

const (
	phaseBoot phase = iota
	phaseReady
	phaseFail
)

type (
	tickMsg   time.Time
	pollMsg   struct{}
	statusMsg string
	noteMsg   string
	readyMsg  struct{ eng *engine.Engine }
	failMsg   struct{ err error }
	stateMsg  struct {
		st  engine.State
		err error
	}
	searchMsg struct {
		res       engine.SearchResults
		err       error
		keepInput bool // library load keeps the input focused
	}
	lyricsMsg struct {
		id string // track the lyrics belong to
		ly lyrics.Lyrics
	}
	visualizerOpenedMsg struct {
		service *visualizer.Service
		err     error
		source  string
	}
)

func tick() tea.Cmd {
	return tea.Tick(time.Second/30, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type model struct {
	w, h   int
	t      float64 // seconds since start, drives animation
	phase  phase
	status string
	note   string
	noteAt float64 // m.t when the note was set; expires after 6s
	eng    *engine.Engine
	st     engine.State
	focus  int // 0 queue, 1 player
	selIdx int

	statusCh chan string

	// queue-replacement in flight (slow setQueue on playlists/albums)
	loading         string  // title being loaded, "" = none
	loadSnap        string  // state snapshot at fire time; change = done
	loadStart       float64 // m.t at fire time, for the give-up timeout
	loadHideQueue   bool    // true when the queue itself is being replaced
	initStart       float64 // m.t when the DRM audio initialization began
	initFailed      bool    // a MusicKit error ends this initialization attempt
	initPending     bool    // a local play attempt awaits engine confirmation
	initSeen        bool    // a poll observed Initializing during the pending attempt
	initReadyPolls  int     // consecutive same-snapshot Playing polls while pending
	initTimerActive bool    // initStart is valid and the 30s warning timer is running

	// lyrics for the current track
	ly     lyrics.Lyrics
	lyFor  string // track ID the fetch was fired for
	lyBusy bool

	// real spectrum; falls back to fake animation
	vizService  *visualizer.Service
	vizOpening  bool
	vizLive     bool
	vizSource   string
	vizTerminal bool
	vizBands    [32]float64 // smoothed, 0..1
	vizTargets  [32]float64

	// search overlay
	searchOpen bool
	sInput     bool
	sQuery     string
	sTab, sSel int
	sBusy      bool
	sRes       engine.SearchResults
}

func (m model) visualizerTitle() string {
	if m.vizLive {
		return "VISUALIZER · LIVE · " + m.vizSource
	}
	if m.vizOpening {
		return "VISUALIZER · STARTING"
	}
	return "VISUALIZER · SIMULATED"
}

func (m *model) failVisualizer() {
	m.vizService = nil
	m.vizOpening = false
	m.vizLive = false
	m.vizSource = ""
	if !m.vizTerminal {
		m.vizTerminal = true
		m.note = visualizerUnavailableNote
		m.noteAt = m.t
	}
}

func (m *model) closeVisualizerAsync() {
	service := m.vizService
	m.vizService = nil
	m.vizOpening = false
	m.vizLive = false
	m.vizSource = ""
	closeServiceAsync(service)
}

func closeServiceAsync(service *visualizer.Service) {
	if service != nil {
		go func() {
			_ = service.Close()
		}()
	}
}

func (m model) audioInitializing() bool {
	return (m.st.Initializing || m.initPending) && !m.initFailed
}

func (m *model) beginAudioInitialization() {
	m.initPending = true
	m.initSeen = false
	m.initReadyPolls = 0
	m.initFailed = false
	m.initStart = m.t
	m.initTimerActive = true
}

func (m *model) resetAudioInitialization() {
	m.initPending = false
	m.initSeen = false
	m.initReadyPolls = 0
	m.initFailed = false
	m.initStart = 0
	m.initTimerActive = false
}

func (m *model) failAudioInitialization() {
	m.initPending = false
	m.initSeen = false
	m.initReadyPolls = 0
	m.initFailed = true
	m.initStart = 0
	m.initTimerActive = false
}

func (m model) audioInitializingLine() string {
	sp := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	return lipgloss.NewStyle().Foreground(accentHi).Render(" "+string(sp[int(m.t*12)%len(sp)])+" ") +
		lipgloss.NewStyle().Foreground(fgDim).Render(audioInitializingText)
}

func connectCmd(ch chan string) tea.Cmd {
	return func() tea.Msg {
		eng, err := engine.Connect(func(s string) { ch <- s })
		if err != nil {
			return failMsg{err}
		}
		return readyMsg{eng}
	}
}

func listenStatus(ch chan string) tea.Cmd {
	return func() tea.Msg { return statusMsg(<-ch) }
}

func (m model) fetchState() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		st, err := eng.State()
		return stateMsg{st, err}
	}
}

func openVisualizerCmd(openSource func() (visualizer.Source, error)) tea.Cmd {
	return func() tea.Msg {
		source, err := openSource()
		if err != nil {
			return visualizerOpenedMsg{err: err}
		}
		sourceName := ""
		if source != nil {
			sourceName = source.Name()
		}
		service, err := visualizer.NewService(source)
		if err != nil {
			if source != nil {
				_ = source.Close()
			}
			return visualizerOpenedMsg{err: err}
		}
		return visualizerOpenedMsg{service: service, source: sourceName}
	}
}

func closeVisualizerCmd(service *visualizer.Service) tea.Cmd {
	return func() tea.Msg {
		_ = service.Close()
		return nil
	}
}

func visualizerFrameStale(now, frameAt time.Time) bool {
	return !now.IsZero() && !frameAt.IsZero() &&
		now.Sub(frameAt) > visualizerFrameFreshness
}

func snap(st engine.State) string {
	return fmt.Sprintf("%s|%d|%d", st.Now.ID, len(st.Queue), st.QueuePos)
}

func doCmd(f func() error) tea.Cmd {
	return func() tea.Msg {
		if err := f(); err != nil {
			return noteMsg(err.Error())
		}
		return nil
	}
}

func fetchLyricsCmd(id, artist, title string, dur time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ly, err := lyrics.Fetch(ctx, artist, title, dur)
		if err != nil {
			ly = lyrics.Lyrics{} // network miss = just "no lyrics", not an error banner
		}
		return lyricsMsg{id: id, ly: ly}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(), connectCmd(m.statusCh), listenStatus(m.statusCh))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		var visualizerClose tea.Cmd
		now := time.Time(msg)
		m.t += 1.0 / 30
		if m.phase == phaseReady && m.st.Playing && m.st.Pos < m.st.Dur {
			m.st.Pos += time.Second / 30 // optimistic between polls
		}
		if m.loading != "" && m.t-m.loadStart > 12 && !m.audioInitializing() {
			m.loading = "" // give up quietly
		}
		if m.audioInitializing() && m.initTimerActive &&
			m.t-m.initStart > 30 && m.note != audioInitializingWarning {
			m.note, m.noteAt = audioInitializingWarning, m.t
		}
		if m.note != "" && m.t-m.noteAt > 6 &&
			!(m.audioInitializing() && m.note == audioInitializingWarning) {
			m.note = ""
		}
		if m.vizService != nil {
			service := m.vizService
			if service.Err() != nil {
				m.failVisualizer()
				visualizerClose = closeVisualizerCmd(service)
			} else if frame, ok := service.Latest(); ok {
				if visualizerFrameStale(now, frame.At) {
					m.vizTargets = [32]float64{}
				} else {
					m.vizTargets = frame.Bands
					m.vizLive = frame.Live
					m.vizSource = frame.Source
					m.vizOpening = false
				}
			}
		}
		for i := range m.vizBands {
			m.vizBands[i] += (m.vizTargets[i] - m.vizBands[i]) * 0.45
		}
		if visualizerClose != nil {
			return m, tea.Batch(tick(), visualizerClose)
		}
		return m, tick()
	case statusMsg:
		m.status = string(msg)
		return m, listenStatus(m.statusCh)
	case readyMsg:
		m.phase, m.eng = phaseReady, msg.eng
		m.vizOpening = true
		return m, tea.Batch(
			m.fetchState(),
			openVisualizerCmd(visualizer.OpenSystemSource),
		)
	case visualizerOpenedMsg:
		if m.phase != phaseReady || !m.vizOpening {
			if msg.service != nil {
				return m, closeVisualizerCmd(msg.service)
			}
			return m, nil
		}
		if msg.err != nil || msg.service == nil {
			m.failVisualizer()
			return m, nil
		}
		m.vizService = msg.service
		m.vizOpening = false
		m.vizLive = true
		m.vizSource = msg.source
	case failMsg:
		m.phase, m.status = phaseFail, msg.err.Error()
	case pollMsg:
		return m, m.fetchState()
	case stateMsg:
		var cmd tea.Cmd
		if msg.err == nil {
			wasInitializing := m.st.Initializing
			newSt := msg.st
			// keep the optimistic clock for small drift: the polled position
			// lags by up to the poll interval and would flick lyrics back
			if newSt.Now.ID == m.st.Now.ID {
				if d := newSt.Pos - m.st.Pos; d > -1500*time.Millisecond && d < 1500*time.Millisecond {
					newSt.Pos = m.st.Pos
				}
			}
			m.st = newSt
			nowInitializing := m.st.Initializing
			if m.initPending && nowInitializing {
				m.initSeen = true
				m.initReadyPolls = 0
			}
			if nowInitializing && !m.initTimerActive {
				m.initStart = m.t
				m.initTimerActive = true
			}
			if m.initPending && !nowInitializing {
				complete := false
				if m.st.Playing {
					complete = m.initSeen || snap(m.st) != m.loadSnap
					if !complete {
						m.initReadyPolls++
						complete = m.initReadyPolls >= 2
					}
				} else {
					m.initReadyPolls = 0
				}
				if complete {
					m.resetAudioInitialization()
					m.loading = ""
				}
			}
			m.selIdx = min(m.selIdx, max(0, len(m.st.Queue)-1))
			if m.st.Err != "" {
				m.note, m.noteAt = m.st.Err, m.t // MusicKit rejection from an action
				m.failAudioInitialization()
				m.loading = ""
			} else if wasInitializing && !nowInitializing {
				m.initFailed = false
				if !m.initPending {
					m.initStart = 0
					m.initTimerActive = false
				}
			}
			if m.loading != "" && snap(m.st) != m.loadSnap && !m.audioInitializing() {
				m.loading = "" // queue switched — loading done
			}
			if id := m.st.Now.ID; id != "" && id != m.lyFor {
				m.lyFor, m.lyBusy, m.ly = id, true, lyrics.Lyrics{}
				cmd = fetchLyricsCmd(id, m.st.Now.Artist, m.st.Now.Title, m.st.Now.Duration)
			}
		}
		poll := tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return pollMsg{} })
		return m, tea.Batch(poll, cmd)
	case lyricsMsg:
		if msg.id == m.lyFor {
			m.ly, m.lyBusy = msg.ly, false
		}
	case noteMsg:
		m.note, m.noteAt = string(msg), m.t
		m.loading = "" // action failed — stop the spinner
		if m.st.Initializing || m.initPending {
			m.failAudioInitialization()
		}
	case searchMsg:
		m.sBusy = false
		if msg.err != nil {
			m.note, m.noteAt = msg.err.Error(), m.t
		} else {
			m.sRes, m.sSel, m.sInput = msg.res, 0, msg.keepInput
			if !msg.keepInput && m.sTab == 0 {
				m.sTab = 1 // catalog search has no RECENT — land on SONGS
			}
		}
	case tea.KeyMsg:
		if m.searchOpen {
			return m.updateSearch(msg)
		}
		return m.updateKeys(msg)
	}
	return m, nil
}

func (m model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.phase != phaseReady {
		if s := msg.String(); s == "q" || s == "ctrl+c" {
			m.closeVisualizerAsync()
			return m, tea.Quit
		}
		return m, nil
	}
	if m.audioInitializing() {
		switch msg.String() {
		case " ", "n", "p", "enter":
			m.note, m.noteAt = audioInitializingText, m.t
			return m, nil
		}
	}
	eng := m.eng
	switch msg.String() {
	case "q", "ctrl+c":
		m.closeVisualizerAsync()
		if eng != nil {
			eng.Close()
		}
		return m, tea.Quit
	case "tab":
		m.focus = 1 - m.focus
	case " ":
		m.st.Playing = !m.st.Playing
		return m, doCmd(eng.PlayPause)
	case "n":
		return m, doCmd(eng.Next)
	case "p":
		return m, doCmd(eng.Prev)
	case "s":
		return m, doCmd(eng.ToggleShuffle)
	case "r":
		return m, doCmd(eng.CycleRepeat)
	case "R": // reload the web player when it wedges
		m.note, m.noteAt = "reloading engine…", m.t
		m.lyFor = ""
		m.resetAudioInitialization()
		return m, doCmd(eng.Reload)
	case "/":
		m.searchOpen, m.sInput = true, true
		if strings.TrimSpace(m.sQuery) == "" {
			m.sBusy, m.sTab, m.sSel = true, 0, 0 // open on recently played
			return m, m.libraryCmd()
		}
	case "j", "down":
		if m.focus == 0 {
			m.selIdx = min(m.selIdx+1, max(0, len(m.st.Queue)-1))
		} else {
			m.st.Volume = max(m.st.Volume-5, 0)
			v := m.st.Volume
			return m, doCmd(func() error { return eng.SetVolume(v) })
		}
	case "k", "up":
		if m.focus == 0 {
			m.selIdx = max(m.selIdx-1, 0)
		} else {
			m.st.Volume = min(m.st.Volume+5, 100)
			v := m.st.Volume
			return m, doCmd(func() error { return eng.SetVolume(v) })
		}
	case "enter":
		if m.focus == 0 && len(m.st.Queue) > 0 {
			i := m.selIdx
			m.loading = m.st.Queue[i].Title
			m.loadSnap, m.loadStart, m.loadHideQueue = snap(m.st), m.t, false
			return m, doCmd(func() error { return eng.JumpTo(i) })
		}
	case "left":
		if m.focus == 1 {
			m.st.Pos = max(m.st.Pos-5*time.Second, 0)
			p := m.st.Pos
			return m, doCmd(func() error { return eng.SeekTo(p) })
		}
	case "right":
		if m.focus == 1 {
			m.st.Pos = min(m.st.Pos+5*time.Second, m.st.Dur)
			p := m.st.Pos
			return m, doCmd(func() error { return eng.SeekTo(p) })
		}
	}
	return m, nil
}

// pad truncates/pads a styled line to exactly w columns.
func pad(s string, w int) string {
	if lipgloss.Width(s) > w {
		s = lipgloss.NewStyle().MaxWidth(w).Render(s)
	}
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

func centeredRows(s string, w, rows int) string {
	var b strings.Builder
	for r := 0; r < rows; r++ {
		line := ""
		if r == rows/2 {
			line = strings.Repeat(" ", max(0, (w-lipgloss.Width(s))/2)) + s
		}
		b.WriteString(pad(line, w) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func panel(title, body string, w, h int, focused bool) string {
	bc, tc := borderDim, fgFaint
	if focused {
		bc, tc = accent, accentHi
	}
	head := lipgloss.NewStyle().Foreground(tc).Bold(true).Render(" " + title)
	content := pad(head, w) + "\n" + body
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(bc).
		Width(w).Height(h).Render(content)
}

func (m model) queuePanel(w, h int) string {
	var b strings.Builder
	rows := h - 1
	if m.audioInitializing() {
		return centeredRows(m.audioInitializingLine(), w, rows)
	}
	if m.loading != "" && m.loadHideQueue {
		sp := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		line := lipgloss.NewStyle().Foreground(accentHi).Render(" "+string(sp[int(m.t*12)%len(sp)])+" ") +
			lipgloss.NewStyle().Foreground(fgDim).Render("loading ") +
			lipgloss.NewStyle().Foreground(fgBright).Bold(true).Render(m.loading) +
			lipgloss.NewStyle().Foreground(fgDim).Render("…")
		for r := 0; r < rows; r++ {
			s := ""
			if r == rows/2 {
				s = line
			}
			b.WriteString(pad(s, w) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	q := m.st.Queue
	if len(q) == 0 {
		empty := lipgloss.NewStyle().Foreground(fgFaint).Render(" queue is empty — press / to search")
		return pad(empty, w)
	}
	start := max(0, min(m.selIdx-rows/2, len(q)-rows))
	for r := 0; r < rows; r++ {
		i := start + r
		if i >= len(q) {
			b.WriteString(pad("", w) + "\n")
			continue
		}
		tr := q[i]
		prefix, ts, as := "  ", fgDim, fgFaint
		if i == m.st.QueuePos {
			prefix, ts, as = "▶ ", fgBright, fgDim
		}
		left := lipgloss.NewStyle().Foreground(accentHi).Render(prefix) +
			lipgloss.NewStyle().Foreground(ts).Render(tr.Title) +
			lipgloss.NewStyle().Foreground(as).Render(" — "+tr.Artist)
		dur := lipgloss.NewStyle().Foreground(fgFaint).Render(fmtTime(tr.Duration))
		gap := w - lipgloss.Width(left) - lipgloss.Width(dur) - 1
		line := left + strings.Repeat(" ", max(1, gap)) + dur
		if m.focus == 0 && i == m.selIdx {
			line = lipgloss.NewStyle().Background(selBg).Render(pad(line, w))
		}
		b.WriteString(pad(line, w) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func liveBarHeights(bands [32]float64, bars, rows int) []float64 {
	if bars <= 0 {
		return nil
	}
	heights := make([]float64, bars)
	for i := range heights {
		start := i * len(bands) / bars
		end := (i + 1) * len(bands) / bars
		if end <= start {
			end = start + 1
		}
		end = min(end, len(bands))

		var peak float64
		for _, value := range bands[start:end] {
			peak = max(peak, value)
		}
		heights[i] = min(peak*1.15, 1) * float64(max(rows, 0))
	}
	return heights
}

func (m model) vizPanel(w, h int) string {
	rows, bars := h-1, max(1, w/3)
	heights := make([]float64, bars)
	if m.vizLive {
		heights = liveBarHeights(m.vizBands, bars, rows)
	} else if !m.vizOpening {
		for i := range heights {
			x := m.t*1.35 + float64(i)*0.55
			a := 0.24 + 0.62*math.Abs(math.Sin(x))*(0.5+0.5*math.Sin(m.t*0.43+float64(i)*1.9))
			if !m.st.Playing {
				a *= 0.06 // ponytail: bars just collapse on pause, no decay animation
			}
			heights[i] = a * float64(rows)
		}
	}
	partial := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for r := 0; r < rows; r++ {
		level := float64(rows - r)
		zone := level / float64(rows)
		color := accentLo
		if zone > 0.66 {
			color = accentHi
		} else if zone > 0.33 {
			color = accent
		}
		st := lipgloss.NewStyle().Foreground(color)
		var row strings.Builder
		for i := 0; i < bars; i++ {
			switch {
			case heights[i] >= level:
				row.WriteString(st.Render("██"))
			case heights[i] > level-1:
				c := string(partial[min(7, int((heights[i]-level+1)*8))])
				row.WriteString(st.Render(c + c))
			default:
				row.WriteString("  ")
			}
			row.WriteString(" ")
		}
		b.WriteString(pad(" "+row.String(), w) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) lyricsPanel(w, h int) string {
	rows := h - 1
	faint := lipgloss.NewStyle().Foreground(fgFaint)
	center := func(s string) string {
		var b strings.Builder
		for r := 0; r < rows; r++ {
			line := ""
			if r == rows/2 {
				line = s
			}
			b.WriteString(pad(line, w) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	switch {
	case m.lyBusy:
		sp := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		return center(faint.Render(" " + string(sp[int(m.t*12)%len(sp)]) + " looking up lyrics…"))
	case len(m.ly.Lines) == 0:
		return center(faint.Render(" no lyrics for this track"))
	}
	// pick the line to center on: by timestamp when synced, by progress when plain
	cur := -1
	if m.ly.Synced {
		cur = m.ly.Current(m.st.Pos)
	} else if m.st.Dur > 0 {
		cur = int(float64(m.st.Pos) / float64(m.st.Dur) * float64(len(m.ly.Lines)))
	}
	start := max(0, min(cur-rows/2, len(m.ly.Lines)-rows))
	var b strings.Builder
	for r := 0; r < rows; r++ {
		i := start + r
		if i >= len(m.ly.Lines) {
			b.WriteString(pad("", w) + "\n")
			continue
		}
		text := m.ly.Lines[i].Text
		st := faint // past lines
		if i > cur {
			st = lipgloss.NewStyle().Foreground(fgDim) // upcoming, slightly brighter
		}
		if m.ly.Synced && i == cur {
			st = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
			if text == "" {
				text = "♪"
			}
		}
		b.WriteString(pad(st.Render(" "+text), w) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func fmtTime(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (m model) transportPanel(w int) string {
	icon := "⏸"
	if !m.st.Playing {
		icon = "▶"
	}
	dim := lipgloss.NewStyle().Foreground(fgDim)
	faint := lipgloss.NewStyle().Foreground(fgFaint)
	pink := lipgloss.NewStyle().Foreground(accentHi)

	title, artist := m.st.Now.Title, m.st.Now.Artist
	if title == "" {
		title, artist = "nothing playing", "press / to find music"
	}
	hints := "↑↓ select · ↵ play · / search · space pause · tab → player · q quit "
	if m.focus == 1 {
		hints = "←→ seek · ↑↓ volume · n/p track · s/r modes · R reload · tab → queue · q quit "
	}
	if m.note != "" {
		hints = m.note + " "
	}
	l1 := pink.Render(" ▀▀ ") +
		lipgloss.NewStyle().Foreground(fgBright).Bold(true).Render(title) +
		faint.Render(" — "+artist)
	if m.audioInitializing() {
		l1 = m.audioInitializingLine()
	} else if m.loading != "" {
		sp := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		l1 = pink.Render(" "+string(sp[int(m.t*12)%len(sp)])+" ") +
			dim.Render("loading ") +
			lipgloss.NewStyle().Foreground(fgBright).Bold(true).Render(m.loading) +
			dim.Render("…")
	}
	r1 := faint.Render(hints)
	line1 := l1 + strings.Repeat(" ", max(1, w-lipgloss.Width(l1)-lipgloss.Width(r1))) + r1

	volSegs := (m.st.Volume + 9) / 17 // 0..6 segments
	volSt := faint
	if m.focus == 1 {
		volSt = pink
	}
	shuf, rep := "off", [3]string{"off", "one", "all"}[min(m.st.Repeat, 2)]
	if m.st.Shuffle {
		shuf = "on"
	}
	r2 := faint.Render(fmt.Sprintf("⇄ %s  ↻ %s  vol ", shuf, rep)) +
		volSt.Render(strings.Repeat("▮", volSegs)+strings.Repeat("▯", 6-volSegs)) +
		dim.Render(fmt.Sprintf(" %d%% ", m.st.Volume))
	times := fmtTime(m.st.Pos) + " "
	timee := " " + fmtTime(m.st.Dur)
	barW := max(10, w-lipgloss.Width(times)-lipgloss.Width(timee)-lipgloss.Width(r2)-8)
	frac := 0.0
	if m.st.Dur > 0 {
		frac = min(float64(m.st.Pos)/float64(m.st.Dur), 1)
	}
	filled := int(frac * float64(barW))
	bar := pink.Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(borderDim).Render(strings.Repeat("─", barW-filled))
	l2 := lipgloss.NewStyle().Foreground(accent).Render(" ▄▄ ") +
		pink.Render(icon+" ") + dim.Render(times) + bar + dim.Render(timee)
	line2 := l2 + strings.Repeat(" ", max(1, w-lipgloss.Width(l2)-lipgloss.Width(r2))) + r2
	return pad(line1, w) + "\n" + pad(line2, w)
}

func (m model) bootView() string {
	logo := lipgloss.NewStyle().Foreground(accentHi).Bold(true).Render("amtui")
	sub := lipgloss.NewStyle().Foreground(fgFaint).Render("apple music in your terminal")
	var line string
	if m.phase == phaseFail {
		line = lipgloss.NewStyle().Foreground(accent).Render("✕ "+m.status) +
			lipgloss.NewStyle().Foreground(fgFaint).Render("  ·  q quit")
	} else {
		sp := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		line = lipgloss.NewStyle().Foreground(fgDim).
			Render(string(sp[int(m.t*12)%len(sp)]) + " " + m.status)
	}
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center,
		logo+"\n"+sub+"\n\n"+line)
}

func (m model) View() string {
	if m.w < 70 || m.h < 22 {
		return "\n  terminal too small — need at least 70×22\n"
	}
	if m.phase != phaseReady {
		return m.bootView()
	}
	if m.searchOpen {
		return m.searchView()
	}
	topH := m.h - 4
	leftW := m.w * 42 / 100
	rightW := m.w - leftW

	qw, qh := leftW-2, topH-2
	vh := topH * 62 / 100
	vw := rightW - 2
	lh := topH - vh - 2

	left := panel("QUEUE", m.queuePanel(qw, qh), qw, qh, m.focus == 0)
	viz := panel(m.visualizerTitle(), m.vizPanel(vw, vh-2), vw, vh-2, false)
	lyr := panel("LYRICS", m.lyricsPanel(vw, lh), vw, lh, false)
	right := lipgloss.JoinVertical(lipgloss.Left, viz, lyr)
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	tw := m.w - 2
	transport := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(map[bool]lipgloss.Color{true: accent, false: borderDim}[m.focus == 1]).
		Width(tw).Render(m.transportPanel(tw))

	return lipgloss.JoinVertical(lipgloss.Left, top, transport)
}

func main() {
	m := model{statusCh: make(chan string, 8), status: "starting…"}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
