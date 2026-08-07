package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/k1y0miiii/applemusic-tui/engine"
)

func accountCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		acct, err := eng.Account()
		return accountMsg{acct: acct, err: err}
	}
}

func signOutCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		return signedOutMsg{err: eng.SignOut()}
	}
}

// updateAccount runs while the account overlay is open. Unlike help, this
// overlay has an action in it, so a stray key must not fire it — only enter
// signs out, and everything else closes.
func (m model) updateAccount(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" && m.eng != nil && !m.acctBusy {
		m.acctBusy = true
		return m, signOutCmd(m.eng)
	}
	m.acctOpen = false
	return m, nil
}

// accountLines is the body of the overlay: who is signed in, then the action.
// Fetching the name is a round trip to Apple, so the overlay opens immediately
// and says so rather than waiting for it.
func (m model) accountLines() []string {
	who := "signed in"
	switch {
	case m.acctBusy:
		who = "signing out…"
	case m.acct.Name != "" && m.acct.Handle != "":
		who = m.acct.Name + " · @" + m.acct.Handle
	case m.acct.Name != "":
		who = m.acct.Name
	case m.acctLoading:
		who = "reading account…"
	}
	where := ""
	if m.acct.Storefront != "" {
		where = m.acct.Storefront
		if m.acct.Country != "" {
			where += " (" + m.acct.Country + ")"
		}
	} else if m.acct.Country != "" {
		where = m.acct.Country
	}
	return []string{who, where}
}

func (m model) accountView() string {
	pink := lipgloss.NewStyle().Foreground(accentHi)
	dim := lipgloss.NewStyle().Foreground(fgDim)
	faint := lipgloss.NewStyle().Foreground(fgFaint)

	lines := m.accountLines()
	action := " ↵  sign out"
	if m.acctBusy {
		action = " ↵  signing out…"
	}
	hint := " enter signs out · any other key closes "

	// The box fits its own content; a fixed width would start truncating the
	// first time someone has a long profile name.
	iw := lipgloss.Width(hint)
	for _, s := range []string{" " + lines[0], " " + lines[1], action} {
		iw = max(iw, lipgloss.Width(s))
	}
	iw = min(iw, max(20, m.w-6))

	body := []string{
		pad(" "+pink.Bold(true).Render(lines[0]), iw),
		pad(" "+dim.Render(lines[1]), iw),
		pad("", iw),
		pad(pink.Render(action), iw),
		pad("", iw),
		pad(faint.Render(hint), iw),
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(accent).
		Width(iw).Render(strings.Join(body, "\n"))
	title := pink.Bold(true).Render("ACCOUNT")
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, title+"\n"+box)
}
