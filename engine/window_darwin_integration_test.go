//go:build darwin && integration

package engine

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

func integrationChrome(t *testing.T) string {
	t.Helper()

	if configured := os.Getenv("AMTUI_CHROME"); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			t.Skipf("Chrome unavailable at AMTUI_CHROME=%q: %v", configured, err)
		}
		return path
	}

	for _, candidate := range []string{
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}

	t.Skip("Chrome unavailable")
	return ""
}

func TestHiddenPageRemainsVisibleAfterParking(t *testing.T) {
	t.Setenv("AMTUI_CHROME", integrationChrome(t))
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

	testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := chromedp.Run(testCtx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("launch headed Chrome: %v", err)
	}
	if err := parkOffscreen(testCtx); err != nil {
		t.Fatalf("park Chrome window: %v", err)
	}

	var visibility string
	if err := chromedp.Run(testCtx, chromedp.Evaluate(`document.visibilityState`, &visibility)); err != nil {
		t.Fatalf("read document visibility: %v", err)
	}
	if visibility != "visible" {
		t.Fatalf("document.visibilityState = %q, want visible", visibility)
	}

	var bounds *cdpbrowser.Bounds
	if err := chromedp.Run(testCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, got, err := cdpbrowser.GetWindowForTarget().Do(ctx)
		bounds = got
		return err
	})); err != nil {
		t.Fatalf("read Chrome window bounds: %v", err)
	}
	if bounds == nil || bounds.WindowState != cdpbrowser.WindowStateNormal {
		t.Fatalf("window bounds = %+v, want state normal", bounds)
	}
}
