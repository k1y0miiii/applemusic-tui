//go:build darwin && cgo

package visualizer

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

type fakeCoreAudioTap struct {
	readFunc   func([]float32) int
	readCalls  atomic.Int32
	closeCalls atomic.Int32
	closeErr   error
}

type blockingCloseCoreAudioTap struct {
	closeCalls    atomic.Int32
	closeStarted  chan struct{}
	releaseClose  chan struct{}
	closeFinished chan struct{}
	startOnce     sync.Once
	finishOnce    sync.Once
}

func (t *fakeCoreAudioTap) read(dst []float32) int {
	t.readCalls.Add(1)
	if t.readFunc == nil {
		return 0
	}
	return t.readFunc(dst)
}

func (t *fakeCoreAudioTap) close() error {
	t.closeCalls.Add(1)
	return t.closeErr
}

func (t *blockingCloseCoreAudioTap) read([]float32) int {
	return 0
}

func (t *blockingCloseCoreAudioTap) close() error {
	t.closeCalls.Add(1)
	t.startOnce.Do(func() { close(t.closeStarted) })
	<-t.releaseClose
	t.finishOnce.Do(func() { close(t.closeFinished) })
	return nil
}

func TestOpenSystemSourceTimesOutAndClosesLateNativeTap(t *testing.T) {
	previousOpener := coreAudioNativeOpener
	previousTimeout := coreAudioOpenTimeout
	releaseOpen := make(chan struct{})
	openStarted := make(chan struct{})
	var startOnce sync.Once
	tap := &fakeCoreAudioTap{}
	coreAudioNativeOpener = func() (coreAudioTap, Format, error) {
		startOnce.Do(func() { close(openStarted) })
		<-releaseOpen
		return tap, Format{SampleRate: 48_000, Channels: 2}, nil
	}
	coreAudioOpenTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		coreAudioNativeOpener = previousOpener
		coreAudioOpenTimeout = previousTimeout
		select {
		case <-releaseOpen:
		default:
			close(releaseOpen)
		}
	})

	startedAt := time.Now()
	source, err := OpenSystemSource()
	elapsed := time.Since(startedAt)
	if source != nil {
		t.Fatalf("OpenSystemSource() source = %T, want nil", source)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenSystemSource() error = %v, want ErrUnavailable", err)
	}
	for _, text := range []string{
		"System Audio Recording permission may be waiting",
		"Terminal/amtui",
		"signed app bundle",
		"NSAudioCaptureUsageDescription",
		"restart",
	} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("OpenSystemSource() error = %q, want %q", err, text)
		}
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("OpenSystemSource() took %v, want a bounded return", elapsed)
	}
	select {
	case <-openStarted:
	case <-time.After(time.Second):
		t.Fatal("native opener was not called")
	}

	close(releaseOpen)
	waitForAtomicAtLeast(t, &tap.closeCalls, 1)
	time.Sleep(10 * time.Millisecond)
	if got := tap.closeCalls.Load(); got != 1 {
		t.Fatalf("late native close calls = %d, want 1", got)
	}
}

func TestOpenSystemSourceTimeoutDoesNotWaitForBlockingLateClose(t *testing.T) {
	previousOpener := coreAudioNativeOpener
	previousTimeout := coreAudioOpenTimeout
	releaseOpen := make(chan struct{})
	openStarted := make(chan struct{})
	tap := &blockingCloseCoreAudioTap{
		closeStarted:  make(chan struct{}),
		releaseClose:  make(chan struct{}),
		closeFinished: make(chan struct{}),
	}
	coreAudioNativeOpener = func() (coreAudioTap, Format, error) {
		close(openStarted)
		<-releaseOpen
		return tap, Format{SampleRate: 48_000, Channels: 2}, nil
	}
	coreAudioOpenTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		coreAudioNativeOpener = previousOpener
		coreAudioOpenTimeout = previousTimeout
		closeChannelOnce(releaseOpen)
		closeChannelOnce(tap.releaseClose)
	})

	startedAt := time.Now()
	source, err := OpenSystemSource()
	elapsed := time.Since(startedAt)
	if source != nil {
		t.Fatalf("OpenSystemSource() source = %T, want nil", source)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenSystemSource() error = %v, want ErrUnavailable", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("OpenSystemSource() waited %v for native cleanup", elapsed)
	}
	select {
	case <-openStarted:
	case <-time.After(time.Second):
		t.Fatal("native opener was not called")
	}

	close(releaseOpen)
	select {
	case <-tap.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("late native close did not start")
	}
	select {
	case <-tap.closeFinished:
		t.Fatal("late native close finished before it was released")
	default:
	}
	if got := tap.closeCalls.Load(); got != 1 {
		t.Fatalf("late native close calls = %d, want 1", got)
	}

	close(tap.releaseClose)
	select {
	case <-tap.closeFinished:
	case <-time.After(time.Second):
		t.Fatal("late native close remained blocked after release")
	}
	if got := tap.closeCalls.Load(); got != 1 {
		t.Fatalf("late native close calls = %d, want 1", got)
	}
}

func TestCoreAudioSourceReportsBackendAndActualFormat(t *testing.T) {
	tap := &fakeCoreAudioTap{}
	format := Format{SampleRate: 44_100, Channels: 2}
	source, err := newCoreAudioSource(func() (coreAudioTap, Format, error) {
		return tap, format, nil
	})
	if err != nil {
		t.Fatalf("newCoreAudioSource() error = %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })

	if got := source.Name(); got != "COREAUDIO" {
		t.Errorf("Name() = %q, want COREAUDIO", got)
	}
	if got := source.Format(); got != format {
		t.Errorf("Format() = %+v, want %+v", got, format)
	}
}

func TestCoreAudioSourceRejectsInvalidNativeFormatAndClosesTap(t *testing.T) {
	tap := &fakeCoreAudioTap{}
	_, err := newCoreAudioSource(func() (coreAudioTap, Format, error) {
		return tap, Format{SampleRate: 48_000, Channels: 3}, nil
	})
	if err == nil {
		t.Fatal("newCoreAudioSource() error = nil, want invalid format error")
	}
	if got := tap.closeCalls.Load(); got != 1 {
		t.Fatalf("native close calls = %d, want 1", got)
	}
}

func TestCoreAudioSourceReadPollsUntilSamplesArrive(t *testing.T) {
	tap := &fakeCoreAudioTap{}
	tap.readFunc = func(dst []float32) int {
		if tap.readCalls.Load() < 2 {
			return 0
		}
		return copy(dst, []float32{0.25, -0.5})
	}
	source := mustCoreAudioSource(t, tap)

	dst := make([]float32, 4)
	n, err := source.Read(context.Background(), dst)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 2 || dst[0] != 0.25 || dst[1] != -0.5 {
		t.Fatalf("Read() = (%d, %v), want (2, [0.25 -0.5])", n, dst[:n])
	}
	if got := tap.readCalls.Load(); got < 2 {
		t.Fatalf("native read calls = %d, want at least 2", got)
	}
}

func TestCoreAudioSourceReadHonorsContextWhileRingIsEmpty(t *testing.T) {
	tap := &fakeCoreAudioTap{}
	source := mustCoreAudioSource(t, tap)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	go func() {
		_, err := source.Read(ctx, make([]float32, 8))
		result <- err
	}()

	waitForAtomicAtLeast(t, &tap.readCalls, 1)
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read() did not stop after context cancellation")
	}
}

func TestCoreAudioSourceZeroLengthReadDoesNotCallNativeCode(t *testing.T) {
	tap := &fakeCoreAudioTap{}
	source := mustCoreAudioSource(t, tap)

	n, err := source.Read(context.Background(), nil)
	if n != 0 || err != nil {
		t.Fatalf("Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if got := tap.readCalls.Load(); got != 0 {
		t.Fatalf("native read calls = %d, want 0", got)
	}
}

func TestCoreAudioSourceSerializesNativeReads(t *testing.T) {
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var startOnce sync.Once
	tap := &fakeCoreAudioTap{
		readFunc: func(dst []float32) int {
			startOnce.Do(func() { close(readStarted) })
			<-releaseRead
			dst[0] = 0.5
			return 1
		},
	}
	source := mustCoreAudioSource(t, tap)
	results := make(chan error, 2)

	go func() {
		_, err := source.Read(context.Background(), make([]float32, 1))
		results <- err
	}()
	<-readStarted
	go func() {
		_, err := source.Read(context.Background(), make([]float32, 1))
		results <- err
	}()

	time.Sleep(20 * time.Millisecond)
	concurrentCalls := tap.readCalls.Load()
	close(releaseRead)
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Read() remained blocked")
		}
	}
	if concurrentCalls != 1 {
		t.Fatalf("concurrent native read calls = %d, want 1", concurrentCalls)
	}
}

func TestCoreAudioSourceCloseIsIdempotentAndUnblocksRead(t *testing.T) {
	tap := &fakeCoreAudioTap{}
	source := mustCoreAudioSource(t, tap)
	result := make(chan error, 1)

	go func() {
		_, err := source.Read(context.Background(), make([]float32, 8))
		result <- err
	}()
	waitForAtomicAtLeast(t, &tap.readCalls, 1)

	if err := source.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got := tap.closeCalls.Load(); got != 1 {
		t.Fatalf("native close calls = %d, want 1", got)
	}

	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("blocked Read() error = %v, want io.ErrClosedPipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read() remained blocked after Close()")
	}

	if n, err := source.Read(context.Background(), make([]float32, 1)); n != 0 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read() after Close() = (%d, %v), want (0, io.ErrClosedPipe)", n, err)
	}
}

func TestCoreAudioSourceCloseReturnsNativeErrorExactlyOnce(t *testing.T) {
	closeErr := errors.New("AudioDeviceStop failed")
	tap := &fakeCoreAudioTap{closeErr: closeErr}
	source := mustCoreAudioSource(t, tap)

	firstErr := source.Close()
	if firstErr != closeErr {
		t.Fatalf("first Close() error = %v, want exact %v", firstErr, closeErr)
	}
	secondErr := source.Close()
	if secondErr != closeErr {
		t.Fatalf("second Close() error = %v, want exact %v", secondErr, closeErr)
	}
	if got := tap.closeCalls.Load(); got != 1 {
		t.Fatalf("native close calls = %d, want 1", got)
	}
}

func TestCoreAudioSourceCloseWaitsForNativeRead(t *testing.T) {
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var startOnce sync.Once
	tap := &fakeCoreAudioTap{
		readFunc: func([]float32) int {
			startOnce.Do(func() { close(readStarted) })
			<-releaseRead
			return 0
		},
	}
	source := mustCoreAudioSource(t, tap)
	readResult := make(chan error, 1)
	closeResult := make(chan error, 1)

	go func() {
		_, err := source.Read(context.Background(), make([]float32, 8))
		readResult <- err
	}()
	<-readStarted
	go func() {
		closeResult <- source.Close()
	}()

	select {
	case err := <-closeResult:
		t.Fatalf("Close() returned during native read: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if got := tap.closeCalls.Load(); got != 0 {
		t.Fatalf("native close calls during native read = %d, want 0", got)
	}

	close(releaseRead)
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() remained blocked after native read returned")
	}
	select {
	case err := <-readResult:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Read() error = %v, want io.ErrClosedPipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read() remained blocked after native read returned")
	}
}

func TestCoreAudioNativeConversionFormatsAndLayouts(t *testing.T) {
	kinds := []struct {
		name string
		kind coreAudioNativeSampleKind
	}{
		{name: "Float32", kind: coreAudioNativeFloat32},
		{name: "Float64", kind: coreAudioNativeFloat64},
		{name: "PCM16", kind: coreAudioNativePCM16},
	}
	layouts := []struct {
		name           string
		nonInterleaved bool
	}{
		{name: "interleaved"},
		{name: "non-interleaved", nonInterleaved: true},
	}
	expected := []float32{0.25, -0.5, 0.5, -1}

	for _, kind := range kinds {
		for _, layout := range layouts {
			t.Run(kind.name+"/"+layout.name, func(t *testing.T) {
				plane0, plane0Bytes, plane1, plane1Bytes, keepAlive :=
					nativeConversionInput(kind.kind, layout.nonInterleaved)
				dst := make([]float32, len(expected))
				n := coreAudioConvertForTest(
					kind.kind,
					layout.nonInterleaved,
					2,
					plane0,
					plane0Bytes,
					plane1,
					plane1Bytes,
					dst,
				)
				runtime.KeepAlive(keepAlive)

				if n != len(expected) {
					t.Fatalf("native conversion count = %d, want %d", n, len(expected))
				}
				if !slices.Equal(dst, expected) {
					t.Fatalf("native conversion = %v, want %v", dst, expected)
				}
			})
		}
	}
}

func TestCoreAudioNativeRingWraps(t *testing.T) {
	ring := make([]float32, 5)
	var readIndex, writeIndex uint64

	if !coreAudioRingPushForTest(
		ring,
		&readIndex,
		&writeIndex,
		[]float32{1, 2, 3, 4},
	) {
		t.Fatal("first native ring push was dropped")
	}
	firstRead := make([]float32, 3)
	if n := coreAudioRingReadForTest(ring, &readIndex, &writeIndex, firstRead); n != 3 {
		t.Fatalf("first native ring read count = %d, want 3", n)
	}
	if !slices.Equal(firstRead, []float32{1, 2, 3}) {
		t.Fatalf("first native ring read = %v, want [1 2 3]", firstRead)
	}

	if !coreAudioRingPushForTest(
		ring,
		&readIndex,
		&writeIndex,
		[]float32{5, 6, 7},
	) {
		t.Fatal("wrapping native ring push was dropped")
	}
	secondRead := make([]float32, 5)
	n := coreAudioRingReadForTest(ring, &readIndex, &writeIndex, secondRead)
	if n != 4 || !slices.Equal(secondRead[:n], []float32{4, 5, 6, 7}) {
		t.Fatalf("wrapped native ring read = %v (%d), want [4 5 6 7]", secondRead[:n], n)
	}
}

func TestCoreAudioNativeRingDropsNewestChunkOnOverflow(t *testing.T) {
	ring := make([]float32, 4)
	var readIndex, writeIndex uint64

	if !coreAudioRingPushForTest(
		ring,
		&readIndex,
		&writeIndex,
		[]float32{1, 2, 3},
	) {
		t.Fatal("first native ring push was dropped")
	}
	writeBeforeOverflow := writeIndex
	if coreAudioRingPushForTest(
		ring,
		&readIndex,
		&writeIndex,
		[]float32{4, 5},
	) {
		t.Fatal("overflowing native ring push succeeded, want drop-newest")
	}
	if writeIndex != writeBeforeOverflow {
		t.Fatalf("write index after drop = %d, want %d", writeIndex, writeBeforeOverflow)
	}

	dst := make([]float32, len(ring))
	n := coreAudioRingReadForTest(ring, &readIndex, &writeIndex, dst)
	if n != 3 || !slices.Equal(dst[:n], []float32{1, 2, 3}) {
		t.Fatalf("native ring after drop = %v (%d), want [1 2 3]", dst[:n], n)
	}
}

func TestCoreAudioNativeTeardownReleasePolicy(t *testing.T) {
	tests := []struct {
		name          string
		stopStatus    int32
		destroyStatus int32
		wantRelease   bool
	}{
		{name: "success", wantRelease: true},
		{name: "stop failure leaks safely", stopStatus: -1},
		{name: "IOProc destroy failure leaks safely", destroyStatus: -1},
		{
			name:          "both failures leak safely",
			stopStatus:    -1,
			destroyStatus: -2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coreAudioTeardownCanReleaseForTest(
				tt.stopStatus,
				tt.destroyStatus,
			)
			if got != tt.wantRelease {
				t.Fatalf(
					"teardown release policy = %t, want %t",
					got,
					tt.wantRelease,
				)
			}
		})
	}
}

func TestCoreAudioNativeRingSPSCConcurrentStress(t *testing.T) {
	const (
		sampleCount  = 250_000
		ringCapacity = 31
	)
	if status := coreAudioRingSPSCStressForTest(sampleCount, ringCapacity); status != 0 {
		t.Fatalf("native SPSC stress status = %d, want 0", status)
	}
}

func TestCoreAudioLiveSmoke(t *testing.T) {
	if os.Getenv("AMTUI_TEST_COREAUDIO") != "1" {
		t.Skip("set AMTUI_TEST_COREAUDIO=1 to exercise the live CoreAudio tap")
	}

	overallCtx, cancelOverall := context.WithTimeout(
		context.Background(),
		defaultCoreAudioOpenTimeout+3*time.Second,
	)
	defer cancelOverall()

	source, err := OpenSystemSource()
	if err != nil {
		t.Fatalf(
			"open CoreAudio tap failed before signal check: %v (System Audio Recording permission is expected for this opt-in test)",
			err,
		)
	}
	defer func() {
		if err := source.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	readCtx, cancelRead := context.WithTimeout(overallCtx, 2*time.Second)
	defer cancelRead()
	buffer := make([]float32, 4096)
	total := 0
	nonZero := false
	for {
		n, readErr := source.Read(readCtx, buffer)
		total += n
		for _, sample := range buffer[:n] {
			if sample != 0 {
				nonZero = true
				break
			}
		}
		if readErr != nil {
			if errors.Is(readErr, context.DeadlineExceeded) {
				break
			}
			t.Fatalf("Read() error = %v", readErr)
		}
	}

	if nonZero {
		t.Logf("CoreAudio tap captured %d samples with a non-zero signal", total)
	} else {
		t.Logf(
			"CoreAudio tap opened and read %d samples; no signal was present during the 2s window (play audio and verify System Audio Recording permission)",
			total,
		)
	}
}

func nativeConversionInput(
	kind coreAudioNativeSampleKind,
	nonInterleaved bool,
) (
	plane0 unsafe.Pointer,
	plane0Bytes int,
	plane1 unsafe.Pointer,
	plane1Bytes int,
	keepAlive any,
) {
	switch kind {
	case coreAudioNativeFloat32:
		if nonInterleaved {
			planes := [][]float32{{0.25, 0.5}, {-0.5, -1}}
			return unsafe.Pointer(&planes[0][0]), len(planes[0]) * 4,
				unsafe.Pointer(&planes[1][0]), len(planes[1]) * 4, planes
		}
		samples := []float32{0.25, -0.5, 0.5, -1}
		return unsafe.Pointer(&samples[0]), len(samples) * 4, nil, 0, samples
	case coreAudioNativeFloat64:
		if nonInterleaved {
			planes := [][]float64{{0.25, 0.5}, {-0.5, -1}}
			return unsafe.Pointer(&planes[0][0]), len(planes[0]) * 8,
				unsafe.Pointer(&planes[1][0]), len(planes[1]) * 8, planes
		}
		samples := []float64{0.25, -0.5, 0.5, -1}
		return unsafe.Pointer(&samples[0]), len(samples) * 8, nil, 0, samples
	case coreAudioNativePCM16:
		if nonInterleaved {
			planes := [][]int16{{8192, 16384}, {-16384, -32768}}
			return unsafe.Pointer(&planes[0][0]), len(planes[0]) * 2,
				unsafe.Pointer(&planes[1][0]), len(planes[1]) * 2, planes
		}
		samples := []int16{8192, -16384, 16384, -32768}
		return unsafe.Pointer(&samples[0]), len(samples) * 2, nil, 0, samples
	default:
		panic("unknown native sample kind")
	}
}

func mustCoreAudioSource(t *testing.T, tap coreAudioTap) *coreAudioSource {
	t.Helper()
	source, err := newCoreAudioSource(func() (coreAudioTap, Format, error) {
		return tap, Format{SampleRate: 48_000, Channels: 2}, nil
	})
	if err != nil {
		t.Fatalf("newCoreAudioSource() error = %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })
	return source
}

func waitForAtomicAtLeast(t *testing.T, value *atomic.Int32, minimum int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for value.Load() < minimum {
		if time.Now().After(deadline) {
			t.Fatalf("value remained below %d", minimum)
		}
		time.Sleep(time.Millisecond)
	}
}

func closeChannelOnce(channel chan struct{}) {
	select {
	case <-channel:
	default:
		close(channel)
	}
}
