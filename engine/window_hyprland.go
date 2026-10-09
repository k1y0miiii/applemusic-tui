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
	// Hyprland 0.56 replaced the keybind dispatchers with a Lua API, so the
	// legacy `dispatch movetoworkspacesilent special:amtui,pid:N` form no
	// longer parses (exit status 7). Try the Lua form first, then fall back
	// for Hyprland versions that still use the string dispatcher.
	if err := hyprctlRun(ctx, "eval", hyprlandHideScript(h.pid)); err == nil {
		return nil
	}
	return hyprctlRun(ctx, "dispatch", "movetoworkspacesilent",
		fmt.Sprintf("special:amtui,pid:%d", h.pid))
}

// hyprlandHideScript moves the window owning pid to the hidden special
// workspace without following it. hl.get_window resolves the pid selector;
// a missing window is left alone rather than moving whatever is focused.
func hyprlandHideScript(pid int) string {
	return fmt.Sprintf(`local w = hl.get_window("pid:%d")
if not w then return end
hl.dispatch(hl.dsp.window.move({ window = w, workspace = "special:amtui", follow = false }))`, pid)
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
	return defaultWindowController()
}
