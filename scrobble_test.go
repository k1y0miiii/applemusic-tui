package main

import (
	"testing"
	"time"

	"github.com/k1y0miiii/applemusic-tui/engine"
	"github.com/k1y0miiii/applemusic-tui/lastfm"
)

const frame = time.Second / 30

func scrobbleModel(track engine.Track) model {
	return model{
		scrobbler: &lastfm.Client{APIKey: "k", APISecret: "s", SessionKey: "sk"},
		st:        engine.State{Now: track, Playing: true, Dur: track.Duration},
	}
}

var song = engine.Track{Title: "Song", Artist: "Band", Duration: 4 * time.Minute}

// run ticks the model for a stretch of playing time and reports whether a
// scrobble fired. The first tick of a new track always returns the now-playing
// command, which is not a scrobble.
func run(m *model, d time.Duration) bool {
	fired := false
	for elapsed := time.Duration(0); elapsed < d; elapsed += frame {
		if cmd := m.advanceScrobble(frame); cmd != nil && m.scrDone {
			fired = true
		}
	}
	return fired
}

func TestScrobbleFiresOnlyAfterEnoughIsHeard(t *testing.T) {
	m := scrobbleModel(song)
	point := lastfm.ScrobblePoint(song.Duration) // 2 minutes

	if run(&m, point-time.Second) {
		t.Fatalf("scrobbled after %v, before the %v mark", point-time.Second, point)
	}
	if !run(&m, 2*time.Second) {
		t.Fatalf("did not scrobble after passing the %v mark", point)
	}
}

func TestSeekingCannotFakeAPlay(t *testing.T) {
	// The whole reason listening time is accumulated rather than read off the
	// playback position: jumping to the end of a track would otherwise look
	// exactly like having heard it.
	m := scrobbleModel(song)
	run(&m, 5*time.Second)

	m.st.Pos = song.Duration - time.Second // the user dragged the progress bar
	if run(&m, time.Second) {
		t.Error("a seek to the end counted as a full play")
	}
	if m.scrDone {
		t.Error("scrobble marked done without the listening time")
	}
}

func TestPausedTimeDoesNotCount(t *testing.T) {
	m := scrobbleModel(song)
	m.st.Playing = false
	if run(&m, 10*time.Minute) {
		t.Fatal("scrobbled a track that was never playing")
	}
	if m.scrPlayed != 0 {
		t.Errorf("accumulated %v of listening while paused", m.scrPlayed)
	}
}

func TestShortTracksAreNeverScrobbled(t *testing.T) {
	short := engine.Track{Title: "Interlude", Artist: "Band", Duration: 20 * time.Second}
	m := scrobbleModel(short)
	if run(&m, time.Minute) {
		t.Error("scrobbled a track under the 30-second floor")
	}
}

func TestEachPlayScrobblesOnce(t *testing.T) {
	m := scrobbleModel(song)
	if !run(&m, 3*time.Minute) {
		t.Fatal("first play did not scrobble")
	}
	if run(&m, 3*time.Minute) {
		t.Error("the same play scrobbled twice")
	}

	// A different track resets everything, including the start timestamp.
	before := m.scrStart
	m.st.Now = engine.Track{Title: "Next", Artist: "Band", Duration: 4 * time.Minute}
	m.st.Dur = m.st.Now.Duration
	m.advanceScrobble(frame)
	if m.scrDone || m.scrPlayed != 0 {
		t.Errorf("new track kept state: done=%v played=%v", m.scrDone, m.scrPlayed)
	}
	if !m.scrStart.After(before) && !m.scrStart.Equal(before) {
		t.Error("new track did not restamp its start time")
	}
	if !run(&m, 3*time.Minute) {
		t.Error("the next track never scrobbled")
	}
}

func TestTheSameTrackAgainIsANewPlay(t *testing.T) {
	// Repeat-one is a real listening pattern, and each pass is its own scrobble.
	m := scrobbleModel(song)
	run(&m, 3*time.Minute)

	m.st.Now = engine.Track{Title: "Other", Artist: "Band", Duration: time.Minute}
	m.advanceScrobble(frame)
	m.st.Now, m.st.Dur = song, song.Duration
	m.advanceScrobble(frame)

	if m.scrDone {
		t.Fatal("coming back to the track kept it marked as scrobbled")
	}
	if !run(&m, 3*time.Minute) {
		t.Error("the second play of the same track did not scrobble")
	}
}

func TestNoScrobblerIsSilent(t *testing.T) {
	m := model{st: engine.State{Now: song, Playing: true, Dur: song.Duration}}
	if run(&m, 10*time.Minute) {
		t.Fatal("scrobbled with Last.fm unconfigured")
	}
	if m.scrPlayed != 0 || m.scrKey != "" {
		t.Error("unconfigured scrobbling still kept state")
	}
}
