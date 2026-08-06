package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1y0miiii/applemusic-tui/engine"
	"github.com/k1y0miiii/applemusic-tui/mpris"
)

// mprisControls adapts the engine to what MPRIS needs. Quit goes through a
// channel rather than calling os.Exit, so the desktop asking us to quit still
// runs the same shutdown as pressing q — the browser has to be closed.
type mprisControls struct {
	eng  *engine.Engine
	quit chan struct{}
}

func (c mprisControls) PlayPause() error             { return c.eng.PlayPause() }
func (c mprisControls) Next() error                  { return c.eng.Next() }
func (c mprisControls) Prev() error                  { return c.eng.Prev() }
func (c mprisControls) SeekTo(d time.Duration) error { return c.eng.SeekTo(d) }
func (c mprisControls) SetVolume(v int) error        { return c.eng.SetVolume(v) }

func (c mprisControls) Quit() {
	select {
	case c.quit <- struct{}{}:
	default: // a quit is already on its way
	}
}

// mprisState converts what the UI polls into what the desktop shows.
func mprisState(st engine.State) mpris.State {
	return mpris.State{
		TrackID:  st.Now.ID,
		Title:    st.Now.Title,
		Artist:   st.Now.Artist,
		Album:    st.Now.Album,
		ArtURL:   artworkURL(st.Now.Art, 512),
		Duration: st.Dur,
		Position: st.Pos,
		Playing:  st.Playing,
		Volume:   st.Volume,
		Repeat:   st.Repeat,
		Shuffle:  st.Shuffle,
	}
}

// listenMPRISQuit turns a Quit from the desktop into a message, so shutdown
// runs through Update like any other quit rather than from a D-Bus goroutine.
func listenMPRISQuit(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return mprisQuitMsg{}
	}
}
