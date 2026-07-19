package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1y0miiii/applemusic-tui/engine"
	"github.com/k1y0miiii/applemusic-tui/visualizer"
)

func TestVisualizerTitleReflectsCaptureState(t *testing.T) {
	tests := []struct {
		name string
		m    model
		want string
	}{
		{
			name: "starting",
			m:    model{vizOpening: true},
			want: "VISUALIZER · STARTING",
		},
		{
			name: "live CoreAudio",
			m:    model{vizLive: true, vizSource: "COREAUDIO"},
			want: "VISUALIZER · LIVE · COREAUDIO",
		},
		{
			name: "live PipeWire",
			m:    model{vizLive: true, vizSource: "PIPEWIRE"},
			want: "VISUALIZER · LIVE · PIPEWIRE",
		},
		{
			name: "simulated",
			m:    model{},
			want: "VISUALIZER · SIMULATED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.visualizerTitle(); got != tt.want {
				t.Fatalf("visualizerTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestViewRendersVisualizerStateLabel(t *testing.T) {
	m := model{
		w:          100,
		h:          30,
		phase:      phaseReady,
		vizLive:    true,
		vizSource:  "COREAUDIO",
		vizOpening: false,
	}

	if got := m.View(); !strings.Contains(got, m.visualizerTitle()) {
		t.Fatalf("View() does not contain %q:\n%s", m.visualizerTitle(), got)
	}
}

func TestSyntheticBarsRenderOnlyAfterVisualizerFallsBack(t *testing.T) {
	starting := model{
		t:          1.7,
		st:         engine.State{Playing: true},
		vizOpening: true,
	}
	if got := starting.vizPanel(60, 10); strings.ContainsAny(got, "▁▂▃▄▅▆▇█") {
		t.Fatalf("starting visualizer rendered simulated bars:\n%s", got)
	}

	simulated := starting
	simulated.vizOpening = false
	if got := simulated.vizPanel(60, 10); !strings.ContainsAny(got, "▁▂▃▄▅▆▇█") {
		t.Fatalf("fallback visualizer did not render simulated bars:\n%s", got)
	}
}

func TestLiveBarHeightsIncludeEverySpectrumBandAtNarrowWidth(t *testing.T) {
	const bars = 8
	for band := range 32 {
		t.Run(fmt.Sprintf("band_%d", band), func(t *testing.T) {
			var bands [32]float64
			bands[band] = 1

			heights := liveBarHeights(bands, bars, 10)
			wantBar := band / (32 / bars)
			if heights[wantBar] == 0 {
				t.Fatalf("band %d did not contribute to bar %d: %v", band, wantBar, heights)
			}
		})
	}
}

func TestReadyStartsVisualizerOpenAlongsideInitialStateFetch(t *testing.T) {
	m := model{}

	next, cmd := m.Update(readyMsg{eng: &engine.Engine{}})
	got := next.(model)

	if !got.vizOpening {
		t.Fatal("ready should mark the visualizer as opening")
	}
	if cmd == nil {
		t.Fatal("ready should return state-fetch and visualizer-open commands")
	}
	msg := invokeCommandWithoutPanic(cmd)
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("ready command returned %T, want tea.BatchMsg", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("ready batch contains %d commands, want 2", len(batch))
	}
}

func invokeCommandWithoutPanic(cmd tea.Cmd) (msg tea.Msg) {
	defer func() {
		if recover() != nil {
			msg = nil
		}
	}()
	return cmd()
}

func executeBatchCommands(t *testing.T, cmd tea.Cmd) tea.BatchMsg {
	t.Helper()
	batch, ok := invokeCommandWithoutPanic(cmd).(tea.BatchMsg)
	if !ok {
		t.Fatalf("command is not a tea.BatchMsg")
	}
	for _, command := range batch {
		if command != nil {
			_ = command()
		}
	}
	return batch
}

type uiFakeSource struct {
	name       string
	format     visualizer.Format
	read       func(context.Context, []float32) (int, error)
	closeFn    func() error
	closeCalls atomic.Int32
}

func (s *uiFakeSource) Name() string              { return s.name }
func (s *uiFakeSource) Format() visualizer.Format { return s.format }
func (s *uiFakeSource) Read(ctx context.Context, dst []float32) (int, error) {
	return s.read(ctx, dst)
}
func (s *uiFakeSource) Close() error {
	s.closeCalls.Add(1)
	if s.closeFn != nil {
		return s.closeFn()
	}
	return nil
}

func TestVisualizerOpenClosesSourceWhenServiceConstructionFails(t *testing.T) {
	source := &uiFakeSource{
		name:   "invalid",
		format: visualizer.Format{},
		read: func(context.Context, []float32) (int, error) {
			t.Fatal("invalid source should not be read")
			return 0, nil
		},
	}

	msg := openVisualizerCmd(func() (visualizer.Source, error) {
		return source, nil
	})().(visualizerOpenedMsg)

	if msg.service != nil {
		t.Fatal("failed service construction returned a service")
	}
	if msg.err == nil {
		t.Fatal("failed service construction returned no error")
	}
	if calls := source.closeCalls.Load(); calls != 1 {
		t.Fatalf("source Close calls = %d, want 1", calls)
	}
}

func TestVisualizerOpenHandlesNilSourceFromBackend(t *testing.T) {
	cmd := openVisualizerCmd(func() (visualizer.Source, error) {
		return nil, nil
	})

	msg := invokeCommandWithoutPanic(cmd)
	opened, ok := msg.(visualizerOpenedMsg)
	if !ok {
		t.Fatalf("nil backend source returned %T, want visualizerOpenedMsg", msg)
	}
	if opened.service != nil || opened.err == nil {
		t.Fatalf("nil backend source returned service=%v err=%v", opened.service, opened.err)
	}
}

func TestVisualizerOpenMessageCarriesSourceName(t *testing.T) {
	source := &uiFakeSource{
		name:   "PIPEWIRE",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, _ []float32) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	msg := openVisualizerCmd(func() (visualizer.Source, error) {
		return source, nil
	})().(visualizerOpenedMsg)
	if msg.service == nil {
		t.Fatalf("open command returned no service: %v", msg.err)
	}
	t.Cleanup(func() { _ = msg.service.Close() })
	if msg.source != "PIPEWIRE" {
		t.Fatalf("message source = %q, want PIPEWIRE", msg.source)
	}
}

func TestVisualizerOpenedMarksSilentCaptureLiveImmediately(t *testing.T) {
	source := &uiFakeSource{
		name:   "COREAUDIO",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, _ []float32) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	next, _ := (model{phase: phaseReady, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service, source: "COREAUDIO"},
	)
	got := next.(model)

	if got.vizService != service {
		t.Fatal("opened visualizer service was not stored")
	}
	if got.vizOpening {
		t.Fatal("successful backend open should end starting state")
	}
	if !got.vizLive || got.vizSource != "COREAUDIO" {
		t.Fatalf("silent capture live=%v source=%q", got.vizLive, got.vizSource)
	}
	if title := got.visualizerTitle(); title != "VISUALIZER · LIVE · COREAUDIO" {
		t.Fatalf("visualizerTitle() = %q", title)
	}
	if _, ok := service.Latest(); ok {
		t.Fatal("silent source unexpectedly produced a frame")
	}
}

func TestLateVisualizerOpenOutsideReadyClosesServiceInsteadOfStoringIt(t *testing.T) {
	source := &uiFakeSource{
		name:   "COREAUDIO",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, _ []float32) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	next, cmd := (model{phase: phaseBoot, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service},
	)
	got := next.(model)

	if cmd == nil {
		t.Fatal("late visualizer result returned no Close command")
	}
	if got.vizService != nil {
		t.Fatal("late visualizer result was stored outside ready phase")
	}
	if calls := source.closeCalls.Load(); calls != 0 {
		t.Fatalf("source closed before Close command ran: calls=%d", calls)
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("Close command returned unexpected message %T", msg)
	}
	if calls := source.closeCalls.Load(); calls != 1 {
		t.Fatalf("late-open source Close calls = %d, want 1", calls)
	}
}

func TestQuitRejectsVisualizerResultThatArrivesBeforeQuitCommandRuns(t *testing.T) {
	source := &uiFakeSource{
		name:   "COREAUDIO",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, _ []float32) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	next, quitCmd := (model{phase: phaseReady, vizOpening: true}).updateKeys(
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
	)
	quitting := next.(model)
	if quitting.phase != phaseReady || quitting.vizOpening {
		t.Fatalf(
			"quit left phase=%v opening=%v, want ready and not opening",
			quitting.phase,
			quitting.vizOpening,
		)
	}

	next, closeCmd := quitting.Update(visualizerOpenedMsg{service: service})
	afterOpen := next.(model)
	if afterOpen.vizService != nil {
		t.Fatal("visualizer result arriving after q was stored")
	}
	if closeCmd == nil {
		t.Fatal("visualizer result arriving after q returned no Close command")
	}
	if calls := source.closeCalls.Load(); calls != 0 {
		t.Fatalf("source closed before returned Close command ran: calls=%d", calls)
	}
	if msg := closeCmd(); msg != nil {
		t.Fatalf("Close command returned unexpected message %T", msg)
	}
	if calls := source.closeCalls.Load(); calls != 1 {
		t.Fatalf("late-open source Close calls = %d, want 1", calls)
	}

	next, cmd := afterOpen.Update(visualizerOpenedMsg{err: errors.New("late open error")})
	afterError := next.(model)
	if cmd != nil {
		t.Fatal("late open error returned a command")
	}
	if afterError.note != "" || afterError.vizTerminal {
		t.Fatalf(
			"late open error re-notified after q: note=%q terminal=%v",
			afterError.note,
			afterError.vizTerminal,
		)
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not retain its Quit command")
	}
}

func TestTickAppliesLatest32BandsAndLiveMetadata(t *testing.T) {
	sent := false
	source := &uiFakeSource{
		name:   "PIPEWIRE",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, dst []float32) (int, error) {
			if !sent {
				sent = true
				for i := range dst {
					dst[i] = float32(0.8 * math.Sin(2*math.Pi*1_000*float64(i)/48_000))
				}
				return len(dst), nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	frame := waitForVisualizerFrame(t, service)
	next, _ := (model{vizService: service, vizOpening: true}).Update(tickMsg{})
	got := next.(model)

	for i, want := range frame.Bands {
		if got.vizTargets[i] != want {
			t.Fatalf("vizTargets[%d] = %v, want %v", i, got.vizTargets[i], want)
		}
	}
	if !got.vizLive {
		t.Fatal("latest live frame did not mark visualizer live")
	}
	if got.vizSource != "PIPEWIRE" {
		t.Fatalf("vizSource = %q, want PIPEWIRE", got.vizSource)
	}
	if got.vizOpening {
		t.Fatal("first live frame should end visualizer starting state")
	}
}

func TestTickKeepsSilentServiceLiveWithoutFirstFrame(t *testing.T) {
	source := &uiFakeSource{
		name:   "COREAUDIO",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, _ []float32) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	next, _ := (model{phase: phaseReady, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service, source: "COREAUDIO"},
	)
	live := next.(model)

	next, cmd := live.Update(tickMsg(time.Now().Add(10 * time.Second)))
	got := next.(model)

	if got.vizService != service || got.vizOpening || !got.vizLive {
		t.Fatalf(
			"silent service state service=%v opening=%v live=%v",
			got.vizService,
			got.vizOpening,
			got.vizLive,
		)
	}
	if got.vizSource != "COREAUDIO" || got.note != "" || got.vizTerminal {
		t.Fatalf(
			"silent service source=%q note=%q terminal=%v",
			got.vizSource,
			got.note,
			got.vizTerminal,
		)
	}
	if calls := source.closeCalls.Load(); calls != 0 {
		t.Fatalf("silent source Close calls = %d, want 0", calls)
	}
	if _, ok := invokeCommandWithoutPanic(cmd).(tea.BatchMsg); ok {
		t.Fatal("silent service scheduled a Close batch")
	}
}

func TestStaleFrameZeroesTargetsWithoutClosingService(t *testing.T) {
	sent := false
	source := &uiFakeSource{
		name:   "PIPEWIRE",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, dst []float32) (int, error) {
			if !sent {
				sent = true
				for i := range dst {
					dst[i] = float32(0.8 * math.Sin(2*math.Pi*440*float64(i)/48_000))
				}
				return len(dst), nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	frame := waitForVisualizerFrame(t, service)
	next, _ := (model{phase: phaseReady, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service, source: "PIPEWIRE"},
	)
	live := next.(model)
	next, _ = live.Update(tickMsg(frame.At))
	withFrame := next.(model)
	nonzero := false
	for _, value := range withFrame.vizTargets {
		nonzero = nonzero || value > 0
	}
	if !nonzero {
		t.Fatal("fresh frame did not produce nonzero targets")
	}

	next, cmd := withFrame.Update(tickMsg(frame.At.Add(time.Second)))
	got := next.(model)

	if got.vizService != service || got.vizOpening || !got.vizLive {
		t.Fatalf(
			"stale frame state service=%v opening=%v live=%v",
			got.vizService,
			got.vizOpening,
			got.vizLive,
		)
	}
	if got.vizSource != "PIPEWIRE" || got.note != "" || got.vizTerminal {
		t.Fatalf(
			"stale frame source=%q note=%q terminal=%v",
			got.vizSource,
			got.note,
			got.vizTerminal,
		)
	}
	for band, value := range got.vizTargets {
		if value != 0 {
			t.Fatalf("stale target %d = %v, want 0", band, value)
		}
	}
	if calls := source.closeCalls.Load(); calls != 0 {
		t.Fatalf("stale-frame source Close calls = %d, want 0", calls)
	}
	if _, ok := invokeCommandWithoutPanic(cmd).(tea.BatchMsg); ok {
		t.Fatal("stale frame scheduled a Close batch")
	}
}

func TestFreshFrameAfterStaleSilenceRestoresTargets(t *testing.T) {
	secondRead := make(chan struct{})
	reads := 0
	source := &uiFakeSource{
		name:   "PIPEWIRE",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, dst []float32) (int, error) {
			switch reads {
			case 0:
				reads++
				fillSine(dst, 440)
				return len(dst), nil
			case 1:
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-secondRead:
				}
				reads++
				fillSine(dst, 1_000)
				return len(dst), nil
			default:
				<-ctx.Done()
				return 0, ctx.Err()
			}
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	first := waitForVisualizerFrame(t, service)
	next, _ := (model{phase: phaseReady, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service, source: "PIPEWIRE"},
	)
	next, _ = next.(model).Update(tickMsg(first.At))
	withFrame := next.(model)
	next, _ = withFrame.Update(tickMsg(first.At.Add(time.Second)))
	silent := next.(model)
	for band, value := range silent.vizTargets {
		if value != 0 {
			t.Fatalf("stale target %d = %v, want 0", band, value)
		}
	}

	close(secondRead)
	second := waitForVisualizerFrameAfter(t, service, first.At)
	next, _ = silent.Update(tickMsg(second.At))
	resumed := next.(model)

	nonzero := false
	for _, value := range resumed.vizTargets {
		nonzero = nonzero || value > 0
	}
	if !nonzero {
		t.Fatal("fresh frame after silence did not restore nonzero targets")
	}
	if resumed.vizService != service || !resumed.vizLive || resumed.vizSource != "PIPEWIRE" {
		t.Fatalf(
			"resumed state service=%v live=%v source=%q",
			resumed.vizService,
			resumed.vizLive,
			resumed.vizSource,
		)
	}
}

func TestSilentCaptureTransitionsFromPauseToPlaying(t *testing.T) {
	play := make(chan struct{})
	sent := false
	source := &uiFakeSource{
		name:   "COREAUDIO",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, dst []float32) (int, error) {
			if !sent {
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-play:
				}
				sent = true
				fillSine(dst, 880)
				return len(dst), nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	next, _ := (model{phase: phaseReady, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service, source: "COREAUDIO"},
	)
	paused := next.(model)
	if !paused.vizLive || paused.vizService != service {
		t.Fatalf("paused capture live=%v service=%v", paused.vizLive, paused.vizService)
	}
	if _, ok := service.Latest(); ok {
		t.Fatal("paused source unexpectedly published a frame")
	}

	close(play)
	frame := waitForVisualizerFrame(t, service)
	next, _ = paused.Update(tickMsg(frame.At))
	playing := next.(model)

	nonzero := false
	for _, value := range playing.vizTargets {
		nonzero = nonzero || value > 0
	}
	if !nonzero {
		t.Fatal("playing source did not produce nonzero targets")
	}
	if playing.vizService != service || !playing.vizLive || playing.vizSource != "COREAUDIO" {
		t.Fatalf(
			"playing capture service=%v live=%v source=%q",
			playing.vizService,
			playing.vizLive,
			playing.vizSource,
		)
	}
}

func TestTickKeepsFreshSilenceFrameLive(t *testing.T) {
	sent := false
	source := &uiFakeSource{
		name:   "COREAUDIO",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(ctx context.Context, dst []float32) (int, error) {
			if !sent {
				sent = true
				clear(dst)
				return len(dst), nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	frame := waitForVisualizerFrame(t, service)
	next, _ := (model{phase: phaseReady, vizOpening: true}).Update(
		visualizerOpenedMsg{service: service, source: "COREAUDIO"},
	)
	next, _ = next.(model).Update(tickMsg(frame.At.Add(visualizerFrameFreshness / 2)))
	got := next.(model)

	if got.vizService != service || !got.vizLive || got.vizOpening || got.vizTerminal {
		t.Fatalf(
			"fresh silence left service=%v live=%v opening=%v terminal=%v",
			got.vizService,
			got.vizLive,
			got.vizOpening,
			got.vizTerminal,
		)
	}
	if got.vizSource != "COREAUDIO" {
		t.Fatalf("vizSource = %q, want COREAUDIO", got.vizSource)
	}
	for band, value := range got.vizTargets {
		if value != 0 {
			t.Fatalf("fresh silence target %d = %v, want 0", band, value)
		}
	}
}

func waitForVisualizerFrame(t *testing.T, service *visualizer.Service) visualizer.Frame {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if frame, ok := service.Latest(); ok {
			return frame
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for visualizer frame")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForVisualizerFrameAfter(
	t *testing.T,
	service *visualizer.Service,
	after time.Time,
) visualizer.Frame {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if frame, ok := service.Latest(); ok && frame.At.After(after) {
			return frame
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for visualizer frame after %v", after)
		}
		time.Sleep(time.Millisecond)
	}
}

func fillSine(dst []float32, frequency float64) {
	for i := range dst {
		dst[i] = float32(0.8 * math.Sin(2*math.Pi*frequency*float64(i)/48_000))
	}
}

func TestVisualizerOpenErrorFallsBackAndNotifiesOnce(t *testing.T) {
	m := model{
		phase:      phaseReady,
		t:          10,
		vizOpening: true,
		vizLive:    true,
		vizSource:  "COREAUDIO",
	}

	next, cmd := m.Update(visualizerOpenedMsg{err: errors.New("permission denied")})
	got := next.(model)

	if cmd != nil {
		t.Fatal("open error should not schedule a retry")
	}
	if got.vizService != nil || got.vizOpening || got.vizLive || got.vizSource != "" {
		t.Fatalf(
			"open error left service=%v opening=%v live=%v source=%q",
			got.vizService,
			got.vizOpening,
			got.vizLive,
			got.vizSource,
		)
	}
	if !got.vizTerminal {
		t.Fatal("open error should latch terminal fallback state")
	}
	if got.note != "visualizer unavailable · simulated" {
		t.Fatalf("note = %q, want short simulated fallback note", got.note)
	}
	noteAt := got.noteAt

	got.note = ""
	got.t = 20
	next, _ = got.Update(visualizerOpenedMsg{err: errors.New("same result")})
	repeated := next.(model)
	if repeated.note != "" {
		t.Fatalf("repeated open error produced another note %q", repeated.note)
	}
	if repeated.noteAt != noteAt {
		t.Fatalf("repeated open error changed noteAt from %v to %v", noteAt, repeated.noteAt)
	}
}

func TestTerminalServiceErrorFallsBackClosesAsyncAndNotifiesOnce(t *testing.T) {
	terminalErr := errors.New("audio server disappeared")
	source := &uiFakeSource{
		name:   "PIPEWIRE",
		format: visualizer.Format{SampleRate: 48_000, Channels: 1},
		read: func(context.Context, []float32) (int, error) {
			return 0, terminalErr
		},
	}
	service, err := visualizer.NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	waitForVisualizerError(t, service, terminalErr)

	m := model{
		t:           5,
		vizService:  service,
		vizOpening:  true,
		vizLive:     true,
		vizSource:   "PIPEWIRE",
		vizTerminal: false,
	}
	next, cmd := m.Update(tickMsg{})
	got := next.(model)

	if got.vizService != nil || got.vizOpening || got.vizLive || got.vizSource != "" {
		t.Fatalf(
			"terminal error left service=%v opening=%v live=%v source=%q",
			got.vizService,
			got.vizOpening,
			got.vizLive,
			got.vizSource,
		)
	}
	if !got.vizTerminal {
		t.Fatal("terminal service error should latch fallback state")
	}
	if got.note != visualizerUnavailableNote {
		t.Fatalf("note = %q, want %q", got.note, visualizerUnavailableNote)
	}
	noteAt := got.noteAt

	batch := executeBatchCommands(t, cmd)
	if len(batch) != 2 {
		t.Fatalf("terminal-error tick batch contains %d commands, want tick and Close", len(batch))
	}
	// Service.run closes terminal sources before Close is requested; the
	// explicit command must remain idempotent.
	if calls := source.closeCalls.Load(); calls != 1 {
		t.Fatalf("terminal-error source Close calls = %d, want 1", calls)
	}

	next, _ = got.Update(tickMsg{})
	repeated := next.(model)
	if repeated.noteAt != noteAt {
		t.Fatalf("next tick changed fallback noteAt from %v to %v", noteAt, repeated.noteAt)
	}
}

func waitForVisualizerError(t *testing.T, service *visualizer.Service, want error) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if err := service.Err(); errors.Is(err, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for visualizer error %v; got %v", want, service.Err())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestQuitStartsVisualizerCloseWithoutWaiting(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	} {
		t.Run(key.String(), func(t *testing.T) {
			closeStarted := make(chan struct{})
			releaseClose := make(chan struct{})
			defer close(releaseClose)
			source := &uiFakeSource{
				name:   "COREAUDIO",
				format: visualizer.Format{SampleRate: 48_000, Channels: 1},
				read: func(ctx context.Context, _ []float32) (int, error) {
					<-ctx.Done()
					return 0, ctx.Err()
				},
				closeFn: func() error {
					close(closeStarted)
					<-releaseClose
					return nil
				},
			}
			service, err := visualizer.NewService(source)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			t.Cleanup(func() { _ = service.Close() })

			returned := make(chan struct {
				next tea.Model
				cmd  tea.Cmd
			}, 1)
			go func() {
				next, cmd := (model{phase: phaseBoot, vizService: service}).updateKeys(key)
				returned <- struct {
					next tea.Model
					cmd  tea.Cmd
				}{next: next, cmd: cmd}
			}()

			var result struct {
				next tea.Model
				cmd  tea.Cmd
			}
			select {
			case result = <-returned:
			case <-time.After(250 * time.Millisecond):
				t.Fatal("quit waited for visualizer Close")
			}
			if result.next.(model).vizService != nil {
				t.Fatal("quit retained visualizer service")
			}
			if _, ok := result.cmd().(tea.QuitMsg); !ok {
				t.Fatalf("quit command returned unexpected message")
			}

			select {
			case <-closeStarted:
			case <-time.After(250 * time.Millisecond):
				t.Fatal("quit did not start visualizer Close")
			}
		})
	}
}
