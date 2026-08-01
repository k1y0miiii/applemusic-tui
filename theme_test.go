package main

import (
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
