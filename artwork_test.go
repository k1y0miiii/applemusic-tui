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
	// Wide panel: cover takes 2 columns per row, capped at 24.
	cols, rows := artworkLayout(68, 11, 30)
	if rows != 11 || cols != 22 {
		t.Errorf("artworkLayout(68,11,30) = (%d,%d), want (22,11)", cols, rows)
	}

	// Cap: a very tall panel must not produce a cover wider than 24.
	cols, _ = artworkLayout(100, 20, 30)
	if cols != 24 {
		t.Errorf("cols = %d, want the 24-column cap", cols)
	}

	// Narrow panel: hiding the cover leaves the lyrics their full width.
	cols, rows = artworkLayout(39, 11, 30)
	if cols != 0 || rows != 0 {
		t.Errorf("artworkLayout(39,11,30) = (%d,%d), want (0,0)", cols, rows)
	}
}

func TestArtworkLayoutRespectsMinLyricsWidth(t *testing.T) {
	// 68 - 22 - 1 gap = 45 columns of lyrics: fits a 40 minimum, not a 50 one.
	if cols, _ := artworkLayout(68, 11, 40); cols == 0 {
		t.Error("cover hidden even though 45 columns remain for lyrics")
	}
	if cols, _ := artworkLayout(68, 11, 50); cols != 0 {
		t.Error("cover shown even though only 45 columns remain for lyrics")
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
	artCols, _ := artworkLayout(w, h-1, 30)
	if artCols == 0 {
		t.Fatal("artworkLayout gave no cover at a size that should fit one")
	}

	for i, line := range strings.Split(m.lyricsPanel(w, h), "\n") {
		plain := lipgloss.NewStyle().Render(line)
		if idx := strings.Index(plain, "UNIQUELYRICLINE"); idx >= 0 && idx < artCols {
			t.Fatalf("line %d puts lyrics at column %d, inside the %d-column cover", i, idx, artCols)
		}
	}
}
