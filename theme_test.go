package main

import (
	"os"
	"path/filepath"
	"testing"
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
