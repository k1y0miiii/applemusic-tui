package main

import (
	"errors"
	"testing"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func drainStates(t *testing.T, m model, msg stateMsg, n int) model {
	t.Helper()
	for i := 0; i < n; i++ {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func TestSignedOutPollsTriggerReconnect(t *testing.T) {
	cases := map[string]stateMsg{
		"signed out":  {st: engine.State{Authed: false}},
		"poll failed": {err: errors.New("context deadline exceeded")},
	}

	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			m := model{phase: phaseReady, statusCh: make(chan string, 1)}

			m = drainStates(t, m, bad, badPollsToReconnect-1)
			if m.phase != phaseReady {
				t.Fatalf("phase = %v after %d bad polls, want phaseReady — a hiccup must not restart the browser",
					m.phase, badPollsToReconnect-1)
			}

			next, cmd := m.Update(bad)
			m = next.(model)
			if m.phase != phaseBoot {
				t.Fatalf("phase = %v after %d bad polls, want phaseBoot", m.phase, badPollsToReconnect)
			}
			if cmd == nil {
				t.Fatal("reconnect returned no command")
			}
			if m.status == "" {
				t.Fatal("reconnect left the user with no status line")
			}
		})
	}
}

func TestGoodPollResetsTheBadPollCount(t *testing.T) {
	m := model{phase: phaseReady, statusCh: make(chan string, 1)}

	m = drainStates(t, m, stateMsg{err: errors.New("blip")}, badPollsToReconnect-1)
	m = drainStates(t, m, stateMsg{st: engine.State{Authed: true}}, 1)
	if m.badPolls != 0 {
		t.Fatalf("badPolls = %d after a good poll, want 0", m.badPolls)
	}

	m = drainStates(t, m, stateMsg{err: errors.New("blip")}, badPollsToReconnect-1)
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want phaseReady — the count did not restart", m.phase)
	}
}
