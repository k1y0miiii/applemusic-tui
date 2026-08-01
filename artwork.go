package main

import (
	"context"
	"image"
	_ "image/jpeg" // Apple serves JPEG artwork
	_ "image/png"  // …but decode PNG too rather than fail
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// artworkPx is the fetch size. Big enough for a ~24-column cover and for a
// stable dominant-color histogram, small enough to decode in a few ms.
const artworkPx = 128

// artworkTimeout bounds the cover fetch; artwork is decoration, never a stall.
const artworkTimeout = 5 * time.Second

// artworkURL fills MusicKit's {w}/{h} placeholders. An empty template stays
// empty so callers can treat "" as "no artwork".
func artworkURL(template string, px int) string {
	if template == "" {
		return ""
	}
	s := strconv.Itoa(px)
	r := strings.NewReplacer("{w}", s, "{h}", s, "{f}", "jpg")
	return r.Replace(template)
}

func fetchArtwork(ctx context.Context, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	img, _, err := image.Decode(resp.Body)
	return img, err
}

// artCache keeps the last few decoded covers so switching back and forth in a
// queue does not refetch. ponytail: insertion-order eviction, not true LRU —
// the working set is a handful of tracks.
type artCache struct {
	max   int
	order []string
	items map[string]image.Image
}

func newArtCache(max int) *artCache {
	return &artCache{max: max, items: map[string]image.Image{}}
}

// get and put are nil-safe: artwork is decoration, so a model built without a
// cache (tests, future entry points) just goes uncached instead of crashing.
func (c *artCache) get(key string) (image.Image, bool) {
	if c == nil {
		return nil, false
	}
	img, ok := c.items[key]
	return img, ok
}

func (c *artCache) put(key string, img image.Image) {
	if c == nil || c.max <= 0 {
		return
	}
	if c.items == nil {
		c.items = map[string]image.Image{}
	}
	if _, ok := c.items[key]; ok {
		return
	}
	if len(c.order) >= c.max {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
	c.order = append(c.order, key)
	c.items[key] = img
}

// renderArtwork draws img into w columns and h rows using the upper half block:
// the glyph's foreground paints the upper pixel and its background the lower
// one, so every cell carries two vertical pixels.
func renderArtwork(img image.Image, w, h int) []string {
	if img == nil || w <= 0 || h <= 0 {
		return nil
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil
	}
	sample := func(cx, cy int) lipgloss.Color {
		x := b.Min.X + cx*b.Dx()/w
		y := b.Min.Y + cy*b.Dy()/(h*2)
		r, g, bl, _ := img.At(x, y).RGBA()
		return lipgloss.Color("#" +
			hex2(uint8(r>>8)) + hex2(uint8(g>>8)) + hex2(uint8(bl>>8)))
	}
	rows := make([]string, h)
	for row := 0; row < h; row++ {
		var sb strings.Builder
		for col := 0; col < w; col++ {
			top := sample(col, row*2)
			bottom := sample(col, row*2+1)
			sb.WriteString(lipgloss.NewStyle().
				Foreground(top).Background(bottom).Render("▀"))
		}
		rows[row] = sb.String()
	}
	return rows
}

// artworkMaxCols keeps the cover from dwarfing the lyrics on tall terminals.
const artworkMaxCols = 24

// artworkLayout sizes the cover for a lyrics panel of w columns and h rows.
// Terminal cells are roughly 1:2, so a square needs cols = 2*rows. Returns
// (0, 0) when the lyrics would be squeezed below minLyrics columns.
func artworkLayout(w, h, minLyrics int) (cols, rows int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	rows = h
	cols = rows * 2
	if cols > artworkMaxCols {
		cols = artworkMaxCols
		rows = cols / 2
	}
	if w-cols-1 < minLyrics {
		return 0, 0
	}
	return cols, rows
}

func hex2(v uint8) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[v>>4], digits[v&0x0F]})
}
