//go:build darwin && cgo

package visualizer

/*
#cgo CFLAGS: -mmacosx-version-min=14.2
#cgo LDFLAGS: -mmacosx-version-min=14.2 -framework CoreAudio -framework Foundation
#include "coreaudio_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	coreAudioPollInterval       = 5 * time.Millisecond
	defaultCoreAudioOpenTimeout = 5 * time.Second
)

type coreAudioTap interface {
	read([]float32) int
	close() error
}

type coreAudioTapOpener func() (coreAudioTap, Format, error)

type coreAudioOpenDecision uint8

const (
	coreAudioOpenAccepted coreAudioOpenDecision = iota + 1
	coreAudioOpenAbandoned
)

var (
	coreAudioNativeOpener = openNativeCoreAudioTap
	coreAudioOpenTimeout  = defaultCoreAudioOpenTimeout
)

type coreAudioOpenResult struct {
	tap    coreAudioTap
	format Format
	err    error
}

type coreAudioSource struct {
	format Format

	mu   sync.Mutex
	tap  coreAudioTap
	done chan struct{}

	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

var _ Source = (*coreAudioSource)(nil)

func openSystemSource() (Source, error) {
	opener := coreAudioNativeOpener
	timeout := coreAudioOpenTimeout
	return openCoreAudioSystemSource(opener, timeout)
}

func openCoreAudioSystemSource(
	opener coreAudioTapOpener,
	timeout time.Duration,
) (*coreAudioSource, error) {
	if opener == nil {
		return nil, fmt.Errorf("%w: CoreAudio tap opener is nil", ErrUnavailable)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf(
			"%w: CoreAudio open timeout must be positive, got %v",
			ErrUnavailable,
			timeout,
		)
	}

	resultCh := make(chan coreAudioOpenResult, 1)
	ownershipDecision := make(chan coreAudioOpenDecision, 1)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// CoreAudio/TCC has no cancellation API and may block in any open stage,
	// including AudioDeviceCreateIOProcID or AudioDeviceStart. The worker owns
	// and closes a result that arrives after the caller's overall deadline.
	// Until TCC resolves, one native goroutine can remain blocked as a last
	// resort; openSystemSource starts only one such attempt.
	go func() {
		tap, format, err := opener()
		resultCh <- coreAudioOpenResult{
			tap:    tap,
			format: format,
			err:    err,
		}

		switch <-ownershipDecision {
		case coreAudioOpenAbandoned:
			abandoned := <-resultCh
			_ = abandoned.closeTap()
		case coreAudioOpenAccepted:
		}
	}()

	select {
	case result := <-resultCh:
		ownershipDecision <- coreAudioOpenAccepted
		return newCoreAudioSource(func() (coreAudioTap, Format, error) {
			return result.tap, result.format, result.err
		})
	case <-timer.C:
		ownershipDecision <- coreAudioOpenAbandoned
		return nil, fmt.Errorf(
			"%w: CoreAudio open timed out after %v; "+
				"System Audio Recording permission may be waiting; grant permission "+
				"to Terminal/amtui or run from a signed app bundle with "+
				"NSAudioCaptureUsageDescription, then restart",
			ErrUnavailable,
			timeout,
		)
	}
}

func (r coreAudioOpenResult) closeTap() error {
	if r.tap != nil {
		return r.tap.close()
	}
	return nil
}

func newCoreAudioSource(opener coreAudioTapOpener) (*coreAudioSource, error) {
	if opener == nil {
		return nil, fmt.Errorf("visualizer: CoreAudio tap opener is nil")
	}

	tap, format, err := opener()
	if err != nil {
		if tap != nil {
			_ = tap.close()
		}
		return nil, err
	}
	if tap == nil {
		return nil, fmt.Errorf("visualizer: CoreAudio returned a nil tap")
	}
	if err := validateSourceFormat(format); err != nil {
		_ = tap.close()
		return nil, fmt.Errorf("visualizer: CoreAudio tap format: %w", err)
	}

	return &coreAudioSource{
		format: format,
		tap:    tap,
		done:   make(chan struct{}),
	}, nil
}

func (s *coreAudioSource) Name() string {
	return "COREAUDIO"
}

func (s *coreAudioSource) Format() Format {
	return s.format
}

func (s *coreAudioSource) Read(ctx context.Context, dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.closed.Load() {
		return 0, io.ErrClosedPipe
	}

	ticker := time.NewTicker(coreAudioPollInterval)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		tap := s.tap
		if tap == nil {
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		n := tap.read(dst)
		s.mu.Unlock()

		if n < 0 || n > len(dst) {
			return 0, fmt.Errorf(
				"visualizer: CoreAudio returned invalid sample count %d for buffer length %d",
				n,
				len(dst),
			)
		}
		if n != 0 {
			return n, nil
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.done:
			return 0, io.ErrClosedPipe
		case <-ticker.C:
		}
	}
}

func (s *coreAudioSource) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.done)

		s.mu.Lock()
		tap := s.tap
		s.tap = nil
		if tap != nil {
			s.closeErr = tap.close()
		}
		s.mu.Unlock()
	})
	return s.closeErr
}

type nativeCoreAudioTap struct {
	ptr *C.amtui_tap
}

func openNativeCoreAudioTap() (coreAudioTap, Format, error) {
	var tap *C.amtui_tap
	var rate C.double
	var channels C.uint32_t
	var errorBuffer [1024]C.char

	result := C.amtui_tap_open(
		&tap,
		&rate,
		&channels,
		&errorBuffer[0],
		C.size_t(len(errorBuffer)),
	)
	if result != 0 {
		message := C.GoString(&errorBuffer[0])
		if message == "" {
			message = "CoreAudio tap open failed without an error message"
		}
		return nil, Format{}, fmt.Errorf("visualizer: %s", message)
	}
	if tap == nil {
		return nil, Format{}, fmt.Errorf("visualizer: CoreAudio tap open returned nil")
	}

	sampleRate := float64(rate)
	maxInt := float64(^uint(0) >> 1)
	if math.IsNaN(sampleRate) || math.IsInf(sampleRate, 0) ||
		sampleRate <= 0 || sampleRate > maxInt {
		_ = C.amtui_tap_close(tap)
		return nil, Format{}, fmt.Errorf(
			"visualizer: CoreAudio returned invalid sample rate %v",
			sampleRate,
		)
	}

	return &nativeCoreAudioTap{ptr: tap}, Format{
		SampleRate: int(math.Round(sampleRate)),
		Channels:   int(channels),
	}, nil
}

func (t *nativeCoreAudioTap) read(dst []float32) int {
	if t == nil || t.ptr == nil || len(dst) == 0 {
		return 0
	}
	maxSamples := len(dst)
	const maxCInt = 1<<31 - 1
	if maxSamples > maxCInt {
		maxSamples = maxCInt
	}
	return int(C.amtui_tap_read(
		t.ptr,
		(*C.float)(unsafe.Pointer(&dst[0])),
		C.int(maxSamples),
	))
}

func (t *nativeCoreAudioTap) close() error {
	if t == nil || t.ptr == nil {
		return nil
	}
	status := int32(C.amtui_tap_close(t.ptr))
	t.ptr = nil
	if status != 0 {
		return fmt.Errorf(
			"visualizer: CoreAudio close failed (OSStatus=%d)",
			status,
		)
	}
	return nil
}

type coreAudioNativeSampleKind uint32

const (
	coreAudioNativeFloat32 coreAudioNativeSampleKind = C.AMTUI_INTERNAL_SAMPLE_FLOAT32
	coreAudioNativeFloat64 coreAudioNativeSampleKind = C.AMTUI_INTERNAL_SAMPLE_FLOAT64
	coreAudioNativePCM16   coreAudioNativeSampleKind = C.AMTUI_INTERNAL_SAMPLE_PCM16
)

func coreAudioConvertForTest(
	kind coreAudioNativeSampleKind,
	nonInterleaved bool,
	channels int,
	buffer0 unsafe.Pointer,
	buffer0Bytes int,
	buffer1 unsafe.Pointer,
	buffer1Bytes int,
	dst []float32,
) int {
	if channels <= 0 || buffer0Bytes < 0 || buffer1Bytes < 0 || len(dst) == 0 {
		return -1
	}
	layout := C.int(0)
	if nonInterleaved {
		layout = 1
	}
	return int(C.amtui_internal_convert_push(
		C.uint32_t(kind),
		layout,
		C.uint32_t(channels),
		buffer0,
		C.size_t(buffer0Bytes),
		buffer1,
		C.size_t(buffer1Bytes),
		(*C.float)(unsafe.Pointer(&dst[0])),
		C.int(len(dst)),
	))
}

func coreAudioRingPushForTest(
	ring []float32,
	readIndex *uint64,
	writeIndex *uint64,
	src []float32,
) bool {
	if len(ring) == 0 || readIndex == nil || writeIndex == nil || len(src) == 0 {
		return false
	}
	read := C.uint64_t(*readIndex)
	write := C.uint64_t(*writeIndex)
	result := C.amtui_internal_ring_push(
		(*C.float)(unsafe.Pointer(&ring[0])),
		C.int(len(ring)),
		&read,
		&write,
		(*C.float)(unsafe.Pointer(&src[0])),
		C.int(len(src)),
	)
	*readIndex = uint64(read)
	*writeIndex = uint64(write)
	return result == 1
}

func coreAudioRingReadForTest(
	ring []float32,
	readIndex *uint64,
	writeIndex *uint64,
	dst []float32,
) int {
	if len(ring) == 0 || readIndex == nil || writeIndex == nil || len(dst) == 0 {
		return -1
	}
	read := C.uint64_t(*readIndex)
	write := C.uint64_t(*writeIndex)
	result := C.amtui_internal_ring_read(
		(*C.float)(unsafe.Pointer(&ring[0])),
		C.int(len(ring)),
		&read,
		&write,
		(*C.float)(unsafe.Pointer(&dst[0])),
		C.int(len(dst)),
	)
	*readIndex = uint64(read)
	*writeIndex = uint64(write)
	return int(result)
}

func coreAudioTeardownCanReleaseForTest(
	stopStatus int32,
	destroyIOProcStatus int32,
) bool {
	return C.amtui_internal_teardown_can_release(
		C.int32_t(stopStatus),
		C.int32_t(destroyIOProcStatus),
	) == 1
}

func coreAudioRingSPSCStressForTest(sampleCount, capacity int) int {
	if sampleCount <= 0 || capacity <= 0 {
		return -1
	}
	return int(C.amtui_internal_ring_spsc_stress(
		C.uint32_t(sampleCount),
		C.uint32_t(capacity),
	))
}
