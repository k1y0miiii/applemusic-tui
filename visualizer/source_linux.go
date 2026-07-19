//go:build linux

package visualizer

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

const (
	pulseSampleRate          = 48_000
	pulseLatencySeconds      = 0.03
	pulseQueueDepth          = 8
	pulseClientTimeout       = 3 * time.Second
	pulseServerProbeInterval = time.Second
)

func openSystemSource() (Source, error) {
	client, err := pulse.NewClient(
		pulse.ClientApplicationName("amtui"),
		pulse.ClientTimeout(pulseClientTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to PulseAudio server: %w", err)
	}

	sink, err := client.DefaultSink()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("resolve default PulseAudio sink: %w", err)
	}

	var target atomic.Pointer[pcmSource]
	stream, err := client.NewRecord(
		pulse.Float32Writer(func(samples []float32) (int, error) {
			source := target.Load()
			if source == nil {
				return len(samples), nil
			}
			return source.write(samples)
		}),
		pulse.RecordMonitor(sink),
		pulse.RecordStereo,
		pulse.RecordSampleRate(pulseSampleRate),
		pulse.RecordLatency(pulseLatencySeconds),
		pulse.RecordMediaName("amtui system audio visualizer"),
	)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("create PulseAudio monitor stream: %w", err)
	}

	format := Format{
		SampleRate: stream.SampleRate(),
		Channels:   stream.Channels(),
	}
	if err := validateSourceFormat(format); err != nil {
		client.Close()
		return nil, fmt.Errorf("PulseAudio monitor stream format: %w", err)
	}

	monitorDone := make(chan struct{})
	cleanup := newSourceCleanupCoordinator(
		monitorDone,
		client.Close,
	)
	source, err := newPCMSource("PIPEWIRE", format, pulseQueueDepth, cleanup.close)
	if err != nil {
		client.Close()
		return nil, err
	}
	target.Store(source)

	stream.Start()
	go monitorPulseServer(source, client, monitorDone)
	return source, nil
}

func monitorPulseServer(
	source *pcmSource,
	client *pulse.Client,
	monitorDone chan<- struct{},
) {
	defer close(monitorDone)

	timer := time.NewTimer(pulseServerProbeInterval)
	defer timer.Stop()

	for {
		select {
		case <-source.done:
			return
		case <-timer.C:
		}

		var serverInfo proto.GetServerInfoReply
		if err := client.RawRequest(&proto.GetServerInfo{}, &serverInfo); err != nil {
			source.fail(fmt.Errorf("PulseAudio server connection: %w", err))
			return
		}
		timer.Reset(pulseServerProbeInterval)
	}
}
