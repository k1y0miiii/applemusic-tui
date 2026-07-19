package visualizer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const noProgressBackoff = 5 * time.Millisecond

type terminalError struct {
	err error
}

type Service struct {
	source     Source
	sourceName string
	format     Format
	analyzer   *Analyzer
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}

	latest   atomic.Pointer[Frame]
	terminal atomic.Pointer[terminalError]

	sourceCloseOnce sync.Once
	sourceCloseErr  error
	closeOnce       sync.Once
	closeErr        error
}

func NewService(source Source) (*Service, error) {
	if source == nil {
		return nil, errors.New("visualizer: nil source")
	}

	format := source.Format()
	sourceName := source.Name()
	analyzer, err := NewAnalyzer(format)
	if err != nil {
		return nil, fmt.Errorf("visualizer: source %q: %w", sourceName, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		source:     source,
		sourceName: sourceName,
		format:     format,
		analyzer:   analyzer,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go service.run()
	return service, nil
}

func (s *Service) Latest() (Frame, bool) {
	frame := s.latest.Load()
	if frame == nil {
		return Frame{}, false
	}
	return *frame, true
}

func (s *Service) Err() error {
	terminal := s.terminal.Load()
	if terminal == nil {
		return nil
	}
	return terminal.err
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = s.closeSource()
		<-s.done
	})
	return s.closeErr
}

func (s *Service) run() {
	defer close(s.done)
	defer s.closeSource()

	buffer := make([]float32, windowFrames*s.format.Channels)
	var noProgressTimer *time.Timer
	defer func() {
		if noProgressTimer != nil {
			noProgressTimer.Stop()
		}
	}()
	for {
		if s.ctx.Err() != nil {
			return
		}
		n, readErr := s.source.Read(s.ctx, buffer)
		if s.ctx.Err() != nil {
			return
		}
		if n < 0 || n > len(buffer) {
			s.setTerminalError(fmt.Errorf(
				"visualizer: source %q returned invalid sample count %d for buffer length %d",
				s.sourceName,
				n,
				len(buffer),
			))
			return
		}

		if n > 0 {
			bands, err := s.analyzer.Process(buffer[:n])
			if err != nil {
				s.setTerminalError(fmt.Errorf("visualizer: source %q: %w", s.sourceName, err))
				return
			}
			s.latest.Store(&Frame{
				Bands:  bands,
				Source: s.sourceName,
				Live:   true,
				At:     time.Now(),
			})
		}

		if readErr != nil {
			if s.ctx.Err() == nil {
				s.setTerminalError(readErr)
			}
			return
		}
		if n == 0 {
			if noProgressTimer == nil {
				noProgressTimer = time.NewTimer(noProgressBackoff)
			} else {
				noProgressTimer.Reset(noProgressBackoff)
			}
			select {
			case <-s.ctx.Done():
				return
			case <-noProgressTimer.C:
			}
		}
	}
}

func (s *Service) setTerminalError(err error) {
	s.terminal.CompareAndSwap(nil, &terminalError{err: err})
}

func (s *Service) closeSource() error {
	s.sourceCloseOnce.Do(func() {
		s.sourceCloseErr = s.source.Close()
	})
	return s.sourceCloseErr
}
