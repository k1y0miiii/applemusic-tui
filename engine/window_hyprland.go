package engine

// Wayland forbids clients from positioning their own windows, and Hyprland
// ignores minimize, so the CDP offscreen/minimize strategy cannot hide the
// browser there. Instead the whole window is moved to a hidden special
// workspace via hyprctl; audio keeps playing because occlusion throttling is
// disabled at launch.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/chromedp/chromedp"
)

var hyprctlRun = func(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "hyprctl", args...).Run()
}

var hyprctlOutput = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "hyprctl", args...).Output()
}

var hyprctlAvailable = func() bool {
	_, err := exec.LookPath("hyprctl")
	return err == nil
}

type hyprlandWindowController struct{ pid int }

// hide moves the window to the hidden special workspace. hyprctl's exit code
// alone cannot tell us it worked: without the Lua config manager (e.g.
// Hyprland 0.55) `eval` replies "eval is only supported with the lua config
// manager" yet still exits 0, and the Lua script itself
// exits 0 when the window isn't mapped yet. So success is judged by where the
// window actually ended up (via `hyprctl clients -j`), retrying both forms
// until it's parked or ctx runs out.
func (h hyprlandWindowController) hide(ctx context.Context) error {
	for {
		evalErr := hyprctlRun(ctx, "eval", hyprlandHideScript(h.pid))
		ok, known := h.parked(ctx)
		if !known {
			// Can't verify placement; fall back to the old exit-code check
			// so startup still works if `clients -j` is ever unavailable.
			if evalErr == nil {
				return nil
			}
			return hyprctlRun(ctx, "dispatch", "movetoworkspacesilent",
				fmt.Sprintf("special:amtui,pid:%d", h.pid))
		}
		if ok {
			return nil
		}

		_ = hyprctlRun(ctx, "dispatch", "movetoworkspacesilent",
			fmt.Sprintf("special:amtui,pid:%d", h.pid))
		if ok, _ := h.parked(ctx); ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("browser window (pid %d) not moved to special:amtui: %w", h.pid, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// parked reports whether every window owned by h.pid currently sits on the
// special:amtui workspace. known is false when the client list can't be
// read or decoded, meaning placement can't be verified at all.
func (h hyprlandWindowController) parked(ctx context.Context) (ok bool, known bool) {
	out, err := hyprctlOutput(ctx, "clients", "-j")
	if err != nil {
		return false, false
	}
	var clients []struct {
		Pid       int `json:"pid"`
		Workspace struct {
			Name string `json:"name"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(out, &clients); err != nil {
		return false, false
	}
	found := false
	for _, c := range clients {
		if c.Pid != h.pid {
			continue
		}
		found = true
		if c.Workspace.Name != "special:amtui" {
			return false, true
		}
	}
	return found, true
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
