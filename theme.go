package main

import (
	"bufio"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
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

// Pixels below these thresholds are background, not identity: near-grey or
// near-black areas would otherwise dominate most covers.
const (
	dominantMinSat = 0.25
	dominantMinVal = 0.15
	dominantBins   = 24 // hue buckets
)

func colorfulRGB(r, g, b uint8) colorful.Color {
	return colorful.Color{
		R: float64(r) / 255, G: float64(g) / 255, B: float64(b) / 255,
	}
}

// dominantColor buckets the image's colorful pixels by hue and returns the
// weighted mean color of the heaviest bucket. ok is false when the image has no
// colorful pixels at all (greyscale covers), in which case the caller keeps the
// preset accent.
func dominantColor(img image.Image) (colorful.Color, bool) {
	if img == nil {
		return colorful.Color{}, false
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return colorful.Color{}, false
	}
	// Sample on a grid of at most ~64x64 points regardless of source size.
	stepX := max(1, b.Dx()/64)
	stepY := max(1, b.Dy()/64)

	var sumR, sumG, sumB, weight [dominantBins]float64
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 < 0x8000 {
				continue // transparent
			}
			c := colorfulRGB(uint8(r16>>8), uint8(g16>>8), uint8(b16>>8))
			h, s, v := c.Hsv()
			if s < dominantMinSat || v < dominantMinVal {
				continue
			}
			bin := int(h/360*dominantBins) % dominantBins
			w := s * v // vivid, bright pixels count more
			sumR[bin] += c.R * w
			sumG[bin] += c.G * w
			sumB[bin] += c.B * w
			weight[bin] += w
		}
	}
	best, bestW := -1, 0.0
	for i, w := range weight {
		if w > bestW {
			best, bestW = i, w
		}
	}
	if best < 0 || bestW == 0 {
		return colorful.Color{}, false
	}
	return colorful.Color{
		R: sumR[best] / bestW, G: sumG[best] / bestW, B: sumB[best] / bestW,
	}, true
}

// accentTrio turns one cover color into the accent/accentHi/accentLo set,
// clamping saturation and value so dark or washed-out covers stay readable.
func accentTrio(c colorful.Color) (lipgloss.Color, lipgloss.Color, lipgloss.Color) {
	h, s, v := c.Hsv()
	s = math.Max(s, 0.55)
	v = math.Min(math.Max(v, 0.62), 0.95)
	base := colorful.Hsv(h, s, v)
	hi := colorful.Hsv(h, math.Max(s-0.15, 0), math.Min(v+0.15, 1))
	lo := colorful.Hsv(h, math.Min(s+0.10, 1), v*0.42)
	return lipgloss.Color(base.Hex()),
		lipgloss.Color(hi.Hex()),
		lipgloss.Color(lo.Hex())
}

// hexToHSV is a test and debugging helper for asserting on generated colors.
func hexToHSV(hex string) (h, s, v float64) {
	c, err := colorful.Hex(hex)
	if err != nil {
		return 0, 0, 0
	}
	return c.Hsv()
}

// applyAutoTheme re-derives the accent trio from the current artwork. Only the
// accent moves — background and text stay neutral so no cover can make the UI
// unreadable. A no-op unless the "auto" theme is active.
func (m *model) applyAutoTheme() {
	if m.themeName != "auto" || m.art == nil {
		return
	}
	c, ok := dominantColor(m.art)
	if !ok {
		return // greyscale cover: keep whatever accent is already showing
	}
	accent, accentHi, accentLo = accentTrio(c)
}

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
