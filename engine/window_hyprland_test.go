package engine

import (
	"context"
	"strings"
	"testing"
)

func captureHyprctl(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	orig := hyprctlRun
	hyprctlRun = func(ctx context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { hyprctlRun = orig })
	return &calls
}

func TestHyprlandControllerHidesIntoSpecialWorkspaceByPID(t *testing.T) {
	calls := captureHyprctl(t)
	h := hyprlandWindowController{pid: 42}

	if err := h.minimize(context.Background()); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	if err := h.parkOffscreen(context.Background()); err != nil {
		t.Fatalf("parkOffscreen: %v", err)
	}

	want := "dispatch movetoworkspacesilent special:amtui,pid:42"
	if len(*calls) != 2 || (*calls)[0] != want || (*calls)[1] != want {
		t.Fatalf("hyprctl calls = %q, want two of %q", *calls, want)
	}
}

func TestHyprlandControllerEnsurePlayableIsNoOp(t *testing.T) {
	calls := captureHyprctl(t)
	h := hyprlandWindowController{pid: 42}

	if err := h.ensurePlayable(context.Background()); err != nil {
		t.Fatalf("ensurePlayable: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("ensurePlayable ran hyprctl: %q", *calls)
	}
}

func TestNewWindowControllerSelectsHyprlandOnlyWithSignatureAndPID(t *testing.T) {
	origLook := hyprctlAvailable
	hyprctlAvailable = func() bool { return true }
	t.Cleanup(func() { hyprctlAvailable = origLook })

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "sig")
	if _, ok := newWindowController(42).(hyprlandWindowController); !ok {
		t.Fatal("want hyprland controller with signature set and pid known")
	}
	if _, ok := newWindowController(0).(cdpWindowController); !ok {
		t.Fatal("want cdp controller when browser pid is unknown")
	}

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	if _, ok := newWindowController(42).(cdpWindowController); !ok {
		t.Fatal("want cdp controller outside Hyprland")
	}
}

func TestNewWindowControllerFallsBackWithoutHyprctl(t *testing.T) {
	origLook := hyprctlAvailable
	hyprctlAvailable = func() bool { return false }
	t.Cleanup(func() { hyprctlAvailable = origLook })

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "sig")
	if _, ok := newWindowController(42).(cdpWindowController); !ok {
		t.Fatal("want cdp controller when hyprctl is missing")
	}
}
