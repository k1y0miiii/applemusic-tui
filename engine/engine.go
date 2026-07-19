// Package engine drives a headed Chromium running music.apple.com via CDP. The
// window stays mostly offscreen through the first full playback initialization,
// then minimizes. All player operations go through window.MusicKit.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type Track struct {
	ID, Title, Artist, Album string
	Kind                     string // song | album | playlist (for playable results)
	Duration                 time.Duration
}

type State struct {
	Playing      bool
	Initializing bool
	Pos, Dur     time.Duration
	Volume       int // 0..100
	Shuffle      bool
	Repeat       int // 0 off, 1 one, 2 all
	Now          Track
	Queue        []Track
	QueuePos     int
	Err          string // last swallowed action rejection, read-and-clear
}

type SearchResults struct {
	Songs, Albums, Playlists, Recent []Track
}

type windowController interface {
	parkOffscreen(context.Context) error
	ensurePlayable(context.Context) error
	minimize(context.Context) error
}

type cdpWindowController struct{}

type Engine struct {
	mu      sync.Mutex
	ctx     context.Context
	cancels []context.CancelFunc
	park    bool
	primed  bool
	windows windowController
}

func profileDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".config", "amtui", "chrome")
	return d, os.MkdirAll(d, 0o755)
}

func launch(dir string, visible bool) (context.Context, []context.CancelFunc) {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(dir),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.Flag("hide-crash-restore-bubble", true),
		chromedp.Flag("disable-session-crashed-bubble", true),
		// The browser stays headed and normal through initial playback setup,
		// then minimizes. Keep Chrome from throttling its background page.
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-features", "IntensiveWakeUpThrottling"),
	}
	if p := os.Getenv("AMTUI_CHROME"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	if os.Getenv("AMTUI_DEBUG") != "" {
		opts = append(opts, chromedp.Flag("auto-open-devtools-for-tabs", true))
	}
	if !visible {
		// Headed window, not true headless — headless has no audio output and
		// unreliable Widevine. macOS clamps the offscreen position to leave a
		// narrow strip visible; open() reapplies normal offscreen bounds via CDP.
		opts = append(opts,
			chromedp.Flag("window-position", "-32000,-32000"),
			chromedp.Flag("window-size", "1000,700"),
		)
	}
	actx, acancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ccancel := chromedp.NewContext(actx)
	return ctx, []context.CancelFunc{ccancel, acancel}
}

func closeAll(cancels []context.CancelFunc) {
	for _, c := range cancels {
		c()
	}
}

func open(dir string, visible bool) (context.Context, []context.CancelFunc, error) {
	ctx, cancels := launch(dir, visible)
	if err := chromedp.Run(ctx, chromedp.Navigate("https://music.apple.com/")); err != nil {
		closeAll(cancels)
		return nil, nil, fmt.Errorf("launching browser: %w (set AMTUI_CHROME to your Chrome binary if not found)", err)
	}
	if !visible {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := newWindowController(browserPID(ctx)).parkOffscreen(tctx)
		cancel()
		if err != nil {
			closeAll(cancels)
			return nil, nil, fmt.Errorf("parking browser window: %w", err)
		}
	}
	return ctx, cancels, nil
}

func parkedWindowBounds() *cdpbrowser.Bounds {
	return &cdpbrowser.Bounds{
		Left:        -32000,
		Top:         -32000,
		Width:       1000,
		Height:      700,
		WindowState: cdpbrowser.WindowStateNormal,
	}
}

func minimizedWindowBounds() *cdpbrowser.Bounds {
	return &cdpbrowser.Bounds{WindowState: cdpbrowser.WindowStateMinimized}
}

// parkOffscreen keeps the headed browser playable while moving almost all of
// its normal window outside the visible desktop.
func parkOffscreen(ctx context.Context) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		id, _, err := cdpbrowser.GetWindowForTarget().Do(ctx)
		if err != nil {
			return err
		}
		return cdpbrowser.SetWindowBounds(id, parkedWindowBounds()).Do(ctx)
	}))
}

func minimizeWindow(ctx context.Context) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		id, _, err := cdpbrowser.GetWindowForTarget().Do(ctx)
		if err != nil {
			return err
		}
		return cdpbrowser.SetWindowBounds(id, minimizedWindowBounds()).Do(ctx)
	}))
}

func playableWindowBounds(current *cdpbrowser.Bounds, park bool) *cdpbrowser.Bounds {
	if current == nil || current.WindowState != cdpbrowser.WindowStateMinimized {
		return nil
	}
	if park {
		return parkedWindowBounds()
	}
	return &cdpbrowser.Bounds{WindowState: cdpbrowser.WindowStateNormal}
}

// ensurePlayableWindow restores a minimized browser for initial playback or
// emergency recovery. Normal windows are left exactly where they are.
func ensurePlayableWindow(ctx context.Context, park bool) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		id, current, err := cdpbrowser.GetWindowForTarget().Do(ctx)
		if err != nil {
			return err
		}
		desired := playableWindowBounds(current, park)
		if desired == nil {
			return nil
		}
		return cdpbrowser.SetWindowBounds(id, desired).Do(ctx)
	}))
}

func (cdpWindowController) parkOffscreen(ctx context.Context) error {
	return parkOffscreen(ctx)
}

func (cdpWindowController) ensurePlayable(ctx context.Context) error {
	return ensurePlayableWindow(ctx, true)
}

func (cdpWindowController) minimize(ctx context.Context) error {
	return minimizeWindow(ctx)
}

func (e *Engine) windowController() windowController {
	if e.windows != nil {
		return e.windows
	}
	return cdpWindowController{}
}

func (e *Engine) parkOffscreenLocked() error {
	tctx, cancel := context.WithTimeout(e.ctx, 750*time.Millisecond)
	defer cancel()
	return e.windowController().parkOffscreen(tctx)
}

func (e *Engine) ensurePlayableLocked() error {
	tctx, cancel := context.WithTimeout(e.ctx, 750*time.Millisecond)
	defer cancel()
	return e.windowController().ensurePlayable(tctx)
}

func (e *Engine) minimizeLocked() error {
	tctx, cancel := context.WithTimeout(e.ctx, 750*time.Millisecond)
	defer cancel()
	return e.windowController().minimize(tctx)
}

func (e *Engine) prepareReloadLocked() error {
	if !e.park {
		return nil
	}
	e.primed = false
	if err := e.parkOffscreenLocked(); err != nil {
		return fmt.Errorf("reload: restoring browser window offscreen: %w", err)
	}
	return nil
}

func (e *Engine) updateWindowLifecycleLocked(engineReady, hiddenStall bool) {
	if !e.park {
		return
	}
	if e.primed && hiddenStall {
		if e.ensurePlayableLocked() == nil {
			e.primed = false
		}
		return
	}
	if !e.primed && engineReady {
		if e.minimizeLocked() == nil {
			e.primed = true
		}
	}
}

func poll(ctx context.Context, expr string, timeout time.Duration, what string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ok bool
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := chromedp.Run(tctx, chromedp.Evaluate(expr, &ok))
		cancel()
		if err == nil && ok {
			return nil
		}
		time.Sleep(700 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

const mkReady = `typeof MusicKit !== 'undefined' && !!MusicKit.getInstance()`
const mkAuthorized = mkReady + ` && MusicKit.getInstance().isAuthorized === true`

// Connect launches the mostly-offscreen browser and, if the session is
// missing, walks the user through a visible login window. status receives
// progress messages.
func Connect(status func(string)) (*Engine, error) {
	dir, err := profileDir()
	if err != nil {
		return nil, err
	}
	status("launching browser…")
	// AMTUI_DEBUG=1 keeps the browser visible with DevTools for live poking
	visible := os.Getenv("AMTUI_DEBUG") != ""
	ctx, cancels, err := open(dir, visible)
	if err != nil {
		return nil, err
	}
	status("loading music.apple.com…")
	if err := poll(ctx, mkReady, 90*time.Second, "music.apple.com"); err != nil {
		closeAll(cancels)
		return nil, err
	}
	var authed bool
	_ = chromedp.Run(ctx, chromedp.Evaluate(mkAuthorized, &authed))
	if !authed {
		closeAll(cancels)
		status("login required — sign in to Apple Music in the browser window")
		vctx, vcancels, err := open(dir, true)
		if err != nil {
			return nil, err
		}
		if err := poll(vctx, mkAuthorized, 5*time.Minute, "login"); err != nil {
			closeAll(vcancels)
			return nil, err
		}
		status("login detected — restarting browser…")
		closeAll(vcancels)
		ctx, cancels, err = open(dir, visible)
		if err != nil {
			return nil, err
		}
		if err := poll(ctx, mkAuthorized, 90*time.Second, "session"); err != nil {
			closeAll(cancels)
			return nil, err
		}
	}
	var ok bool
	_ = chromedp.Run(ctx, chromedp.Evaluate(autoplayJS, &ok))
	status("connected")
	return &Engine{
		ctx:     ctx,
		cancels: cancels,
		park:    !visible,
		windows: newWindowController(browserPID(ctx)),
	}, nil
}

func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	// ponytail: SIGKILL instead of graceful Browser.close — instant quit,
	// profile survives, crash-restore bubbles are suppressed by flags.
	if c := chromedp.FromContext(e.ctx); c != nil && c.Browser != nil {
		if p := c.Browser.Process(); p != nil {
			_ = p.Kill()
		}
	}
	closeAll(e.cancels)
}

func awaitPromise(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// do runs fire-and-forget JS; the snippet must end with `return true`.
func (e *Engine) do(js string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.park && !e.primed {
		_ = e.ensurePlayableLocked()
	}
	tctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	var ok bool
	return chromedp.Run(tctx, chromedp.Evaluate(js, &ok, awaitPromise))
}

// evalJSON runs JS returning a JSON string and unmarshals it into out.
func (e *Engine) evalJSON(js string, out any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evalJSONLocked(js, out)
}

func (e *Engine) evalJSONLocked(js string, out any) error {
	tctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	var raw string
	if err := chromedp.Run(tctx, chromedp.Evaluate(js, &raw, awaitPromise)); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

type jsTrack struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Kind   string `json:"kind"`
	DurMs  int    `json:"durMs"`
}

func (t jsTrack) track() Track {
	return Track{
		ID: t.ID, Title: t.Title, Artist: t.Artist, Album: t.Album, Kind: t.Kind,
		Duration: time.Duration(t.DurMs) * time.Millisecond,
	}
}

func tracks(ts []jsTrack) []Track {
	out := make([]Track, len(ts))
	for i, t := range ts {
		out[i] = t.track()
	}
	return out
}

func (e *Engine) State() (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var raw struct {
		Err          string    `json:"err"`
		Playing      bool      `json:"playing"`
		Initializing bool      `json:"initializing"`
		EngineReady  bool      `json:"engineReady"`
		HiddenStall  bool      `json:"hiddenStall"`
		Pos          float64   `json:"pos"`
		Dur          float64   `json:"dur"`
		Volume       int       `json:"volume"`
		Shuffle      bool      `json:"shuffle"`
		Repeat       int       `json:"repeat"`
		Now          *jsTrack  `json:"now"`
		QueuePos     int       `json:"queuePos"`
		Queue        []jsTrack `json:"queue"`
	}
	if err := e.evalJSONLocked(stateJS, &raw); err != nil {
		return State{}, err
	}
	e.updateWindowLifecycleLocked(raw.EngineReady, raw.HiddenStall)
	st := State{
		Playing:      raw.Playing,
		Initializing: raw.Initializing,
		Pos:          time.Duration(raw.Pos * float64(time.Second)),
		Dur:          time.Duration(raw.Dur * float64(time.Second)),
		Volume:       raw.Volume, Shuffle: raw.Shuffle, Repeat: raw.Repeat,
		QueuePos: raw.QueuePos, Queue: tracks(raw.Queue), Err: raw.Err,
	}
	if raw.Now != nil {
		st.Now = raw.Now.track()
	}
	return st, nil
}

// PlayPause pauses via MusicKit's player (PlayActivity must see pause),
// then resumes through the ladder in playPauseJS.
func (e *Engine) PlayPause() error     { return e.do(playPauseJS) }
func (e *Engine) Next() error          { return e.do(nextJS) }
func (e *Engine) Prev() error          { return e.do(prevJS) }
func (e *Engine) ToggleShuffle() error { return e.do(shuffleJS) }
func (e *Engine) CycleRepeat() error   { return e.do(repeatJS) }
func (e *Engine) JumpTo(i int) error   { return e.do(fmt.Sprintf(jumpJS, i)) }
func (e *Engine) SeekTo(d time.Duration) error {
	return e.do(fmt.Sprintf(seekJS, d.Seconds()))
}
func (e *Engine) SetVolume(v int) error {
	return e.do(fmt.Sprintf(volumeJS, float64(min(max(v, 0), 100))/100))
}

var kinds = map[string]bool{"song": true, "album": true, "playlist": true}

func (e *Engine) Play(kind, id string) error {
	if !kinds[kind] {
		return fmt.Errorf("unknown kind %q", kind)
	}
	return e.do(fmt.Sprintf(playItemJS, kind, jsStr(id)))
}

func (e *Engine) QueueNext(kind, id string) error {
	if !kinds[kind] {
		return fmt.Errorf("unknown kind %q", kind)
	}
	return e.do(fmt.Sprintf(queueNextJS, kind, jsStr(id)))
}

func (e *Engine) QueueLater(kind, id string) error {
	if !kinds[kind] {
		return fmt.Errorf("unknown kind %q", kind)
	}
	return e.do(fmt.Sprintf(queueLaterJS, kind, jsStr(id)))
}

func (e *Engine) results(js string) (SearchResults, error) {
	var raw struct {
		Songs     []jsTrack `json:"songs"`
		Albums    []jsTrack `json:"albums"`
		Playlists []jsTrack `json:"playlists"`
		Recent    []jsTrack `json:"recent"`
	}
	if err := e.evalJSON(js, &raw); err != nil {
		return SearchResults{}, err
	}
	return SearchResults{
		Songs: tracks(raw.Songs), Albums: tracks(raw.Albums),
		Playlists: tracks(raw.Playlists), Recent: tracks(raw.Recent),
	}, nil
}

// Search queries the full Apple Music catalog.
func (e *Engine) Search(term string) (SearchResults, error) {
	return e.results(fmt.Sprintf(searchJS, jsStr(term)))
}

// Library returns the user's own albums and playlists.
func (e *Engine) Library() (SearchResults, error) {
	return e.results(libraryJS)
}

// Reload reloads music.apple.com in place — the un-wedge lever when the web
// player's media pipeline gets stuck. The MusicKit queue does not survive.
func (e *Engine) Reload() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.prepareReloadLocked(); err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	if err := chromedp.Run(tctx, chromedp.Navigate("https://music.apple.com/")); err != nil {
		return err
	}
	if err := poll(e.ctx, mkReady, 60*time.Second, "reload"); err != nil {
		return err
	}
	var ok bool
	_ = chromedp.Run(e.ctx, chromedp.Evaluate(autoplayJS, &ok))
	return nil
}

func jsStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
