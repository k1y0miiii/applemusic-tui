package engine

import (
	"context"
	"testing"

	cdpbrowser "github.com/chromedp/cdproto/browser"
)

func captureBounds(t *testing.T) *[]cdpbrowser.Bounds {
	t.Helper()
	var got []cdpbrowser.Bounds
	orig := setWindowBounds
	setWindowBounds = func(_ context.Context, b *cdpbrowser.Bounds) error {
		got = append(got, *b)
		return nil
	}
	t.Cleanup(func() { setWindowBounds = orig })
	return &got
}

// The whole point of the darwin controller: a hidden window is a hidden page,
// and a hidden page never starts the next track.
func TestDarwinControllerNeverHidesTheWindow(t *testing.T) {
	got := captureBounds(t)
	d := darwinWindowController{}

	if err := d.parkOffscreen(context.Background()); err != nil {
		t.Fatalf("parkOffscreen: %v", err)
	}
	if err := d.minimize(context.Background()); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	if err := d.ensurePlayable(context.Background()); err != nil {
		t.Fatalf("ensurePlayable: %v", err)
	}

	if len(*got) != 2 {
		t.Fatalf("window moves = %d, want 2 (park and minimize; ensurePlayable is a no-op)", len(*got))
	}
	for i, b := range *got {
		if b.WindowState != cdpbrowser.WindowStateNormal {
			t.Fatalf("move %d asked for state %q, want %q", i, b.WindowState, cdpbrowser.WindowStateNormal)
		}
		if b != *darwinParkedBounds() {
			t.Fatalf("move %d = %+v, want the parked corner %+v", i, b, *darwinParkedBounds())
		}
	}
}

// Chrome clamps the window back onto the screen, so the far bottom-left corner
// is what leaves the smallest visible nub — a negative Top would leave a strip
// down the whole left edge instead.
func TestDarwinParkedBoundsAimAtTheBottomLeftCorner(t *testing.T) {
	got := darwinParkedBounds()

	if got.Left >= 0 || got.Top <= 0 {
		t.Fatalf("parked position = (%d, %d), want left off-screen and top below the desktop", got.Left, got.Top)
	}
	if got.Width != 1000 || got.Height != 700 {
		t.Fatalf("parked size = %dx%d, want 1000x700", got.Width, got.Height)
	}
}
