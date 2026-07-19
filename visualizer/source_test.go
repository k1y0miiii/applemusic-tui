package visualizer

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

var testFormat = Format{SampleRate: 48_000, Channels: 2}

func TestPCMSourceReportsNameAndFormat(t *testing.T) {
	source, err := newPCMSource("PIPEWIRE", testFormat, 2, nil)
	if err != nil {
		t.Fatalf("newPCMSource() error = %v", err)
	}

	if got := source.Name(); got != "PIPEWIRE" {
		t.Errorf("Name() = %q, want PIPEWIRE", got)
	}
	if got := source.Format(); got != testFormat {
		t.Errorf("Format() = %+v, want %+v", got, testFormat)
	}
}

func TestPCMSourceRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		format     Format
		queueDepth int
	}{
		{name: "sample rate", format: Format{SampleRate: 0, Channels: 2}, queueDepth: 1},
		{name: "channels", format: Format{SampleRate: 48_000, Channels: 3}, queueDepth: 1},
		{name: "queue depth", format: testFormat, queueDepth: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newPCMSource("PIPEWIRE", tt.format, tt.queueDepth, nil); err == nil {
				t.Fatal("newPCMSource() error = nil, want configuration error")
			}
		})
	}
}

func TestPCMSourceCopiesChunksAndDropsNewestWhenQueueIsFull(t *testing.T) {
	source := mustPCMSource(t, 1, nil)
	first := []float32{1, 2, 3}

	if n, err := source.write(first); n != len(first) || err != nil {
		t.Fatalf("write(first) = (%d, %v), want (%d, nil)", n, err, len(first))
	}
	first[0] = 99

	second := []float32{4, 5, 6}
	if n, err := source.write(second); n != len(second) || err != nil {
		t.Fatalf("write(second) = (%d, %v), want (%d, nil)", n, err, len(second))
	}

	dst := make([]float32, 3)
	n, err := source.Read(context.Background(), dst)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 || !slices.Equal(dst[:n], []float32{1, 2, 3}) {
		t.Fatalf("Read() = %v (%d samples), want [1 2 3]", dst[:n], n)
	}
}

func TestPCMSourceCarriesRemainderAcrossReads(t *testing.T) {
	source := mustPCMSource(t, 2, nil)
	samples := []float32{1, 2, 3, 4, 5}
	if _, err := source.write(samples); err != nil {
		t.Fatalf("write() error = %v", err)
	}

	first := make([]float32, 2)
	n, err := source.Read(context.Background(), first)
	if err != nil {
		t.Fatalf("first Read() error = %v", err)
	}
	if n != 2 || !slices.Equal(first[:n], []float32{1, 2}) {
		t.Fatalf("first Read() = %v (%d samples), want [1 2]", first[:n], n)
	}

	second := make([]float32, 4)
	n, err = source.Read(context.Background(), second)
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if n != 3 || !slices.Equal(second[:n], []float32{3, 4, 5}) {
		t.Fatalf("second Read() = %v (%d samples), want [3 4 5]", second[:n], n)
	}
}

func TestPCMSourceReadHonorsCanceledContext(t *testing.T) {
	source := mustPCMSource(t, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n, err := source.Read(ctx, make([]float32, 1))
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() = (%d, %v), want (0, context.Canceled)", n, err)
	}
}

func TestPCMSourceTerminalErrorUnblocksRead(t *testing.T) {
	source := mustPCMSource(t, 1, nil)
	serverErr := errors.New("server connection closed")
	result := make(chan error, 1)

	go func() {
		_, err := source.Read(context.Background(), make([]float32, 1))
		result <- err
	}()

	source.fail(serverErr)

	select {
	case err := <-result:
		if !errors.Is(err, serverErr) {
			t.Fatalf("Read() error = %v, want %v", err, serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Read() remained blocked after terminal error")
	}
}

func TestPCMSourceCloseIsIdempotentAndUnblocksRead(t *testing.T) {
	var closeCalls atomic.Int32
	closeErr := errors.New("backend close failed")
	source := mustPCMSource(t, 1, func() error {
		closeCalls.Add(1)
		return closeErr
	})
	result := make(chan error, 1)

	go func() {
		_, err := source.Read(context.Background(), make([]float32, 1))
		result <- err
	}()

	if err := source.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("first Close() error = %v, want %v", err, closeErr)
	}
	if err := source.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("second Close() error = %v, want %v", err, closeErr)
	}
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("backend close calls = %d, want 1", got)
	}

	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Read() error = %v, want io.ErrClosedPipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read() remained blocked after Close()")
	}
}

func TestPCMSourceWriteAfterCloseIsSafe(t *testing.T) {
	source := mustPCMSource(t, 1, nil)
	if err := source.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	samples := []float32{1, 2}
	n, err := source.write(samples)
	if n != len(samples) || err != nil {
		t.Fatalf("write() after close = (%d, %v), want (%d, nil)", n, err, len(samples))
	}
}

func TestSourceCleanupNormalCloseWaitsForMonitor(t *testing.T) {
	monitorDone := make(chan struct{})
	var clientCloseCalls atomic.Int32
	cleanup := newSourceCleanupCoordinator(
		monitorDone,
		func() { clientCloseCalls.Add(1) },
	)
	result := make(chan error, 1)

	go func() {
		result <- cleanup.close()
	}()

	select {
	case err := <-result:
		t.Fatalf("close() returned before monitor exit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if got := clientCloseCalls.Load(); got != 0 {
		t.Fatalf("client close calls before monitor exit = %d, want 0", got)
	}

	close(monitorDone)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close() remained blocked after monitor exit")
	}
	if err := cleanup.close(); err != nil {
		t.Fatalf("second close() error = %v", err)
	}
	if got := clientCloseCalls.Load(); got != 1 {
		t.Fatalf("client close calls = %d, want 1", got)
	}
}

func TestSourceCleanupServerFailureWaitsForMonitor(t *testing.T) {
	monitorDone := make(chan struct{})
	var clientCloseCalls atomic.Int32
	cleanup := newSourceCleanupCoordinator(
		monitorDone,
		func() { clientCloseCalls.Add(1) },
	)
	source := mustPCMSource(t, 1, cleanup.close)
	source.fail(errors.New("server connection closed"))
	result := make(chan error, 1)

	go func() {
		result <- source.Close()
	}()

	select {
	case err := <-result:
		t.Fatalf("Close() returned before failed monitor exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if got := clientCloseCalls.Load(); got != 0 {
		t.Fatalf("client close calls before monitor exit = %d, want 0", got)
	}

	close(monitorDone)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() remained blocked after failed monitor exited")
	}
	if err := source.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got := clientCloseCalls.Load(); got != 1 {
		t.Fatalf("client close calls = %d, want 1", got)
	}
}

func TestSourceCleanupConcurrentServerFailureAndClose(t *testing.T) {
	monitorDone := make(chan struct{})
	var clientCloseCalls atomic.Int32
	cleanup := newSourceCleanupCoordinator(
		monitorDone,
		func() { clientCloseCalls.Add(1) },
	)
	source := mustPCMSource(t, 1, cleanup.close)
	start := make(chan struct{})
	closeResult := make(chan error, 1)
	monitorFinished := make(chan struct{})

	go func() {
		<-start
		closeResult <- source.Close()
	}()
	go func() {
		<-start
		source.fail(errors.New("server connection closed"))
		close(monitorDone)
		close(monitorFinished)
	}()
	close(start)

	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() deadlocked with server failure")
	}
	select {
	case <-monitorFinished:
	case <-time.After(time.Second):
		t.Fatal("server failure goroutine deadlocked")
	}

	if got := clientCloseCalls.Load(); got != 1 {
		t.Errorf("client close calls = %d, want 1", got)
	}
}

func mustPCMSource(t *testing.T, queueDepth int, closeBackend func() error) *pcmSource {
	t.Helper()
	source, err := newPCMSource("PIPEWIRE", testFormat, queueDepth, closeBackend)
	if err != nil {
		t.Fatalf("newPCMSource() error = %v", err)
	}
	return source
}
