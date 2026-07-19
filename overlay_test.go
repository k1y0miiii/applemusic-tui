package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSearchTabSwitchesCategory(t *testing.T) {
	for _, input := range []bool{true, false} {
		m := model{phase: phaseReady, searchOpen: true, sInput: input, sQuery: "daft punk"}
		next, _ := m.updateSearch(tea.KeyMsg{Type: tea.KeyTab})
		if got := next.(model).sTab; got != 1 {
			t.Errorf("sInput=%v: tab should switch category, got sTab=%d", input, got)
		}
	}
}
