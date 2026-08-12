package main

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/k1y0miiii/applemusic-tui/lyrics"
)

func solidImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestArtworkURLSubstitutesSize(t *testing.T) {
	got := artworkURL("https://example.com/img/{w}x{h}bb.jpg", 128)
	want := "https://example.com/img/128x128bb.jpg"
	if got != want {
		t.Errorf("artworkURL() = %q, want %q", got, want)
	}
	if got := artworkURL("", 128); got != "" {
		t.Errorf("artworkURL(\"\") = %q, want \"\"", got)
	}
}

func TestRenderArtworkGeometry(t *testing.T) {
	img := solidImage(64, 64, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	rows := renderArtwork(img, 10, 5)
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w != 10 {
			t.Errorf("row %d width = %d, want 10", i, w)
		}
	}
}

func TestRenderArtworkUsesHalfBlocks(t *testing.T) {
	img := solidImage(8, 8, color.RGBA{R: 0, G: 0, B: 255, A: 255})
	rows := renderArtwork(img, 4, 2)
	if !strings.Contains(rows[0], "▀") {
		t.Errorf("row does not use the half-block glyph: %q", rows[0])
	}
}

func TestRenderArtworkRejectsDegenerateSizes(t *testing.T) {
	img := solidImage(8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	if got := renderArtwork(img, 0, 4); got != nil {
		t.Errorf("renderArtwork(w=0) = %v, want nil", got)
	}
	if got := renderArtwork(img, 4, 0); got != nil {
		t.Errorf("renderArtwork(h=0) = %v, want nil", got)
	}
	if got := renderArtwork(nil, 4, 4); got != nil {
		t.Errorf("renderArtwork(nil) = %v, want nil", got)
	}
}

func TestArtworkLayout(t *testing.T) {
	// Narrow panel: the cover is as tall as the panel and twice as wide.
	cols, rows := artworkLayout(68, 11, 30, 0)
	if rows != 11 || cols != 22 {
		t.Errorf("artworkLayout(68,11,30,0) = (%d,%d), want (22,11)", cols, rows)
	}

	// Wide panel, short song lines: the cover grows into the space the lyrics
	// leave empty instead of sitting at a fixed width.
	cols, rows = artworkLayout(200, 30, 30, 40)
	if cols != 60 || rows != 30 {
		t.Errorf("artworkLayout(200,30,30,40) = (%d,%d), want (60,30) filling the panel height", cols, rows)
	}

	// Long song lines take priority: the cover shrinks to leave them room.
	cols, rows = artworkLayout(100, 30, 30, 70)
	if cols != 26 || rows != 13 {
		t.Errorf("artworkLayout(100,30,30,70) = (%d,%d), want (26,13) left over by the lyrics", cols, rows)
	}

	// A long line on a narrow panel must not squeeze the cover away: it falls
	// back to the base size as long as the lyrics keep their minimum.
	cols, rows = artworkLayout(60, 20, 30, 47)
	if cols != artworkBaseCols || rows != artworkBaseCols/2 {
		t.Errorf("artworkLayout(60,20,30,47) = (%d,%d), want the %d-column base size", cols, rows, artworkBaseCols)
	}

	// Narrow panel: hiding the cover leaves the lyrics their full width.
	cols, rows = artworkLayout(39, 11, 30, 0)
	if cols != 0 || rows != 0 {
		t.Errorf("artworkLayout(39,11,30,0) = (%d,%d), want (0,0)", cols, rows)
	}
}

func TestArtworkLayoutRespectsMinLyricsWidth(t *testing.T) {
	// 68 - 22 cover - 3 gap = 43 columns of lyrics: fits a 40 minimum, not a 50 one.
	if cols, _ := artworkLayout(68, 11, 40, 0); cols == 0 {
		t.Error("cover hidden even though 43 columns remain for lyrics")
	}
	if cols, _ := artworkLayout(68, 11, 50, 0); cols != 0 {
		t.Error("cover shown even though only 43 columns remain for lyrics")
	}
}

func TestArtworkCacheEvictsOldest(t *testing.T) {
	c := newArtCache(2)
	a := solidImage(2, 2, color.RGBA{R: 1, A: 255})
	b := solidImage(2, 2, color.RGBA{R: 2, A: 255})
	d := solidImage(2, 2, color.RGBA{R: 3, A: 255})

	c.put("a", a)
	c.put("b", b)
	if _, ok := c.get("a"); !ok {
		t.Error("a evicted too early")
	}
	c.put("d", d)
	if _, ok := c.get("a"); ok {
		t.Error("a should have been evicted")
	}
	if _, ok := c.get("d"); !ok {
		t.Error("d missing from the cache")
	}
}

func TestArtworkCacheNilIsSafe(t *testing.T) {
	var c *artCache
	c.put("a", solidImage(2, 2, color.RGBA{R: 1, A: 255})) // must not panic
	if _, ok := c.get("a"); ok {
		t.Error("nil cache reported a hit")
	}
}

// The cover sits to the left of the lyrics, so the text has to start after it.
func TestLyricsCoverSitsLeftOfTheText(t *testing.T) {
	const w, h = 80, 20
	m := model{
		w: 120, h: 35, phase: phaseReady, st: demoState(),
		art: solidImage(32, 32, color.RGBA{200, 40, 40, 255}),
		ly:  lyrics.Lyrics{Lines: []lyrics.Line{{Text: "UNIQUELYRICLINE"}}},
	}
	artCols, _ := artworkLayout(w, h-1, 30, m.lyricsWidth())
	if artCols == 0 {
		t.Fatal("artworkLayout gave no cover at a size that should fit one")
	}

	for i, line := range strings.Split(m.lyricsPanel(w, h), "\n") {
		plain := lipgloss.NewStyle().Render(line)
		// The lyrics must clear the cover and the gap after it: at one column of
		// gap the text reads as glued to the artwork.
		if idx := strings.Index(plain, "UNIQUELYRICLINE"); idx >= 0 && idx < artCols+artworkGap {
			t.Fatalf("line %d puts lyrics at column %d, want at least %d (%d-column cover plus a %d-column gap)",
				i, idx, artCols+artworkGap, artCols, artworkGap)
		}
	}
}

func TestArtworkURLFillsAppleSecondTemplateShape(t *testing.T) {
	// Apple hands out two shapes. The one with the crop code and format left to
	// the caller was not handled: {c} survived into the URL and mzstatic
	// answered 400 with JSON, which decodes to no image — so those covers came
	// up blank while the baked-in ones worked.
	got := artworkURL("https://example.com/img/{w}x{h}{c}.{f}", 128)
	if strings.ContainsAny(got, "{}") {
		t.Fatalf("artworkURL left a placeholder in %q", got)
	}
	if want := "https://example.com/img/128x128.jpg"; got != want {
		t.Errorf("artworkURL = %q, want %q", got, want)
	}
}

func TestArtworkURLLeavesNoPlaceholderBehind(t *testing.T) {
	// Whatever Apple adds next must degrade to a URL that still loads, not to a
	// 400. Every one of these is a real or plausible template shape.
	for _, template := range []string{
		"https://example.com/{w}x{h}bb.jpg",
		"https://example.com/{w}x{h}{c}.{f}",
		"https://example.com/{w}x{h}{c}bb.{f}",
		"https://example.com/{w}x{h}{unknown}.{f}",
		"https://example.com/{w}x{h}.jpg",
	} {
		got := artworkURL(template, 64)
		if strings.ContainsAny(got, "{}") {
			t.Errorf("%q -> %q still carries a placeholder", template, got)
		}
		if !strings.Contains(got, "64x64") {
			t.Errorf("%q -> %q lost the size", template, got)
		}
		if strings.HasSuffix(got, ".") {
			t.Errorf("%q -> %q ends in a bare dot", template, got)
		}
	}
}
