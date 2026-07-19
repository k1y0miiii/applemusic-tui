package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func testPage(t *testing.T) context.Context {
	t.Helper()

	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
		)...,
	)
	t.Cleanup(allocCancel)

	ctx, cancel := chromedp.NewContext(allocCtx)
	t.Cleanup(cancel)
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func eval(t *testing.T, ctx context.Context, js string, out any) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, out)); err != nil {
		t.Fatal(err)
	}
}

func waitForJS(t *testing.T, ctx context.Context, condition string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ready bool
		eval(t, ctx, condition, &ready)
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", condition)
}

func readStateSignals(t *testing.T, ctx context.Context) struct {
	EngineReady bool `json:"engineReady"`
	HiddenStall bool `json:"hiddenStall"`
} {
	t.Helper()
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(stateJS, &raw, awaitPromise)); err != nil {
		t.Fatal(err)
	}
	var got struct {
		EngineReady bool `json:"engineReady"`
		HiddenStall bool `json:"hiddenStall"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStateReportsInitialPlaybackLoading(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const audio = { paused: false, readyState: 0 };
	  const cp = { _deferredPlay: {} };
	  const mk = {
	    playbackState: 1,
	    currentPlaybackTime: 0,
	    currentPlaybackDuration: 0,
	    volume: 1,
	    queue: { position: 0, items: [] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	  };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	})()`, nil)

	eval(t, ctx, stateJS+`.then((state) => { window.__state = state; })`, nil)
	var raw string
	eval(t, ctx, `window.__state`, &raw)
	var got struct {
		Initializing bool `json:"initializing"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Initializing {
		t.Fatal("state did not report initial playback loading")
	}
}

func TestStateEngineReadyRequiresFullyUsableMedia(t *testing.T) {
	tests := []struct {
		name   string
		buffer string
		want   bool
	}{
		{
			name: "MSE open and still buffering",
			buffer: `_buffer: {
			  mediaSource: { readyState: 'open', sourceBuffers: { length: 1 } },
			  isFullyBuffered: false,
			},`,
		},
		{
			name: "MSE ended",
			buffer: `_buffer: {
			  mediaSource: { readyState: 'ended', sourceBuffers: { length: 1 } },
			  isFullyBuffered: false,
			},`,
			want: true,
		},
		{
			name: "MSE reports fully buffered",
			buffer: `_buffer: {
			  mediaSource: { readyState: 'open', sourceBuffers: { length: 1 } },
			  isFullyBuffered: true,
			},`,
			want: true,
		},
		{
			name: "MSE has no attached source buffer",
			buffer: `_buffer: {
			  mediaSource: { readyState: 'ended', sourceBuffers: { length: 0 } },
			  isFullyBuffered: true,
			},`,
		},
		{
			name:   "non-MSE ready asset",
			buffer: `_buffer: { rawAsset: true },`,
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testPage(t)
			eval(t, ctx, fmt.Sprintf(`(() => {
			  const audio = { paused: false, readyState: 4 };
			  const cp = { %s };
			  const mk = {
			    playbackState: 2,
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
			})()`, test.buffer), nil)

			got := readStateSignals(t, ctx)
			if got.EngineReady != test.want {
				t.Fatalf("engineReady = %v, want %v", got.EngineReady, test.want)
			}
		})
	}
}

func TestStateHiddenStallRequiresExactMinimizedLoadingSignature(t *testing.T) {
	tests := []struct {
		name          string
		playbackState int
		deferred      bool
		mediaState    string
		sourceBuffers int
		audioReady    int
		paused        bool
		want          bool
	}{
		{
			name:          "exact hidden stall",
			playbackState: 1,
			deferred:      true,
			mediaState:    "closed",
			audioReady:    0,
			want:          true,
		},
		{
			name:          "healthy hidden playback",
			playbackState: 2,
			mediaState:    "ended",
			sourceBuffers: 1,
			audioReady:    4,
		},
		{
			name:          "hidden pause",
			playbackState: 3,
			mediaState:    "ended",
			sourceBuffers: 1,
			audioReady:    4,
			paused:        true,
		},
		{
			name:          "hidden MSE loading normally",
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
			deferred := "undefined"
			if test.deferred {
				deferred = "{}"
			}
			eval(t, ctx, fmt.Sprintf(`(() => {
			  Object.defineProperty(document, 'visibilityState', {
			    configurable: true,
			    get: () => 'hidden',
			  });
			  const audio = { paused: %t, readyState: %d };
			  const cp = {
			    _deferredPlay: %s,
			    _buffer: {
			      mediaSource: {
			        readyState: %s,
			        sourceBuffers: { length: %d },
			      },
			    },
			  };
			  const mk = {
			    playbackState: %d,
			    nowPlayingItem: {},
			    currentPlaybackTime: 0,
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
				test.paused,
				test.audioReady,
				deferred,
				jsStr(test.mediaState),
				test.sourceBuffers,
				test.playbackState,
			), nil)

			got := readStateSignals(t, ctx)
			if got.HiddenStall != test.want {
				t.Fatalf("hiddenStall = %v, want %v", got.HiddenStall, test.want)
			}
		})
	}
}

func TestJumpWaitsForColdPlaybackAndDoesNotAwaitSkipPromise(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const state = window.__mock = { pos: 0, calls: 0 };
	  const cp = { _deferredPlay: {} };
	  const queue = {
	    items: [{}, {}, {}, {}],
	    get position() { return state.pos; },
	  };
	  const mk = {
	    queue,
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    skipToNextItem() {
	      state.calls++;
	      setTimeout(() => state.pos++, 20);
	      return new Promise(() => {});
	    },
	    skipToPreviousItem() { return new Promise(() => {}); },
	  };
	  window.MusicKit = { getInstance: () => mk };
	  window.__mock.cp = cp;
	})()`, nil)

	var ok bool
	eval(t, ctx, fmt.Sprintf(jumpJS, 3), &ok)
	if !ok {
		t.Fatal("jump snippet returned false")
	}

	time.Sleep(80 * time.Millisecond)
	var calls int
	eval(t, ctx, `window.__mock.calls`, &calls)
	if calls != 0 {
		t.Fatalf("skip started during cold playback: calls=%d", calls)
	}

	eval(t, ctx, `window.__mock.cp._deferredPlay = undefined`, nil)
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		var pos int
		eval(t, ctx, `window.__mock.pos`, &pos)
		if pos == 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	var pos int
	eval(t, ctx, `window.__mock.pos`, &pos)
	t.Fatalf("jump remained slow with pending MusicKit promises: position=%d", pos)
}

func TestPauseLetsMusicKitPauseBeforeNativeFallback(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const state = window.__mock = {
	    internalSawPlaying: false,
	    nativePauseCalls: 0,
	  };
	  const audio = {
	    paused: false,
	    readyState: 4,
	    pause() {
	      state.nativePauseCalls++;
	      this.paused = true;
	    },
	  };
	  const cp = {
	    pause() {
	      return Promise.resolve().then(() => {
	        state.internalSawPlaying = !audio.paused;
	        audio.paused = true;
	      });
	    },
	  };
	  const mk = {
	    playbackState: 2,
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	  };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	})()`, nil)

	var ok bool
	eval(t, ctx, playPauseJS, &ok)
	if !ok {
		t.Fatal("play/pause snippet returned false")
	}
	time.Sleep(30 * time.Millisecond)

	var got struct {
		InternalSawPlaying bool `json:"internalSawPlaying"`
		NativePauseCalls   int  `json:"nativePauseCalls"`
	}
	eval(t, ctx, `window.__mock`, &got)
	if !got.InternalSawPlaying {
		t.Fatal("native pause ran before MusicKit's internal pause")
	}
	if got.NativePauseCalls != 0 {
		t.Fatalf("unexpected native pause fallback: calls=%d", got.NativePauseCalls)
	}
}

func TestTransportActionsRemainStandaloneIIFEs(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const state = window.__mock = {
	    nextCalls: 0,
	    prevCalls: 0,
	    setQueueCalls: 0,
	    queue: null,
	    legacyVisualizerReads: 0,
	  };
	  const legacyKey = '__amtui' + 'Viz';
	  Object.defineProperty(window, legacyKey, {
	    configurable: true,
	    get() {
	      state.legacyVisualizerReads++;
	      return null;
	    },
	  });
	  const pending = new Promise(() => {});
	  const mk = {
	    skipToNextItem() {
	      state.nextCalls++;
	      return pending;
	    },
	    skipToPreviousItem() {
	      state.prevCalls++;
	      return pending;
	    },
	    setQueue(queue) {
	      state.setQueueCalls++;
	      state.queue = queue;
	      return pending;
	    },
	  };
	  window.MusicKit = { getInstance: () => mk };
	})()`, nil)

	for name, script := range map[string]string{
		"next":      nextJS,
		"previous":  prevJS,
		"play item": fmt.Sprintf(playItemJS, "song", jsStr("song-1")),
	} {
		t.Run(name, func(t *testing.T) {
			var ok bool
			eval(t, ctx, script, &ok)
			if !ok {
				t.Fatalf("%s snippet did not return true", name)
			}
		})
	}

	var got struct {
		NextCalls             int `json:"nextCalls"`
		PrevCalls             int `json:"prevCalls"`
		SetQueueCalls         int `json:"setQueueCalls"`
		LegacyVisualizerReads int `json:"legacyVisualizerReads"`
		Queue                 struct {
			Song         string `json:"song"`
			StartPlaying bool   `json:"startPlaying"`
		} `json:"queue"`
	}
	eval(t, ctx, `window.__mock`, &got)

	if got.NextCalls != 1 || got.PrevCalls != 1 || got.SetQueueCalls != 1 {
		t.Fatalf(
			"transport calls next=%d prev=%d setQueue=%d, want 1 each",
			got.NextCalls,
			got.PrevCalls,
			got.SetQueueCalls,
		)
	}
	if got.Queue.Song != "song-1" || !got.Queue.StartPlaying {
		t.Fatalf("setQueue argument = %+v", got.Queue)
	}
	if got.LegacyVisualizerReads != 0 {
		t.Fatalf("transport snippets touched legacy visualizer %d times", got.LegacyVisualizerReads)
	}
}

func TestInitialPlayWatchdogRecoversExactClosedMediaSourceStall(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const pending = new Promise(() => {});
	  const state = window.__mock = {
	    finishCalls: 0,
	    stopCalls: 0,
	    changeCalls: 0,
	    changeIndex: -1,
	    userInitiated: false,
	  };
	  const cp = {
	    _deferredPlay: {},
	    _buffer: {
	      mediaSource: {
	        readyState: 'closed',
	        sourceBuffers: { length: 0 },
	      },
	    },
	    finishPlaybackSequence() {
	      state.finishCalls++;
	    },
	    stopMediaAndCleanup() {
	      state.stopCalls++;
	      this._buffer = null;
	      return pending;
	    },
	  };
	  const mk = {
	    playbackState: 1,
	    queue: { position: 3, items: [{}, {}, {}, {}] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    _playbackController: {
	      _changeToMediaAtIndex(index, options) {
	        state.changeCalls++;
	        state.changeIndex = index;
	        state.userInitiated = options && options.userInitiated === true;
	        return pending;
	      },
	    },
	    setQueue() {
	      return pending;
	    },
	  };
	  const audio = { readyState: 0, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiWatchdogDelay = 0;
	})()`, nil)

	actionCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	script := fmt.Sprintf(playItemJS, "song", jsStr("song-1"))
	var immediate struct {
		OK          bool `json:"ok"`
		FinishCalls int  `json:"finishCalls"`
	}
	eval(t, actionCtx, fmt.Sprintf(`(() => {
	  const ok = %s;
	  return { ok, finishCalls: window.__mock.finishCalls };
	})()`, script), &immediate)
	cancel()
	if !immediate.OK {
		t.Fatal("play item snippet returned false")
	}
	if immediate.FinishCalls != 0 {
		t.Fatalf("watchdog ran synchronously before Evaluate returned: %+v", immediate)
	}

	waitForJS(t, ctx, `window.__mock.changeCalls === 1`, 500*time.Millisecond)
	var got struct {
		FinishCalls   int  `json:"finishCalls"`
		StopCalls     int  `json:"stopCalls"`
		ChangeCalls   int  `json:"changeCalls"`
		ChangeIndex   int  `json:"changeIndex"`
		UserInitiated bool `json:"userInitiated"`
	}
	eval(t, ctx, `window.__mock`, &got)
	if got.FinishCalls != 1 || got.StopCalls != 1 || got.ChangeCalls != 1 {
		t.Fatalf("recovery calls = %+v, want finish/stop/change once", got)
	}
	if got.ChangeIndex != 3 || !got.UserInitiated {
		t.Fatalf("retry arguments = index %d, userInitiated %v", got.ChangeIndex, got.UserInitiated)
	}
}

func TestInitialPlayWatchdogIgnoresNonStalledPlayback(t *testing.T) {
	tests := []struct {
		name          string
		playbackState int
		mediaState    string
		sourceBuffers int
		audioReady    int
	}{
		{
			name:          "playing",
			playbackState: 2,
			mediaState:    "closed",
			audioReady:    0,
		},
		{
			name:          "media source open",
			playbackState: 1,
			mediaState:    "open",
			audioReady:    0,
		},
		{
			name:          "source buffer attached",
			playbackState: 1,
			mediaState:    "closed",
			sourceBuffers: 1,
			audioReady:    0,
		},
		{
			name:          "audio has data",
			playbackState: 1,
			mediaState:    "closed",
			audioReady:    1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testPage(t)
			eval(t, ctx, fmt.Sprintf(`(() => {
			  const pending = new Promise(() => {});
			  const state = window.__mock = {
			    finishCalls: 0,
			    stopCalls: 0,
			    changeCalls: 0,
			  };
			  const cp = {
			    _deferredPlay: {},
			    _buffer: {
			      mediaSource: {
			        readyState: %s,
			        sourceBuffers: { length: %d },
			      },
			    },
			    finishPlaybackSequence() {
			      state.finishCalls++;
			    },
			    stopMediaAndCleanup() {
			      state.stopCalls++;
			      this._buffer = null;
			      return pending;
			    },
			  };
			  const mk = {
			    playbackState: %d,
			    queue: { position: 0, items: [{}] },
			    services: { mediaItemPlayback: { _currentPlayer: cp } },
			    _playbackController: {
			      _changeToMediaAtIndex() {
			        state.changeCalls++;
			        return pending;
			      },
			    },
			    setQueue() {
			      return pending;
			    },
			  };
			  const audio = { readyState: %d, paused: false };
			  window.MusicKit = { getInstance: () => mk };
			  const querySelector = document.querySelector.bind(document);
			  document.querySelector = (selector) =>
			    selector === 'audio' ? audio : querySelector(selector);
			  window.__amtuiWatchdogDelay = 0;
			})()`, jsStr(test.mediaState), test.sourceBuffers, test.playbackState, test.audioReady), nil)

			var ok bool
			eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
			if !ok {
				t.Fatal("play item snippet returned false")
			}
			time.Sleep(100 * time.Millisecond)

			var got struct {
				FinishCalls int `json:"finishCalls"`
				StopCalls   int `json:"stopCalls"`
				ChangeCalls int `json:"changeCalls"`
			}
			eval(t, ctx, `window.__mock`, &got)
			if got.FinishCalls != 0 || got.StopCalls != 0 || got.ChangeCalls != 0 {
				t.Fatalf("unexpected recovery calls: %+v", got)
			}
		})
	}
}

func TestInitialPlayWatchdogIsCancelledByNewPlayGeneration(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const pending = new Promise(() => {});
	  const state = window.__mock = {
	    finishCalls: 0,
	    stopCalls: 0,
	    changeCalls: 0,
	  };
	  const cp = {
	    _deferredPlay: {},
	    _buffer: {
	      mediaSource: {
	        readyState: 'closed',
	        sourceBuffers: { length: 0 },
	      },
	    },
	    finishPlaybackSequence() {
	      state.finishCalls++;
	    },
	    stopMediaAndCleanup() {
	      state.stopCalls++;
	      this._buffer = null;
	      return pending;
	    },
	  };
	  const mk = window.__mockMK = {
	    playbackState: 1,
	    queue: { position: 0, items: [{}] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    _playbackController: {
	      _changeToMediaAtIndex() {
	        state.changeCalls++;
	        return pending;
	      },
	    },
	    setQueue() {
	      return pending;
	    },
	  };
	  const audio = { readyState: 0, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiWatchdogDelay = 75;
	})()`, nil)

	var ok bool
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
	if !ok {
		t.Fatal("first play item snippet returned false")
	}
	eval(t, ctx, `window.__amtuiWatchdogDelay = 500`, nil)
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-2")), &ok)
	if !ok {
		t.Fatal("second play item snippet returned false")
	}

	time.Sleep(150 * time.Millisecond)
	var got struct {
		FinishCalls int `json:"finishCalls"`
		StopCalls   int `json:"stopCalls"`
		ChangeCalls int `json:"changeCalls"`
	}
	eval(t, ctx, `window.__mock`, &got)
	if got.FinishCalls != 0 || got.StopCalls != 0 || got.ChangeCalls != 0 {
		t.Fatalf("superseded watchdog recovered later play: %+v", got)
	}
}

func TestPlayItemSetQueueRejectionOnlyAffectsCurrentGeneration(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const rejects = window.__setQueueRejects = [];
	  const mk = {
	    playbackState: 2,
	    setQueue() {
	      return new Promise((_, reject) => rejects.push(reject));
	    },
	  };
	  const audio = { readyState: 4, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiWatchdogDelay = 0;
	})()`, nil)

	var ok bool
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
	if !ok {
		t.Fatal("first play item snippet returned false")
	}
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-2")), &ok)
	if !ok {
		t.Fatal("second play item snippet returned false")
	}

	eval(t, ctx, `(() => {
	  window.__amtuiErr = '';
	  window.__setQueueRejects[0](new Error('play 1 failed'));
	  Promise.resolve().then(() => { window.__oldSetQueueRejectionFlushed = true; });
	})()`, nil)
	waitForJS(t, ctx, `window.__oldSetQueueRejectionFlushed === true`, 500*time.Millisecond)
	var got string
	eval(t, ctx, `window.__amtuiErr`, &got)
	if got != "" {
		t.Fatalf("old setQueue rejection leaked into current play: %q", got)
	}

	eval(t, ctx, `(() => {
	  window.__setQueueRejects[1](new Error('play 2 failed'));
	  Promise.resolve().then(() => { window.__currentSetQueueRejectionFlushed = true; });
	})()`, nil)
	waitForJS(t, ctx, `window.__currentSetQueueRejectionFlushed === true`, 500*time.Millisecond)
	eval(t, ctx, `window.__amtuiErr`, &got)
	if got != "setQueue: play 2 failed" {
		t.Fatalf("current setQueue rejection = %q, want %q", got, "setQueue: play 2 failed")
	}
}

func TestPlayItemSynchronousSetQueueFailureStillStartsWatchdog(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const pending = new Promise(() => {});
	  const state = window.__mock = {
	    finishCalls: 0,
	    stopCalls: 0,
	    changeCalls: 0,
	  };
	  const cp = {
	    _deferredPlay: {},
	    _buffer: {
	      mediaSource: {
	        readyState: 'closed',
	        sourceBuffers: { length: 0 },
	      },
	    },
	    finishPlaybackSequence() {
	      state.finishCalls++;
	    },
	    stopMediaAndCleanup() {
	      state.stopCalls++;
	      this._buffer = null;
	      return pending;
	    },
	  };
	  const mk = {
	    playbackState: 1,
	    queue: { position: 0, items: [{}] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    _playbackController: {
	      _changeToMediaAtIndex() {
	        state.changeCalls++;
	        return pending;
	      },
	    },
	    setQueue() {
	      throw new Error('sync setQueue failed');
	    },
	  };
	  const audio = { readyState: 0, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiErr = '';
	  window.__amtuiWatchdogDelay = 0;
	})()`, nil)

	script := fmt.Sprintf(playItemJS, "song", jsStr("song-1"))
	var immediate struct {
		OK         bool   `json:"ok"`
		Thrown     string `json:"thrown"`
		EngineErr  string `json:"engineErr"`
		Generation int    `json:"generation"`
	}
	eval(t, ctx, fmt.Sprintf(`(() => {
	  let ok = false;
	  let thrown = '';
	  try {
	    ok = %s;
	  } catch (e) {
	    thrown = (e && e.message) || String(e);
	  }
	  return {
	    ok,
	    thrown,
	    engineErr: window.__amtuiErr || '',
	    generation: window.__amtuiPlayGeneration | 0,
	  };
	})()`, script), &immediate)
	if !immediate.OK || immediate.Thrown != "" {
		t.Fatalf("synchronous setQueue failure escaped snippet: %+v", immediate)
	}
	if immediate.EngineErr != "setQueue: sync setQueue failed" || immediate.Generation != 1 {
		t.Fatalf("synchronous setQueue report = %+v", immediate)
	}

	waitForJS(t, ctx, `window.__mock.changeCalls === 1`, 500*time.Millisecond)
	var got struct {
		FinishCalls int `json:"finishCalls"`
		StopCalls   int `json:"stopCalls"`
		ChangeCalls int `json:"changeCalls"`
	}
	eval(t, ctx, `window.__mock`, &got)
	if got.FinishCalls != 1 || got.StopCalls != 1 || got.ChangeCalls != 1 {
		t.Fatalf("watchdog did not complete after synchronous setQueue failure: %+v", got)
	}
	var recovering bool
	eval(t, ctx, `window.__amtuiRecovering`, &recovering)
	if recovering {
		t.Fatal("watchdog recovery flag remained set")
	}
}

func TestInitialPlayWatchdogIgnoresOldRetryRejectionAfterNewPlay(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const pending = new Promise(() => {});
	  const retry = new Promise((_, reject) => {
	    window.__rejectRetry = reject;
	  });
	  const state = window.__mock = { changeCalls: 0 };
	  const cp = {
	    _deferredPlay: {},
	    _buffer: {
	      mediaSource: {
	        readyState: 'closed',
	        sourceBuffers: { length: 0 },
	      },
	    },
	    finishPlaybackSequence() {},
	    stopMediaAndCleanup() {
	      this._buffer = null;
	      return pending;
	    },
	  };
	  const mk = window.__mockMK = {
	    playbackState: 1,
	    queue: { position: 0, items: [{}] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    _playbackController: {
	      _changeToMediaAtIndex() {
	        state.changeCalls++;
	        return retry;
	      },
	    },
	    setQueue() {
	      return pending;
	    },
	  };
	  const audio = { readyState: 0, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiWatchdogDelay = 0;
	})()`, nil)

	var ok bool
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
	if !ok {
		t.Fatal("first play item snippet returned false")
	}
	waitForJS(t, ctx, `window.__mock.changeCalls === 1`, 500*time.Millisecond)

	eval(t, ctx, `window.__mockMK.playbackState = 2`, nil)
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-2")), &ok)
	if !ok {
		t.Fatal("second play item snippet returned false")
	}
	eval(t, ctx, `(() => {
	  window.__amtuiErr = '';
	  window.__rejectRetry(new Error('old retry failed'));
	  Promise.resolve().then(() => { window.__retryRejectionFlushed = true; });
	})()`, nil)
	waitForJS(t, ctx, `window.__retryRejectionFlushed === true`, 500*time.Millisecond)

	var got string
	eval(t, ctx, `window.__amtuiErr`, &got)
	if got != "" {
		t.Fatalf("old retry rejection leaked into new play: %q", got)
	}
}

func TestInitialPlayWatchdogDoesNotOverlapRecovery(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const pending = new Promise(() => {});
	  const state = window.__mock = {
	    finishCalls: 0,
	    stopCalls: 0,
	    changeCalls: 0,
	  };
	  const cp = {
	    _deferredPlay: {},
	    _buffer: {
	      mediaSource: {
	        readyState: 'closed',
	        sourceBuffers: { length: 0 },
	      },
	    },
	    finishPlaybackSequence() {
	      state.finishCalls++;
	    },
	    stopMediaAndCleanup() {
	      state.stopCalls++;
	      this._buffer = null;
	      return pending;
	    },
	  };
	  const mk = {
	    playbackState: 1,
	    queue: { position: 0, items: [{}] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    _playbackController: {
	      _changeToMediaAtIndex() {
	        state.changeCalls++;
	        return pending;
	      },
	    },
	    setQueue() {
	      return pending;
	    },
	  };
	  const audio = { readyState: 0, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiWatchdogDelay = 0;
	  window.__amtuiRecovering = true;
	})()`, nil)

	var ok bool
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
	if !ok {
		t.Fatal("play item snippet returned false")
	}
	time.Sleep(100 * time.Millisecond)

	var got struct {
		FinishCalls int `json:"finishCalls"`
		StopCalls   int `json:"stopCalls"`
		ChangeCalls int `json:"changeCalls"`
	}
	eval(t, ctx, `window.__mock`, &got)
	if got.FinishCalls != 0 || got.StopCalls != 0 || got.ChangeCalls != 0 {
		t.Fatalf("overlapping watchdog entered recovery: %+v", got)
	}
}

func TestInitialPlayWatchdogRetriesGenerationOnlyOnce(t *testing.T) {
	ctx := testPage(t)
	eval(t, ctx, `(() => {
	  const pending = new Promise(() => {});
	  const state = window.__mock = {
	    finishCalls: 0,
	    stopCalls: 0,
	    changeCalls: 0,
	  };
	  const cp = window.__mockCP = {
	    _deferredPlay: {},
	    _buffer: {
	      mediaSource: {
	        readyState: 'closed',
	        sourceBuffers: { length: 0 },
	      },
	    },
	    finishPlaybackSequence() {
	      state.finishCalls++;
	    },
	    stopMediaAndCleanup() {
	      state.stopCalls++;
	      this._buffer = null;
	      return pending;
	    },
	  };
	  const mk = {
	    playbackState: 1,
	    queue: { position: 0, items: [{}] },
	    services: { mediaItemPlayback: { _currentPlayer: cp } },
	    _playbackController: {
	      _changeToMediaAtIndex() {
	        state.changeCalls++;
	        return pending;
	      },
	    },
	    setQueue() {
	      return pending;
	    },
	  };
	  const audio = { readyState: 0, paused: false };
	  window.MusicKit = { getInstance: () => mk };
	  const querySelector = document.querySelector.bind(document);
	  document.querySelector = (selector) =>
	    selector === 'audio' ? audio : querySelector(selector);
	  window.__amtuiWatchdogDelay = 0;
	})()`, nil)

	var ok bool
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
	if !ok {
		t.Fatal("play item snippet returned false")
	}
	waitForJS(t, ctx, `window.__mock.changeCalls === 1`, 500*time.Millisecond)

	var recovering bool
	eval(t, ctx, `window.__amtuiRecovering`, &recovering)
	if recovering {
		t.Fatal("recovery single-flight flag was not cleared")
	}

	eval(t, ctx, `(() => {
	  window.__mockCP._deferredPlay = {};
	  window.__mockCP._buffer = {
	    mediaSource: {
	      readyState: 'closed',
	      sourceBuffers: { length: 0 },
	    },
	  };
	  window.__amtuiPlayGeneration = 0;
	})()`, nil)
	eval(t, ctx, fmt.Sprintf(playItemJS, "song", jsStr("song-1")), &ok)
	if !ok {
		t.Fatal("repeated play item snippet returned false")
	}
	time.Sleep(100 * time.Millisecond)

	var got struct {
		FinishCalls int `json:"finishCalls"`
		StopCalls   int `json:"stopCalls"`
		ChangeCalls int `json:"changeCalls"`
	}
	eval(t, ctx, `window.__mock`, &got)
	if got.FinishCalls != 1 || got.StopCalls != 1 || got.ChangeCalls != 1 {
		t.Fatalf("same generation retried more than once: %+v", got)
	}
}
