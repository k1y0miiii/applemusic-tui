package engine

// macOS has no way to hide a window and keep its page alive. Minimizing it, and
// hiding the whole app with Cmd+H, both flip document.visibilityState to
// hidden, and Apple Music will not build the MSE/DRM pipeline for the next
// track while hidden — the engine then noticed the stall and un-minimized to
// recover, so the browser jumped into the user's face after every track change.
//
// So on darwin the window is never hidden at all, only parked. Chrome clamps a
// window to keep about 40x40 px of it on screen, so asking for the far
// bottom-left corner leaves a 40x40 nub sitting behind every other window,
// instead of the full-height 40px strip a negative Top leaves at the left edge.
// Measured on macOS 26: (-32000, 32000) lands at (-960, 1199) on a 2056x1329
// desktop and document.visibilityState stays "visible".

import (
	"context"

	cdpbrowser "github.com/chromedp/cdproto/browser"
)

func darwinParkedBounds() *cdpbrowser.Bounds {
	return &cdpbrowser.Bounds{
		Left:        -32000,
		Top:         32000,
		Width:       1000,
		Height:      700,
		WindowState: cdpbrowser.WindowStateNormal,
	}
}

type darwinWindowController struct{}

func (darwinWindowController) parkOffscreen(ctx context.Context) error {
	return setWindowBounds(ctx, darwinParkedBounds())
}

// minimize re-parks instead of minimizing: a minimized window is a hidden page,
// and a hidden page cannot start the next track.
func (d darwinWindowController) minimize(ctx context.Context) error {
	return d.parkOffscreen(ctx)
}

// ponytail: nothing to restore — the window is never hidden, so hiddenStall
// cannot fire. Add a recovery here if a parked window ever stalls anyway.
func (darwinWindowController) ensurePlayable(context.Context) error { return nil }

func defaultWindowController() windowController { return darwinWindowController{} }
