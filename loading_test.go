package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func TestPlaybackKeysBlockedWhileAudioInitializes(t *testing.T) {
	keys := map[string]tea.KeyMsg{
		"space": {Type: tea.KeySpace},
		"n":     {Type: tea.KeyRunes, Runes: []rune("n")},
		"p":     {Type: tea.KeyRunes, Runes: []rune("p")},
		"enter": {Type: tea.KeyEnter},
	}

	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			m := model{
				phase: phaseReady,
				st: engine.State{
					Initializing: true,
					Queue:        []engine.Track{{ID: "track-1", Title: "Track"}},
				},
			}

			next, cmd := m.updateKeys(key)
			got := next.(model)

			if cmd != nil {
				t.Fatal("playback command should be blocked while audio initializes")
			}
			if !strings.Contains(got.note, "initializing") {
				t.Fatalf("note = %q, want an initializing message", got.note)
			}
		})
	}
}

func TestInitializingMessageIsRendered(t *testing.T) {
	m := model{
		w:     100,
		h:     30,
		phase: phaseReady,
		st: engine.State{
			Initializing: true,
			Queue:        []engine.Track{{ID: "track-1", Title: "Track"}},
		},
	}

	if got := m.View(); strings.Count(got, "initializing Apple Music audio") < 2 {
		t.Fatalf("View() should contain the initialization message in queue and transport:\n%s", got)
	}
}

func TestQueueChangeDoesNotHideColdStartSpinner(t *testing.T) {
	oldState := engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	}
	m := model{
		phase:    phaseReady,
		st:       oldState,
		loading:  "Album",
		loadSnap: snap(oldState),
	}
	newState := engine.State{
		Initializing: true,
		Now:          engine.Track{ID: "new"},
		Queue:        []engine.Track{{ID: "new"}},
	}

	next, _ := m.Update(stateMsg{st: newState})
	if got := next.(model).loading; got != "Album" {
		t.Fatalf("loading = %q, want it preserved during cold-start initialization", got)
	}
}

func TestMusicKitErrorEndsInitialLoading(t *testing.T) {
	st := engine.State{
		Initializing: true,
		Now:          engine.Track{ID: "track-1"},
		Queue:        []engine.Track{{ID: "track-1"}},
	}
	m := model{
		phase:    phaseReady,
		st:       st,
		loading:  "Album",
		loadSnap: snap(st),
	}
	st.Err = "MusicKit rejected playback"

	next, _ := m.Update(stateMsg{st: st})
	got := next.(model)

	if got.audioInitializing() {
		t.Fatal("audio initialization should end after a MusicKit error")
	}
	if got.loading != "" {
		t.Fatalf("loading = %q, want it cleared after a MusicKit error", got.loading)
	}
}

func TestZeroInitStartDoesNotWarn(t *testing.T) {
	m := model{
		phase: phaseReady,
		t:     60,
		st:    engine.State{Initializing: true},
	}

	next, _ := m.Update(tickMsg{})
	if got := next.(model).note; got == audioInitializingWarning {
		t.Fatal("zero initStart should mean the initialization timer has not started")
	}
}

func TestNewPlayAttemptRestartsInitializationTimer(t *testing.T) {
	m := model{
		phase:      phaseReady,
		t:          90,
		st:         engine.State{Initializing: true},
		initStart:  10,
		initFailed: true,
		sTab:       1,
		sRes: engine.SearchResults{
			Songs: []engine.Track{{ID: "song-1", Kind: "song", Title: "Song"}},
		},
	}

	next, cmd := m.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if cmd == nil {
		t.Fatal("new play attempt should be allowed after an initialization failure")
	}
	if got.initFailed || got.initStart != 90 {
		t.Fatalf("new play attempt left initFailed=%v initStart=%v, want false and 90",
			got.initFailed, got.initStart)
	}

	got.t = 91
	next, _ = got.Update(stateMsg{st: engine.State{Initializing: true}})
	if restarted := next.(model).initStart; restarted != 90 {
		t.Fatalf("initStart = %v, want the pending timer to keep its start at 90", restarted)
	}
}

func TestReloadRestartsInitializationTimer(t *testing.T) {
	m := model{
		phase:      phaseReady,
		t:          120,
		st:         engine.State{Initializing: true},
		initStart:  10,
		initFailed: true,
	}

	next, cmd := m.updateKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	got := next.(model)
	if cmd == nil {
		t.Fatal("reload should remain available after an initialization failure")
	}
	if got.initFailed || got.initStart != 0 {
		t.Fatalf("reload left initFailed=%v initStart=%v, want false and 0",
			got.initFailed, got.initStart)
	}

	next, _ = got.Update(stateMsg{st: engine.State{Initializing: true}})
	if restarted := next.(model).initStart; restarted != 120 {
		t.Fatalf("initStart = %v, want fresh start at 120", restarted)
	}
}

func TestCommandErrorEndsAudioInitialization(t *testing.T) {
	m := model{
		phase:     phaseReady,
		st:        engine.State{Initializing: true},
		loading:   "Album",
		initStart: 10,
	}

	next, _ := m.Update(noteMsg("reload failed"))
	got := next.(model)

	if got.audioInitializing() {
		t.Fatal("command error should end audio initialization")
	}
	if got.initStart != 0 {
		t.Fatalf("initStart = %v, want 0 after command error", got.initStart)
	}
	if got.loading != "" {
		t.Fatalf("loading = %q, want it cleared after command error", got.loading)
	}
	if got.note != "reload failed" {
		t.Fatalf("note = %q, want command error preserved", got.note)
	}
}

func TestInitializationWarningDoesNotRefreshNoteTime(t *testing.T) {
	m := model{
		phase:           phaseReady,
		t:               31,
		st:              engine.State{Initializing: true},
		initStart:       1,
		initTimerActive: true,
	}

	next, _ := m.Update(tickMsg{})
	warned := next.(model)
	if warned.note != audioInitializingWarning {
		t.Fatalf("note = %q, want initialization warning", warned.note)
	}
	noteAt := warned.noteAt

	next, _ = warned.Update(tickMsg{})
	got := next.(model)
	if got.note == "" {
		t.Fatal("initialization warning should remain visible")
	}
	if got.noteAt != noteAt {
		t.Fatalf("noteAt changed from %v to %v on the next tick", noteAt, got.noteAt)
	}
}

func startPlayAttempt(t *testing.T, st engine.State) model {
	t.Helper()
	m := model{
		phase: phaseReady,
		st:    st,
		sTab:  1,
		sRes: engine.SearchResults{
			Songs: []engine.Track{{ID: "new", Kind: "song", Title: "New Song"}},
		},
	}

	next, cmd := m.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("play attempt should return a command")
	}
	return next.(model)
}

func TestPlayAttemptImmediatelyMarksAudioInitializing(t *testing.T) {
	m := startPlayAttempt(t, engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	})

	if !m.audioInitializing() {
		t.Fatal("play attempt should initialize audio before the first state poll")
	}
}

func TestStaleStatePreservesPendingInitialization(t *testing.T) {
	oldState := engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	}
	m := startPlayAttempt(t, oldState)

	next, _ := m.Update(stateMsg{st: oldState})
	got := next.(model)

	if !got.audioInitializing() {
		t.Fatal("stale state with the original snapshot should preserve pending initialization")
	}
	if got.loading == "" {
		t.Fatal("stale state should not clear the pending loading UI")
	}
}

func TestChangedPlayingStateCompletesUnobservedInitialization(t *testing.T) {
	oldState := engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	}
	m := startPlayAttempt(t, oldState)
	readyState := engine.State{
		Playing: true,
		Now:     engine.Track{ID: "new"},
		Queue:   []engine.Track{{ID: "new"}},
	}

	next, _ := m.Update(stateMsg{st: readyState})
	got := next.(model)

	if got.audioInitializing() {
		t.Fatal("playing state with a changed snapshot should complete pending initialization")
	}
	if got.loading != "" {
		t.Fatalf("loading = %q, want it cleared after playback becomes ready", got.loading)
	}
}

func TestPendingPlayWarnsWithoutInitializationPoll(t *testing.T) {
	m := startPlayAttempt(t, engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	})
	m.t = 31

	next, _ := m.Update(tickMsg{})
	if got := next.(model).note; got != audioInitializingWarning {
		t.Fatalf("note = %q, want warning after 30 seconds of pending initialization", got)
	}
}

func TestReloadClearsLocalInitializationState(t *testing.T) {
	m := model{
		phase:           phaseReady,
		t:               20,
		initPending:     true,
		initSeen:        true,
		initFailed:      true,
		initStart:       1,
		initTimerActive: true,
		st: engine.State{
			Now:   engine.Track{ID: "old"},
			Queue: []engine.Track{{ID: "old"}},
		},
	}

	next, cmd := m.updateKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd == nil {
		t.Fatal("reload should return a command")
	}

	reloaded := next.(model)
	next, _ = reloaded.Update(stateMsg{st: engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	}})
	got := next.(model)

	if got.audioInitializing() {
		t.Fatal("reload followed by a ready state should not leave audio initialization pending")
	}
	if got.initPending || got.initSeen || got.initFailed || got.initTimerActive || got.initStart != 0 {
		t.Fatalf("reload left initPending=%v initSeen=%v initFailed=%v timerActive=%v initStart=%v",
			got.initPending, got.initSeen, got.initFailed, got.initTimerActive, got.initStart)
	}
}

func TestPendingCompletionStopsInitializationTimer(t *testing.T) {
	m := startPlayAttempt(t, engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	})
	if !m.initTimerActive {
		t.Fatal("play attempt should start the initialization timer")
	}

	next, _ := m.Update(stateMsg{st: engine.State{
		Playing: true,
		Now:     engine.Track{ID: "new"},
		Queue:   []engine.Track{{ID: "new"}},
	}})
	if got := next.(model); got.initTimerActive {
		t.Fatal("completed pending initialization should stop its timer")
	}
}

func TestPendingCommandErrorStopsInitializationTimer(t *testing.T) {
	m := startPlayAttempt(t, engine.State{
		Now:   engine.Track{ID: "old"},
		Queue: []engine.Track{{ID: "old"}},
	})
	if !m.initTimerActive {
		t.Fatal("play attempt should start the initialization timer")
	}

	next, _ := m.Update(noteMsg("play failed"))
	got := next.(model)
	if got.initTimerActive {
		t.Fatal("command error should stop the initialization timer")
	}
	if !got.initFailed {
		t.Fatal("command error should leave the initialization failure latched")
	}
}

func TestRepeatedSameSnapshotPlayCompletesAfterTwoReadyPolls(t *testing.T) {
	sameState := engine.State{
		Playing: true,
		Now:     engine.Track{ID: "new"},
		Queue:   []engine.Track{{ID: "new"}},
	}
	m := startPlayAttempt(t, sameState)

	next, _ := m.Update(stateMsg{st: sameState})
	first := next.(model)
	if !first.audioInitializing() {
		t.Fatal("one identical playing poll may be stale and must preserve pending initialization")
	}

	next, _ = first.Update(stateMsg{st: sameState})
	got := next.(model)
	if got.audioInitializing() {
		t.Fatal("two identical playing polls should complete repeated-track initialization")
	}
	if got.initTimerActive {
		t.Fatal("completed repeated-track initialization should stop its timer")
	}
	if got.loading != "" {
		t.Fatalf("loading = %q, want it cleared after repeated-track initialization", got.loading)
	}
}

func TestChangedSnapshotWaitsForPlaying(t *testing.T) {
	for _, initSeen := range []bool{false, true} {
		t.Run(map[bool]string{false: "unobserved", true: "observed"}[initSeen], func(t *testing.T) {
			oldState := engine.State{
				Now:   engine.Track{ID: "old"},
				Queue: []engine.Track{{ID: "old"}},
			}
			m := startPlayAttempt(t, oldState)
			if initSeen {
				next, _ := m.Update(stateMsg{st: engine.State{
					Initializing: true,
					Now:          engine.Track{ID: "old"},
					Queue:        []engine.Track{{ID: "old"}},
				}})
				m = next.(model)
			}

			changedState := engine.State{
				Now:   engine.Track{ID: "new"},
				Queue: []engine.Track{{ID: "new"}},
			}
			next, _ := m.Update(stateMsg{st: changedState})
			waiting := next.(model)
			if !waiting.audioInitializing() {
				t.Fatal("changed snapshot without playback must preserve pending initialization")
			}
			if waiting.loading == "" {
				t.Fatal("changed snapshot without playback must preserve loading UI")
			}

			changedState.Playing = true
			next, _ = waiting.Update(stateMsg{st: changedState})
			got := next.(model)
			if got.audioInitializing() {
				t.Fatal("playing changed snapshot should complete pending initialization")
			}
			if got.loading != "" {
				t.Fatalf("loading = %q, want it cleared once playback starts", got.loading)
			}
		})
	}
}
