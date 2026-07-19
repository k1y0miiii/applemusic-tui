package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeWindowController struct {
	parkCalls     int
	ensureCalls   int
	minimizeCalls int
	parkErr       error
	ensureErr     error
	minimizeErr   error
}

func (f *fakeWindowController) parkOffscreen(context.Context) error {
	f.parkCalls++
	return f.parkErr
}

func (f *fakeWindowController) ensurePlayable(context.Context) error {
	f.ensureCalls++
	return f.ensureErr
}

func (f *fakeWindowController) minimize(context.Context) error {
	f.minimizeCalls++
	return f.minimizeErr
}

func installLifecyclePlayer(
	t *testing.T,
	ctx context.Context,
	visibility string,
	playbackState int,
	deferred bool,
	mediaState string,
	sourceBuffers int,
	fullyBuffered bool,
	audioReady int,
	paused bool,
) {
	t.Helper()
	deferredJS := "undefined"
	if deferred {
		deferredJS = "{}"
	}
	eval(t, ctx, fmt.Sprintf(`(() => {
	  Object.defineProperty(document, 'visibilityState', {
	    configurable: true,
	    get: () => %s,
	  });
	  const audio = { paused: %t, readyState: %d };
	  const cp = {
	    _deferredPlay: %s,
	    _buffer: {
	      mediaSource: {
	        readyState: %s,
	        sourceBuffers: { length: %d },
	      },
	      isFullyBuffered: %t,
	    },
	  };
	  const mk = {
	    playbackState: %d,
	    nowPlayingItem: {},
	    currentPlaybackTime: 1,
	    currentPlaybackDuration: 180,
	    volume: 1,
	    queue: { position: 0, items: [] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	  };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	})()`,
		jsStr(visibility),
		paused,
		audioReady,
		deferredJS,
		jsStr(mediaState),
		sourceBuffers,
		fullyBuffered,
		playbackState,
	), nil)
}

func TestEnginePrimesOnceAndStopsWakingActions(t *testing.T) {
	ctx := testPage(t)
	installLifecyclePlayer(t, ctx, "visible", 1, true, "open", 1, false, 0, false)
	windows := &fakeWindowController{}
	e := &Engine{ctx: ctx, park: true, windows: windows}

	if err := e.do(`true`); err != nil {
		t.Fatal(err)
	}
	if windows.ensureCalls != 1 {
		t.Fatalf("pre-prime ensure calls = %d, want 1", windows.ensureCalls)
	}

	installLifecyclePlayer(t, ctx, "visible", 2, false, "ended", 1, false, 4, false)
	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if windows.minimizeCalls != 1 || !e.primed {
		t.Fatalf("first ready poll: minimize calls=%d primed=%v", windows.minimizeCalls, e.primed)
	}

	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if err := e.do(`true`); err != nil {
		t.Fatal(err)
	}
	if windows.minimizeCalls != 1 {
		t.Fatalf("ready polls minimized %d times, want once", windows.minimizeCalls)
	}
	if windows.ensureCalls != 1 {
		t.Fatalf("primed action woke browser: ensure calls=%d", windows.ensureCalls)
	}
}

func TestEngineOnlyPrimesAfterSuccessfulMinimize(t *testing.T) {
	ctx := testPage(t)
	installLifecyclePlayer(t, ctx, "visible", 2, false, "ended", 1, false, 4, false)
	windows := &fakeWindowController{minimizeErr: errors.New("minimize failed")}
	e := &Engine{ctx: ctx, park: true, windows: windows}

	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if e.primed {
		t.Fatal("engine primed after failed minimize")
	}
	if err := e.do(`true`); err != nil {
		t.Fatal(err)
	}
	if windows.ensureCalls != 1 {
		t.Fatalf("unprimed action ensure calls = %d, want 1", windows.ensureCalls)
	}
	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if windows.minimizeCalls != 2 {
		t.Fatalf("failed minimize attempts = %d, want retry on next ready poll", windows.minimizeCalls)
	}
}

func TestDebugEngineNeverControlsWindowLifecycle(t *testing.T) {
	ctx := testPage(t)
	installLifecyclePlayer(t, ctx, "visible", 2, false, "ended", 1, false, 4, false)
	windows := &fakeWindowController{}
	e := &Engine{ctx: ctx, park: false, windows: windows}

	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if err := e.do(`true`); err != nil {
		t.Fatal(err)
	}
	if windows.minimizeCalls != 0 || windows.ensureCalls != 0 || windows.parkCalls != 0 {
		t.Fatalf("debug window was controlled: %+v", windows)
	}
	if e.primed {
		t.Fatal("debug engine should not enter minimized lifecycle")
	}
}

func TestHiddenStallWakesAndReadyRecoveryMinimizesAgain(t *testing.T) {
	ctx := testPage(t)
	installLifecyclePlayer(t, ctx, "hidden", 1, true, "closed", 0, false, 0, false)
	windows := &fakeWindowController{}
	e := &Engine{ctx: ctx, park: true, primed: true, windows: windows}

	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if windows.ensureCalls != 1 || e.primed {
		t.Fatalf("hidden stall: ensure calls=%d primed=%v", windows.ensureCalls, e.primed)
	}

	installLifecyclePlayer(t, ctx, "visible", 2, false, "ended", 1, false, 4, false)
	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if windows.minimizeCalls != 1 || !e.primed {
		t.Fatalf("ready recovery: minimize calls=%d primed=%v", windows.minimizeCalls, e.primed)
	}
}

func TestPrimedEngineIgnoresNonStalledHiddenStates(t *testing.T) {
	tests := []struct {
		name          string
		playbackState int
		deferred      bool
		mediaState    string
		sourceBuffers int
		audioReady    int
		paused        bool
	}{
		{
			name:          "healthy playback",
			playbackState: 2,
			mediaState:    "ended",
			sourceBuffers: 1,
			audioReady:    4,
		},
		{
			name:          "paused",
			playbackState: 3,
			mediaState:    "ended",
			sourceBuffers: 1,
			audioReady:    4,
			paused:        true,
		},
		{
			name:          "open MSE loading",
			playbackState: 1,
			deferred:      true,
			mediaState:    "open",
			sourceBuffers: 1,
			audioReady:    0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testPage(t)
			installLifecyclePlayer(
				t,
				ctx,
				"hidden",
				test.playbackState,
				test.deferred,
				test.mediaState,
				test.sourceBuffers,
				false,
				test.audioReady,
				test.paused,
			)
			windows := &fakeWindowController{}
			e := &Engine{ctx: ctx, park: true, primed: true, windows: windows}

			if _, err := e.State(); err != nil {
				t.Fatal(err)
			}
			if windows.ensureCalls != 0 || windows.minimizeCalls != 0 {
				t.Fatalf("non-stall controlled window: %+v", windows)
			}
			if !e.primed {
				t.Fatal("non-stall unprimed engine")
			}
		})
	}
}

func TestPrepareReloadWakesAndRestartsPrimingLifecycle(t *testing.T) {
	ctx := testPage(t)
	installLifecyclePlayer(t, ctx, "visible", 1, true, "open", 1, false, 0, false)
	windows := &fakeWindowController{}
	e := &Engine{ctx: ctx, park: true, primed: true, windows: windows}

	e.mu.Lock()
	err := e.prepareReloadLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if e.primed || windows.parkCalls != 1 {
		t.Fatalf("reload preparation: primed=%v park calls=%d", e.primed, windows.parkCalls)
	}

	if err := e.do(`true`); err != nil {
		t.Fatal(err)
	}
	if windows.ensureCalls != 1 {
		t.Fatalf("first post-reload action ensure calls=%d, want 1", windows.ensureCalls)
	}
	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if windows.minimizeCalls != 0 {
		t.Fatalf("not-ready reload minimized %d times", windows.minimizeCalls)
	}

	installLifecyclePlayer(t, ctx, "visible", 2, false, "ended", 1, false, 4, false)
	if _, err := e.State(); err != nil {
		t.Fatal(err)
	}
	if !e.primed || windows.minimizeCalls != 1 {
		t.Fatalf("ready reload: primed=%v minimize calls=%d", e.primed, windows.minimizeCalls)
	}
}

func TestReloadWindowFailureUnprimesAndStopsBeforeNavigation(t *testing.T) {
	windowErr := errors.New("cannot restore window")
	windows := &fakeWindowController{parkErr: windowErr}
	e := &Engine{
		ctx:     context.Background(),
		park:    true,
		primed:  true,
		windows: windows,
	}

	err := e.Reload()
	if !errors.Is(err, windowErr) {
		t.Fatalf("Reload error = %v, want wrapped window error", err)
	}
	if !strings.Contains(err.Error(), "reload") || !strings.Contains(err.Error(), "window") {
		t.Fatalf("Reload error lacks context: %v", err)
	}
	if e.primed {
		t.Fatal("failed reload preparation left engine primed")
	}
	if windows.parkCalls != 1 {
		t.Fatalf("failed reload park calls=%d, want 1", windows.parkCalls)
	}
}

func TestPrepareReloadDoesNotTouchDebugWindow(t *testing.T) {
	windows := &fakeWindowController{}
	e := &Engine{
		ctx:     context.Background(),
		park:    false,
		windows: windows,
	}

	e.mu.Lock()
	err := e.prepareReloadLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if windows.ensureCalls != 0 || windows.minimizeCalls != 0 || windows.parkCalls != 0 {
		t.Fatalf("debug reload controlled window: %+v", windows)
	}
}
