package main

import "github.com/charmbracelet/lipgloss"

// theme is a full palette. The zero-value name "" is never registered.
type theme struct {
	name      string
	accent    lipgloss.Color
	accentHi  lipgloss.Color
	accentLo  lipgloss.Color
	fgBright  lipgloss.Color
	fgDim     lipgloss.Color
	fgFaint   lipgloss.Color
	borderDim lipgloss.Color
	selBg     lipgloss.Color
}

// themes[0] is the default. "auto" carries the apple palette as its base and
// has its accent trio replaced per track by the artwork's dominant color.
var themes = []theme{
	{"apple", "#FA233B", "#FB5C74", "#8A1E30", "#F2F2F7", "#8E8E93", "#5A5A5E", "#3A3A3C", "#2C2C2E"},
	{"catppuccin", "#F38BA8", "#F5C2E7", "#7D3C50", "#CDD6F4", "#9399B2", "#6C7086", "#45475A", "#313244"},
	{"gruvbox", "#FB4934", "#FE8019", "#9D0006", "#EBDBB2", "#A89984", "#7C6F64", "#504945", "#3C3836"},
	{"nord", "#88C0D0", "#8FBCBB", "#5E81AC", "#ECEFF4", "#D8DEE9", "#7B88A1", "#434C5E", "#3B4252"},
	{"mono", "#E5E5E5", "#FFFFFF", "#7A7A7A", "#F5F5F5", "#9A9A9A", "#6A6A6A", "#3A3A3A", "#2A2A2A"},
	{"auto", "#FA233B", "#FB5C74", "#8A1E30", "#F2F2F7", "#8E8E93", "#5A5A5E", "#3A3A3C", "#2C2C2E"},
}

func applyTheme(t theme) {
	accent, accentHi, accentLo = t.accent, t.accentHi, t.accentLo
	fgBright, fgDim, fgFaint = t.fgBright, t.fgDim, t.fgFaint
	borderDim, selBg = t.borderDim, t.selBg
}

func themeByName(name string) *theme {
	for i := range themes {
		if themes[i].name == name {
			return &themes[i]
		}
	}
	return nil
}

// nextTheme cycles; an unknown name restarts at the default.
func nextTheme(name string) theme {
	for i := range themes {
		if themes[i].name == name {
			return themes[(i+1)%len(themes)]
		}
	}
	return themes[0]
}
