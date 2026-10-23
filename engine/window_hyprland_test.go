package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

// client is the minimal shape `hyprctl clients -j` reports, for building
// fake responses in tests.
type client struct {
	Pid       int `json:"pid"`
	Workspace struct {
		Name string `json:"name"`
	} `json:"workspace"`
}

func clientsJSON(t *testing.T, cs ...client) []byte {
	t.Helper()
	b, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("marshal clients: %v", err)
	}
	return b
}

func stubHyprctlOutput(t *testing.T, fn func(ctx context.Context, args ...string) ([]byte, error)) {
	t.Helper()
	orig := hyprctlOutput
	hyprctlOutput = fn
	t.Cleanup(func() { hyprctlOutput = orig })
}

func parkedClient(pid int) client {
	c := client{Pid: pid}
	c.Workspace.Name = "special:amtui"
	return c
}

func unparkedClient(pid int) client {
	c := client{Pid: pid}
	c.Workspace.Name = "1"
	return c
}

func TestHyprlandControllerHidesIntoSpecialWorkspaceByPID(t *testing.T) {
	calls := captureHyprctl(t)
	stubHyprctlOutput(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return clientsJSON(t, parkedClient(42)), nil
	})
	h := hyprlandWindowController{pid: 42}

	if err := h.minimize(context.Background()); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	if err := h.parkOffscreen(context.Background()); err != nil {
		t.Fatalf("parkOffscreen: %v", err)
	}

	want := "eval " + hyprlandHideScript(42)
	if len(*calls) != 2 || (*calls)[0] != want || (*calls)[1] != want {
		t.Fatalf("hyprctl calls = %q, want two of %q", *calls, want)
	}
}

// Old Hyprland: `eval` exits 0 (hyprctl treats it as an unknown request) but
// the window never actually moves. hide must notice via clients -j and fall
// back to the legacy dispatch, which then succeeds.
func TestHyprlandControllerFallsBackToLegacyDispatchOnOldHyprland(t *testing.T) {
	var calls []string
	dispatchCount := 0
	parked := false
	orig := hyprctlRun
	hyprctlRun = func(ctx context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "dispatch" {
			dispatchCount++
			parked = true
		}
		return nil // old hyprctl: both eval and dispatch exit 0
	}
	t.Cleanup(func() { hyprctlRun = orig })

	stubHyprctlOutput(t, func(ctx context.Context, args ...string) ([]byte, error) {
		if parked {
			return clientsJSON(t, parkedClient(42)), nil
		}
		return clientsJSON(t, unparkedClient(42)), nil
	})

	h := hyprlandWindowController{pid: 42}
	if err := h.hide(context.Background()); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if dispatchCount != 1 {
		t.Fatalf("legacy dispatch calls = %d, want 1", dispatchCount)
	}

	want := []string{
		"eval " + hyprlandHideScript(42),
		"dispatch movetoworkspacesilent special:amtui,pid:42",
	}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("hyprctl calls = %q, want %q", calls, want)
	}
}

// Modern Hyprland: eval actually parks the window, so legacy dispatch must
// never be invoked.
func TestHyprlandControllerSkipsLegacyDispatchWhenEvalWorks(t *testing.T) {
	calls := captureHyprctl(t)
	stubHyprctlOutput(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return clientsJSON(t, parkedClient(42)), nil
	})

	h := hyprlandWindowController{pid: 42}
	if err := h.hide(context.Background()); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != "eval "+hyprlandHideScript(42) {
		t.Fatalf("hyprctl calls = %q, want exactly one eval call", *calls)
	}
}

// clients -j unavailable: fall back to the old exit-code behaviour (eval
// succeeds -> hide succeeds, no legacy dispatch attempted).
func TestHyprlandControllerFallsBackToExitCodeWhenClientsUnavailable(t *testing.T) {
	calls := captureHyprctl(t)
	stubHyprctlOutput(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("clients -j unsupported")
	})

	h := hyprlandWindowController{pid: 42}
	if err := h.hide(context.Background()); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != "eval "+hyprlandHideScript(42) {
		t.Fatalf("hyprctl calls = %q, want exactly one eval call", *calls)
	}
}

// Window never ends up on special:amtui: hide must give up once ctx expires.
func TestHyprlandControllerHideTimesOutIfNeverParked(t *testing.T) {
	captureHyprctl(t)
	stubHyprctlOutput(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return clientsJSON(t, unparkedClient(42)), nil
	})

	h := hyprlandWindowController{pid: 42}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if err := h.hide(ctx); err == nil {
		t.Fatal("hide: want error on timeout, got nil")
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
	if got := newWindowController(0); got != defaultWindowController() {
		t.Fatalf("controller with unknown pid = %T, want the platform default", got)
	}

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	if got := newWindowController(42); got != defaultWindowController() {
		t.Fatalf("controller outside Hyprland = %T, want the platform default", got)
	}
}

func TestNewWindowControllerFallsBackWithoutHyprctl(t *testing.T) {
	origLook := hyprctlAvailable
	hyprctlAvailable = func() bool { return false }
	t.Cleanup(func() { hyprctlAvailable = origLook })

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "sig")
	if got := newWindowController(42); got != defaultWindowController() {
		t.Fatalf("controller without hyprctl = %T, want the platform default", got)
	}
}
