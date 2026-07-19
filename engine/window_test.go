package engine

import (
	"testing"

	cdpbrowser "github.com/chromedp/cdproto/browser"
)

func TestParkedWindowBoundsKeepBrowserNormal(t *testing.T) {
	got := parkedWindowBounds()

	if got.WindowState != cdpbrowser.WindowStateNormal {
		t.Fatalf("window state = %q, want %q", got.WindowState, cdpbrowser.WindowStateNormal)
	}
	if got.Left >= 0 || got.Top >= 0 {
		t.Fatalf("parked position = (%d, %d), want negative coordinates", got.Left, got.Top)
	}
	if got.Width != 1000 || got.Height != 700 {
		t.Fatalf("parked size = %dx%d, want 1000x700", got.Width, got.Height)
	}
}

func TestMinimizedWindowBoundsFullyHideBrowser(t *testing.T) {
	got := minimizedWindowBounds()
	if *got != (cdpbrowser.Bounds{WindowState: cdpbrowser.WindowStateMinimized}) {
		t.Fatalf("minimized bounds = %+v, want only minimized state", got)
	}
}

func TestPlayableWindowBoundsOnlyRestoreMinimizedWindow(t *testing.T) {
	normal := &cdpbrowser.Bounds{
		Left:        120,
		Top:         80,
		Width:       1000,
		Height:      700,
		WindowState: cdpbrowser.WindowStateNormal,
	}
	if got := playableWindowBounds(normal, true); got != nil {
		t.Fatalf("normal window would be repositioned to %+v", got)
	}

	minimized := &cdpbrowser.Bounds{WindowState: cdpbrowser.WindowStateMinimized}
	got := playableWindowBounds(minimized, true)
	if got == nil {
		t.Fatal("minimized hidden window was not restored")
	}
	if got.WindowState != cdpbrowser.WindowStateNormal || got.Left >= 0 || got.Top >= 0 {
		t.Fatalf("hidden restore bounds = %+v, want normal offscreen", got)
	}

	got = playableWindowBounds(minimized, false)
	if got == nil {
		t.Fatal("minimized debug window was not restored")
	}
	if *got != (cdpbrowser.Bounds{WindowState: cdpbrowser.WindowStateNormal}) {
		t.Fatalf("debug restore bounds = %+v, want only normal state", got)
	}
}
