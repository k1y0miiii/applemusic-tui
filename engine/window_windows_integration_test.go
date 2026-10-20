//go:build windows && integration

package engine

import (
	"context"
	"testing"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// TestWindowsParkedPageStaysVisible exercises the cdpWindowController lifecycle
// against a real headed Chrome/Edge on a Windows runner (windows-latest ships
// both). It mirrors window_darwin_integration_test.go's shape.
func TestWindowsParkedPageStaysVisible(t *testing.T) {
	browser := browserPath()
	if browser == "" {
		t.Skip("no Chrome or Edge found")
	}
	t.Setenv("AMTUI_CHROME", browser)
	t.Setenv("AMTUI_DEBUG", "")

	ctx, cancels := launch(t.TempDir(), false)
	t.Cleanup(func() {
		if c := chromedp.FromContext(ctx); c != nil && c.Browser != nil {
			if process := c.Browser.Process(); process != nil {
				_ = process.Kill()
			}
		}
		closeAll(cancels)
	})

	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(testCtx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("launch headed browser: %v", err)
	}

	windows := defaultWindowController()

	if err := windows.parkOffscreen(testCtx); err != nil {
		t.Fatalf("park window offscreen: %v", err)
	}
	assertVisible(t, testCtx, "after parkOffscreen")
	logWindowBounds(t, testCtx)

	// Informational only: on Linux the lifecycle minimizes then recovers via
	// ensurePlayable on a hidden stall, and CDP minimize on Windows is expected
	// to flip visibilityState to hidden rather than keep the page alive.
	if err := windows.minimize(testCtx); err != nil {
		t.Fatalf("minimize window: %v", err)
	}
	t.Logf("document.visibilityState after minimize = %q", readVisibility(t, testCtx))

	if err := windows.ensurePlayable(testCtx); err != nil {
		t.Fatalf("ensurePlayable: %v", err)
	}
	assertVisible(t, testCtx, "after ensurePlayable")

	var bounds *cdpbrowser.Bounds
	if err := chromedp.Run(testCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, got, err := cdpbrowser.GetWindowForTarget().Do(ctx)
		bounds = got
		return err
	})); err != nil {
		t.Fatalf("read window bounds: %v", err)
	}
	if bounds == nil || bounds.WindowState != cdpbrowser.WindowStateNormal {
		t.Fatalf("window bounds = %+v, want state normal", bounds)
	}
}

func readVisibility(t *testing.T, ctx context.Context) string {
	t.Helper()
	var visibility string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.visibilityState`, &visibility)); err != nil {
		t.Fatalf("read document visibility: %v", err)
	}
	return visibility
}

func assertVisible(t *testing.T, ctx context.Context, when string) {
	t.Helper()
	if got := readVisibility(t, ctx); got != "visible" {
		t.Fatalf("document.visibilityState %s = %q, want visible", when, got)
	}
}

func logWindowBounds(t *testing.T, ctx context.Context) {
	t.Helper()
	var bounds *cdpbrowser.Bounds
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, got, err := cdpbrowser.GetWindowForTarget().Do(ctx)
		bounds = got
		return err
	})); err != nil {
		t.Fatalf("read window bounds: %v", err)
	}
	t.Logf("window bounds = %+v", bounds)
}
