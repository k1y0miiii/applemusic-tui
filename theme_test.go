package main

import "testing"

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
