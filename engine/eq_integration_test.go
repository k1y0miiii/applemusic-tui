//go:build darwin && integration

package engine

import (
	"testing"
	"time"
)

// TestEQAttachProducesAudio is the spike that decided EQ feasibility: a Web
// Audio graph attached to MusicKit's <audio> element must observe real,
// non-zero signal, or Chrome is feeding Web Audio silence for EME content and
// the EQ cannot work this way. It plays an ALBUM rather than a song because
// Apple's /v1/catalog/{storefront}/songs lookup route was returning 503 on
// their edge at the time this was written (confirmed via plain curl,
// independent of this app) while /albums and /search responded normally.
func TestEQAttachProducesAudio(t *testing.T) {
	e, err := Connect(func(s string) { t.Log(s) })
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer e.Close()

	res, err := e.Search("Queen Greatest Hits")
	if err != nil || len(res.Albums) == 0 {
		t.Fatalf("search: %v (albums=%d)", err, len(res.Albums))
	}
	album := res.Albums[0]
	if err := e.Play(album.Kind, album.ID); err != nil {
		t.Fatalf("play: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		st, err := e.State()
		if err != nil {
			t.Fatalf("state: %v", err)
		}
		if st.Err != "" {
			t.Fatalf("state err: %s", st.Err)
		}
		if st.Playing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never started playing, last state: %+v", st)
		}
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(2 * time.Second) // let audio actually flow

	var rms struct {
		Rms float64 `json:"rms"`
	}
	if err := e.evalJSON(`(() => {
		const audio = document.querySelector('audio');
		const ctx = new (window.AudioContext || window.webkitAudioContext)();
		const analyser = ctx.createAnalyser();
		analyser.fftSize = 2048;
		ctx.createMediaElementSource(audio).connect(analyser);
		analyser.connect(ctx.destination);
		return new Promise((resolve) => setTimeout(() => {
			const buf = new Float32Array(analyser.fftSize);
			analyser.getFloatTimeDomainData(buf);
			let sum = 0;
			for (let i = 0; i < buf.length; i++) sum += buf[i]*buf[i];
			ctx.close();
			resolve(JSON.stringify({rms: Math.sqrt(sum / buf.length)}));
		}, 1000));
	})()`, &rms); err != nil {
		t.Fatalf("attach graph / sample rms: %v", err)
	}
	if rms.Rms <= 0 {
		t.Fatalf("EQ attached but RMS = %v, want > 0 (Web Audio receiving silence)", rms.Rms)
	}
	t.Logf("EQ attached, RMS = %v", rms.Rms)
}
