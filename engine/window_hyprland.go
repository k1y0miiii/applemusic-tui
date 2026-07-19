package engine

// Wayland forbids clients from positioning their own windows, and Hyprland
// ignores minimize, so the CDP offscreen/minimize strategy cannot hide the
// browser there. Instead the whole window is moved to a hidden special
// workspace via hyprctl; audio keeps playing because occlusion throttling is
// disabled at launch.

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/chromedp/chromedp"
)

var hyprctlRun = func(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "hyprctl", args...).Run()
}

var hyprctlAvailable = func() bool {
	_, err := exec.LookPath("hyprctl")
	return err == nil
}

type hyprlandWindowController struct{ pid int }

func (h hyprlandWindowController) hide(ctx context.Context) error {
	return hyprctlRun(ctx, "dispatch", "movetoworkspacesilent",
		fmt.Sprintf("special:amtui,pid:%d", h.pid))
}

func (h hyprlandWindowController) parkOffscreen(ctx context.Context) error {
	return h.hide(ctx)
}

func (h hyprlandWindowController) minimize(ctx context.Context) error {
	return h.hide(ctx)
}

// ponytail: no bring-back on Hyprland — hidden windows keep rendering, so
// playback starts fine in the special workspace; restoring would flash the
// browser over the TUI. Add movetoworkspace recovery if hidden stalls appear.
func (h hyprlandWindowController) ensurePlayable(ctx context.Context) error {
	return nil
}

func browserPID(ctx context.Context) int {
	if c := chromedp.FromContext(ctx); c != nil && c.Browser != nil {
		if p := c.Browser.Process(); p != nil {
			return p.Pid
		}
	}
	return 0
}

func newWindowController(pid int) windowController {
	if pid > 0 && os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" && hyprctlAvailable() {
		return hyprlandWindowController{pid: pid}
	}
	return cdpWindowController{}
}
