package visualizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("visualizer: system audio source unavailable")

type Format struct {
	SampleRate int
	Channels   int
}

type Source interface {
	Name() string
	Format() Format
	Read(context.Context, []float32) (int, error)
	Close() error
}

type Frame struct {
	Bands  [32]float64
	Source string
	Live   bool
	At     time.Time
}

func OpenSystemSource() (Source, error) {
	source, err := openSystemSource()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if source == nil {
		return nil, fmt.Errorf("%w: backend returned a nil source", ErrUnavailable)
	}
	return source, nil
}

type pcmSource struct {
	name   string
	format Format
	chunks chan []float32
	done   chan struct{}

	readGate chan struct{}
	carry    []float32

	finishOnce  sync.Once
	terminalMu  sync.Mutex
	terminalErr error

	closeBackend func() error
	closeOnce    sync.Once
	closeErr     error
}

var _ Source = (*pcmSource)(nil)

func newPCMSource(
	name string,
	format Format,
	queueDepth int,
	closeBackend func() error,
) (*pcmSource, error) {
	if err := validateSourceFormat(format); err != nil {
		return nil, err
	}
	if queueDepth <= 0 {
		return nil, fmt.Errorf("visualizer: source queue depth must be positive, got %d", queueDepth)
	}
	return &pcmSource{
		name:         name,
		format:       format,
		chunks:       make(chan []float32, queueDepth),
		done:         make(chan struct{}),
		readGate:     make(chan struct{}, 1),
		closeBackend: closeBackend,
	}, nil
}

func validateSourceFormat(format Format) error {
	if format.SampleRate <= 0 {
		return fmt.Errorf("visualizer: invalid source sample rate %d", format.SampleRate)
	}
	if format.Channels != 1 && format.Channels != 2 {
		return fmt.Errorf(
			"visualizer: source channels must be mono or stereo, got %d",
			format.Channels,
		)
	}
	return nil
}

func (s *pcmSource) Name() string {
	return s.name
}

func (s *pcmSource) Format() Format {
	return s.format
}

func (s *pcmSource) Read(ctx context.Context, dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	select {
	case s.readGate <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.done:
		return 0, s.finishedError()
	}
	defer func() { <-s.readGate }()

	select {
	case <-s.done:
		return 0, s.finishedError()
	default:
	}
	if len(s.carry) != 0 {
		return s.readCarry(dst), nil
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.done:
		return 0, s.finishedError()
	case chunk := <-s.chunks:
		s.carry = chunk
		return s.readCarry(dst), nil
	}
}

func (s *pcmSource) readCarry(dst []float32) int {
	n := copy(dst, s.carry)
	s.carry = s.carry[n:]
	if len(s.carry) == 0 {
		s.carry = nil
	}
	return n
}

// write copies a Pulse callback chunk without blocking the protocol read loop.
// When the bounded queue is full, it drops the newest chunk so already queued
// audio remains in chronological order.
func (s *pcmSource) write(samples []float32) (int, error) {
	n := len(samples)
	if n == 0 {
		return 0, nil
	}

	select {
	case <-s.done:
		return n, nil
	default:
	}

	chunk := append([]float32(nil), samples...)
	select {
	case <-s.done:
	case s.chunks <- chunk:
	default:
	}
	return n, nil
}

func (s *pcmSource) fail(err error) {
	if err == nil {
		err = io.EOF
	}
	s.finish(err)
}

func (s *pcmSource) finish(err error) {
	s.finishOnce.Do(func() {
		s.terminalMu.Lock()
		s.terminalErr = err
		close(s.done)
		s.terminalMu.Unlock()
	})
}

func (s *pcmSource) finishedError() error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	if s.terminalErr == nil {
		return io.EOF
	}
	return s.terminalErr
}

func (s *pcmSource) Close() error {
	s.closeOnce.Do(func() {
		s.finish(io.ErrClosedPipe)
		if s.closeBackend != nil {
			s.closeErr = s.closeBackend()
		}
	})
	return s.closeErr
}

type sourceCleanupCoordinator struct {
	monitorDone <-chan struct{}
	closeClient func()
	closeOnce   sync.Once
}

func newSourceCleanupCoordinator(
	monitorDone <-chan struct{},
	closeClient func(),
) *sourceCleanupCoordinator {
	return &sourceCleanupCoordinator{
		monitorDone: monitorDone,
		closeClient: closeClient,
	}
}

func (c *sourceCleanupCoordinator) close() error {
	c.closeOnce.Do(func() {
		<-c.monitorDone
		if c.closeClient != nil {
			c.closeClient()
		}
	})
	return nil
}
