package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestApplyThemeSwitchesPalette(t *testing.T) {
	defer applyTheme(themes[0])

	applyTheme(themes[0])
	if accent != themes[0].accent {
		t.Fatalf("accent = %v, want %v", accent, themes[0].accent)
	}

	nord := themeByName("nord")
	if nord == nil {
		t.Fatal("themeByName(\"nord\") = nil, want a theme")
	}
	applyTheme(*nord)
	if accent != nord.accent {
		t.Errorf("accent = %v, want %v", accent, nord.accent)
	}
	if selBg != nord.selBg {
		t.Errorf("selBg = %v, want %v", selBg, nord.selBg)
	}
}

func TestThemeByNameUnknown(t *testing.T) {
	if got := themeByName("does-not-exist"); got != nil {
		t.Errorf("themeByName(unknown) = %v, want nil", got)
	}
}

func TestNextThemeWraps(t *testing.T) {
	last := themes[len(themes)-1].name
	if got := nextTheme(last).name; got != themes[0].name {
		t.Errorf("nextTheme(%q) = %q, want %q", last, got, themes[0].name)
	}
	if got := nextTheme(themes[0].name).name; got != themes[1].name {
		t.Errorf("nextTheme(%q) = %q, want %q", themes[0].name, got, themes[1].name)
	}
	if got := nextTheme("unknown").name; got != themes[0].name {
		t.Errorf("nextTheme(unknown) = %q, want %q", got, themes[0].name)
	}
}

func TestSaveAndLoadThemeName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AMTUI_CONFIG_DIR", dir)

	if got := loadThemeName(); got != "" {
		t.Errorf("loadThemeName() on empty dir = %q, want \"\"", got)
	}

	saveThemeName("nord")
	if got := loadThemeName(); got != "nord" {
		t.Errorf("loadThemeName() = %q, want \"nord\"", got)
	}

	// Trailing whitespace written by a human editing the file is tolerated.
	if err := os.WriteFile(filepath.Join(dir, "theme"), []byte("gruvbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadThemeName(); got != "gruvbox" {
		t.Errorf("loadThemeName() = %q, want \"gruvbox\"", got)
	}
}

func TestSaveThemeNameIsBestEffort(t *testing.T) {
	t.Setenv("AMTUI_CONFIG_DIR", "/proc/nonexistent-amtui-path")
	saveThemeName("nord") // must not panic on an unwritable directory
}

func TestParseConfig(t *testing.T) {
	src := `
# a comment line
name = "apple"

[theme]
name = "nord"          # inline comment
accent = "#123456"

[visualizer]
pulse = false
peaks   =   true
bogus-line-without-equals
`
	got := parseConfig(strings.NewReader(src))
	want := map[string]string{
		"name":             "apple",
		"theme.name":       "nord",
		"theme.accent":     "#123456",
		"visualizer.pulse": "false",
		"visualizer.peaks": "true",
	}
	if len(got) != len(want) {
		t.Fatalf("parseConfig() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseConfig()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestConfigThemeOverridesColors(t *testing.T) {
	defer applyTheme(themes[0])

	cfg := map[string]string{"theme.name": "nord", "theme.accent": "#123456"}
	got := themeFromConfig(cfg, "apple")
	if got.name != "nord" {
		t.Errorf("name = %q, want \"nord\"", got.name)
	}
	if got.accent != lipgloss.Color("#123456") {
		t.Errorf("accent = %v, want #123456", got.accent)
	}
	// Unoverridden entries keep the preset's values.
	nord := themeByName("nord")
	if got.selBg != nord.selBg {
		t.Errorf("selBg = %v, want %v", got.selBg, nord.selBg)
	}
}

func TestConfigThemeFallsBackToSaved(t *testing.T) {
	got := themeFromConfig(map[string]string{}, "gruvbox")
	if got.name != "gruvbox" {
		t.Errorf("name = %q, want \"gruvbox\"", got.name)
	}
}

func TestConfigBool(t *testing.T) {
	cfg := map[string]string{"visualizer.pulse": "false", "visualizer.peaks": "true"}
	if configBool(cfg, "visualizer.pulse", true) {
		t.Error("configBool(pulse) = true, want false")
	}
	if !configBool(cfg, "visualizer.peaks", false) {
		t.Error("configBool(peaks) = false, want true")
	}
	if !configBool(cfg, "visualizer.missing", true) {
		t.Error("configBool(missing) did not return the default")
	}
}

func TestConfigInt(t *testing.T) {
	cfg := map[string]string{"artwork.min_lyrics_width": "42", "artwork.bad": "nope"}
	if got := configInt(cfg, "artwork.min_lyrics_width", 30); got != 42 {
		t.Errorf("configInt() = %d, want 42", got)
	}
	if got := configInt(cfg, "artwork.bad", 30); got != 30 {
		t.Errorf("configInt(unparsable) = %d, want the default 30", got)
	}
	if got := configInt(cfg, "artwork.missing", 30); got != 30 {
		t.Errorf("configInt(missing) = %d, want the default 30", got)
	}
}

func TestDominantColorPicksTheSaturatedHue(t *testing.T) {
	// Mostly dark grey with a block of strong blue: the blue must win.
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 20, B: 22, A: 255})
		}
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 80, B: 220, A: 255})
		}
	}
	c, ok := dominantColor(img)
	if !ok {
		t.Fatal("dominantColor() reported no usable color")
	}
	if !(c.B > c.R && c.B > c.G) {
		t.Errorf("dominant color %v is not blue-dominant", c)
	}
}

func TestDominantColorRejectsGreyscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	if _, ok := dominantColor(img); ok {
		t.Error("dominantColor() accepted a greyscale image; want ok = false")
	}
}

func TestDominantColorNilImage(t *testing.T) {
	if _, ok := dominantColor(nil); ok {
		t.Error("dominantColor(nil) returned ok = true")
	}
}

func TestAccentTrioIsReadable(t *testing.T) {
	// A near-black cover must still produce a visible accent.
	dark := colorfulRGB(10, 10, 40)
	a, hi, lo := accentTrio(dark)
	if a == "" || hi == "" || lo == "" {
		t.Fatal("accentTrio returned empty colors")
	}
	_, _, v := hexToHSV(string(a))
	if v < 0.5 {
		t.Errorf("accent value = %.2f, want >= 0.50 for readability", v)
	}
	_, _, vhi := hexToHSV(string(hi))
	if vhi < v {
		t.Errorf("accentHi value %.2f is not brighter than accent %.2f", vhi, v)
	}
	_, _, vlo := hexToHSV(string(lo))
	if vlo >= v {
		t.Errorf("accentLo value %.2f is not darker than accent %.2f", vlo, v)
	}
}
