package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The transport hint line ran to 96 cells against a 70-column minimum terminal,
// so its tail was simply cut off — and every key added to the app made that
// worse. The inline hints now carry only what gets a beginner moving, and this
// overlay carries the full set.
//
// Entries are terse on purpose. Spelled out, the list needs 34 rows in one
// column, which overflows the 22-row minimum terminal, and two columns of prose
// do not fit across 70 either. A key reference is scanned, not read.

// shortHints is the last thing dropped: whatever else does not fit, the user
// still needs to be told where the full list is.
const shortHints = "? keys "

// transportHints is the one-line reminder in the corner of the transport bar.
func transportHints(focus int) string {
	switch focus {
	case focusRecent:
		return "←→ covers · ↑↓ rows · ↵ play · / search · ? keys "
	case focusPlayer:
		return "←→ seek · ↑↓ volume · n/p track · v viz · t theme · ? keys "
	}
	return "↑↓ select · ↵ play · / search · space pause · ? keys "
}

// fitHints picks the longest form that fits whole in the space left beside the
// track title. Truncating instead would cut mid-word and, worse, silently.
func fitHints(hints string, room int) string {
	if lipgloss.Width(hints) <= room {
		return hints
	}
	if lipgloss.Width(shortHints) <= room {
		return shortHints
	}
	return ""
}

type helpEntry struct{ keys, action string }

type helpSection struct {
	title   string
	entries []helpEntry
}

var helpSections = []helpSection{
	{"PLAYBACK", []helpEntry{
		{"space", "play / pause"},
		{"n / p", "next / prev track"},
		{"← →", "seek −5 s / +5 s"},
		{"↑ ↓", "volume down / up"},
		{"s", "shuffle on / off"},
		{"r", "cycle repeat"},
	}},
	{"BROWSING", []helpEntry{
		{"/", "search"},
		{"tab", "cycle focus"},
		{"j k / ↑ ↓", "move selection"},
		{"↵", "play selection"},
		{"← →", "prev / next cover"},
	}},
	{"IN SEARCH", []helpEntry{
		{"↵", "search / play"},
		{"tab", "next category"},
		{"a / A", "queue end / next"},
		{"esc", "close"},
	}},
	{"LOOK", []helpEntry{
		{"v", "bars/torus/sphere"},
		{"t", "cycle theme"},
	}},
	{"OTHER", []helpEntry{
		{"?", "this help"},
		{"a", "account, sign out"},
		{"R", "reload web player"},
		{"q / ctrl+c", "quit"},
	}},
}

// helpColumns splits the sections into two columns of roughly equal height. Two
// columns are not a nicety here: one column is taller than the shortest
// terminal the app agrees to draw in.
func helpColumns() ([]helpSection, []helpSection) {
	total := 0
	for _, s := range helpSections {
		total += len(s.entries) + 2
	}
	running, split := 0, len(helpSections)
	for i, s := range helpSections {
		if running >= total/2 {
			split = i
			break
		}
		running += len(s.entries) + 2
	}
	return helpSections[:split], helpSections[split:]
}

// helpWidths measures the content so the columns fit it exactly, rather than
// carrying magic numbers that quietly start truncating when an entry is added.
func helpWidths() (keys, action int) {
	for _, s := range helpSections {
		for _, e := range s.entries {
			keys = max(keys, lipgloss.Width(e.keys))
			action = max(action, lipgloss.Width(e.action))
		}
	}
	return keys, action
}

func renderHelpColumn(sections []helpSection, keyWidth, width int) []string {
	pink := lipgloss.NewStyle().Foreground(accentHi)
	dim := lipgloss.NewStyle().Foreground(fgDim)

	var lines []string
	for i, section := range sections {
		if i > 0 {
			lines = append(lines, pad("", width))
		}
		lines = append(lines, pad(" "+pink.Bold(true).Render(section.title), width))
		for _, e := range section.entries {
			key := pad(e.keys, keyWidth)
			lines = append(lines, pad("  "+pink.Render(key)+"  "+dim.Render(e.action), width))
		}
	}
	return lines
}

func (m model) helpView() string {
	faint := lipgloss.NewStyle().Foreground(fgFaint)
	pink := lipgloss.NewStyle().Foreground(accentHi)

	keyWidth, actionWidth := helpWidths()
	colWidth := 2 + keyWidth + 2 + actionWidth
	// Never wider than half the screen; pad truncates rather than letting the
	// box push past the edge.
	colWidth = min(colWidth, max(12, (m.w-6)/2-1))

	left, right := helpColumns()
	leftLines := renderHelpColumn(left, keyWidth, colWidth)
	rightLines := renderHelpColumn(right, keyWidth, colWidth)

	var body []string
	for i := 0; i < max(len(leftLines), len(rightLines)); i++ {
		line := pad("", colWidth)
		if i < len(leftLines) {
			line = leftLines[i]
		}
		if i < len(rightLines) {
			line += " " + rightLines[i]
		} else {
			line += " " + pad("", colWidth)
		}
		body = append(body, line)
	}

	iw := lipgloss.Width(body[0])
	content := strings.Join(body, "\n") + "\n\n" + pad(faint.Render(" any key closes"), iw)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(accent).
		Width(iw).Render(content)
	title := pink.Bold(true).Render("KEYS")
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, title+"\n"+box)
}
