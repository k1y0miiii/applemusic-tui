package main

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func TestAccountOverlayOnlyEnterSignsOut(t *testing.T) {
	// A stray key must not sign the user out — this overlay has an action in it,
	// unlike the help one where any key just closes.
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyEsc},
		{Type: tea.KeySpace},
	} {
		m := model{phase: phaseReady, acctOpen: true}
		next, cmd := m.updateAccount(key)
		got := next.(model)
		if got.acctOpen {
			t.Fatalf("%v left the overlay open", key)
		}
		if cmd != nil {
			t.Fatalf("%v fired a command, want none", key)
		}
		if got.acctBusy {
			t.Fatalf("%v started a sign-out", key)
		}
	}
}

func TestAccountOverlayEnterNeedsAnEngine(t *testing.T) {
	m := model{phase: phaseReady, acctOpen: true} // eng is nil mid-reconnect
	next, cmd := m.updateAccount(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if cmd != nil || got.acctBusy {
		t.Fatal("sign out ran without an engine")
	}
	if got.acctOpen {
		t.Fatal("overlay should close rather than hang on a dead engine")
	}
}

func TestSignedOutReconnects(t *testing.T) {
	m := model{phase: phaseReady, acctOpen: true, acctBusy: true, statusCh: make(chan string, 1)}
	next, cmd := m.Update(signedOutMsg{})
	got := next.(model)
	if got.phase != phaseBoot {
		t.Fatalf("phase = %v after signing out, want phaseBoot", got.phase)
	}
	if cmd == nil {
		t.Fatal("signing out did not start a reconnect")
	}
	if got.acctOpen || got.acctBusy {
		t.Fatal("overlay still open after signing out")
	}
}

func TestSignOutFailureKeepsThePlayer(t *testing.T) {
	m := model{phase: phaseReady, acctBusy: true, statusCh: make(chan string, 1)}
	next, _ := m.Update(signedOutMsg{err: errors.New("unauthorize wedged")})
	got := next.(model)
	if got.phase != phaseReady {
		t.Fatalf("phase = %v, want phaseReady — a failed sign-out must not kill the session", got.phase)
	}
	if !strings.Contains(got.note, "unauthorize wedged") {
		t.Fatalf("note = %q, want the failure reason", got.note)
	}
}

func TestAccountLinesFallBackWithoutAProfile(t *testing.T) {
	// A subscriber who never made an Apple Music profile has no name; the
	// overlay must say something true rather than render an empty line.
	m := model{acct: engine.Account{Storefront: "United States", Country: "us"}}
	lines := m.accountLines()
	if lines[0] != "signed in" {
		t.Fatalf("who = %q, want %q", lines[0], "signed in")
	}
	if lines[1] != "United States (us)" {
		t.Fatalf("where = %q, want %q", lines[1], "United States (us)")
	}

	m.acct = engine.Account{Name: "k1y0mi", Handle: "k1y0mi", Storefront: "United States", Country: "us"}
	if lines = m.accountLines(); lines[0] != "k1y0mi · @k1y0mi" {
		t.Fatalf("who = %q, want the name and handle", lines[0])
	}
}
