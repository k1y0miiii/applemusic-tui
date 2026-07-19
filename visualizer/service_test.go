package visualizer

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	name    string
	format  Format
	readFn  func(context.Context, []float32) (int, error)
	closeFn func() error
}

func (f *fakeSource) Name() string   { return f.name }
func (f *fakeSource) Format() Format { return f.format }

func (f *fakeSource) Read(ctx context.Context, dst []float32) (int, error) {
	return f.readFn(ctx, dst)
}

func (f *fakeSource) Close() error {
	if f.closeFn == nil {
		return nil
	}
	return f.closeFn()
}

func TestServicePublishesLiveFrameWithSourceMetadata(t *testing.T) {
	pcm := sinePCM(48_000, 1, 1_000, 0.8, 4_096, false)
	offset := 0
	source := &fakeSource{
		name:   "fake-loopback",
		format: Format{SampleRate: 48_000, Channels: 1},
		readFn: func(ctx context.Context, dst []float32) (int, error) {
			if offset < len(pcm) {
				n := copy(dst, pcm[offset:])
				offset += n
				return n, nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	startedAt := time.Now()
	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	frame := waitForFrame(t, service)
	if frame.Source != "fake-loopback" {
		t.Errorf("Source = %q, want fake-loopback", frame.Source)
	}
	if !frame.Live {
		t.Error("Live = false, want true")
	}
	if frame.At.Before(startedAt) || frame.At.After(time.Now()) {
		t.Errorf("At = %v, want current timestamp", frame.At)
	}
	peak := peakBand(frame.Bands)
	if frame.Bands[peak] < 0.5 {
		t.Errorf("peak strength = %.3f, want >= 0.5", frame.Bands[peak])
	}
}

func TestServiceLatestAndErrAreNonBlocking(t *testing.T) {
	readStarted := make(chan struct{})
	var startedOnce sync.Once
	source := &fakeSource{
		name:   "blocked",
		format: Format{SampleRate: 48_000, Channels: 1},
		readFn: func(ctx context.Context, _ []float32) (int, error) {
			startedOnce.Do(func() { close(readStarted) })
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	<-readStarted

	returned := make(chan struct{})
	go func() {
		if _, ok := service.Latest(); ok {
			t.Error("Latest reported a frame before any PCM was read")
		}
		if err := service.Err(); err != nil {
			t.Errorf("Err before termination = %v, want nil", err)
		}
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Latest or Err blocked")
	}
}

func TestServiceCloseCancelsReadAndIsIdempotent(t *testing.T) {
	readStarted := make(chan struct{})
	readUnblock := make(chan struct{})
	var unblockOnce sync.Once
	var capturedContext context.Context
	var closeCalls atomic.Int32
	var closeSawCancellation atomic.Bool
	closeErr := errors.New("fake close error")

	source := &fakeSource{
		name:   "close-controlled",
		format: Format{SampleRate: 48_000, Channels: 1},
		readFn: func(ctx context.Context, _ []float32) (int, error) {
			capturedContext = ctx
			close(readStarted)
			<-readUnblock
			return 0, ctx.Err()
		},
		closeFn: func() error {
			closeCalls.Add(1)
			if capturedContext.Err() != nil {
				closeSawCancellation.Store(true)
			}
			unblockOnce.Do(func() { close(readUnblock) })
			return closeErr
		},
	}

	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	<-readStarted

	firstClose := make(chan error, 1)
	go func() { firstClose <- service.Close() }()
	select {
	case err := <-firstClose:
		if !errors.Is(err, closeErr) {
			t.Fatalf("first Close error = %v, want %v", err, closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Read")
	}

	if err := service.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("second Close error = %v, want %v", err, closeErr)
	}
	if got := closeCalls.Load(); got != 1 {
		t.Errorf("source Close calls = %d, want 1", got)
	}
	if !closeSawCancellation.Load() {
		t.Error("source Close ran before the read context was cancelled")
	}
}

func TestServiceCloseStopsSourceThatKeepsReturningSamples(t *testing.T) {
	var readCalls atomic.Int64
	source := &fakeSource{
		name:   "ignores-cancellation",
		format: Format{SampleRate: 48_000, Channels: 1},
		readFn: func(context.Context, []float32) (int, error) {
			readCalls.Add(1)
			return 1, nil
		},
	}

	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for readCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("source Read was not called")
		}
		time.Sleep(time.Millisecond)
	}

	closed := make(chan error, 1)
	go func() { closed <- service.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Close remained blocked while source kept returning positive n")
	}
}

func TestServiceBacksOffWhenSourceMakesNoProgress(t *testing.T) {
	var readCalls atomic.Int64
	source := &fakeSource{
		name:   "no-progress",
		format: Format{SampleRate: 48_000, Channels: 1},
		readFn: func(context.Context, []float32) (int, error) {
			readCalls.Add(1)
			return 0, nil
		},
	}

	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	deadline := time.Now().Add(time.Second)
	for readCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("source Read was not called")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)

	if got := readCalls.Load(); got > 20 {
		t.Fatalf("Read called %d times without progress in 30ms, want at most 20", got)
	}

	closed := make(chan error, 1)
	go func() { closed <- service.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close did not cancel the no-progress backoff")
	}
}

func TestServiceSurfacesTerminalReadError(t *testing.T) {
	terminalErr := errors.New("audio server disappeared")
	source := &fakeSource{
		name:   "failing",
		format: Format{SampleRate: 44_100, Channels: 2},
		readFn: func(context.Context, []float32) (int, error) {
			return 0, terminalErr
		},
	}

	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	deadline := time.Now().Add(time.Second)
	for !errors.Is(service.Err(), terminalErr) {
		if time.Now().After(deadline) {
			t.Fatalf("Err = %v, want %v", service.Err(), terminalErr)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestServiceRejectsNonFinitePCMWithoutPublishingFrame(t *testing.T) {
	sent := false
	source := &fakeSource{
		name:   "non-finite",
		format: Format{SampleRate: 48_000, Channels: 1},
		readFn: func(ctx context.Context, dst []float32) (int, error) {
			if !sent {
				sent = true
				for i := 0; i < 1_600; i++ {
					dst[i] = 0.1
				}
				dst[800] = float32(math.NaN())
				return 1_600, nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	service, err := NewService(source)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	deadline := time.Now().Add(time.Second)
	for {
		if err := service.Err(); err != nil {
			if _, ok := service.Latest(); ok {
				t.Fatal("service published a frame for rejected non-finite PCM")
			}
			return
		}
		if frame, ok := service.Latest(); ok {
			for band, value := range frame.Bands {
				if math.IsNaN(value) || math.IsInf(value, 0) {
					t.Fatalf("service published non-finite band %d: %v", band, value)
				}
			}
			t.Fatal("service published a frame instead of rejecting non-finite PCM")
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not surface a terminal error for non-finite PCM")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNewServiceRejectsInvalidSourceFormat(t *testing.T) {
	source := &fakeSource{
		name:   "invalid",
		format: Format{SampleRate: 0, Channels: 1},
		readFn: func(context.Context, []float32) (int, error) {
			t.Fatal("Read called for invalid format")
			return 0, nil
		},
	}

	if _, err := NewService(source); err == nil {
		t.Fatal("NewService accepted an invalid source format")
	}
}

func waitForFrame(t *testing.T, service *Service) Frame {
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
