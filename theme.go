package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

// configDir matches the engine's convention (engine/engine.go:65) rather than
// os.UserConfigDir, which on macOS points at ~/Library/Application Support.
// AMTUI_CONFIG_DIR overrides it, which is also how the tests isolate state.
func configDir() string {
	if d := os.Getenv("AMTUI_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "amtui")
}

// The chosen theme lives in its own one-line file so writing it back never
// clobbers the comments in config.toml.
func themeFile() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "theme")
}

func loadThemeName() string {
	p := themeFile()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveThemeName is best-effort: a read-only config dir must not break the UI.
func saveThemeName(name string) {
	p := themeFile()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(name+"\n"), 0o644)
}

// stripComment removes a trailing `#` comment, ignoring `#` inside double
// quotes so hex colors like accent = "#FA233B" survive.
func stripComment(line string) string {
	inQuotes := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuotes = !inQuotes
		case '#':
			if !inQuotes {
				return strings.TrimSpace(line[:i])
			}
		}
	}
	return strings.TrimSpace(line)
}

// parseConfig reads a deliberately small TOML subset: `[section]` headers and
// `key = value` lines, `#` comments, optional quotes around values. Keys come
// back as "section.key". A hand-rolled parser keeps the module dependency-free.
// ponytail: no arrays, no nesting, no types — add a real TOML library only if
// the config actually grows to need them.
func parseConfig(r io.Reader) map[string]string {
	out := map[string]string{}
	section := ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := stripComment(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.TrimSpace(line[1 : len(line)-1])
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"`)
			if section != "" {
				k = section + "." + k
			}
			out[k] = v
		}
	}
	return out
}

// loadConfig reads ~/.config/amtui/config.toml. A missing or unreadable file is
// not an error — the defaults simply stand.
func loadConfig() map[string]string {
	d := configDir()
	if d == "" {
		return map[string]string{}
	}
	f, err := os.Open(filepath.Join(d, "config.toml"))
	if err != nil {
		return map[string]string{}
	}
	defer f.Close()
	return parseConfig(f)
}

func configBool(cfg map[string]string, key string, def bool) bool {
	v, ok := cfg[key]
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func configInt(cfg map[string]string, key string, def int) int {
	v, ok := cfg[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// applyAutoTheme re-derives the accent from the current artwork when the
// "auto" theme is active. Implemented in Task 7.
func (m *model) applyAutoTheme() {}

// themeFromConfig picks the preset named by the config (falling back to the
// saved name, then the default) and applies any per-color overrides on top.
func themeFromConfig(cfg map[string]string, saved string) theme {
	name := cfg["theme.name"]
	if name == "" {
		name = saved
	}
	t := themes[0]
	if found := themeByName(name); found != nil {
		t = *found
	}
	set := func(key string, dst *lipgloss.Color) {
		if v := cfg["theme."+key]; v != "" {
			*dst = lipgloss.Color(v)
		}
	}
	set("accent", &t.accent)
	set("accent_hi", &t.accentHi)
	set("accent_lo", &t.accentLo)
	set("fg_bright", &t.fgBright)
	set("fg_dim", &t.fgDim)
	set("fg_faint", &t.fgFaint)
	set("border_dim", &t.borderDim)
	set("sel_bg", &t.selBg)
	return t
}
