package main

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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
